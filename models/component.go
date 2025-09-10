package models

import (
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// GlobalComponent represents a component that can be used across multiple templates
type GlobalComponent struct {
	ID          primitive.ObjectID `json:"_id" bson:"_id"`
	Name        string             `json:"name" bson:"name"`
	DisplayName string             `json:"displayName" bson:"displayName"`
	Description string             `json:"description" bson:"description"`
	Category    string             `json:"category" bson:"category"` // "search", "communication", "reporting", etc.
	Type        string             `json:"type" bson:"type"`         // "feature", "module", "tool"
	Version     string             `json:"version" bson:"version"`   // Component version for updates
	IsActive    bool               `json:"isActive" bson:"isActive"` // Whether this component is available for use
	IsRequired  bool               `json:"isRequired" bson:"isRequired"` // Whether this component is required for basic functionality
	Metadata    map[string]interface{} `json:"metadata" bson:"metadata"` // Component-specific configuration
	CreatedAt   primitive.DateTime `json:"createdAt" bson:"createdAt"`
	UpdatedAt   primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
	CreatedBy   string             `json:"createdBy" bson:"createdBy"`
}

// TemplateComponentReference represents a reference to a global component with template-specific settings
type TemplateComponentReference struct {
	ComponentID primitive.ObjectID            `json:"componentId" bson:"componentId"`
	Enabled     bool                          `json:"enabled" bson:"enabled"`
	Settings    map[string]interface{}        `json:"settings" bson:"settings"` // Template-specific component settings
	Order       int                           `json:"order" bson:"order"`       // Display order in the template
}

// ComponentCategory represents a category for organizing components
type ComponentCategory struct {
	ID          primitive.ObjectID `json:"_id" bson:"_id"`
	Name        string             `json:"name" bson:"name"`
	Description string             `json:"description" bson:"description"`
	Order       int                `json:"order" bson:"order"`
	IsActive    bool               `json:"isActive" bson:"isActive"`
	CreatedAt   primitive.DateTime `json:"createdAt" bson:"createdAt"`
	UpdatedAt   primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
}
