package models

import (
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// GlobalTemplate represents a centralized template that can be shared across communities
type GlobalTemplate struct {
	ID          primitive.ObjectID `json:"_id" bson:"_id"`
	Name        string             `json:"name" bson:"name"`
	Description string             `json:"description" bson:"description"`
	Category    string             `json:"category" bson:"category"` // e.g., "police", "ems", "fire", "dispatch"
	IsDefault   bool               `json:"isDefault" bson:"isDefault"` // Whether this is a default template
	IsActive    bool               `json:"isActive" bson:"isActive"`   // Whether this template is available for use
	Components  []TemplateComponentReference `json:"components" bson:"components"` // References to global components
	CreatedAt   primitive.DateTime `json:"createdAt" bson:"createdAt"`
	UpdatedAt   primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
	CreatedBy   string             `json:"createdBy" bson:"createdBy"` // User ID who created this template
}

// TemplateReference represents a reference to a global template with customizations
type TemplateReference struct {
	TemplateID    primitive.ObjectID            `json:"templateId" bson:"templateId"`
	Customizations map[string]ComponentOverride `json:"customizations" bson:"customizations"` // Component ID -> Override settings
	IsActive      bool                          `json:"isActive" bson:"isActive"`
}

// ComponentOverride represents custom settings for a component within a template reference
type ComponentOverride struct {
	Enabled bool                   `json:"enabled" bson:"enabled"`
	Settings map[string]interface{} `json:"settings" bson:"settings"` // Component-specific custom settings
}
