package handlers

// Staff password help: the admin console's "Send reset email" button.
//
// What was wrong before, and why the fix looks the way it does.
//
// This handler used to email `<site>/reset-password?token=<raw>`. The website
// has no /reset-password route — its reset page is /reset/:token — so the link
// 404'd. It also stored sha256(token) while the website compared the raw token,
// so even at the right URL the lookup found nothing. Both halves had to be
// wrong for the link to look plausible and work never; a member who asked
// staff for help got a dead link either way.
//
// One scheme now, on both sides: the raw token travels in the link, and only
// sha256(raw) is stored. A stolen database row is then not a usable link. The
// website keeps accepting a stored *raw* token for the 40-hex shape its own
// /forgot-password used to write, so links already in inboxes work until they
// expire; see app/reset-token.js in police-cad.
//
// Two more things this handler is careful about:
//
//   - It does not touch `updatedAt`. The console reads updatedAt for "last
//     activity", and bumping it when a reset email goes out made the account
//     look like the password had just changed. "Last password change" now has
//     a field of its own that only a real password write stamps.
//   - `lastResetEmailAt` is written after SendGrid accepts the message, never
//     before, and a failed send returns an error instead of 200. Staff seeing
//     "sent" when nothing was sent is how the original report happened.

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/sendgrid/sendgrid-go"
	"github.com/sendgrid/sendgrid-go/helpers/mail"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/models"
)

// resetTokenTTL matches the website's own /forgot-password window. A member
// who gets both emails should not find that one link outlives the other.
const resetTokenTTL = time.Hour

// The website's own /forgot-password records "Member" in lastResetEmailBy;
// a staff send records the staff member's name instead, so the console can
// say which of the two it was looking at.
//
// newResetToken returns the token that goes in the link and the hash that goes
// in the database. 32 bytes, so the raw token is 64 hex characters — long
// enough that the website can tell it apart from the 40-hex legacy shape and
// refuse to treat a stored hash as a token.
func newResetToken() (raw, hashed string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("failed to generate reset token: %w", err)
	}
	raw = hex.EncodeToString(buf)
	sum := sha256.Sum256([]byte(raw))
	return raw, hex.EncodeToString(sum[:]), nil
}

// webBaseURL is the public website, without a trailing slash.
func webBaseURL() string {
	base := strings.TrimRight(os.Getenv("PUBLIC_WEB_BASE_URL"), "/")
	if base == "" {
		base = "https://www.linespolice-cad.com"
	}
	return base
}

// userResetLink builds the link the member clicks. /reset/:token is the route
// the website actually serves; the token is a path segment, not a query param.
func userResetLink(baseURL, rawToken string) string {
	return strings.TrimRight(baseURL, "/") + "/reset/" + url.PathEscape(rawToken)
}

// adminDisplayLabel is who the console shows as having sent the email. The
// admin's name when the account has one, otherwise the address it signs in
// with — the admins panel already lists staff by email, so this adds nothing
// a console user cannot already see.
func adminDisplayLabel(admin *models.AdminUser) string {
	if admin == nil {
		return ""
	}
	name := strings.TrimSpace(strings.TrimSpace(admin.FirstName) + " " + strings.TrimSpace(admin.LastName))
	if name != "" {
		return name
	}
	return strings.TrimSpace(admin.Email)
}

// adminResetRequest is what the website's /admin/api/users/:id/reset-password
// proxy forwards. The identity comes from the admin session on that side, and
// this endpoint requires the gateway secret, so it is not something a browser
// can assert for itself.
//
// The id is the only way in. An email would be a request string in a Mongo
// filter for no gain: session.admin.id is what the website's own admin session
// is keyed on — adminSessionRevoked cannot re-check without it — so any session
// that reaches here has one.
type adminResetRequest struct {
	CurrentUser struct {
		ID string `json:"id"`
	} `json:"currentUser"`
}

// AdminUserResetPasswordHandler sends a member a password reset email on a
// staff member's behalf, and records that it happened.
//
// Staff never see the link or the token. The only outcome they get is whether
// the email was accepted for delivery.
func (h Admin) AdminUserResetPasswordHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// This writes a reset token for an arbitrary account and sends mail, so it
	// is reachable only from our own backend — the website proxies it behind
	// requireAdminSession. Called straight from a browser it would be an
	// unauthenticated way to put a live reset token on any account.
	if !requireFirstPartyCaller(w, r, "admin user password reset") {
		return
	}

	// Parse the id rather than testing it: the parsed ObjectID and the hex it
	// re-encodes to are built from 12 validated bytes, so nothing from the
	// request string reaches the query. A validity check that passed the
	// original string through would be the same value in the filter, and
	// neither Mongo nor CodeQL has any reason to believe otherwise.
	userOID, err := primitive.ObjectIDFromHex(strings.TrimSpace(mux.Vars(r)["id"]))
	if err != nil {
		writeAdminResetError(w, http.StatusBadRequest, "invalid user ID")
		return
	}
	userID := userOID.Hex()

	// Accounts predate a consistent _id type: some are stored as a string,
	// some as an ObjectID. Match either.
	userFilter := bson.M{"$or": []bson.M{{"_id": userID}, {"_id": userOID}}}

	user, err := h.findUserForReset(r, userFilter)
	if err != nil {
		writeAdminResetError(w, http.StatusNotFound, "user not found")
		return
	}
	if strings.TrimSpace(user.Details.Email) == "" {
		// Nothing to send to. Saying so plainly beats writing a token that can
		// never be delivered.
		writeAdminResetError(w, http.StatusConflict, "user has no email address on file")
		return
	}

	var req adminResetRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	admin := h.resolveActingAdmin(r, req)
	if admin == nil {
		// The reset is recorded against a named staff member, so an
		// unidentifiable caller is refused rather than logged as nobody.
		writeAdminResetError(w, http.StatusForbidden, "the acting admin could not be identified")
		return
	}
	sentBy := adminDisplayLabel(admin)

	rawToken, hashedToken, err := newResetToken()
	if err != nil {
		zap.S().Errorw("admin reset: failed to generate token", "error", err)
		writeAdminResetError(w, http.StatusInternalServerError, "failed to create reset token")
		return
	}

	// Deliberately no `updatedAt`: see the note at the top of this file.
	if _, err := h.UDB.UpdateOne(r.Context(), userFilter, bson.M{
		"$set": bson.M{
			"user.resetPasswordToken":   hashedToken,
			"user.resetPasswordExpires": time.Now().Add(resetTokenTTL),
		},
	}); err != nil {
		zap.S().Errorw("admin reset: failed to store token", "userID", userID, "error", err)
		writeAdminResetError(w, http.StatusInternalServerError, "failed to create reset token")
		return
	}

	if err := sendUserResetEmailFn(user.Details.Email, userResetLink(webBaseURL(), rawToken)); err != nil {
		// The token is live but undeliverable. Report the failure: a success
		// here is what sent staff looking in the wrong place last time.
		zap.S().Errorw("admin reset: failed to send email", "userID", userID, "error", err)
		writeAdminResetError(w, http.StatusBadGateway, "failed to send reset email")
		return
	}

	// Only now is it true that an email was sent.
	if _, err := h.UDB.UpdateOne(r.Context(), userFilter, bson.M{
		"$set": bson.M{
			"user.lastResetEmailAt": time.Now(),
			"user.lastResetEmailBy": sentBy,
		},
	}); err != nil {
		// The member has a working link; losing the staff-facing record of it
		// is not worth failing the request over.
		zap.S().Warnw("admin reset: email sent but the record was not written",
			"userID", userID, "error", err)
	}

	h.trackAdminAction(admin.ID, "user_reset_initiated", userID, "user",
		fmt.Sprintf("Sent a password reset email to %s", user.Details.Email), r)

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": "reset email sent"})
}

// findUserForReset reads the account, tolerating both _id shapes. The second
// decode target exists because a string-_id document will not decode into a
// models.User whose ID is typed as an ObjectID, and vice versa.
func (h Admin) findUserForReset(r *http.Request, filter bson.M) (*models.User, error) {
	var user models.User
	if err := h.UDB.FindOne(r.Context(), filter).Decode(&user); err == nil {
		return &user, nil
	}

	var objUser struct {
		ID      primitive.ObjectID `bson:"_id"`
		Details models.UserDetails `bson:"user"`
		Version int32              `bson:"__v"`
	}
	if err := h.UDB.FindOne(r.Context(), filter).Decode(&objUser); err != nil {
		return nil, err
	}
	return &models.User{ID: objUser.ID.Hex(), Details: objUser.Details, Version: objUser.Version}, nil
}

// resolveActingAdmin looks the staff member up in admin_users rather than
// believing anything in the request body, so what the console displays as
// "sent by" is the account that actually pressed the button.
//
// The id is parsed before it is queried, so the filter holds a typed ObjectID
// and not a string that arrived in the body.
func (h Admin) resolveActingAdmin(r *http.Request, req adminResetRequest) *models.AdminUser {
	adminOID, err := primitive.ObjectIDFromHex(strings.TrimSpace(req.CurrentUser.ID))
	if err != nil {
		return nil
	}
	admin, err := h.ADB.FindOne(r.Context(), bson.M{"_id": adminOID})
	if err != nil {
		return nil
	}
	return admin
}

func writeAdminResetError(w http.ResponseWriter, status int, message string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": message})
}

// sendUserResetEmailFn is the send, indirected so tests can assert on the link
// that actually goes in the email rather than reaching SendGrid.
var sendUserResetEmailFn = sendUserResetEmail

// sendUserResetEmail sends the member the reset link.
func sendUserResetEmail(toEmail, resetLink string) error {
	// No key means no email, however the rest of the call behaves. Catching it
	// here is what keeps a misconfigured deploy from writing lastResetEmailAt
	// and telling staff the member has a link.
	if os.Getenv("SENDGRID_API_KEY") == "" {
		return errors.New("SENDGRID_API_KEY is not set, cannot send the reset email")
	}

	from := mail.NewEmail("Lines Police CAD", "no-reply@linespolice-cad.com")
	subject := "Password Reset Request"
	to := mail.NewEmail("", toEmail)
	plain := fmt.Sprintf(`Hello,

You have requested a password reset for your account.

Click the following link to reset your password:
%s

This link will expire in 1 hour.

If you did not request this reset, please ignore this email.

Best regards,
Lines Police CAD Team`, resetLink)

	html := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
    <meta charset="utf-8">
    <title>Password Reset Request</title>
</head>
<body style="font-family: Arial, sans-serif; line-height: 1.6; color: #333;">
    <div style="max-width: 600px; margin: 0 auto; padding: 20px;">
        <h2 style="color: #2c3e50;">Password Reset Request</h2>
        <p>Hello,</p>
        <p>You have requested a password reset for your account.</p>
        <p>Click the following button to reset your password:</p>
        <div style="text-align: center; margin: 30px 0;">
            <a href="%s" style="background-color: #3498db; color: white; padding: 12px 24px; text-decoration: none; border-radius: 5px; display: inline-block;">Reset Password</a>
        </div>
        <p>Or copy and paste this link into your browser:</p>
        <p style="word-break: break-all; color: #3498db;">%s</p>
        <p><strong>This link will expire in 1 hour.</strong></p>
        <p>If you did not request this reset, please ignore this email.</p>
        <hr style="margin: 30px 0; border: none; border-top: 1px solid #eee;">
        <p style="color: #7f8c8d; font-size: 14px;">Best regards,<br>Lines Police CAD Team</p>
    </div>
</body>
</html>`, resetLink, resetLink)

	msg := mail.NewSingleEmail(from, subject, to, plain, html)
	msg.SetTrackingSettings(noTrackingSettings())
	status, body, err := sendGridSendFn(msg)
	if err != nil {
		return fmt.Errorf("could not reach sendgrid: %w", err)
	}
	// client.Send returns a nil error for a 4xx or 5xx: the request reached
	// SendGrid, which is all the transport cared about. A rejected message is
	// a failed send, and the caller has to hear about it — reporting "sent"
	// for one is the thing this whole change exists to stop.
	if status < 200 || status > 299 {
		return fmt.Errorf("sendgrid rejected the reset email: status %d: %s", status, truncateForLog(body))
	}
	return nil
}

// sendGridSendFn posts a message and reports SendGrid's HTTP status and body.
// Indirected so a test can hand back a non-2xx without a network call; the
// int/string signature keeps SendGrid's rest types out of the seam.
var sendGridSendFn = sendGridSend

func sendGridSend(msg *mail.SGMailV3) (status int, body string, err error) {
	resp, err := sendgrid.NewSendClient(os.Getenv("SENDGRID_API_KEY")).Send(msg)
	if err != nil {
		return 0, "", err
	}
	if resp == nil {
		return 0, "", errors.New("sendgrid returned no response")
	}
	return resp.StatusCode, resp.Body, nil
}

// truncateForLog bounds SendGrid's error body. It names the offending field,
// which is worth keeping, but it goes into an error string that reaches the
// logs and does not need to be unbounded.
func truncateForLog(body string) string {
	const max = 300
	body = strings.TrimSpace(body)
	if body == "" {
		return "(no response body)"
	}
	if len(body) > max {
		return body[:max] + "…"
	}
	return body
}
