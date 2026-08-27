package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

// buildUnseenNotifications returns `total` notifications, the first `unseen` of
// which are unread. Timestamps descend so the handler's sort is a no-op.
func buildUnseenNotifications(sentFromID string, total, unseen int) []models.Notification {
	base := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	notifications := make([]models.Notification, 0, total)
	for i := 0; i < total; i++ {
		notifications = append(notifications, models.Notification{
			ID:         fmt.Sprintf("notification-%d", i),
			SentFromID: sentFromID,
			Type:       "friend-request",
			Message:    "hello",
			Seen:       i >= unseen,
			CreatedAt:  base.Add(-time.Duration(i) * time.Minute).Format(time.RFC3339),
		})
	}
	return notifications
}

// getNotificationsV2 runs GetUserNotificationsHandlerV2 against a user whose
// notification list is `notifications`, and returns the decoded response body.
func getNotificationsV2(t *testing.T, notifications []models.Notification, query string) map[string]interface{} {
	t.Helper()

	uID := primitive.NewObjectID()
	mockUserDB := &mocks.UserDatabase{}

	result := &mocks.SingleResultHelper{}
	result.On("Decode", mock.Anything).Run(func(args mock.Arguments) {
		ptr := args.Get(0).(*models.User)
		*ptr = models.User{
			ID:      uID.Hex(),
			Details: models.UserDetails{Notifications: notifications},
		}
	}).Return(nil)
	mockUserDB.On("FindOne", mock.Anything, bson.M{"_id": uID}).Return(result)

	// Senders are batch-fetched only when a page actually contains notifications.
	mockUserDB.On("Find", mock.Anything, mock.Anything).Return([]models.User{}, nil).Maybe()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v2/users/"+uID.Hex()+"/notifications"+query, nil)
	req = mux.SetURLVars(req, map[string]string{"user_id": uID.Hex()})

	handler := User{DB: mockUserDB}
	handler.GetUserNotificationsHandlerV2(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var body map[string]interface{}
	assert.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	return body
}

// TestGetUserNotificationsV2_PastEndKeepsUnseenCount guards the early-exit
// branch taken when ?page= starts past the end of the list. `total` and
// `unseenCount` describe the user's whole notification list, not the requested
// page, so paging past the end must still report the real unseen count — the
// mobile app drives its app-icon badge off unseenCount, and a hardcoded 0 here
// would silently clear a badge that should still be showing unread items.
func TestGetUserNotificationsV2_PastEndKeepsUnseenCount(t *testing.T) {
	notifications := buildUnseenNotifications(primitive.NewObjectID().Hex(), 5, 3)

	body := getNotificationsV2(t, notifications, "?page=3&limit=10")

	assert.Empty(t, body["notifications"], "a page past the end carries no notifications")
	assert.Equal(t, float64(3), body["page"])
	assert.Equal(t, float64(10), body["limit"])
	assert.Equal(t, float64(5), body["total"])
	assert.Equal(t, float64(3), body["unseenCount"], "unseenCount must describe the whole list, not the empty page")
}

// TestGetUserNotificationsV2_PastEndAllSeen confirms the hoisted loop still
// reports 0 when every notification really has been seen.
func TestGetUserNotificationsV2_PastEndAllSeen(t *testing.T) {
	notifications := buildUnseenNotifications(primitive.NewObjectID().Hex(), 4, 0)

	body := getNotificationsV2(t, notifications, "?page=2&limit=10")

	assert.Equal(t, float64(4), body["total"])
	assert.Equal(t, float64(0), body["unseenCount"])
}

// TestGetUserNotificationsV2_NoNotificationsPastEnd covers a user with an empty
// list, where skip >= 0 == len takes the early exit on page 1.
func TestGetUserNotificationsV2_NoNotificationsPastEnd(t *testing.T) {
	body := getNotificationsV2(t, nil, "")

	assert.Equal(t, float64(0), body["total"])
	assert.Equal(t, float64(0), body["unseenCount"])
}
