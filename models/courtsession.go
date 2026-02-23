package models

import "go.mongodb.org/mongo-driver/bson/primitive"

// CourtSession holds the structure for the courtsessions collection in mongo
type CourtSession struct {
	ID      primitive.ObjectID   `json:"_id" bson:"_id"`
	Details CourtSessionDetails  `json:"courtSession" bson:"courtSession"`
	Version int32                `json:"__v" bson:"__v"`
}

// CourtSessionDetails holds the structure for the inner court session details
type CourtSessionDetails struct {
	CommunityID  string `json:"communityID" bson:"communityID"`
	DepartmentID string `json:"departmentID" bson:"departmentID"` // judicial department

	JudgeID   string `json:"judgeID" bson:"judgeID"`
	JudgeName string `json:"judgeName" bson:"judgeName"`

	// Title for this court session (e.g., "Morning Docket - Feb 21")
	Title string `json:"title" bson:"title"`

	// Docket — ordered list of cases for this session
	Docket []DocketEntry `json:"docket" bson:"docket"`

	// Status: "scheduled", "in_progress", "completed"
	Status string `json:"status" bson:"status"`

	// Participants currently in the session
	Participants []SessionParticipant `json:"participants" bson:"participants"`

	ScheduledStart primitive.DateTime `json:"scheduledStart" bson:"scheduledStart"`
	ScheduledEnd   primitive.DateTime `json:"scheduledEnd" bson:"scheduledEnd"`
	StartedAt      primitive.DateTime `json:"startedAt" bson:"startedAt"`
	EndedAt        primitive.DateTime `json:"endedAt" bson:"endedAt"`

	CreatedAt primitive.DateTime `json:"createdAt" bson:"createdAt"`
	UpdatedAt primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
}

// DocketEntry represents a single case in the court session's docket
type DocketEntry struct {
	CourtCaseID  string `json:"courtCaseID" bson:"courtCaseID"`
	CivilianName string `json:"civilianName" bson:"civilianName"` // denormalized for display
	UserID       string `json:"userID" bson:"userID"`             // user who owns the civilian (for defendant role)
	Order        int    `json:"order" bson:"order"`
	Status       string `json:"status" bson:"status"` // "pending", "active", "completed"
}

// SessionParticipant represents a user present in a court session
type SessionParticipant struct {
	UserID   string             `json:"userID" bson:"userID"`
	UserName string             `json:"userName" bson:"userName"`
	Role     string             `json:"role" bson:"role"` // "judge", "defendant", "spectator"
	JoinedAt primitive.DateTime `json:"joinedAt" bson:"joinedAt"`
}
