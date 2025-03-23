package models

import (
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Community holds the structure for the community collection in mongo
type Community struct {
	ID      primitive.ObjectID `json:"_id" bson:"_id"`
	Details CommunityDetails   `json:"community" bson:"community"`
	Version int32              `json:"__v" bson:"__v"`
}

// CommunityDetails holds the structure for the inner community collection in mongo
type CommunityDetails struct {
	Name            string                 `json:"name" bson:"name"`
	OwnerID         string                 `json:"ownerID" bson:"ownerID"`
	Code            string                 `json:"code" bson:"code"`
	ActivePanics    map[string]interface{} `json:"activePanics" bson:"activePanics"`
	ActiveSignal100 bool                   `json:"activeSignal100" bson:"activeSignal100"`
	ImageLink       string                 `json:"imageLink" bson:"imageLink"`
	Visibility      string                 `json:"visibility" bson:"visibility"`
	PromotionalText string                 `json:"promotionalText" bson:"promotionalText"`
	InviteCodes     []InviteCode           `json:"inviteCodes" bson:"inviteCodes"`
	Roles           []Role                 `json:"roles" bson:"roles"`
	BanList         []string               `json:"banList" bson:"banList"`
	Description     string                 `json:"description" bson:"description"`
	Events          []Event                `json:"events" bson:"events"`
	Departments     []Department           `json:"departments" bson:"departments"`
	Templates       []Template             `json:"templates" bson:"templates"`
	CreatedAt       primitive.DateTime     `json:"createdAt" bson:"createdAt"`
	UpdatedAt       primitive.DateTime     `json:"updatedAt" bson:"updatedAt"`
}

// Department holds the structure for a department
type Department struct {
	ID          primitive.ObjectID `json:"_id" bson:"_id"`
	Name        string             `json:"name" bson:"name"`
	Description string             `json:"description" bson:"description"`
	Image       string             `json:"image" bson:"image"`
	Members     []MemberStatus     `json:"members" bson:"members"`
	TemplateID  string             `json:"template" bson:"template"`
	CreatedAt   primitive.DateTime `json:"createdAt" bson:"createdAt"`
	UpdatedAt   primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
}

// Template holds the structure for a department template
type Template struct {
	ID          primitive.ObjectID `json:"_id" bson:"_id"`
	Name        string             `json:"name" bson:"name"`
	Description string             `json:"description" bson:"description"`
	Components  []Component        `json:"components" bson:"components"`
}

// Component holds the structure for a template component
type Component struct {
	ID   primitive.ObjectID `json:"_id" bson:"_id"`
	Name string             `json:"name" bson:"name"`
}

// Event holds the structure for an event
type Event struct {
	ID            primitive.ObjectID `json:"_id" bson:"_id"`
	CommunityID   string             `json:"communityID" bson:"communityID"`
	Title         string             `json:"title" bson:"title"`
	ScheduledDate string             `json:"scheduledDate" bson:"scheduledDate"`
	Image         string             `json:"image" bson:"image"`
	Location      string             `json:"location" bson:"location"`
	Description   string             `json:"description" bson:"description"`
	Host          string             `json:"host" bson:"host"`
	CoHosts       []string           `json:"coHosts" bson:"coHosts"`
	Required      bool               `json:"required" bson:"required"`
	Attendance    Attendance         `json:"attendance" bson:"attendance"`
	CreatedAt     primitive.DateTime `json:"createdAt" bson:"createdAt"`
	UpdatedAt     primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
}

// MemberStatus holds the structure for a member status
type MemberStatus struct {
	UserID string `json:"userID" bson:"userID"`
	Status string `json:"status" bson:"status"`
}

// Attendance holds the structure for attendance
type Attendance struct {
	Confirmed []string `json:"confirmed" bson:"confirmed"`
	Maybe     []string `json:"maybe" bson:"maybe"`
	Declined  []string `json:"declined" bson:"declined"`
}

// InviteCode holds the structure for invite codes
type InviteCode struct {
	Code          string             `json:"code" bson:"code"`
	RemainingUses int                `json:"remainingUses" bson:"remainingUses"`
	CreatedBy     string             `json:"createdBy" bson:"createdBy"`
	CreatedAt     primitive.DateTime `json:"createdAt" bson:"createdAt"`
}

// Role holds the structure for a role
type Role struct {
	ID          primitive.ObjectID `json:"_id" bson:"_id"`
	Name        string             `json:"name" bson:"name"`
	Members     []string           `json:"members" bson:"members"`
	Permissions []Permission       `json:"permissions" bson:"permissions"`
}

// Permission holds the structure for a permission
type Permission struct {
	ID          primitive.ObjectID `json:"_id" bson:"_id"`
	Name        string             `json:"name" bson:"name"`
	Description string             `json:"description" bson:"description"`
	Enabled     bool               `json:"enabled" bson:"enabled"`
}
