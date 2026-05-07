package handlers

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"fmt"
	"math/big"
	"os"
	"time"

	"github.com/sendgrid/sendgrid-go"
	"github.com/sendgrid/sendgrid-go/helpers/mail"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// Tunables for sensitive-change verification flows. Centralized so deployments can audit them in one place.
const (
	sensitiveCodeTTL    = 15 * time.Minute
	sensitiveMaxRetries = 5
	sensitiveResendGap  = 60 * time.Second
)

// generateNumericCode returns a zero-padded 6-digit code drawn from crypto/rand.
func generateNumericCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// codesEqualConstantTime returns true iff a and b are byte-identical, in constant time.
func codesEqualConstantTime(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// timeFromBSON parses the loose interface{} value pendingVerifications stores for time fields
// (primitive.DateTime, time.Time, RFC3339 string). Returns zero time when the input is unrecognized.
func timeFromBSON(v interface{}) time.Time {
	switch t := v.(type) {
	case primitive.DateTime:
		return t.Time()
	case time.Time:
		return t
	case string:
		if parsed, err := time.Parse(time.RFC3339, t); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

// checkSensitiveCodeRateLimit enforces a 60-second gap between consecutive code requests for the
// same (userID, purpose). Returns nil when a new code may be issued.
func checkSensitiveCodeRateLimit(ctx context.Context, pvdb databases.PendingVerificationDatabase, userID primitive.ObjectID, purpose string) error {
	existing, err := pvdb.FindOne(ctx, bson.M{"userID": userID, "purpose": purpose})
	if err == mongo.ErrNoDocuments {
		return nil
	}
	if err != nil {
		return err
	}
	last := timeFromBSON(existing.CreatedAt)
	if !last.IsZero() && time.Since(last) < sensitiveResendGap {
		return fmt.Errorf("rate_limited")
	}
	return nil
}

// upsertSensitiveCode writes (or refreshes) the pendingVerifications row for a sensitive change.
// Keyed by (userID, purpose) so re-requests overwrite prior codes and reset attempt counters.
func upsertSensitiveCode(ctx context.Context, pvdb databases.PendingVerificationDatabase, userID primitive.ObjectID, purpose, currentEmail, newEmail, code string) error {
	now := time.Now()
	expires := now.Add(sensitiveCodeTTL)
	filter := bson.M{"userID": userID, "purpose": purpose}

	existing, err := pvdb.FindOne(ctx, filter)
	if err != nil && err != mongo.ErrNoDocuments {
		return err
	}

	setFields := bson.M{
		"code":      code,
		"createdAt": primitive.NewDateTimeFromTime(now),
		"expiresAt": primitive.NewDateTimeFromTime(expires),
		"attempts":  0,
		"email":     currentEmail,
		"newEmail":  newEmail,
	}

	if existing != nil {
		return pvdb.UpdateOne(ctx, filter, bson.M{"$set": setFields})
	}

	row := models.PendingVerification{
		ID:        primitive.NewObjectID(),
		Email:     currentEmail,
		Code:      code,
		Attempts:  0,
		CreatedAt: primitive.NewDateTimeFromTime(now),
		Purpose:   purpose,
		UserID:    userID,
		NewEmail:  newEmail,
		ExpiresAt: primitive.NewDateTimeFromTime(expires),
	}
	_, insertErr := pvdb.InsertOne(ctx, row)
	return insertErr
}

// sensitiveEmail bundles the four fields SendGrid needs for the simple transactional emails this
// flow sends. Caller-supplied HTML is rendered by templates/html.
type sensitiveEmail struct {
	To        string
	Subject   string
	PlainText string
	HTML      string
}

// sendSensitiveEmailAsync fires off a SendGrid send in the background. Intended for fire-and-forget
// notifications and verification codes — the request that triggered it should not block on delivery.
func sendSensitiveEmailAsync(em sensitiveEmail) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				zap.S().Errorw("panic in sendSensitiveEmail", "to", em.To, "subject", em.Subject, "panic", r)
			}
		}()

		apiKey := os.Getenv("SENDGRID_API_KEY")
		if apiKey == "" {
			zap.S().Errorw("SENDGRID_API_KEY not set", "to", em.To, "subject", em.Subject)
			return
		}

		from := mail.NewEmail("Lines Police CAD", "no-reply@linespolice-cad.com")
		to := mail.NewEmail("", em.To)
		message := mail.NewSingleEmail(from, em.Subject, to, em.PlainText, em.HTML)

		client := sendgrid.NewSendClient(apiKey)
		response, err := client.Send(message)
		if err != nil {
			zap.S().Errorw("failed to send sensitive email", "to", em.To, "subject", em.Subject, "error", err)
			return
		}
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			zap.S().Infow("sensitive email sent", "to", em.To, "subject", em.Subject, "statusCode", response.StatusCode)
		} else {
			zap.S().Warnw("sensitive email non-2xx", "to", em.To, "subject", em.Subject, "statusCode", response.StatusCode, "body", response.Body)
		}
	}()
}
