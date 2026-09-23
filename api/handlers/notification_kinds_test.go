package handlers

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/linesmerrill/police-cad-api/models"
)

// The types the website and the app actually send. If one of these stopped
// being allowed, friend requests or join requests would silently break.
func TestNotificationKinds_CoversWhatClientsSend(t *testing.T) {
	for _, kind := range []string{"friend_request", "join_request", "request_approved", "request_declined"} {
		msg, ok := notificationMessageFor(models.Notification{Type: kind})
		assert.True(t, ok, kind)
		assert.NotEmpty(t, msg, kind)
	}
	msg, ok := notificationMessageFor(models.Notification{Type: "  JOIN_REQUEST "})
	assert.True(t, ok, "type matching is forgiving about case and spacing")
	assert.Equal(t, "has requested to join", msg)
}

// The approve and decline sentences used to be built in the browser. They are
// now composed here from the same data fields the clients already send, so a
// recipient sees the same thing.
func TestNotificationKinds_ApprovalWordingIsBuiltServerSide(t *testing.T) {
	tests := []struct {
		name, kind, community, department, want string
	}{
		{"community approved", "request_approved", "SCRP PS4", "", "Your request to join SCRP PS4 has been approved."},
		{"department declined", "request_declined", "California state RP", "California Highway Patrol",
			"Your request to join California state RP's department California Highway Patrol has been declined."},
		{"department only", "request_approved", "", "POLICE", "Your request to join POLICE has been approved."},
		{"nothing named", "request_declined", "", "", "Your request to join has been declined."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := notificationMessageFor(models.Notification{Type: tt.kind, Data2: tt.community, Data4: tt.department})
			assert.True(t, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

// The website's old type is accepted during the deploy window, with its text
// still ignored, so an approval is not lost between the two deploys.
func TestNotificationKinds_OldWebsiteTypeStillDelivers(t *testing.T) {
	got, ok := notificationMessageFor(models.Notification{
		Type: "notification", Data2: "SCRP PS4", Message: "anything at all",
	})
	assert.True(t, ok)
	assert.Equal(t, "Your request to join SCRP PS4 has been updated.", got)
}

// Whatever text a client sends is ignored, whichever type it picks.
func TestNotificationKinds_ClientTextIsNeverUsed(t *testing.T) {
	got, ok := notificationMessageFor(models.Notification{
		Type: "friend_request", Message: "hey add me on discord, here is my server",
	})
	assert.True(t, ok)
	assert.Equal(t, "sent you a friend request", got)
}

// The point of the change: a client cannot invent a type to carry text, and
// the text it sends is ignored either way.
func TestNotificationKinds_RefusesAnythingElse(t *testing.T) {
	for _, kind := range []string{"", "message", "dm", "panic_alert", "report_update", "chat"} {
		_, ok := notificationMessageFor(models.Notification{Type: kind})
		assert.False(t, ok, kind)
	}
}
