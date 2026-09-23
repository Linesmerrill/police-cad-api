package handlers

import (
	"fmt"
	"strings"

	"github.com/linesmerrill/police-cad-api/models"
)

// Notifications one user can cause another to receive.
//
// POST /users/notifications stored whatever `message` the client sent, to
// whatever user it named. That is an unmoderated private message channel: no
// reporting, no audit, and nothing stopping the text being anything at all.
// Blocking only dropped friend requests, and only that one type.
//
// Every type a client sends is listed here with its wording written on this
// side. Where the text names a community or department, it is composed from
// the notification's own data fields, which is what the clients already put
// there, so what a recipient sees does not change.
var notificationKinds = map[string]func(models.Notification) string{
	"friend_request": func(models.Notification) string { return "sent you a friend request" },
	"join_request":   func(models.Notification) string { return "has requested to join" },

	// The website sent these as type "notification" with the sentence built in
	// the browser: 18,416 of them, 2,644 different strings. Same sentence, made
	// here. Data2 is the community, Data4 the department, if any.
	"request_approved": func(n models.Notification) string { return joinResolvedMessage(n, "approved") },
	"request_declined": func(n models.Notification) string { return joinResolvedMessage(n, "declined") },

	// Transitional: what the website sent before it was updated to name the
	// outcome. Accepted so an approval still reaches the member during the
	// window between this deploying and the website deploying, but its text is
	// ignored like every other type's. Remove once nothing sends it.
	"notification": func(n models.Notification) string { return joinResolvedMessage(n, "updated") },
}

func joinResolvedMessage(n models.Notification, outcome string) string {
	where := strings.TrimSpace(n.Data2)
	if dept := strings.TrimSpace(n.Data4); dept != "" {
		if where == "" {
			where = dept
		} else {
			where = where + "'s department " + dept
		}
	}
	if where == "" {
		return fmt.Sprintf("Your request to join has been %s.", outcome)
	}
	return fmt.Sprintf("Your request to join %s has been %s.", where, outcome)
}

// notificationMessageFor returns the wording for a notification, and whether
// its type is one a client may send at all.
func notificationMessageFor(n models.Notification) (string, bool) {
	build, ok := notificationKinds[strings.ToLower(strings.TrimSpace(n.Type))]
	if !ok {
		return "", false
	}
	return build(n), true
}
