package models

import "go.mongodb.org/mongo-driver/bson/primitive"

// Warrant holds the structure for the warrant collection in mongo
type Warrant struct {
	ID      primitive.ObjectID `json:"_id" bson:"_id"`
	Details WarrantDetails     `json:"warrant" bson:"warrant"`
	Version int32              `json:"__v" bson:"__v"`
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

	// Metadata
	ActiveCommunityID string             `json:"activeCommunityID" bson:"activeCommunityID"`
	ExpirationDate    primitive.DateTime `json:"expirationDate" bson:"expirationDate"`
	CreatedAt         primitive.DateTime `json:"createdAt" bson:"createdAt"`
	UpdatedAt         primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
}
