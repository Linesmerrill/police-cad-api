package models

import "go.mongodb.org/mongo-driver/bson/primitive"

// Warrant holds the structure for the warrant collection in mongo
type Warrant struct {
	ID      primitive.ObjectID `json:"_id" bson:"_id"`
	Details WarrantDetails     `json:"warrant" bson:"warrant"`
	Version int32              `json:"__v" bson:"__v"`
}

// Warrant statuses, as the website and the mobile app already write them.
//
// Named here so a handler can compare against a constant rather than a literal.
// The strings are unchanged: existing documents hold these exact values and
// renaming one would strand every warrant already in the database.
const (
	WarrantStatusPending   = "pending"
	WarrantStatusApproved  = "approved"
	WarrantStatusDenied    = "denied"
	WarrantStatusExecuted  = "executed"
	WarrantStatusExpired   = "expired"
	WarrantStatusWithdrawn = "withdrawn"
)

// WarrantStatuses lists every status, for validating a filter and for the
// error message that names the valid ones.
func WarrantStatuses() []string {
	return []string{
		WarrantStatusPending,
		WarrantStatusApproved,
		WarrantStatusDenied,
		WarrantStatusExecuted,
		WarrantStatusExpired,
		WarrantStatusWithdrawn,
	}
}

// WarrantHistoryEntry records a single event in the warrant lifecycle
type WarrantHistoryEntry struct {
	Action    string             `json:"action" bson:"action"`       // "created", "approved", "denied", "edited", "resubmitted", "executed", "withdrawn"
	UserID    string             `json:"userID" bson:"userID"`
	UserName  string             `json:"userName" bson:"userName"`
	Notes     string             `json:"notes,omitempty" bson:"notes,omitempty"`
	Timestamp primitive.DateTime `json:"timestamp" bson:"timestamp"`
}

// WarrantDetails holds the structure for the inner warrant structure as
// defined in the warrant collection in mongo
type WarrantDetails struct {
	// Type & Status
	WarrantType string `json:"warrantType" bson:"warrantType"` // "arrest", "search", "bench"
	Status      string `json:"status" bson:"status"`           // "pending", "approved", "denied", "executed", "expired", "withdrawn"

	// Subject
	AccusedID        string `json:"accusedID" bson:"accusedID"`
	AccusedFirstName string `json:"accusedFirstName" bson:"accusedFirstName"`
	AccusedLastName  string `json:"accusedLastName" bson:"accusedLastName"`

	// Probable Cause & Charges
	ProbableCause string   `json:"probableCause" bson:"probableCause"`
	Charges       []string `json:"charges" bson:"charges"`

	// Search Warrant Specific
	SearchLocation string `json:"searchLocation" bson:"searchLocation"`

	// Requesting Officer
	RequestingOfficerID   string `json:"requestingOfficerID" bson:"requestingOfficerID"`
	RequestingOfficerName string `json:"requestingOfficerName" bson:"requestingOfficerName"`

	// Judge Review
	JudgeID    string             `json:"judgeID" bson:"judgeID"`
	JudgeName  string             `json:"judgeName" bson:"judgeName"`
	JudgeNotes string             `json:"judgeNotes" bson:"judgeNotes"`
	ReviewedAt primitive.DateTime `json:"reviewedAt" bson:"reviewedAt"`

	// Execution
	ExecutingOfficerID   string             `json:"executingOfficerID" bson:"executingOfficerID"`
	ExecutingOfficerName string             `json:"executingOfficerName" bson:"executingOfficerName"`
	ExecutedAt           primitive.DateTime `json:"executedAt" bson:"executedAt"`

	// History
	History []WarrantHistoryEntry `json:"history" bson:"history"`

	// Department context
	DepartmentID string `json:"departmentId,omitempty" bson:"departmentId,omitempty"` // department the officer was active in

	// Integration provenance, set only when a Developer API key requested the
	// warrant. Source scopes ExternalID the same way it does on a dispatch
	// call, and the pair is what makes a request idempotent across a game
	// server's retries. A warrant a person raised on the website carries none
	// of these, which is also how the two are told apart.
	Source         string        `json:"source,omitempty" bson:"source,omitempty"`
	ExternalID     string        `json:"externalId,omitempty" bson:"externalId,omitempty"`
	CreatedByKeyID string        `json:"createdByKeyId,omitempty" bson:"createdByKeyId,omitempty"`
	RecordSource   *RecordSource `json:"recordSource,omitempty" bson:"recordSource,omitempty"`

	// Metadata
	ActiveCommunityID string             `json:"activeCommunityID" bson:"activeCommunityID"`
	ExpirationDate    primitive.DateTime `json:"expirationDate" bson:"expirationDate"`
	CreatedAt         primitive.DateTime `json:"createdAt" bson:"createdAt"`
	UpdatedAt         primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
}
