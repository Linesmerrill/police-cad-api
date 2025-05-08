package models

import (
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// ArchivedCommunity holds the structure for the community collection in mongo
type ArchivedCommunity struct {
	ID      primitive.ObjectID       `json:"_id" bson:"_id"`
	Details ArchivedCommunityDetails `json:"community" bson:"community"`
	Version int32                    `json:"__v" bson:"__v"`
}

// ArchivedCommunityDetails holds the structure for the inner community collection in mongo
type ArchivedCommunityDetails struct {
	Name                   string                     `json:"name" bson:"name"`
	OwnerID                string                     `json:"ownerID" bson:"ownerID"`
	Code                   string                     `json:"code" bson:"code"`
	ActivePanics           map[string]interface{}     `json:"activePanics" bson:"activePanics"`
	ActiveSignal100        bool                       `json:"activeSignal100" bson:"activeSignal100"`
	ImageLink              string                     `json:"imageLink" bson:"imageLink"`
	Visibility             string                     `json:"visibility" bson:"visibility"`
	PromotionalText        string                     `json:"promotionalText" bson:"promotionalText"`
	PromotionalDescription string                     `json:"promotionalDescription" bson:"promotionalDescription"`
	InviteCodes            []InviteCode               `json:"inviteCodes" bson:"inviteCodes"`
	Tags                   []string                   `json:"tags" bson:"tags"`
	Roles                  []Role                     `json:"roles" bson:"roles"`
	BanList                []string                   `json:"banList" bson:"banList"`
	Description            string                     `json:"description" bson:"description"`
	Members                map[string]MemberDetail    `json:"members" bson:"members"`
	MembersCount           int                        `json:"membersCount" bson:"membersCount"`
	Events                 []Event                    `json:"events" bson:"events"`
	Departments            []Department               `json:"departments" bson:"departments"`
	TenCodes               []TenCodes                 `json:"tenCodes" bson:"tenCodes"`
	Fines                  ArchivedCommunityFine      `json:"fines" bson:"fines"`
	Templates              []Template                 `json:"templates" bson:"templates"`
	Subscription           Subscription               `json:"subscription" bson:"subscription"`
	SubscriptionCreatedBy  string                     `json:"subscriptionCreatedBy" bson:"subscriptionCreatedBy"`
	Analytics              ArchivedCommunityAnalytics `json:"analytics" bson:"analytics"`
	ActivityLevel          string                     `json:"activityLevel" bson:"activityLevel"`
	CreatedAt              primitive.DateTime         `json:"createdAt" bson:"createdAt"`
	UpdatedAt              primitive.DateTime         `json:"updatedAt" bson:"updatedAt"`
}

// ArchivedCommunityAnalytics holds the structure for community analytics
type ArchivedCommunityAnalytics struct {
	CardViews     int `json:"cardViews" bson:"cardViews"`
	HomePageViews int `json:"homePageViews" bson:"homePageViews"`
}

// ArchivedCommunityFine holds the structure for community fines
type ArchivedCommunityFine struct {
	Categories []Category `json:"categories" bson:"categories"`
	Currency   string     `json:"currency" bson:"currency"` // USD, EUR, etc.
}

// ArchivedCommunityCategory holds the structure for a category
type ArchivedCommunityCategory struct {
	Name  string        `json:"name" bson:"name"` // Traffic Citations, Misdeameanors, etc.
	Fines []FineDetails `json:"fines" bson:"fines"`
}

// ArchivedCommunityFineDetails holds the structure for a fine detail
type ArchivedCommunityFineDetails struct {
	Name   string `json:"name" bson:"name"`     // Speeding, Public Intoxication, etc.
	Amount int    `json:"amount" bson:"amount"` // 50, 100, etc.
}

// ArchivedCommunityMemberDetail holds the structure for a member detail
type ArchivedCommunityMemberDetail struct {
	DepartmentID string `json:"departmentID" bson:"departmentID"`
	TenCodeID    string `json:"tenCodeID" bson:"tenCodeID"`
}

// ArchivedCommunityDepartment holds the structure for a department
type ArchivedCommunityDepartment struct {
	ID               primitive.ObjectID `json:"_id" bson:"_id"`
	Name             string             `json:"name" bson:"name"`
	Description      string             `json:"description" bson:"description"`
	Image            string             `json:"image" bson:"image"`
	ApprovalRequired bool               `json:"approvalRequired" bson:"approvalRequired"`
	Members          []MemberStatus     `json:"members" bson:"members"`
	Template         Template           `json:"template" bson:"template"`
	CreatedAt        primitive.DateTime `json:"createdAt" bson:"createdAt"`
	UpdatedAt        primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
}

// ArchivedCommunityTenCodes holds the structure for ten-codes used in departments
type ArchivedCommunityTenCodes struct {
	ID          primitive.ObjectID `json:"_id" bson:"_id"`
	Code        string             `json:"code" bson:"code"`
	Description string             `json:"description" bson:"description"`
}

// ArchivedCommunityTemplate holds the structure for a department template
type ArchivedCommunityTemplate struct {
	ID          primitive.ObjectID `json:"_id" bson:"_id"`
	Name        string             `json:"name" bson:"name"`
	Description string             `json:"description" bson:"description"`
	Components  []Component        `json:"components" bson:"components"`
}

// ArchivedCommunityComponent holds the structure for a template component
type ArchivedCommunityComponent struct {
	ID      primitive.ObjectID `json:"_id" bson:"_id"`
	Name    string             `json:"name" bson:"name"`
	Enabled bool               `json:"enabled" bson:"enabled"`
}

// ArchivedCommunityEvent holds the structure for an event
type ArchivedCommunityEvent struct {
	ID                  primitive.ObjectID `json:"_id" bson:"_id"`
	ArchivedCommunityID string             `json:"communityID" bson:"communityID"`
	Title               string             `json:"title" bson:"title"`
	ScheduledDate       string             `json:"scheduledDate" bson:"scheduledDate"`
	Image               string             `json:"image" bson:"image"`
	Location            string             `json:"location" bson:"location"`
	Description         string             `json:"description" bson:"description"`
	Host                string             `json:"host" bson:"host"`
	CoHosts             []string           `json:"coHosts" bson:"coHosts"`
	Required            bool               `json:"required" bson:"required"`
	Attendance          Attendance         `json:"attendance" bson:"attendance"`
	CreatedAt           primitive.DateTime `json:"createdAt" bson:"createdAt"`
	UpdatedAt           primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
}

// ArchivedCommunityMemberStatus holds the structure for a member status
type ArchivedCommunityMemberStatus struct {
	UserID    string `json:"userID" bson:"userID"`
	Status    string `json:"status" bson:"status"`
	TenCodeID string `json:"tenCodeID" bson:"tenCodeID"`
}

// ArchivedCommunityAttendance holds the structure for attendance
type ArchivedCommunityAttendance struct {
	Confirmed []string `json:"confirmed" bson:"confirmed"`
	Maybe     []string `json:"maybe" bson:"maybe"`
	Declined  []string `json:"declined" bson:"declined"`
}

// ArchivedCommunityInviteCode holds the structure for invite codes
type ArchivedCommunityInviteCode struct {
	Code          string             `json:"code" bson:"code"`
	RemainingUses int                `json:"remainingUses" bson:"remainingUses"`
	CreatedBy     string             `json:"createdBy" bson:"createdBy"`
	CreatedAt     primitive.DateTime `json:"createdAt" bson:"createdAt"`
}

// ArchivedCommunityRole holds the structure for a role
type ArchivedCommunityRole struct {
	ID          primitive.ObjectID `json:"_id" bson:"_id"`
	Name        string             `json:"name" bson:"name"`
	Members     []string           `json:"members" bson:"members"`
	Permissions []Permission       `json:"permissions" bson:"permissions"`
}

// ArchivedCommunityPermission holds the structure for a permission
type ArchivedCommunityPermission struct {
	ID          primitive.ObjectID `json:"_id" bson:"_id"`
	Name        string             `json:"name" bson:"name"`
	Description string             `json:"description" bson:"description"`
	Enabled     bool               `json:"enabled" bson:"enabled"`
}
