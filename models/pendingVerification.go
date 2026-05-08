package models

import "go.mongodb.org/mongo-driver/bson/primitive"

// PendingVerification purposes. Empty/missing values are treated as PurposeSignup
// for backward compatibility with rows written before this field existed.
const (
	PurposeSignup         = "signup"
	PurposeEmailChange    = "email_change"
	PurposePasswordChange = "password_change"
)

// PendingVerification holds the structure for the pending verification collection in MongoDB
type PendingVerification struct {
	ID        primitive.ObjectID `json:"_id" bson:"_id"`
	Email     string             `json:"email" bson:"email"`
	Code      string             `json:"code" bson:"code"`
	Attempts  int                `json:"attempts" bson:"attempts"`
	CreatedAt interface{}        `json:"createdAt" bson:"createdAt"`

	// Sensitive-change fields. Empty on legacy signup rows.
	Purpose      string             `json:"purpose,omitempty" bson:"purpose,omitempty"`
	UserID       primitive.ObjectID `json:"userID,omitempty" bson:"userID,omitempty"`
	NewEmail     string             `json:"newEmail,omitempty" bson:"newEmail,omitempty"`
	ExpiresAt    interface{}        `json:"expiresAt,omitempty" bson:"expiresAt,omitempty"`
	RequestCount int                `json:"requestCount,omitempty" bson:"requestCount,omitempty"`
}
