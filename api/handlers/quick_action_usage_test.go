package handlers_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/linesmerrill/police-cad-api/api/handlers"
	"github.com/linesmerrill/police-cad-api/databases/mocks"
)

func newQAUReq(body, userID string) *http.Request {
	req := httptest.NewRequest("POST", "/", bytes.NewBufferString(body))
	return mux.SetURLVars(req, map[string]string{"userId": userID})
}

func TestRecordQuickActionUsage_HappyPath(t *testing.T) {
	uID := primitive.NewObjectID()
	mockUDB := &mocks.UserDatabase{}
	mockUDB.On("UpdateOne", mock.Anything, bson.M{"_id": uID},
		bson.M{"$inc": bson.M{"user.quickActionUsage.person-search": 1}}).
		Return(&mongo.UpdateResult{MatchedCount: 1, ModifiedCount: 1}, nil)

	u := handlers.User{DB: mockUDB}
	rr := httptest.NewRecorder()
	u.RecordQuickActionUsageHandler(rr, newQAUReq(`{"actionKey":"person-search"}`, uID.Hex()))

	assert.Equal(t, http.StatusOK, rr.Code)
	mockUDB.AssertExpectations(t)
}

// Slugs are interpolated into a Mongo field path, so anything with dots,
// operators, spaces, or uppercase must be rejected before it reaches the DB.
func TestRecordQuickActionUsage_InvalidKeysRejected(t *testing.T) {
	uID := primitive.NewObjectID()
	bad := []string{
		`{"actionKey":"user.subscription.plan"}`, // dotted path traversal
		`{"actionKey":"$where"}`,                 // operator character
		`{"actionKey":"Person Search"}`,          // uppercase + space
		`{"actionKey":""}`,                       // empty
	}
	for _, body := range bad {
		mockUDB := &mocks.UserDatabase{}
		u := handlers.User{DB: mockUDB}
		rr := httptest.NewRecorder()
		u.RecordQuickActionUsageHandler(rr, newQAUReq(body, uID.Hex()))

		assert.Equal(t, http.StatusBadRequest, rr.Code, "expected 400 for %s", body)
		mockUDB.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
	}
}

func TestRecordQuickActionUsage_InvalidUserID(t *testing.T) {
	mockUDB := &mocks.UserDatabase{}
	u := handlers.User{DB: mockUDB}
	rr := httptest.NewRecorder()
	u.RecordQuickActionUsageHandler(rr, newQAUReq(`{"actionKey":"reports"}`, "not-a-hex"))

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	mockUDB.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}
