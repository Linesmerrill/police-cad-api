package handlers

// Did the reset email actually arrive?
//
// Staff could already see that we handed a message to SendGrid; they could not
// see what SendGrid then did with it. That is the gap the original report fell
// into: the member's provider was rejecting our mail, and nothing in the
// console said so, so staff kept re-sending into a wall.
//
// This reads SendGrid's Email Activity Feed with a dedicated read-only key.
// Three things shape it:
//
//   - It is best-effort, always. A missing key, a failed call, a slow call, a
//     message SendGrid has already forgotten — every one of those is
//     "Unknown". Nothing here may break the user detail view, because the rest
//     of that view is what staff actually came for.
//   - Activity history is three days. Past that there is nothing to ask about,
//     so we do not ask: the window check happens before the HTTP call.
//   - It is a separate endpoint, not part of the user payload. The search
//     results fetch that payload once per row, and nobody wants a page of
//     results to mean forty calls to SendGrid.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.uber.org/zap"
)

// The five labels the console shows, and nothing else. Any state we cannot
// map lands on Unknown rather than inventing a sixth.
const (
	deliveryDelivered = "Delivered"
	deliveryBlocked   = "Blocked"
	deliveryBounced   = "Bounced"
	deliveryDelayed   = "Delayed"
	deliveryUnknown   = "Unknown"
)

const (
	// sendGridActivityKeyEnv is its own key, scoped to Email Activity: Read.
	// Deliberately not MAIL_API_KEY: that one sends mail, and a key that can
	// send has no business being used to read.
	sendGridActivityKeyEnv = "SENDGRID_ACTIVITY_API_KEY"

	// sendGridActivityWindow is how long SendGrid keeps Email Activity.
	sendGridActivityWindow = 72 * time.Hour

	// sendGridActivityTimeout bounds each call. A staff member waiting on a
	// modal would rather have "Unknown" quickly than a correct answer slowly.
	sendGridActivityTimeout = 6 * time.Second

	// sendGridActivityPageSize is how many of the member's recent messages to
	// look through for a reset email. Generous enough to see past a burst of
	// other mail, small enough to stay one page.
	sendGridActivityPageSize = 25
)

// resetEmailSubjects are the subjects the two reset emails go out under: the
// API's own (admin_password_help.go) and the website's /forgot-password. A
// member can have had either, and staff asking "did it arrive?" do not care
// which surface sent it.
var resetEmailSubjects = []string{
	"Password Reset Request",
	"Lines Police CAD Reset Password",
}

// resetDeliveryResponse is what the console renders. Reason is SendGrid's own
// wording for a bounce or block, empty when there is nothing to say.
type resetDeliveryResponse struct {
	Label  string `json:"label"`
	Reason string `json:"reason,omitempty"`
}

type sendGridMessage struct {
	MsgID         string `json:"msg_id"`
	Subject       string `json:"subject"`
	ToEmail       string `json:"to_email"`
	Status        string `json:"status"`
	LastEventTime string `json:"last_event_time"`
}

type sendGridMessageList struct {
	Messages []sendGridMessage `json:"messages"`
}

type sendGridEvent struct {
	EventName  string `json:"event_name"`
	Processed  string `json:"processed"`
	Reason     string `json:"reason"`
	BounceType string `json:"type"`
}

type sendGridMessageDetail struct {
	MsgID  string          `json:"msg_id"`
	Events []sendGridEvent `json:"events"`
}

// AdminResetEmailDeliveryHandler reports what SendGrid did with the member's
// most recent reset email.
//
// It answers 200 with "Unknown" far more readily than it answers an error:
// the only failures worth a status code are the ones that mean the caller
// asked the wrong thing.
func (h Admin) AdminResetEmailDeliveryHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Delivery detail about a member's mail is staff-only, and the website
	// proxies this behind requireAdminSession.
	if !requireFirstPartyCaller(w, r, "admin reset email delivery") {
		return
	}

	userOID, err := primitive.ObjectIDFromHex(strings.TrimSpace(mux.Vars(r)["id"]))
	if err != nil {
		writeAdminResetError(w, http.StatusBadRequest, "invalid user ID")
		return
	}
	userID := userOID.Hex()
	userFilter := bson.M{"$or": []bson.M{{"_id": userID}, {"_id": userOID}}}

	user, err := h.findUserForReset(r, userFilter)
	if err != nil {
		writeAdminResetError(w, http.StatusNotFound, "user not found")
		return
	}

	writeResetDelivery(w, resetEmailDelivery(
		user.Details.Email,
		timeFromBSON(user.Details.LastResetEmailAt),
		time.Now(),
	))
}

// resetEmailDelivery is the whole decision, with the HTTP calls behind
// sendGridActivityGetFn so it is testable end to end.
func resetEmailDelivery(email string, sentAt, now time.Time) resetDeliveryResponse {
	unknown := resetDeliveryResponse{Label: deliveryUnknown}

	// Nothing was sent, or it is older than anything SendGrid still holds.
	// Either way there is no call worth making.
	if sentAt.IsZero() || now.Sub(sentAt.UTC()) > sendGridActivityWindow {
		return unknown
	}
	if os.Getenv(sendGridActivityKeyEnv) == "" {
		// Not an error: the key is optional, and the rest of the view works.
		zap.S().Debugw("reset delivery: no activity key configured")
		return unknown
	}

	query, ok := sendGridActivityQuery(email)
	if !ok {
		return unknown
	}

	listBody, err := sendGridActivityGetFn("/v3/messages?limit=" +
		fmt.Sprint(sendGridActivityPageSize) + "&query=" + url.QueryEscape(query))
	if err != nil {
		zap.S().Warnw("reset delivery: could not list messages", "error", err.Error())
		return unknown
	}
	var list sendGridMessageList
	if err := json.Unmarshal(listBody, &list); err != nil {
		zap.S().Warnw("reset delivery: could not parse the message list", "error", err.Error())
		return unknown
	}

	msgID := latestResetMessageID(list.Messages)
	if msgID == "" {
		return unknown
	}

	detailBody, err := sendGridActivityGetFn("/v3/messages/" + url.PathEscape(msgID))
	if err != nil {
		zap.S().Warnw("reset delivery: could not read the message", "error", err.Error())
		return unknown
	}
	var detail sendGridMessageDetail
	if err := json.Unmarshal(detailBody, &detail); err != nil {
		zap.S().Warnw("reset delivery: could not parse the message", "error", err.Error())
		return unknown
	}

	label, reason := latestDeliveryEvent(detail.Events)
	if label == "" {
		return unknown
	}
	return resetDeliveryResponse{Label: label, Reason: reason}
}

// sendGridActivityQuery builds the Email Activity filter for one address.
//
// The query language is a quoted-string grammar, so an address carrying a
// quote or a backslash could change the filter's meaning. Rather than guess at
// escaping rules for someone else's parser, an address that cannot be quoted
// plainly is refused and the caller shows Unknown. A quoted local part is
// legal in an email address and vanishingly rare in a real signup.
func sendGridActivityQuery(email string) (string, bool) {
	addr := strings.TrimSpace(email)
	if addr == "" || len(addr) > 254 {
		return "", false
	}
	if strings.ContainsAny(addr, "\"\\") {
		return "", false
	}
	for _, r := range addr {
		if r < 0x20 || r == 0x7f {
			return "", false
		}
	}
	return `to_email="` + addr + `"`, true
}

// latestResetMessageID picks the most recent reset email out of the member's
// recent activity. SendGrid returns newest first, but the order is not
// promised anywhere, so this compares timestamps rather than trusting it.
func latestResetMessageID(messages []sendGridMessage) string {
	var bestID string
	var bestTime time.Time
	for _, m := range messages {
		if !isResetEmailSubject(m.Subject) || m.MsgID == "" {
			continue
		}
		at, err := time.Parse(time.RFC3339, m.LastEventTime)
		if err != nil {
			// Undated but on-subject: better than nothing, if nothing else turns up.
			if bestID == "" {
				bestID = m.MsgID
			}
			continue
		}
		if bestTime.IsZero() || at.After(bestTime) {
			bestID, bestTime = m.MsgID, at
		}
	}
	return bestID
}

func isResetEmailSubject(subject string) bool {
	s := strings.TrimSpace(subject)
	for _, want := range resetEmailSubjects {
		if strings.EqualFold(s, want) {
			return true
		}
	}
	return false
}

// latestDeliveryEvent reduces a message's event history to one label and one
// reason: the newest event that maps to something staff can act on.
//
// SendGrid lists every event, including opens and clicks, and the order is not
// guaranteed, so this walks the whole list and keeps the newest event that
// maps to a label.
func latestDeliveryEvent(events []sendGridEvent) (label, reason string) {
	var bestTime time.Time
	var haveUndated bool
	for _, ev := range events {
		mapped := deliveryLabelForEvent(ev.EventName, ev.BounceType)
		if mapped == "" {
			continue
		}
		at, err := time.Parse(time.RFC3339, ev.Processed)
		if err != nil {
			// Keep an undated event only while nothing dated has been seen.
			if label == "" || (haveUndated && bestTime.IsZero()) {
				label, reason, haveUndated = mapped, strings.TrimSpace(ev.Reason), true
			}
			continue
		}
		if bestTime.IsZero() || at.After(bestTime) {
			label, reason, bestTime, haveUndated = mapped, strings.TrimSpace(ev.Reason), at, false
		}
	}
	return label, reason
}

// deliveryLabelForEvent maps a SendGrid event to one of the five labels, or ""
// for the events that say nothing about delivery (opens, clicks, unsubscribes).
func deliveryLabelForEvent(eventName, bounceType string) string {
	switch strings.ToLower(strings.TrimSpace(eventName)) {
	case "delivered":
		return deliveryDelivered
	case "blocked":
		return deliveryBlocked
	case "bounce", "bounced":
		// A "blocked" bounce is the receiving server refusing us outright,
		// which is a different conversation from a bad address.
		if strings.EqualFold(strings.TrimSpace(bounceType), "blocked") {
			return deliveryBlocked
		}
		return deliveryBounced
	case "dropped":
		// SendGrid refused to send it at all: bounce list, spam report,
		// unsubscribe, invalid address. Blocked is the honest label.
		return deliveryBlocked
	case "deferred":
		return deliveryDelayed
	case "processed":
		// Accepted and queued, nothing further yet. Of the five labels this
		// is the only one that is not wrong.
		return deliveryDelayed
	default:
		return ""
	}
}

// sendGridActivityGetFn is the one HTTP seam, so tests cover the mapping
// against real response shapes without a network call.
var sendGridActivityGetFn = sendGridActivityGet

func sendGridActivityGet(path string) ([]byte, error) {
	key := os.Getenv(sendGridActivityKeyEnv)
	if key == "" {
		return nil, fmt.Errorf("%s is not set", sendGridActivityKeyEnv)
	}

	req, err := http.NewRequest(http.MethodGet, "https://api.sendgrid.com"+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")

	resp, err := (&http.Client{Timeout: sendGridActivityTimeout}).Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	// Bounded read: this is someone else's response and we only need a page.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// 403 here is the usual one: a key without Email Activity: Read.
		return nil, fmt.Errorf("sendgrid activity returned %d: %s", resp.StatusCode, truncateForLog(string(body)))
	}
	return body, nil
}

func writeResetDelivery(w http.ResponseWriter, body resetDeliveryResponse) {
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(body)
}
