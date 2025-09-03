package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/sendgrid/sendgrid-go"
	"github.com/sendgrid/sendgrid-go/helpers/mail"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

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

	// Check if email already exists in pendingVerifications
	_, err := pv.PVDB.FindOne(context.Background(), bson.M{"email": requestBody.Email})
	if err == nil {
		http.Error(w, `{"success": false, "message": "Verification already in progress for this email"}`, http.StatusBadRequest)
		return
	}

	// Check if email exists in the users collection
	existingUser := models.User{}
	err = pv.UDB.FindOne(context.Background(), bson.M{"email": requestBody.Email}).Decode(&existingUser)
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
	_, err = pv.PVDB.InsertOne(context.Background(), newPending)
	if err != nil {
		config.ErrorStatus("failed to create pending verification", http.StatusInternalServerError, w, err)
		return
	}

	// Send email with the code
	from := mail.NewEmail("Lines Police CAD", "no-reply@linespolice-cad.com")
	subject := "LPC-APP Email Verification Code"
	to := mail.NewEmail("", requestBody.Email)
	plainTextContent := "Verification code: " + code + ". This code will expire in 24 hours."
	htmlContent := templates.RenderCode(code)
	message := mail.NewSingleEmail(from, subject, to, plainTextContent, htmlContent)
	client := sendgrid.NewSendClient(os.Getenv("SENDGRID_API_KEY"))
	response, err := client.Send(message)
	if err != nil {
		log.Println(err)
	} else {
		fmt.Println(response.StatusCode)
		fmt.Println(response.Body)
		fmt.Println(response.Headers)
	}

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

	// Find the pending verification by email
	pendingVerification, err := pv.PVDB.FindOne(context.Background(), bson.M{"email": requestBody.Email})
	if err != nil {
		config.ErrorStatus("failed to find pending verification", http.StatusNotFound, w, err)
		return
	}

	// Check if the code matches
	if pendingVerification.Code != requestBody.Code {
		// Increment the attempts
		err := pv.PVDB.UpdateOne(
			context.Background(),
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
	err = pv.PVDB.DeleteOne(context.Background(), bson.M{"email": requestBody.Email})
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

	// Check if the user already exists in the user database
	existingUser := models.User{}
	err := pv.UDB.FindOne(context.Background(), bson.M{"email": requestBody.Email}).Decode(&existingUser)
	if err == nil {
		http.Error(w, `{"success": false, "message": "Email already exists"}`, http.StatusBadRequest)
		return
	}

	// Check if the email exists in the pendingVerification database
	pendingVerification, err := pv.PVDB.FindOne(context.Background(), bson.M{"email": requestBody.Email})
	if err != nil {
		config.ErrorStatus("failed to find pending verification", http.StatusNotFound, w, err)
		return
	}

	// Check if 1 minute has passed since the last code was sent
	createdAt, ok := pendingVerification.CreatedAt.(primitive.DateTime)
	if !ok {
		http.Error(w, `{"success": false, "message": "Invalid CreatedAt format"}`, http.StatusInternalServerError)
		return
	}
	if time.Since(createdAt.Time()) < time.Minute {
		http.Error(w, `{"success": false, "message": "Please wait at least 1 minute before requesting a new code"}`, http.StatusTooManyRequests)
		return
	}

	// Generate a new 6-digit code
	code := fmt.Sprintf("%06d", 100000+rand.Intn(900000))
	pendingVerification.Code = code
	pendingVerification.CreatedAt = primitive.NewDateTimeFromTime(time.Now())

	// Update the pendingVerification record in the database
	update := bson.M{
		"$set": bson.M{
			"code":      pendingVerification.Code,
			"createdAt": pendingVerification.CreatedAt,
			"attempts":  0,
		},
	}
	err = pv.PVDB.UpdateOne(context.Background(), bson.M{"email": requestBody.Email}, update)
	if err != nil {
		config.ErrorStatus("failed to update pending verification", http.StatusInternalServerError, w, err)
		return
	}

	// Send the email with the new code
	from := mail.NewEmail("Lines Police CAD", "no-reply@linespolice-cad.com")
	subject := "LPC-APP Email Verification Code"
	to := mail.NewEmail("", requestBody.Email)
	plainTextContent := "Verification code: " + code + ". This code will expire in 24 hours."
	htmlContent := templates.RenderCode(code)
	message := mail.NewSingleEmail(from, subject, to, plainTextContent, htmlContent)
	client := sendgrid.NewSendClient(os.Getenv("SENDGRID_API_KEY"))
	response, err := client.Send(message)
	if err != nil {
		log.Println(err)
	} else {
		fmt.Println(response.StatusCode)
		fmt.Println(response.Body)
		fmt.Println(response.Headers)
	}

	// Respond with success
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"success": true}`))
}
