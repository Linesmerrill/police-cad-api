package models

import (
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Community holds the structure for the community collection in mongo
type Community struct {
	ID      string           `json:"_id" bson:"_id"`
	Details CommunityDetails `json:"community" bson:"community"`
	Version int32            `json:"__v" bson:"__v"`
}

// CommunityDetails holds the structure for the inner community collection in mongo
type CommunityDetails struct {
	Name            string                 `json:"name"`
	OwnerID         string                 `json:"ownerID"`
	Code            string                 `json:"code"`
	ActivePanics    map[string]interface{} `json:"activePanics"`
	ActiveSignal100 bool                   `json:"activeSignal100"`
	ImageLink       string                 `json:"imageLink"`
	LastAccessed    string                 `json:"lastAccessed"`
	Description     string                 `json:"description"`
	Events          []Event                `json:"events"`
	CreatedAt       primitive.DateTime     `json:"createdAt"`
	UpdatedAt       primitive.DateTime     `json:"updatedAt"`
}

// Event holds the structure for an event
type Event struct {
	ID            primitive.ObjectID `json:"_id" bson:"_id"`
	CommunityID   string             `json:"communityID" bson:"communityID"`
	Title         string             `json:"title" bson:"title"`
	ScheduledDate primitive.DateTime `json:"scheduledDate" bson:"scheduledDate"`
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

// Attendance holds the structure for attendance
type Attendance struct {
	Confirmed []string `json:"confirmed" bson:"confirmed"`
	Maybe     []string `json:"maybe" bson:"maybe"`
	Declined  []string `json:"declined" bson:"declined"`
}
