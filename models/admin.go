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
	CreatedBy    string             `bson:"createdBy,omitempty" json:"createdBy,omitempty"`
}

// CreateAdminUserRequest represents the request to create a new admin user
type CreateAdminUserRequest struct {
	Email string `json:"email" validate:"required,email"`
	Role  string `json:"role" validate:"required,oneof=admin owner"`
}

// CreateAdminUserResponse represents the response when creating a new admin user
type CreateAdminUserResponse struct {
	Success   bool       `json:"success"`
	Message   string     `json:"message"`
	AdminUser AdminUser  `json:"adminUser"`
	ResetLink string     `json:"resetLink"`
}

// SendAdminResetEmailRequest represents the request to send a password reset email
type SendAdminResetEmailRequest struct {
	Email string `json:"email" validate:"required,email"`
}

// SendAdminResetEmailResponse represents the response when sending a password reset email
type SendAdminResetEmailResponse struct {
	Success   bool   `json:"success"`
	Message   string `json:"message"`
	EmailSent bool   `json:"emailSent"`
}

// ErrorResponse represents a standardized error response
type ErrorResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
	Code    string `json:"code"`
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

// Admin response models
type AdminUserResult struct {
	ID          string      `json:"id"`
	Email       string      `json:"email"`
	Username    string      `json:"username,omitempty"`
	Active      bool        `json:"active"`
	CreatedAt   interface{} `json:"createdAt"`
	LastLoginAt interface{} `json:"lastLoginAt,omitempty"`
}

type AdminCommunityResult struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Active      bool        `json:"active"`
	CreatedAt   interface{} `json:"createdAt"`
	Owner       *OwnerInfo  `json:"owner,omitempty"`
	MemberCount int         `json:"memberCount"`
}

type OwnerInfo struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

type AdminUserDetails struct {
	ID                   string               `json:"id"`
	Email                string               `json:"email"`
	Username             string               `json:"username,omitempty"`
	Active               bool                 `json:"active"`
	CreatedAt            interface{}          `json:"createdAt"`
	LastLoginAt          interface{}          `json:"lastLoginAt,omitempty"`
	Communities          []AdminUserCommunity `json:"communities,omitempty"`
	ResetPasswordToken   string               `json:"resetPasswordToken,omitempty"`
	ResetPasswordExpires interface{}          `json:"resetPasswordExpires,omitempty"`
}

type AdminUserCommunity struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	Role       string      `json:"role"`
	Department string      `json:"department,omitempty"`
	JoinedAt   interface{} `json:"joinedAt,omitempty"`
}

type AdminCommunityDetails struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Active      bool              `json:"active"`
	CreatedAt   interface{}       `json:"createdAt"`
	Owner       *OwnerInfo        `json:"owner,omitempty"`
	MemberCount int               `json:"memberCount"`
	Departments []CommunityDept   `json:"departments,omitempty"`
}

type CommunityDept struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	MemberCount int    `json:"memberCount"`
}


