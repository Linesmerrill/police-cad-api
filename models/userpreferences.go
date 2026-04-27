package models

import "go.mongodb.org/mongo-driver/bson/primitive"

// UserPreferences holds the structure for user preferences collection in mongo
type UserPreferences struct {
	ID                       primitive.ObjectID             `json:"_id" bson:"_id"`
	UserID                   string                         `json:"userId" bson:"userId"`
	BetaCivDashboard         bool                           `json:"betaCivDashboard" bson:"betaCivDashboard"`
	BetaCommandDashboard     bool                           `json:"betaCommandDashboard" bson:"betaCommandDashboard"`
	CommandDashboardOptedAt  interface{}                    `json:"commandDashboardOptedAt,omitempty" bson:"commandDashboardOptedAt,omitempty"`
	// BetaCommandDispatch opts the user into the new Command Bridge layout at
	// /command-dashboard when their active department is a dispatch template.
	// Tracked separately from BetaCommandDashboard (police) so admin adoption
	// metrics can distinguish police vs. dispatch opt-in.
	BetaCommandDispatch      bool                           `json:"betaCommandDispatch" bson:"betaCommandDispatch"`
	CommandDispatchOptedAt   interface{}                    `json:"commandDispatchOptedAt,omitempty" bson:"commandDispatchOptedAt,omitempty"`
	CommunityPreferences     map[string]CommunityPreference `json:"communityPreferences" bson:"communityPreferences"`
	NotificationPreferences  NotificationPreferences        `json:"notificationPreferences" bson:"notificationPreferences"`
	CreatedAt                interface{}                    `json:"createdAt" bson:"createdAt"`
	UpdatedAt                interface{}                    `json:"updatedAt" bson:"updatedAt"`
}

// NotificationPreferences controls which push notification categories the user receives.
// All fields default to true (enabled). AllNotifications is a master toggle.
type NotificationPreferences struct {
	AllNotifications bool `json:"allNotifications" bson:"allNotifications"`
	Friends          bool `json:"friends" bson:"friends"`
	CommunityJoins   bool `json:"communityJoins" bson:"communityJoins"`
	DepartmentJoins  bool `json:"departmentJoins" bson:"departmentJoins"`
	PanicAlerts      bool `json:"panicAlerts" bson:"panicAlerts"`
	General          bool `json:"general" bson:"general"`
}

// DefaultNotificationPreferences returns preferences with all categories enabled
func DefaultNotificationPreferences() NotificationPreferences {
	return NotificationPreferences{
		AllNotifications: true,
		Friends:          true,
		CommunityJoins:   true,
		DepartmentJoins:  true,
		PanicAlerts:      true,
		General:          true,
	}
}

// CommunityPreference holds preferences for a specific community
type CommunityPreference struct {
	DepartmentOrder []DepartmentOrder `json:"departmentOrder" bson:"departmentOrder"`
	// Future preference fields can be added here
	// Theme            string `json:"theme" bson:"theme"`
	// Language         string `json:"language" bson:"language"`
	// NotificationSettings map[string]bool `json:"notificationSettings" bson:"notificationSettings"`
}

// DepartmentOrder holds the order information for a department within a community
type DepartmentOrder struct {
	DepartmentID string `json:"departmentId" bson:"departmentId"`
	Order        int    `json:"order" bson:"order"`
}