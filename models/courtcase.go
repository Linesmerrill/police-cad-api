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

	// Final judgment totals across every charge on every resolved item, computed
	// server-side from the dispositions. SentenceMode ("consecutive"|"concurrent")
	// governs how jail time is combined case-wide; fines always sum.
	SentenceMode         string  `json:"sentenceMode,omitempty" bson:"sentenceMode,omitempty"`
	TotalFine            float64 `json:"totalFine" bson:"totalFine"`
	TotalJailTimeSeconds int64   `json:"totalJailTimeSeconds" bson:"totalJailTimeSeconds"`
	TotalJailTimeLabel   string  `json:"totalJailTimeLabel" bson:"totalJailTimeLabel"`

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
	// Per-item sentencing totals after dispositions, recomputed server-side.
	TotalFine            float64            `json:"totalFine" bson:"totalFine"`
	TotalJailTimeSeconds int64              `json:"totalJailTimeSeconds" bson:"totalJailTimeSeconds"`
	TotalJailTimeLabel   string             `json:"totalJailTimeLabel" bson:"totalJailTimeLabel"`
	ResolvedAt           primitive.DateTime `json:"resolvedAt" bson:"resolvedAt"`
}

// ChargeResolution holds a judge's disposition for a single charge on a
// contested item. FineIndex is the charge's position in its source list
// (CriminalHistory.Fines for citations/warnings, arrestReport.chargesList for
// arrests). Name/category/original* are snapshots so the resolution is self-
// contained for totals + audit. Verdict is kept for back-compat and derives
// from Disposition (upheld|dismissed) for the source-record write-back.
type ChargeResolution struct {
	FineIndex        int     `json:"fineIndex" bson:"fineIndex"`
	Verdict          string  `json:"verdict" bson:"verdict"`                                       // "upheld" | "dismissed" (derived; back-compat)
	Disposition      string  `json:"disposition,omitempty" bson:"disposition,omitempty"`           // "upheld" | "dismissed" | "reduced" | "amended"
	Name             string  `json:"name,omitempty" bson:"name,omitempty"`                         // charge name snapshot
	Category         string  `json:"category,omitempty" bson:"category,omitempty"`                 // charge category snapshot
	OriginalAmount   float64 `json:"originalAmount,omitempty" bson:"originalAmount,omitempty"`     // original fine
	OriginalJailTime string  `json:"originalJailTime,omitempty" bson:"originalJailTime,omitempty"` // original jail (arrests only)
	FinalAmount      float64 `json:"finalAmount,omitempty" bson:"finalAmount,omitempty"`           // judge's final fine (reduced/amended)
	FinalJailTime    string  `json:"finalJailTime,omitempty" bson:"finalJailTime,omitempty"`       // judge's final jail (reduced/amended)
}

// Charge disposition values. Upheld/Dismissed match the legacy Verdict values;
// Reduced/Amended carry judge-entered FinalAmount/FinalJailTime.
const (
	DispositionUpheld    = "upheld"
	DispositionDismissed = "dismissed"
	DispositionReduced   = "reduced"
	DispositionAmended   = "amended"
)

// CourtCaseHistoryEntry records a single event in the court case lifecycle
type CourtCaseHistoryEntry struct {
	Action    string             `json:"action" bson:"action"` // "submitted", "assigned", "scheduled", "started", "completed"
	UserID    string             `json:"userID" bson:"userID"`
	UserName  string             `json:"userName" bson:"userName"`
	Notes     string             `json:"notes,omitempty" bson:"notes,omitempty"`
	Timestamp primitive.DateTime `json:"timestamp" bson:"timestamp"`
}
