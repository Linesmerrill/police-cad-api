package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"golang.org/x/crypto/bcrypt"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/models"
	templates "github.com/linesmerrill/police-cad-api/templates/html"
)

const minPasswordLength = 8

// RequestEmailChangeHandler starts a verified email change. After validating the user's current
// password, it issues a 6-digit code to the user's CURRENT email address and stores a
// pendingVerifications row keyed by (userID, purpose=email_change). The change is not applied
// until ConfirmEmailChangeHandler is called with a matching, unexpired code.
func (pv PendingVerification) RequestEmailChangeHandler(w http.ResponseWriter, r *http.Request) {
	uID, err := primitive.ObjectIDFromHex(mux.Vars(r)["user_id"])
	if err != nil {
		config.ErrorStatus("failed to parse user ID", http.StatusBadRequest, w, err)
		return
	}

	var body struct {
		NewEmail        string `json:"newEmail"`
		CurrentPassword string `json:"currentPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}
	newEmail := strings.TrimSpace(strings.ToLower(body.NewEmail))
	if newEmail == "" || body.CurrentPassword == "" {
		writeJSONError(w, http.StatusBadRequest, "New email and current password are required")
		return
	}
	if !strings.Contains(newEmail, "@") {
		writeJSONError(w, http.StatusBadRequest, "Invalid email format")
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	user := models.User{}
	if err := pv.UDB.FindOne(ctx, bson.M{"_id": uID}).Decode(&user); err != nil {
		if err == mongo.ErrNoDocuments {
			writeJSONError(w, http.StatusNotFound, "User not found")
			return
		}
		config.ErrorStatus("failed to find user", http.StatusInternalServerError, w, err)
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Details.Password), []byte(body.CurrentPassword)); err != nil {
		writeJSONError(w, http.StatusUnauthorized, "Invalid password")
		return
	}

	currentEmail := strings.TrimSpace(strings.ToLower(user.Details.Email))
	if newEmail == currentEmail {
		writeJSONError(w, http.StatusBadRequest, "New email must differ from your current email")
		return
	}

	// Reject if the new email is already used by another account (case-insensitive).
	conflict := models.User{}
	err = pv.UDB.FindOne(ctx, bson.M{"user.email": newEmail, "_id": bson.M{"$ne": uID}}).Decode(&conflict)
	if err == nil {
		writeJSONError(w, http.StatusConflict, "Email already in use")
		return
	}
	if err != mongo.ErrNoDocuments {
		config.ErrorStatus("failed to check email uniqueness", http.StatusInternalServerError, w, err)
		return
	}

	if err := checkSensitiveCodeRateLimit(ctx, pv.PVDB, uID, models.PurposeEmailChange); err != nil {
		writeJSONError(w, http.StatusTooManyRequests, "Please wait at least 60 seconds before requesting a new code")
		return
	}

	code, err := generateNumericCode()
	if err != nil {
		config.ErrorStatus("failed to generate verification code", http.StatusInternalServerError, w, err)
		return
	}
	if err := upsertSensitiveCode(ctx, pv.PVDB, uID, models.PurposeEmailChange, currentEmail, newEmail, code); err != nil {
		config.ErrorStatus("failed to store verification code", http.StatusInternalServerError, w, err)
		return
	}

	sendSensitiveEmailAsync(sensitiveEmail{
		To:        currentEmail,
		Subject:   "Lines Police CAD: Email Change Verification Code",
		PlainText: "Your email-change verification code is: " + code + ". This code expires in 15 minutes. If you did not request this, ignore this email.",
		HTML:      templates.RenderEmailChangeCode(code),
	})

	writeJSONOK(w, "Verification code sent to your current email")
}

// ConfirmEmailChangeHandler completes a verified email change. It accepts the 6-digit code from
// RequestEmailChangeHandler and, on success, applies the new email and sends a notification to
// the previous address. Replaces the legacy password-only ChangeEmailHandler.
func (pv PendingVerification) ConfirmEmailChangeHandler(w http.ResponseWriter, r *http.Request) {
	uID, err := primitive.ObjectIDFromHex(mux.Vars(r)["user_id"])
	if err != nil {
		config.ErrorStatus("failed to parse user ID", http.StatusBadRequest, w, err)
		return
	}

	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}
	if strings.TrimSpace(body.Code) == "" {
		writeJSONError(w, http.StatusBadRequest, "Verification code is required")
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	pending, err := pv.PVDB.FindOne(ctx, bson.M{"userID": uID, "purpose": models.PurposeEmailChange})
	if err != nil {
		if err == mongo.ErrNoDocuments {
			writeJSONError(w, http.StatusBadRequest, "No pending email change. Request a new code.")
			return
		}
		config.ErrorStatus("failed to find pending verification", http.StatusInternalServerError, w, err)
		return
	}

	if expired := timeFromBSON(pending.ExpiresAt); !expired.IsZero() && time.Now().After(expired) {
		_ = pv.PVDB.DeleteOne(ctx, bson.M{"_id": pending.ID})
		writeJSONError(w, http.StatusBadRequest, "Verification code expired. Request a new one.")
		return
	}

	if pending.Attempts >= sensitiveMaxRetries {
		_ = pv.PVDB.DeleteOne(ctx, bson.M{"_id": pending.ID})
		writeJSONError(w, http.StatusBadRequest, "Too many attempts. Request a new code.")
		return
	}

	if !codesEqualConstantTime(pending.Code, body.Code) {
		_ = pv.PVDB.UpdateOne(ctx, bson.M{"_id": pending.ID}, bson.M{"$inc": bson.M{"attempts": 1}})
		writeJSONError(w, http.StatusBadRequest, "Invalid verification code")
		return
	}

	// Re-check uniqueness: another account could have claimed this email between request and confirm.
	conflict := models.User{}
	err = pv.UDB.FindOne(ctx, bson.M{"user.email": pending.NewEmail, "_id": bson.M{"$ne": uID}}).Decode(&conflict)
	if err == nil {
		_ = pv.PVDB.DeleteOne(ctx, bson.M{"_id": pending.ID})
		writeJSONError(w, http.StatusConflict, "Email already in use")
		return
	}
	if err != mongo.ErrNoDocuments {
		config.ErrorStatus("failed to check email uniqueness", http.StatusInternalServerError, w, err)
		return
	}

	oldEmail := pending.Email
	if _, err := pv.UDB.UpdateOne(ctx, bson.M{"_id": uID}, bson.M{
		"$set": bson.M{
			"user.email":     pending.NewEmail,
			"user.updatedAt": primitive.NewDateTimeFromTime(time.Now()),
		},
	}); err != nil {
		config.ErrorStatus("failed to update email", http.StatusInternalServerError, w, err)
		return
	}

	_ = pv.PVDB.DeleteOne(ctx, bson.M{"_id": pending.ID})

	if oldEmail != "" {
		sendSensitiveEmailAsync(sensitiveEmail{
			To:        oldEmail,
			Subject:   "Lines Police CAD: Your Email Address Was Changed",
			PlainText: "The email on your Lines Police CAD account was just changed to " + pending.NewEmail + ". If this wasn't you, contact support immediately at https://www.linespolice-cad.com/contact-us.",
			HTML:      templates.RenderEmailChangedNotice(pending.NewEmail),
		})
	}

	writeJSONOK(w, "Email updated successfully")
}

// RequestPasswordChangeHandler starts a verified password change. The user must already be
// authenticated and supply their current password; on success a 6-digit code is mailed to the
// account's current email address.
func (pv PendingVerification) RequestPasswordChangeHandler(w http.ResponseWriter, r *http.Request) {
	uID, err := primitive.ObjectIDFromHex(mux.Vars(r)["user_id"])
	if err != nil {
		config.ErrorStatus("failed to parse user ID", http.StatusBadRequest, w, err)
		return
	}

	var body struct {
		CurrentPassword string `json:"currentPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}
	if body.CurrentPassword == "" {
		writeJSONError(w, http.StatusBadRequest, "Current password is required")
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	user := models.User{}
	if err := pv.UDB.FindOne(ctx, bson.M{"_id": uID}).Decode(&user); err != nil {
		if err == mongo.ErrNoDocuments {
			writeJSONError(w, http.StatusNotFound, "User not found")
			return
		}
		config.ErrorStatus("failed to find user", http.StatusInternalServerError, w, err)
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Details.Password), []byte(body.CurrentPassword)); err != nil {
		writeJSONError(w, http.StatusUnauthorized, "Invalid password")
		return
	}

	if err := checkSensitiveCodeRateLimit(ctx, pv.PVDB, uID, models.PurposePasswordChange); err != nil {
		writeJSONError(w, http.StatusTooManyRequests, "Please wait at least 60 seconds before requesting a new code")
		return
	}

	code, err := generateNumericCode()
	if err != nil {
		config.ErrorStatus("failed to generate verification code", http.StatusInternalServerError, w, err)
		return
	}
	currentEmail := strings.TrimSpace(strings.ToLower(user.Details.Email))
	if err := upsertSensitiveCode(ctx, pv.PVDB, uID, models.PurposePasswordChange, currentEmail, "", code); err != nil {
		config.ErrorStatus("failed to store verification code", http.StatusInternalServerError, w, err)
		return
	}

	sendSensitiveEmailAsync(sensitiveEmail{
		To:        currentEmail,
		Subject:   "Lines Police CAD: Password Change Verification Code",
		PlainText: "Your password-change verification code is: " + code + ". This code expires in 15 minutes. If you did not request this, ignore this email.",
		HTML:      templates.RenderPasswordChangeCode(code),
	})

	writeJSONOK(w, "Verification code sent to your current email")
}

// ConfirmPasswordChangeHandler completes a verified password change.
func (pv PendingVerification) ConfirmPasswordChangeHandler(w http.ResponseWriter, r *http.Request) {
	uID, err := primitive.ObjectIDFromHex(mux.Vars(r)["user_id"])
	if err != nil {
		config.ErrorStatus("failed to parse user ID", http.StatusBadRequest, w, err)
		return
	}

	var body struct {
		Code        string `json:"code"`
		NewPassword string `json:"newPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}
	if strings.TrimSpace(body.Code) == "" {
		writeJSONError(w, http.StatusBadRequest, "Verification code is required")
		return
	}
	if len(body.NewPassword) < minPasswordLength {
		writeJSONError(w, http.StatusBadRequest, "New password must be at least 8 characters")
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	pending, err := pv.PVDB.FindOne(ctx, bson.M{"userID": uID, "purpose": models.PurposePasswordChange})
	if err != nil {
		if err == mongo.ErrNoDocuments {
			writeJSONError(w, http.StatusBadRequest, "No pending password change. Request a new code.")
			return
		}
		config.ErrorStatus("failed to find pending verification", http.StatusInternalServerError, w, err)
		return
	}

	if expired := timeFromBSON(pending.ExpiresAt); !expired.IsZero() && time.Now().After(expired) {
		_ = pv.PVDB.DeleteOne(ctx, bson.M{"_id": pending.ID})
		writeJSONError(w, http.StatusBadRequest, "Verification code expired. Request a new one.")
		return
	}

	if pending.Attempts >= sensitiveMaxRetries {
		_ = pv.PVDB.DeleteOne(ctx, bson.M{"_id": pending.ID})
		writeJSONError(w, http.StatusBadRequest, "Too many attempts. Request a new code.")
		return
	}

	if !codesEqualConstantTime(pending.Code, body.Code) {
		_ = pv.PVDB.UpdateOne(ctx, bson.M{"_id": pending.ID}, bson.M{"$inc": bson.M{"attempts": 1}})
		writeJSONError(w, http.StatusBadRequest, "Invalid verification code")
		return
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(body.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		config.ErrorStatus("failed to hash new password", http.StatusInternalServerError, w, err)
		return
	}

	if _, err := pv.UDB.UpdateOne(ctx, bson.M{"_id": uID}, bson.M{
		"$set": bson.M{
			"user.password":  string(hashed),
			"user.updatedAt": primitive.NewDateTimeFromTime(time.Now()),
		},
	}); err != nil {
		config.ErrorStatus("failed to update password", http.StatusInternalServerError, w, err)
		return
	}

	_ = pv.PVDB.DeleteOne(ctx, bson.M{"_id": pending.ID})

	if pending.Email != "" {
		sendSensitiveEmailAsync(sensitiveEmail{
			To:        pending.Email,
			Subject:   "Lines Police CAD: Your Password Was Changed",
			PlainText: "The password on your Lines Police CAD account was just changed. If this wasn't you, contact support immediately at https://www.linespolice-cad.com/contact-us.",
			HTML:      templates.RenderPasswordChangedNotice(),
		})
	}

	writeJSONOK(w, "Password updated successfully")
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func writeJSONOK(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": message})
}
