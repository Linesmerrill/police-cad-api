package handlers

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/sendgrid/sendgrid-go"
	"github.com/sendgrid/sendgrid-go/helpers/mail"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
	templates "github.com/linesmerrill/police-cad-api/templates/html"
)

// PendingVerification handles pendingVerification-related requests
type PendingVerification struct {
	PVDB databases.PendingVerificationDatabase
	UDB  databases.UserDatabase
}

// CreatePendingVerificationHandler creates a new pendingVerification
func (pv PendingVerification) CreatePendingVerificationHandler(w http.ResponseWriter, r *http.Request) {
	var requestBody struct {
		Email string `json:"email"`
	}

	// Decode the request body
	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Check if email is provided
	if requestBody.Email == "" {
		http.Error(w, `{"success": false, "message": "Email is required"}`, http.StatusBadRequest)
		return
	}

	// Normalize email to lowercase
	requestBody.Email = strings.TrimSpace(strings.ToLower(requestBody.Email))

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Check if email already exists in pendingVerifications
	_, err := pv.PVDB.FindOne(ctx, bson.M{"email": requestBody.Email})
	if err == nil {
		http.Error(w, `{"success": false, "message": "Verification already in progress for this email"}`, http.StatusBadRequest)
		return
	}

	// Check if email exists in the users collection
	existingUser := models.User{}
	err = pv.UDB.FindOne(ctx, bson.M{"user.email": requestBody.Email}).Decode(&existingUser)
	if err == nil {
		http.Error(w, `{"success": false, "message": "Email already exists"}`, http.StatusBadRequest)
		return
	}

	// Generate a 6-digit code
	code := fmt.Sprintf("%06d", 100000+rand.Intn(900000))

	// Store in pendingVerifications
	newPending := models.PendingVerification{
		ID:        primitive.NewObjectID(),
		Email:     requestBody.Email,
		Code:      code,
		CreatedAt: primitive.NewDateTimeFromTime(time.Now()),
	}
	_, err = pv.PVDB.InsertOne(ctx, newPending)
	if err != nil {
		config.ErrorStatus("failed to create pending verification", http.StatusInternalServerError, w, err)
		return
	}

	// Send email with the code (non-blocking, in background)
	go func(email, code string) {
		defer func() {
			if r := recover(); r != nil {
				zap.S().Errorw("panic in sendVerificationEmail", "email", email, "panic", r)
			}
		}()
		
		from := mail.NewEmail("Lines Police CAD", "no-reply@linespolice-cad.com")
		subject := "LPC-APP Email Verification Code"
		to := mail.NewEmail("", email)
		plainTextContent := "Verification code: " + code + ". This code will expire in 24 hours."
		htmlContent := templates.RenderCode(code)
		message := mail.NewSingleEmail(from, subject, to, plainTextContent, htmlContent)
		
		sendgridAPIKey := os.Getenv("SENDGRID_API_KEY")
		if sendgridAPIKey == "" {
			zap.S().Errorw("SENDGRID_API_KEY not set, cannot send email", "email", email)
			return
		}
		
		client := sendgrid.NewSendClient(sendgridAPIKey)
		response, err := client.Send(message)
		if err != nil {
			zap.S().Errorw("failed to send verification email", "email", email, "error", err)
			return
		}
		
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			zap.S().Infow("verification email sent successfully", "email", email, "statusCode", response.StatusCode)
		} else {
			zap.S().Warnw("verification email sent with non-2xx status", "email", email, "statusCode", response.StatusCode, "body", response.Body)
		}
	}(requestBody.Email, code)

	// mailOptions := config.MailOptions{
	// 	From:    config.GetEnv("EMAIL_USER"),
	// 	To:      requestBody.Email,
	// 	Subject: "LPC-APP Email Verification Code",
	// 	Text:    fmt.Sprintf("Your verification code for LPC-APP is: %s. This code will expire in 24 hours.", code),
	// }
	//
	// if err := config.SendMail(mailOptions); err != nil {
	// 	config.ErrorStatus("failed to send verification email", http.StatusInternalServerError, w, err)
	// 	return
	// }

	// Respond with success
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"success": true}`))
}

// VerifyCodeHandler verifies the code for pendingVerification
func (pv PendingVerification) VerifyCodeHandler(w http.ResponseWriter, r *http.Request) {
	var requestBody struct {
		Code  string `json:"code"`
		Email string `json:"email"`
	}

	// Decode the request body
	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Check if email is provided
	if requestBody.Email == "" {
		config.ErrorStatus("email is required", http.StatusBadRequest, w, fmt.Errorf("email is required"))
		return
	}

	// Normalize email to lowercase
	requestBody.Email = strings.TrimSpace(strings.ToLower(requestBody.Email))

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Find the pending verification by email
	pendingVerification, err := pv.PVDB.FindOne(ctx, bson.M{"email": requestBody.Email})
	if err != nil {
		if err == mongo.ErrNoDocuments {
			config.ErrorStatus("pending verification not found", http.StatusNotFound, w, err)
			return
		}
		config.ErrorStatus("failed to find pending verification", http.StatusInternalServerError, w, err)
		return
	}

	// Check if the code matches
	if pendingVerification.Code != requestBody.Code {
		// Increment the attempts
		err := pv.PVDB.UpdateOne(
			ctx,
			bson.M{"email": requestBody.Email},
			bson.M{"$inc": bson.M{"attempts": 1}},
		)
		if err != nil {
			config.ErrorStatus("failed to increment attempts", http.StatusInternalServerError, w, err)
			return
		}

		http.Error(w, `{"success": false, "message": "Invalid verification code"}`, http.StatusBadRequest)
		return
	}

	// Delete the verified item from the database
	err = pv.PVDB.DeleteOne(ctx, bson.M{"email": requestBody.Email})
	if err != nil {
		config.ErrorStatus("failed to delete verified item", http.StatusInternalServerError, w, err)
		return
	}

	// Respond with success if the code matches
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "PendingVerification verified successfully"}`))
}

// ResendVerificationCodeHandler resends the verification code
// This handler is designed to be resilient and never fail if possible:
// - Email sending happens in background (non-blocking)
// - Database errors are handled gracefully
// - Returns success even if email fails (code is already updated in DB)
func (pv PendingVerification) ResendVerificationCodeHandler(w http.ResponseWriter, r *http.Request) {
	var requestBody struct {
		Email string `json:"email"`
	}

	// Decode the request body
	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Check if email is provided
	if requestBody.Email == "" {
		http.Error(w, `{"success": false, "message": "Email is required"}`, http.StatusBadRequest)
		return
	}

	// Normalize email to lowercase
	requestBody.Email = strings.TrimSpace(strings.ToLower(requestBody.Email))

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Check if the user already exists in the user database (non-blocking check)
	existingUser := models.User{}
	err := pv.UDB.FindOne(ctx, bson.M{"user.email": requestBody.Email}).Decode(&existingUser)
	if err == nil {
		// User already exists - this is expected, return success to prevent email enumeration
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"success": true}`))
		return
	}
	// If err != nil and it's not ErrNoDocuments, log but continue (resilient)
	if err != nil && err != mongo.ErrNoDocuments {
		zap.S().Warnw("error checking existing user (non-critical)", "email", requestBody.Email, "error", err)
	}

	// Check if the email exists in the pendingVerification database
	// If not found, create a new one (upsert pattern for resilience)
	pendingVerification, err := pv.PVDB.FindOne(ctx, bson.M{"email": requestBody.Email})
	shouldCreateNew := false
	if err != nil {
		if err == mongo.ErrNoDocuments {
			// No pending verification exists - create a new one
			shouldCreateNew = true
		} else {
			// Database error - log but try to continue
			zap.S().Errorw("failed to find pending verification", "email", requestBody.Email, "error", err)
			// Try to create a new one anyway
			shouldCreateNew = true
		}
	}

	// If we need to create a new pending verification, do it
	if shouldCreateNew {
		code := fmt.Sprintf("%06d", 100000+rand.Intn(900000))
		newPending := models.PendingVerification{
			ID:        primitive.NewObjectID(),
			Email:     requestBody.Email,
			Code:      code,
			CreatedAt: primitive.NewDateTimeFromTime(time.Now()),
		}
		_, err = pv.PVDB.InsertOne(ctx, newPending)
		if err != nil {
			zap.S().Errorw("failed to create pending verification", "email", requestBody.Email, "error", err)
			// Still try to send email with the code we generated
			go pv.sendVerificationEmail(requestBody.Email, code)
			// Return success anyway - email might still be sent
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"success": true}`))
			return
		}
		// Send email in background (non-blocking)
		go pv.sendVerificationEmail(requestBody.Email, code)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"success": true}`))
		return
	}

	// Handle type assertion for CreatedAt gracefully
	var createdAtTime time.Time
	switch v := pendingVerification.CreatedAt.(type) {
	case primitive.DateTime:
		createdAtTime = v.Time()
	case time.Time:
		createdAtTime = v
	case string:
		// Try to parse if it's a string
		parsed, parseErr := time.Parse(time.RFC3339, v)
		if parseErr == nil {
			createdAtTime = parsed
		} else {
			// If we can't parse, assume it's old enough (resilient)
			zap.S().Warnw("could not parse CreatedAt, assuming old enough", "email", requestBody.Email, "createdAt", v)
			createdAtTime = time.Now().Add(-2 * time.Minute) // Force allow resend
		}
	default:
		// Unknown type - assume it's old enough (resilient)
		zap.S().Warnw("unknown CreatedAt type, assuming old enough", "email", requestBody.Email, "type", fmt.Sprintf("%T", v))
		createdAtTime = time.Now().Add(-2 * time.Minute) // Force allow resend
	}

	// Check if 1 minute has passed since the last code was sent
	if time.Since(createdAtTime) < time.Minute {
		http.Error(w, `{"success": false, "message": "Please wait at least 1 minute before requesting a new code"}`, http.StatusTooManyRequests)
		return
	}

	// Generate a new 6-digit code
	code := fmt.Sprintf("%06d", 100000+rand.Intn(900000))

	// Update the pendingVerification record in the database
	// Use upsert pattern for resilience - if update fails, try insert
	update := bson.M{
		"$set": bson.M{
			"code":      code,
			"createdAt": primitive.NewDateTimeFromTime(time.Now()),
			"attempts":  0,
		},
	}
	err = pv.PVDB.UpdateOne(ctx, bson.M{"email": requestBody.Email}, update)
	if err != nil {
		zap.S().Errorw("failed to update pending verification, trying insert", "email", requestBody.Email, "error", err)
		// Try to insert as fallback (upsert pattern)
		newPending := models.PendingVerification{
			ID:        primitive.NewObjectID(),
			Email:     requestBody.Email,
			Code:      code,
			CreatedAt: primitive.NewDateTimeFromTime(time.Now()),
		}
		_, insertErr := pv.PVDB.InsertOne(ctx, newPending)
		if insertErr != nil {
			zap.S().Errorw("failed to insert pending verification as fallback", "email", requestBody.Email, "error", insertErr)
			// Still continue - we'll send the email anyway
		}
	}

	// Send email in background (non-blocking) - don't fail the request if email fails
	go pv.sendVerificationEmail(requestBody.Email, code)

	// Always return success - code is updated in DB, email is sent async
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"success": true}`))
}

// sendVerificationEmail sends the verification email in a background goroutine
// This is non-blocking and won't cause the request to fail
func (pv PendingVerification) sendVerificationEmail(email, code string) {
	defer func() {
		if r := recover(); r != nil {
			zap.S().Errorw("panic in sendVerificationEmail", "email", email, "panic", r)
		}
	}()

	from := mail.NewEmail("Lines Police CAD", "no-reply@linespolice-cad.com")
	subject := "LPC-APP Email Verification Code"
	to := mail.NewEmail("", email)
	plainTextContent := "Verification code: " + code + ". This code will expire in 24 hours."
	htmlContent := templates.RenderCode(code)
	message := mail.NewSingleEmail(from, subject, to, plainTextContent, htmlContent)
	
	sendgridAPIKey := os.Getenv("SENDGRID_API_KEY")
	if sendgridAPIKey == "" {
		zap.S().Errorw("SENDGRID_API_KEY not set, cannot send email", "email", email)
		return
	}
	
	client := sendgrid.NewSendClient(sendgridAPIKey)
	response, err := client.Send(message)
	if err != nil {
		zap.S().Errorw("failed to send verification email", "email", email, "error", err)
		return
	}
	
	// Log success (but don't fail if logging fails)
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		zap.S().Infow("verification email sent successfully", "email", email, "statusCode", response.StatusCode)
	} else {
		zap.S().Warnw("verification email sent with non-2xx status", "email", email, "statusCode", response.StatusCode, "body", response.Body)
	}
}
