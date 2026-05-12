package models

import "go.mongodb.org/mongo-driver/bson/primitive"

// CourtCase holds the structure for the courtcases collection in mongo
type CourtCase struct {
	ID      primitive.ObjectID `json:"_id" bson:"_id"`
	Details CourtCaseDetails   `json:"courtCase" bson:"courtCase"`
	Version int32              `json:"__v" bson:"__v"`
}

// CourtCaseDetails holds the structure for the inner court case details
type CourtCaseDetails struct {
	// Human-readable case number, format CC-YYYY-NNNNNN, unique per community
	CaseNumber string `json:"caseNumber" bson:"caseNumber"`

	// Civilian
	CivilianID   string `json:"civilianID" bson:"civilianID"`
	CivilianName string `json:"civilianName" bson:"civilianName"`
	UserID       string `json:"userID" bson:"userID"` // the user who owns this civilian

	// Contested items (references to criminal history entries or arrest reports)
	ContestedItems []ContestedItem `json:"contestedItems" bson:"contestedItems"`

	// Civilian's statement for contesting
	Statement string `json:"statement" bson:"statement"`

	// Judicial assignment
	DepartmentID string `json:"departmentID" bson:"departmentID"` // which judicial dept handles this
	CommunityID  string `json:"communityID" bson:"communityID"`

	// Judge
	JudgeID    string `json:"judgeID" bson:"judgeID"`
	JudgeName  string `json:"judgeName" bson:"judgeName"`
	JudgeNotes string `json:"judgeNotes" bson:"judgeNotes"`

	// Scheduling
	ScheduledDate  primitive.DateTime `json:"scheduledDate" bson:"scheduledDate"`
	CourtSessionID string             `json:"courtSessionID" bson:"courtSessionID"` // linked session once created

	// Status: "submitted", "in_review", "scheduled", "in_progress", "completed"
	Status string `json:"status" bson:"status"`

	// Resolution per contested item
	Resolutions []CaseResolution `json:"resolutions" bson:"resolutions"`

	// Audit trail
	History []CourtCaseHistoryEntry `json:"history" bson:"history"`

	CreatedAt primitive.DateTime `json:"createdAt" bson:"createdAt"`
	UpdatedAt primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
}

// ContestedItem represents a single record being contested
type ContestedItem struct {
	ItemID   string `json:"itemID" bson:"itemID"`     // ObjectID of the criminal history entry or arrest report
	ItemType string `json:"itemType" bson:"itemType"` // "citation", "warning", "arrest"
	Summary  string `json:"summary" bson:"summary"`   // human-readable summary of the charge
}

// CaseResolution holds the judge's resolution for a single contested item.
//
// For citations/warnings with multiple charges, ChargeResolutions carries
// the per-fine verdict; Verdict is the derived top-level outcome ("upheld",
// "dismissed", or "partial" when the charges are mixed). For arrests (and
// citations without charges) ChargeResolutions is nil and Verdict is the
// only thing that matters.
type CaseResolution struct {
	ItemID            string             `json:"itemID" bson:"itemID"`         // matches ContestedItem.ItemID
	ItemType          string             `json:"itemType" bson:"itemType"`     // "citation", "warning", "arrest"
	Verdict           string             `json:"verdict" bson:"verdict"`       // "dismissed", "upheld", "partial"
	JudgeNotes        string             `json:"judgeNotes" bson:"judgeNotes"` // judge's notes for this resolution
	ChargeResolutions []ChargeResolution `json:"chargeResolutions,omitempty" bson:"chargeResolutions,omitempty"`
	ResolvedAt        primitive.DateTime `json:"resolvedAt" bson:"resolvedAt"`
}

// ChargeResolution holds the verdict for a single fine within a citation /
// warning. FineIndex references the position in CriminalHistory.Fines.
type ChargeResolution struct {
	FineIndex int    `json:"fineIndex" bson:"fineIndex"`
	Verdict   string `json:"verdict" bson:"verdict"` // "upheld" | "dismissed"
}

// CourtCaseHistoryEntry records a single event in the court case lifecycle
type CourtCaseHistoryEntry struct {
	Action    string             `json:"action" bson:"action"` // "submitted", "assigned", "scheduled", "started", "completed"
	UserID    string             `json:"userID" bson:"userID"`
	UserName  string             `json:"userName" bson:"userName"`
	Notes     string             `json:"notes,omitempty" bson:"notes,omitempty"`
	Timestamp primitive.DateTime `json:"timestamp" bson:"timestamp"`
}
