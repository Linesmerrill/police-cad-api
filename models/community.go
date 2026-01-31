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
	Name                   string                  `json:"name" bson:"name"`
	OwnerID                string                  `json:"ownerID" bson:"ownerID"`
	Code                   string                  `json:"code" bson:"code"`
	ActivePanics           map[string]interface{}  `json:"activePanics" bson:"activePanics"`
	ActiveSignal100        bool                    `json:"activeSignal100" bson:"activeSignal100"`
	Signal100              Signal100Data           `json:"signal100" bson:"signal100"`
	ImageLink              string                  `json:"imageLink" bson:"imageLink"`
	MapLink                string                  `json:"mapLink" bson:"mapLink"`
	Visibility             string                  `json:"visibility" bson:"visibility"`
	PromotionalText        string                  `json:"promotionalText" bson:"promotionalText"`
	PromotionalDescription string                  `json:"promotionalDescription" bson:"promotionalDescription"`
	InviteCodeIds          []string                `json:"inviteCodeIds" bson:"inviteCodeIds"`
	Tags                   []string                `json:"tags" bson:"tags"`
	Roles                  []Role                  `json:"roles" bson:"roles"`
	BanList                []string                `json:"banList" bson:"banList"`
	Description            string                  `json:"description" bson:"description"`
	Members                map[string]MemberDetail `json:"members" bson:"members"`
	MembersCount           int                     `json:"membersCount" bson:"membersCount"`
	Events                 []Event                 `json:"events" bson:"events"`
	Departments            []Department            `json:"departments" bson:"departments"`
	TenCodes               []TenCodes              `json:"tenCodes" bson:"tenCodes"`
	Fines                  CommunityFine           `json:"fines" bson:"fines"`
	Templates              []Template              `json:"templates" bson:"templates"`
	Subscription           Subscription            `json:"subscription" bson:"subscription"`
	SubscriptionCreatedBy  string                  `json:"subscriptionCreatedBy" bson:"subscriptionCreatedBy"`
	Analytics              CommunityAnalytics      `json:"analytics" bson:"analytics"`
	ActivityLevel          string                  `json:"activityLevel" bson:"activityLevel"`
	CivilianCreationLimitsEnabled bool             `json:"civilianCreationLimitsEnabled" bson:"civilianCreationLimitsEnabled"`
	CivilianCreationLimit  int                     `json:"civilianCreationLimit" bson:"civilianCreationLimit"`
	VehicleCreationLimitsEnabled bool              `json:"vehicleCreationLimitsEnabled" bson:"vehicleCreationLimitsEnabled"`
	VehicleCreationLimit  int                      `json:"vehicleCreationLimit" bson:"vehicleCreationLimit"`
	FirearmCreationLimitsEnabled bool              `json:"firearmCreationLimitsEnabled" bson:"firearmCreationLimitsEnabled"`
	FirearmCreationLimit  int                      `json:"firearmCreationLimit" bson:"firearmCreationLimit"`
	CivilianApprovalSystemEnabled bool             `json:"civilianApprovalSystemEnabled" bson:"civilianApprovalSystemEnabled"`
	ActivePanicAlerts       []PanicAlert          `json:"activePanicAlerts" bson:"activePanicAlerts"`
	CreatedAt              primitive.DateTime      `json:"createdAt" bson:"createdAt"`
	UpdatedAt              primitive.DateTime      `json:"updatedAt" bson:"updatedAt"`
}

// CommunityAnalytics holds the structure for community analytics
type CommunityAnalytics struct {
	CardViews     int `json:"cardViews" bson:"cardViews"`
	HomePageViews int `json:"homePageViews" bson:"homePageViews"`
}

// CommunityFine holds the structure for community fines
type CommunityFine struct {
	Categories []Category `json:"categories" bson:"categories"`
	Currency   string     `json:"currency" bson:"currency"` // USD, EUR, etc.
}

// Category holds the structure for a category
type Category struct {
	Name  string        `json:"name" bson:"name"` // Traffic Citations, Misdeameanors, etc.
	Fines []FineDetails `json:"fines" bson:"fines"`
}

// FineDetails holds the structure for a fine detail
type FineDetails struct {
	Name   string `json:"name" bson:"name"`     // Speeding, Public Intoxication, etc.
	Amount int    `json:"amount" bson:"amount"` // 50, 100, etc.
}

// MemberDetail holds the structure for a member detail
type MemberDetail struct {
	DepartmentID         string `json:"departmentID" bson:"departmentID"`
	TenCodeID            string `json:"tenCodeID" bson:"tenCodeID"`
	IsOnline             bool   `json:"isOnline" bson:"isOnline"`
	ActiveDepartmentID   string `json:"activeDepartmentId" bson:"activeDepartmentId"`
	ActiveDepartmentName string `json:"activeDepartmentName" bson:"activeDepartmentName"`
}

// Department holds the structure for a department
type Department struct {
	ID                primitive.ObjectID `json:"_id" bson:"_id"`
	Name              string             `json:"name" bson:"name"`
	Description       string             `json:"description" bson:"description"`
	Image             string             `json:"image" bson:"image"`
	ApprovalRequired  bool               `json:"approvalRequired" bson:"approvalRequired"`
	Members           []MemberStatus     `json:"members" bson:"members"`
	Template          Template           `json:"template" bson:"template"` // Legacy embedded template (for backward compatibility)
	TemplateRef       *TemplateReference `json:"templateRef" bson:"templateRef"` // New template reference system
	CreatedAt         primitive.DateTime `json:"createdAt" bson:"createdAt"`
	UpdatedAt         primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
	OnlineMemberCount int                `json:"onlineMemberCount" bson:"onlineMemberCount"`
}

// TenCodes holds the structure for ten-codes used in departments
type TenCodes struct {
	ID          primitive.ObjectID `json:"_id" bson:"_id"`
	Code        string             `json:"code" bson:"code"`
	Description string             `json:"description" bson:"description"`
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
	ID      primitive.ObjectID `json:"_id" bson:"_id"`
	Name    string             `json:"name" bson:"name"`
	Enabled bool               `json:"enabled" bson:"enabled"`
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
	UserID    string `json:"userID" bson:"userID"`
	Status    string `json:"status" bson:"status"`
	TenCodeID string `json:"tenCodeID" bson:"tenCodeID"`
}

// Attendance holds the structure for attendance
type Attendance struct {
	Confirmed []string `json:"confirmed" bson:"confirmed"`
	Maybe     []string `json:"maybe" bson:"maybe"`
	Declined  []string `json:"declined" bson:"declined"`
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

// Signal100Data holds the structure for Signal 100 state on a community
type Signal100Data struct {
	Active                bool   `json:"active" bson:"active"`
	ActivatedByUserId     string `json:"activatedByUserId,omitempty" bson:"activatedByUserId,omitempty"`
	ActivatedByUsername   string `json:"activatedByUsername,omitempty" bson:"activatedByUsername,omitempty"`
	ActivatedByCallSign   string `json:"activatedByCallSign,omitempty" bson:"activatedByCallSign,omitempty"`
	ActivatedByDepartment string `json:"activatedByDepartment,omitempty" bson:"activatedByDepartment,omitempty"`
	ActivatedAt           string `json:"activatedAt,omitempty" bson:"activatedAt,omitempty"`
	ClearedByUserId       string `json:"clearedByUserId,omitempty" bson:"clearedByUserId,omitempty"`
	ClearedByUsername     string `json:"clearedByUsername,omitempty" bson:"clearedByUsername,omitempty"`
	ClearedByCallSign     string `json:"clearedByCallSign,omitempty" bson:"clearedByCallSign,omitempty"`
	ClearedAt             string `json:"clearedAt,omitempty" bson:"clearedAt,omitempty"`
}

// PanicAlert holds the structure for a panic alert
type PanicAlert struct {
	AlertID       string             `json:"alertId" bson:"alertId"`
	UserID        string             `json:"userId" bson:"userId"`
	Username      string             `json:"username" bson:"username"`
	CallSign      string             `json:"callSign" bson:"callSign"`
	DepartmentType string            `json:"departmentType" bson:"departmentType"`
	CommunityID   string             `json:"communityId" bson:"communityId"`
	TriggeredAt   primitive.DateTime `json:"triggeredAt" bson:"triggeredAt"`
	Status        string             `json:"status" bson:"status"` // "active", "cleared"
	ClearedBy     *string            `json:"clearedBy" bson:"clearedBy"`
	ClearedAt     *primitive.DateTime `json:"clearedAt" bson:"clearedAt"`
}
