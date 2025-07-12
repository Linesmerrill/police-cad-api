package models

import "go.mongodb.org/mongo-driver/bson/primitive"

// UserPreferences holds the structure for user preferences collection in mongo
type UserPreferences struct {
	ID                   primitive.ObjectID           `json:"_id" bson:"_id"`
	UserID               string                       `json:"userId" bson:"userId"`
	CommunityPreferences map[string]CommunityPreference `json:"communityPreferences" bson:"communityPreferences"`
	CreatedAt            interface{}                  `json:"createdAt" bson:"createdAt"`
	UpdatedAt            interface{}                  `json:"updatedAt" bson:"updatedAt"`
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