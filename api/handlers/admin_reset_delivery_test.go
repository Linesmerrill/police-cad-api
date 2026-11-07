package handlers

// Delivery status is best-effort, so most of these tests are about the ways it
// is allowed to fail: every one of them has to end on "Unknown" and a 200.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

// activityStub replaces the HTTP seam. The two calls are told apart by shape
// rather than by matching path fragments: the list call carries a query string
// and the detail call does not. Keying a map on substrings would have left the
// choice to Go's map iteration order.
type activityStub struct {
	list      string
	listErr   error
	detail    string
	detailErr error
	asked     []string
}

func (a *activityStub) install(t *testing.T) *activityStub {
	t.Helper()
	t.Setenv(sendGridActivityKeyEnv, "activity-key")
	original := sendGridActivityGetFn
	t.Cleanup(func() { sendGridActivityGetFn = original })

	sendGridActivityGetFn = func(path string) ([]byte, error) {
		a.asked = append(a.asked, path)
		if strings.Contains(path, "?") {
			if a.listErr != nil {
				return nil, a.listErr
			}
			return []byte(a.list), nil
		}
		if a.detailErr != nil {
			return nil, a.detailErr
		}
		return []byte(a.detail), nil
	}
	return a
}

// listCalls and detailCalls say which of the two calls were actually made.
func (a *activityStub) listCalls() (n int) {
	for _, p := range a.asked {
		if strings.Contains(p, "?") {
			n++
		}
	}
	return n
}

func (a *activityStub) detailCalls() (n int) {
	for _, p := range a.asked {
		if !strings.Contains(p, "?") {
			n++
		}
	}
	return n
}

func listWith(subject, msgID, lastEvent string) string {
	return `{"messages":[{"msg_id":"` + msgID + `","subject":"` + subject +
		`","to_email":"member@example.test","status":"delivered","last_event_time":"` + lastEvent + `"}]}`
}

func detailWith(events string) string {
	return `{"msg_id":"abc","events":[` + events + `]}`
}

// -----------------------------------------------------------------------------
// The label mapping
// -----------------------------------------------------------------------------

func TestDeliveryLabelForEvent(t *testing.T) {
	cases := []struct {
		event, bounceType, want string
	}{
		{"delivered", "", deliveryDelivered},
		{"Delivered", "", deliveryDelivered},
		{"bounce", "", deliveryBounced},
		{"bounce", "bounce", deliveryBounced},
		// A blocked bounce is the receiving server refusing us, not a bad address.
		{"bounce", "blocked", deliveryBlocked},
		{"bounce", "Blocked", deliveryBlocked},
		{"blocked", "", deliveryBlocked},
		{"dropped", "", deliveryBlocked},
		{"deferred", "", deliveryDelayed},
		{"processed", "", deliveryDelayed},
		// Events that say nothing about delivery map to nothing.
		{"open", "", ""},
		{"click", "", ""},
		{"spam_report", "", ""},
		{"unsubscribe", "", ""},
		{"", "", ""},
		{"something_new_sendgrid_added", "", ""},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, deliveryLabelForEvent(c.event, c.bounceType),
			"event %q type %q", c.event, c.bounceType)
	}
}

func TestLatestDeliveryEvent_TakesTheNewestMappableEvent(t *testing.T) {
	label, reason := latestDeliveryEvent([]sendGridEvent{
		{EventName: "processed", Processed: "2026-11-07T10:00:00Z"},
		{EventName: "deferred", Processed: "2026-11-07T10:01:00Z", Reason: "temporarily deferred"},
		{EventName: "delivered", Processed: "2026-11-07T10:05:00Z"},
		// Later, but says nothing about delivery.
		{EventName: "open", Processed: "2026-11-07T11:00:00Z"},
	})
	assert.Equal(t, deliveryDelivered, label)
	assert.Empty(t, reason)
}

func TestLatestDeliveryEvent_KeepsTheReason(t *testing.T) {
	label, reason := latestDeliveryEvent([]sendGridEvent{
		{EventName: "processed", Processed: "2026-11-07T10:00:00Z"},
		{EventName: "bounce", BounceType: "blocked", Processed: "2026-11-07T10:02:00Z",
			Reason: "550 5.7.1 Service unavailable, client host blocked"},
	})
	assert.Equal(t, deliveryBlocked, label)
	assert.Equal(t, "550 5.7.1 Service unavailable, client host blocked", reason)
}

// Order is not promised, so an out-of-order list must still resolve.
func TestLatestDeliveryEvent_IgnoresListOrder(t *testing.T) {
	label, _ := latestDeliveryEvent([]sendGridEvent{
		{EventName: "delivered", Processed: "2026-11-07T10:05:00Z"},
		{EventName: "processed", Processed: "2026-11-07T10:00:00Z"},
	})
	assert.Equal(t, deliveryDelivered, label)
}

func TestLatestDeliveryEvent_NothingMappable(t *testing.T) {
	label, reason := latestDeliveryEvent([]sendGridEvent{
		{EventName: "open", Processed: "2026-11-07T10:00:00Z"},
	})
	assert.Empty(t, label)
	assert.Empty(t, reason)

	label, _ = latestDeliveryEvent(nil)
	assert.Empty(t, label)
}

// -----------------------------------------------------------------------------
// Picking the message
// -----------------------------------------------------------------------------

func TestLatestResetMessageID_PicksTheNewestResetEmail(t *testing.T) {
	id := latestResetMessageID([]sendGridMessage{
		{MsgID: "other", Subject: "Your receipt", LastEventTime: "2026-11-07T12:00:00Z"},
		{MsgID: "older-reset", Subject: "Password Reset Request", LastEventTime: "2026-11-07T09:00:00Z"},
		{MsgID: "newer-reset", Subject: "Lines Police CAD Reset Password", LastEventTime: "2026-11-07T11:00:00Z"},
	})
	assert.Equal(t, "newer-reset", id)
}

func TestLatestResetMessageID_IgnoresOtherMail(t *testing.T) {
	assert.Empty(t, latestResetMessageID([]sendGridMessage{
		{MsgID: "x", Subject: "Lines Police CAD Password Has Been Reset", LastEventTime: "2026-11-07T12:00:00Z"},
		{MsgID: "y", Subject: "Welcome", LastEventTime: "2026-11-07T12:00:00Z"},
	}))
	assert.Empty(t, latestResetMessageID(nil))
}

// -----------------------------------------------------------------------------
// The query
// -----------------------------------------------------------------------------

func TestSendGridActivityQuery(t *testing.T) {
	q, ok := sendGridActivityQuery("member@example.test")
	assert.True(t, ok)
	assert.Equal(t, `to_email="member@example.test"`, q)

	// An address that could change the filter's meaning is refused outright
	// rather than escaped into someone else's grammar.
	for _, bad := range []string{
		`a"@example.test`,
		`a\@example.test`,
		`a" OR to_email="victim@example.test`,
		"a\n@example.test",
		"a\x00@example.test",
		"",
		"   ",
		strings.Repeat("a", 250) + "@example.test",
	} {
		_, ok := sendGridActivityQuery(bad)
		assert.False(t, ok, "address %q should be refused", bad)
	}
}

// -----------------------------------------------------------------------------
// resetEmailDelivery: the whole decision
// -----------------------------------------------------------------------------

func TestResetEmailDelivery_Delivered(t *testing.T) {
	now := time.Now()
	stub := (&activityStub{
		list:   listWith("Password Reset Request", "msg-1", now.Format(time.RFC3339)),
		detail: detailWith(`{"event_name":"delivered","processed":"` + now.Format(time.RFC3339) + `"}`),
	}).install(t)

	got := resetEmailDelivery("member@example.test", now.Add(-time.Hour), now)
	assert.Equal(t, deliveryDelivered, got.Label)
	assert.Empty(t, got.Reason)
	assert.Equal(t, 1, stub.listCalls())
	assert.Equal(t, 1, stub.detailCalls())
	assert.Contains(t, stub.asked[0], "to_email")
	assert.Contains(t, stub.asked[1], "msg-1")
}

func TestResetEmailDelivery_BlockedCarriesTheReason(t *testing.T) {
	now := time.Now()
	(&activityStub{
		list: listWith("Lines Police CAD Reset Password", "msg-2", now.Format(time.RFC3339)),
		detail: detailWith(`{"event_name":"bounce","type":"blocked","processed":"` +
			now.Format(time.RFC3339) + `","reason":"550 5.7.606 Access denied, banned sending IP"}`),
	}).install(t)

	got := resetEmailDelivery("member@example.test", now.Add(-time.Hour), now)
	assert.Equal(t, deliveryBlocked, got.Label)
	assert.Equal(t, "550 5.7.606 Access denied, banned sending IP", got.Reason)
}

// Every best-effort path, and each one has to land on Unknown.
func TestResetEmailDelivery_UnknownPaths(t *testing.T) {
	now := time.Now()

	t.Run("no reset email was ever sent", func(t *testing.T) {
		stub := (&activityStub{}).install(t)
		assert.Equal(t, deliveryUnknown, resetEmailDelivery("member@example.test", time.Time{}, now).Label)
		assert.Empty(t, stub.asked, "nothing to ask about")
	})

	t.Run("older than the three-day history", func(t *testing.T) {
		stub := (&activityStub{}).install(t)
		got := resetEmailDelivery("member@example.test", now.Add(-4*24*time.Hour), now)
		assert.Equal(t, deliveryUnknown, got.Label)
		assert.Empty(t, stub.asked, "no call should be made outside the window")
	})

	t.Run("inside the window by a margin", func(t *testing.T) {
		stub := (&activityStub{
			list:   listWith("Password Reset Request", "msg-1", now.Format(time.RFC3339)),
			detail: detailWith(`{"event_name":"delivered","processed":"` + now.Format(time.RFC3339) + `"}`),
		}).install(t)
		got := resetEmailDelivery("member@example.test", now.Add(-71*time.Hour), now)
		assert.Equal(t, deliveryDelivered, got.Label)
		assert.Equal(t, 1, stub.listCalls())
	})

	t.Run("no activity key configured", func(t *testing.T) {
		stub := (&activityStub{
			list:   listWith("Password Reset Request", "msg-1", now.Format(time.RFC3339)),
			detail: detailWith(`{"event_name":"delivered","processed":"` + now.Format(time.RFC3339) + `"}`),
		}).install(t)
		t.Setenv(sendGridActivityKeyEnv, "")

		assert.Equal(t, deliveryUnknown, resetEmailDelivery("member@example.test", now.Add(-time.Hour), now).Label)
		assert.Empty(t, stub.asked, "no key means no call")
	})

	t.Run("the list call fails", func(t *testing.T) {
		stub := (&activityStub{listErr: assert.AnError}).install(t)
		assert.Equal(t, deliveryUnknown, resetEmailDelivery("member@example.test", now.Add(-time.Hour), now).Label)
		assert.Equal(t, 0, stub.detailCalls(), "a failed list should not be followed by a detail call")
	})

	t.Run("the detail call fails", func(t *testing.T) {
		(&activityStub{
			list:      listWith("Password Reset Request", "msg-1", now.Format(time.RFC3339)),
			detailErr: assert.AnError,
		}).install(t)
		assert.Equal(t, deliveryUnknown, resetEmailDelivery("member@example.test", now.Add(-time.Hour), now).Label)
	})

	t.Run("unparseable responses", func(t *testing.T) {
		(&activityStub{list: "not json at all"}).install(t)
		assert.Equal(t, deliveryUnknown, resetEmailDelivery("member@example.test", now.Add(-time.Hour), now).Label)

		(&activityStub{
			list:   listWith("Password Reset Request", "msg-1", now.Format(time.RFC3339)),
			detail: "{{{",
		}).install(t)
		assert.Equal(t, deliveryUnknown, resetEmailDelivery("member@example.test", now.Add(-time.Hour), now).Label)
	})

	t.Run("no reset email in the member's recent activity", func(t *testing.T) {
		stub := (&activityStub{list: `{"messages":[]}`}).install(t)
		assert.Equal(t, deliveryUnknown, resetEmailDelivery("member@example.test", now.Add(-time.Hour), now).Label)
		assert.Equal(t, 0, stub.detailCalls(), "nothing to fetch details for")
	})

	t.Run("only events that say nothing about delivery", func(t *testing.T) {
		(&activityStub{
			list:   listWith("Password Reset Request", "msg-1", now.Format(time.RFC3339)),
			detail: detailWith(`{"event_name":"open","processed":"` + now.Format(time.RFC3339) + `"}`),
		}).install(t)
		assert.Equal(t, deliveryUnknown, resetEmailDelivery("member@example.test", now.Add(-time.Hour), now).Label)
	})

	t.Run("an address the query grammar cannot hold", func(t *testing.T) {
		stub := (&activityStub{}).install(t)
		assert.Equal(t, deliveryUnknown, resetEmailDelivery(`a"b@example.test`, now.Add(-time.Hour), now).Label)
		assert.Empty(t, stub.asked)
	})
}

// The label is always one of the five the console knows.
func TestResetEmailDelivery_OnlyEverReturnsAKnownLabel(t *testing.T) {
	known := map[string]bool{
		deliveryDelivered: true, deliveryBlocked: true, deliveryBounced: true,
		deliveryDelayed: true, deliveryUnknown: true,
	}
	now := time.Now()
	for _, event := range []string{
		"delivered", "bounce", "blocked", "dropped", "deferred", "processed",
		"open", "click", "spam_report", "group_unsubscribe", "", "surprise",
	} {
		(&activityStub{
			list: listWith("Password Reset Request", "msg-1", now.Format(time.RFC3339)),
			detail: detailWith(`{"event_name":"` + event + `","processed":"` +
				now.Format(time.RFC3339) + `"}`),
		}).install(t)
		got := resetEmailDelivery("member@example.test", now.Add(-time.Hour), now)
		assert.True(t, known[got.Label], "event %q produced label %q", event, got.Label)
	}
}

// -----------------------------------------------------------------------------
// The handler
// -----------------------------------------------------------------------------

func newDeliveryHandler(t *testing.T, memberID primitive.ObjectID, lastResetEmailAt interface{}) Admin {
	t.Helper()
	t.Setenv(apiGatewayKeyEnv, "gw")

	udb := &mocks.UserDatabase{}
	result := &mocks.SingleResultHelper{}
	result.On("Decode", mock.Anything).Run(func(args mock.Arguments) {
		if ptr, ok := args.Get(0).(*models.User); ok {
			*ptr = models.User{ID: memberID.Hex(), Details: models.UserDetails{
				Email:            "member@example.test",
				LastResetEmailAt: lastResetEmailAt,
			}}
		}
	}).Return(nil)
	udb.On("FindOne", mock.Anything, mock.Anything).Return(result)
	return Admin{UDB: udb}
}

func callDelivery(t *testing.T, h Admin, userID string, withSecret bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/users/"+userID+"/reset-email-delivery", nil)
	if withSecret {
		req.Header.Set(apiGatewayHeader, "gw")
	}
	req = mux.SetURLVars(req, map[string]string{"id": userID})
	rr := httptest.NewRecorder()
	http.HandlerFunc(h.AdminResetEmailDeliveryHandler).ServeHTTP(rr, req)
	return rr
}

func TestAdminResetEmailDelivery_ReturnsTheLabel(t *testing.T) {
	memberID := primitive.NewObjectID()
	now := time.Now()
	h := newDeliveryHandler(t, memberID, primitive.NewDateTimeFromTime(now.Add(-time.Hour)))
	(&activityStub{
		list: listWith("Password Reset Request", "msg-1", now.Format(time.RFC3339)),
		detail: detailWith(`{"event_name":"bounce","processed":"` +
			now.Format(time.RFC3339) + `","reason":"550 no such user"}`),
	}).install(t)

	rr := callDelivery(t, h, memberID.Hex(), true)
	assert.Equal(t, http.StatusOK, rr.Code)

	var body resetDeliveryResponse
	assert.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	assert.Equal(t, deliveryBounced, body.Label)
	assert.Equal(t, "550 no such user", body.Reason)
}

// A broken lookup must still be a 200 with Unknown: the rest of the detail
// view is what staff came for, and this must never take it down.
func TestAdminResetEmailDelivery_FailureIsStillATwoHundred(t *testing.T) {
	memberID := primitive.NewObjectID()
	h := newDeliveryHandler(t, memberID, primitive.NewDateTimeFromTime(time.Now().Add(-time.Hour)))
	(&activityStub{listErr: assert.AnError}).install(t)

	rr := callDelivery(t, h, memberID.Hex(), true)
	assert.Equal(t, http.StatusOK, rr.Code)

	var body resetDeliveryResponse
	assert.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	assert.Equal(t, deliveryUnknown, body.Label)
	assert.Empty(t, body.Reason)
}

func TestAdminResetEmailDelivery_RequiresTheFirstPartySecret(t *testing.T) {
	memberID := primitive.NewObjectID()
	h := newDeliveryHandler(t, memberID, nil)
	(&activityStub{}).install(t)
	assert.Equal(t, http.StatusForbidden, callDelivery(t, h, memberID.Hex(), false).Code)
}

func TestAdminResetEmailDelivery_RejectsANonObjectIDPath(t *testing.T) {
	memberID := primitive.NewObjectID()
	h := newDeliveryHandler(t, memberID, nil)
	for _, bad := range []string{"not-an-id", `{"$ne":null}`, ""} {
		assert.Equal(t, http.StatusBadRequest, callDelivery(t, h, bad, true).Code, "id %q", bad)
	}
}

// The activity key is its own, and never the sending key.
func TestResetDeliveryUsesItsOwnKey(t *testing.T) {
	assert.Equal(t, "SENDGRID_ACTIVITY_API_KEY", sendGridActivityKeyEnv)
	assert.NotEqual(t, "MAIL_API_KEY", sendGridActivityKeyEnv)
	assert.NotEqual(t, "SENDGRID_API_KEY", sendGridActivityKeyEnv)
}
