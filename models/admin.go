package models

import (
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// AdminUser represents an administrative user for platform management
type AdminUser struct {
	ID           primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Email        string             `bson:"email" json:"email"`
	PasswordHash string             `bson:"passwordHash" json:"-"`
	Active       bool               `bson:"active" json:"active"`
	Roles        []string           `bson:"roles" json:"roles"`
	Permissions  map[string]bool    `bson:"permissions,omitempty" json:"permissions,omitempty"`
	CreatedAt    interface{}        `bson:"createdAt" json:"createdAt"`
	UpdatedAt    interface{}        `bson:"updatedAt" json:"updatedAt"`
}

// AdminPasswordReset stores password reset tokens for admin accounts
type AdminPasswordReset struct {
    ID        primitive.ObjectID `bson:"_id,omitempty" json:"id"`
    AdminID   primitive.ObjectID `bson:"adminId" json:"adminId"`
    TokenHash string             `bson:"tokenHash" json:"-"`
    ExpiresAt interface{}        `bson:"expiresAt" json:"expiresAt"`
    UsedAt    interface{}        `bson:"usedAt,omitempty" json:"usedAt,omitempty"`
    CreatedAt interface{}        `bson:"createdAt" json:"createdAt"`
}


