package handlers

import (
	"fmt"
	"strings"
	"time"

	"github.com/linesmerrill/police-cad-api/models"
)

// serverOwnedCommunityFields can never be written through the catch-all
// community PATCH, which writes whatever keys it is given under "community.".
//
// Without this, anyone who could reach the endpoint could lift their own
// moderation delisting ({"listingSuspension": null}), hand themselves someone
// else's community ({"ownerID": ...}), or grant themselves a paid plan. Each of
// these has its own endpoint that checks who is asking; this one does not.
// Audited against every caller on the website and in the app: none send them.
var serverOwnedCommunityFields = map[string]bool{
	"listingSuspension":         true,
	"ownerID":                   true,
	"subscription":              true,
	"pendingDeletionAt":         true,
	"scheduledDeletionAt":       true,
	"pendingDeletionNotifiedAt": true,
	"deletionRequestedBy":       true,
	"membersCount":              true,
	"members":                   true,
	"banList":                   true,
}

// rejectServerOwnedCommunityFields returns an error naming the first
// server-owned field in the patch, checking dotted paths too so
// "listingSuspension.until" cannot slip past as a different key.
func rejectServerOwnedCommunityFields(req map[string]interface{}) error {
	for key := range req {
		root := key
		if i := strings.Index(key, "."); i >= 0 {
			root = key[:i]
		}
		if serverOwnedCommunityFields[root] {
			return fmt.Errorf("%s cannot be changed here", root)
		}
	}
	return nil
}

// delistedVisibilityError refuses to make a community public while it is
// delisted by moderation.
//
// The delisting already keeps it out of every discovery surface whatever its
// visibility says, so this is about not misleading the owner: a Public toggle
// that saves but changes nothing reads as though the penalty was lifted. The
// message says what happened and when it ends. Going private, or any other
// change, is unaffected.
func delistedVisibilityError(req map[string]interface{}, community *models.Community, now time.Time) error {
	v, ok := req["visibility"].(string)
	if !ok || !strings.EqualFold(strings.TrimSpace(v), "public") {
		return nil
	}
	if community == nil || !community.Details.ListingSuspension.InForce(now) {
		return nil
	}
	// Re-sending the value it already has changes nothing. The website's
	// profile form sends visibility on every save, so refusing a no-op would
	// stop a delisted owner who was already public from editing anything.
	if strings.EqualFold(strings.TrimSpace(community.Details.Visibility), "public") {
		return nil
	}
	ls := community.Details.ListingSuspension
	until := "until further notice"
	if ls.Until != nil {
		until = "until " + ls.Until.Time().UTC().Format("January 2, 2006")
	}
	why := ""
	if ls.CategoryPhrase != "" {
		why = " after reports of " + ls.CategoryPhrase
	}
	return fmt.Errorf("this community has been removed from public listings %s%s, so it cannot be made public before then. If you think this is a mistake, contact us through linespolice-cad.com/contact-us", until, why)
}
