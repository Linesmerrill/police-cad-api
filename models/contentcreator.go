package models

import "go.mongodb.org/mongo-driver/bson/primitive"

// ContentCreatorApplication represents an application to the Content Creator Program
type ContentCreatorApplication struct {
	ID              primitive.ObjectID              `json:"_id" bson:"_id"`
	UserID          primitive.ObjectID              `json:"userId" bson:"userId"`
	DisplayName     string                          `json:"displayName" bson:"displayName"`
	PrimaryPlatform string                          `json:"primaryPlatform" bson:"primaryPlatform"` // twitch, youtube, tiktok, other
	Platforms       []ContentCreatorPlatform        `json:"platforms" bson:"platforms"`
	Description     string                          `json:"description" bson:"description"` // for admin evaluation
	Bio             string                          `json:"bio" bson:"bio"`                 // for public profile (max 500 chars)
	Status          string                          `json:"status" bson:"status"`           // submitted, under_review, approved, rejected, withdrawn
	RejectionReason string                          `json:"rejectionReason,omitempty" bson:"rejectionReason,omitempty"`
	AdminNotes      string                          `json:"adminNotes,omitempty" bson:"adminNotes,omitempty"`
	Feedback        string                          `json:"feedback,omitempty" bson:"feedback,omitempty"`
	FirstApprovalBy *primitive.ObjectID             `json:"firstApprovalBy,omitempty" bson:"firstApprovalBy,omitempty"`
	FirstApprovalAt *primitive.DateTime             `json:"firstApprovalAt,omitempty" bson:"firstApprovalAt,omitempty"`
	ReviewedBy      *primitive.ObjectID             `json:"reviewedBy,omitempty" bson:"reviewedBy,omitempty"`
	ReviewedAt      *primitive.DateTime             `json:"reviewedAt,omitempty" bson:"reviewedAt,omitempty"`
	CreatorID       *primitive.ObjectID             `json:"creatorId,omitempty" bson:"creatorId,omitempty"` // set when approved
	CreatedAt       primitive.DateTime              `json:"createdAt" bson:"createdAt"`
	UpdatedAt       primitive.DateTime              `json:"updatedAt" bson:"updatedAt"`
}

// ContentCreator represents an approved content creator in the program
type ContentCreator struct {
	ID              primitive.ObjectID              `json:"_id" bson:"_id"`
	UserID          *primitive.ObjectID             `json:"userId,omitempty" bson:"userId,omitempty"` // nullable if creator left platform
	ApplicationID   primitive.ObjectID              `json:"applicationId" bson:"applicationId"`
	DisplayName     string                          `json:"displayName" bson:"displayName"`
	Slug            string                          `json:"slug" bson:"slug"` // unique URL-friendly identifier
	ProfileImage    string                          `json:"profileImage,omitempty" bson:"profileImage,omitempty"`
	Bio             string                          `json:"bio" bson:"bio"`
	PrimaryPlatform string                          `json:"primaryPlatform" bson:"primaryPlatform"`
	Platforms       []ContentCreatorPlatform        `json:"platforms" bson:"platforms"`
	Status          string                          `json:"status" bson:"status"` // active, warned, pending_removal, removed
	Featured        bool                            `json:"featured" bson:"featured"`
	WarnedAt        *primitive.DateTime             `json:"warnedAt,omitempty" bson:"warnedAt,omitempty"`
	WarningMessage  string                          `json:"warningMessage,omitempty" bson:"warningMessage,omitempty"`
	WarningReason   string                          `json:"warningReason,omitempty" bson:"warningReason,omitempty"`
	RemovalReason   string                          `json:"removalReason,omitempty" bson:"removalReason,omitempty"`
	JoinedAt        primitive.DateTime              `json:"joinedAt" bson:"joinedAt"`
	RemovedAt       *primitive.DateTime             `json:"removedAt,omitempty" bson:"removedAt,omitempty"`
	CreatedAt       primitive.DateTime              `json:"createdAt" bson:"createdAt"`
	UpdatedAt       primitive.DateTime              `json:"updatedAt" bson:"updatedAt"`
}

// ContentCreatorPlatform represents a social media platform connection
type ContentCreatorPlatform struct {
	Type            string `json:"type" bson:"type"` // twitch, youtube, tiktok, other
	URL             string `json:"url" bson:"url"`
	Handle          string `json:"handle" bson:"handle"`
	FollowerCount   int    `json:"followerCount" bson:"followerCount"`
	VerifiedByAdmin bool   `json:"verifiedByAdmin" bson:"verifiedByAdmin"`
}

// ContentCreatorEntitlement represents a plan entitlement granted to a creator
type ContentCreatorEntitlement struct {
	ID               primitive.ObjectID  `json:"_id" bson:"_id"`
	ContentCreatorID primitive.ObjectID  `json:"contentCreatorId" bson:"contentCreatorId"`
	TargetType       string              `json:"targetType" bson:"targetType"` // "user" or "community"
	TargetID         primitive.ObjectID  `json:"targetId" bson:"targetId"`
	Plan             string              `json:"plan" bson:"plan"` // "base"
	Source           string              `json:"source" bson:"source"` // "content_creator_program"
	Active           bool                `json:"active" bson:"active"`
	GrantedAt        primitive.DateTime  `json:"grantedAt" bson:"grantedAt"`
	GrantedBy        primitive.ObjectID  `json:"grantedBy" bson:"grantedBy"`
	RevokedAt        *primitive.DateTime `json:"revokedAt,omitempty" bson:"revokedAt,omitempty"`
	RevokedBy        *primitive.ObjectID `json:"revokedBy,omitempty" bson:"revokedBy,omitempty"`
	RevokeReason     string              `json:"revokeReason,omitempty" bson:"revokeReason,omitempty"`
	CreatedAt        primitive.DateTime  `json:"createdAt" bson:"createdAt"`
	UpdatedAt        primitive.DateTime  `json:"updatedAt" bson:"updatedAt"`
}

// ContentCreatorFollowerSnapshot stores historical follower counts for compliance tracking
type ContentCreatorFollowerSnapshot struct {
	ID               primitive.ObjectID       `json:"_id" bson:"_id"`
	ContentCreatorID primitive.ObjectID       `json:"contentCreatorId" bson:"contentCreatorId"`
	Platforms        []ContentCreatorPlatform `json:"platforms" bson:"platforms"`
	TotalFollowers   int                      `json:"totalFollowers" bson:"totalFollowers"`
	MaxFollowers     int                      `json:"maxFollowers" bson:"maxFollowers"` // highest single platform count
	Source           string                   `json:"source" bson:"source"`            // "manual", "api", "admin"
	RecordedAt       primitive.DateTime       `json:"recordedAt" bson:"recordedAt"`
	RecordedBy       *primitive.ObjectID      `json:"recordedBy,omitempty" bson:"recordedBy,omitempty"`
}

// ContentCreatorSettings stores program configuration
type ContentCreatorSettings struct {
	ID                primitive.ObjectID `json:"_id" bson:"_id"`
	FollowerThreshold int                `json:"followerThreshold" bson:"followerThreshold"` // default 500
	GracePeriodDays   int                `json:"gracePeriodDays" bson:"gracePeriodDays"`     // default 60
	CheckFrequency    string             `json:"checkFrequency" bson:"checkFrequency"`       // "daily", "weekly", "monthly"
	UpdatedAt         primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
	UpdatedBy         primitive.ObjectID `json:"updatedBy" bson:"updatedBy"`
}

// --- Request DTOs ---

// CreateContentCreatorApplicationRequest is the request body for submitting an application
type CreateContentCreatorApplicationRequest struct {
	DisplayName     string                   `json:"displayName" validate:"required,min=2,max=50"`
	PrimaryPlatform string                   `json:"primaryPlatform" validate:"required,oneof=twitch youtube tiktok other"`
	Platforms       []ContentCreatorPlatform `json:"platforms" validate:"required,min=1,max=5"`
	Description     string                   `json:"description" validate:"required,min=50,max=1000"` // for admin evaluation
	Bio             string                   `json:"bio" validate:"required,min=20,max=500"`          // for public profile
}

// ReviewApplicationRequest is the request body for approving/rejecting an application
type ReviewApplicationRequest struct {
	Status          string `json:"status" validate:"required,oneof=approved rejected"`
	RejectionReason string `json:"rejectionReason,omitempty"`
	Feedback        string `json:"feedback,omitempty"`
	AdminNotes      string `json:"adminNotes,omitempty"`
}

// UpdateContentCreatorRequest is the request body for updating a creator profile
type UpdateContentCreatorRequest struct {
	DisplayName   string                   `json:"displayName,omitempty"`
	Bio           string                   `json:"bio,omitempty"`
	ProfileImage  string                   `json:"profileImage,omitempty"`
	Platforms     []ContentCreatorPlatform `json:"platforms,omitempty"`
	Featured      *bool                    `json:"featured,omitempty"`
}

// WarnCreatorRequest is the request body for issuing a warning
type WarnCreatorRequest struct {
	Reason  string `json:"reason" validate:"required"`
	Message string `json:"message" validate:"required"`
}

// RemoveCreatorRequest is the request body for removing a creator
type RemoveCreatorRequest struct {
	Reason string `json:"reason" validate:"required"`
}

// GrantEntitlementRequest is the request body for granting an entitlement
type GrantEntitlementRequest struct {
	TargetType string `json:"targetType" validate:"required,oneof=user community"`
	TargetID   string `json:"targetId" validate:"required"`
	Plan       string `json:"plan" validate:"required,oneof=base"`
}

// --- Response DTOs ---

// ContentCreatorPublicResponse is the public response for a content creator
type ContentCreatorPublicResponse struct {
	ID              primitive.ObjectID       `json:"_id"`
	DisplayName     string                   `json:"displayName"`
	Slug            string                   `json:"slug"`
	ProfileImage    string                   `json:"profileImage,omitempty"`
	Bio             string                   `json:"bio"`
	PrimaryPlatform string                   `json:"primaryPlatform"`
	Platforms       []ContentCreatorPlatform `json:"platforms"`
	Featured        bool                     `json:"featured"`
	JoinedAt        primitive.DateTime       `json:"joinedAt"`
}

// ContentCreatorPagination holds pagination metadata for content creator endpoints
type ContentCreatorPagination struct {
	CurrentPage int  `json:"currentPage"`
	TotalPages  int  `json:"totalPages"`
	TotalItems  int  `json:"totalItems"`
	HasNextPage bool `json:"hasNextPage"`
	HasPrevPage bool `json:"hasPrevPage"`
}

// ContentCreatorsListResponse is the response for listing content creators
type ContentCreatorsListResponse struct {
	Success    bool                           `json:"success"`
	Creators   []ContentCreatorPublicResponse `json:"creators"`
	Pagination ContentCreatorPagination       `json:"pagination"`
}

// ContentCreatorApplicationResponse is the response for an application
type ContentCreatorApplicationResponse struct {
	ID              primitive.ObjectID       `json:"_id"`
	DisplayName     string                   `json:"displayName"`
	PrimaryPlatform string                   `json:"primaryPlatform"`
	Platforms       []ContentCreatorPlatform `json:"platforms"`
	Description     string                   `json:"description"`
	Bio             string                   `json:"bio"`
	Status          string                   `json:"status"`
	Feedback        string                   `json:"feedback,omitempty"`
	CreatedAt       primitive.DateTime       `json:"createdAt"`
	ReviewedAt      *primitive.DateTime      `json:"reviewedAt,omitempty"`
}

// ContentCreatorMeResponse is the response for the current user's creator status
type ContentCreatorMeResponse struct {
	Success     bool                               `json:"success"`
	Application *ContentCreatorApplicationResponse `json:"application,omitempty"`
	Creator     *ContentCreatorPrivateResponse     `json:"creator,omitempty"`
}

// ContentCreatorPrivateResponse is the private response for a content creator (includes entitlements)
type ContentCreatorPrivateResponse struct {
	ID              primitive.ObjectID       `json:"_id"`
	DisplayName     string                   `json:"displayName"`
	Slug            string                   `json:"slug"`
	ProfileImage    string                   `json:"profileImage,omitempty"`
	Bio             string                   `json:"bio"`
	PrimaryPlatform string                   `json:"primaryPlatform"`
	Platforms       []ContentCreatorPlatform `json:"platforms"`
	Status          string                   `json:"status"`
	Featured        bool                     `json:"featured"`
	WarnedAt        *primitive.DateTime      `json:"warnedAt,omitempty"`
	WarningMessage  string                   `json:"warningMessage,omitempty"`
	JoinedAt        primitive.DateTime       `json:"joinedAt"`
	Entitlements    EntitlementsSummary      `json:"entitlements"`
}

// EntitlementsSummary summarizes a creator's entitlements
type EntitlementsSummary struct {
	PersonalPlan         bool                 `json:"personalPlan"`
	PersonalPlanFallback bool                 `json:"personalPlanFallback"` // true if user has higher plan, entitlement acts as fallback
	CurrentUserPlan      string               `json:"currentUserPlan,omitempty"` // user's current subscription plan
	CommunityPlan        CommunityPlanSummary `json:"communityPlan"`
}

// CommunityPlanSummary summarizes community plan entitlement
type CommunityPlanSummary struct {
	Active        bool   `json:"active"`
	CommunityName string `json:"communityName,omitempty"`
	CommunityID   string `json:"communityId,omitempty"`
}

// ContentCreatorAnalytics is the analytics response for admin
type ContentCreatorAnalytics struct {
	TotalCreators      int `json:"totalCreators"`
	ActiveCreators     int `json:"activeCreators"`
	WarnedCreators     int `json:"warnedCreators"`
	PendingRemoval     int `json:"pendingRemoval"`
	TotalApplications  int `json:"totalApplications"`
	PendingApplications int `json:"pendingApplications"`
	ApprovedApplications int `json:"approvedApplications"`
	RejectedApplications int `json:"rejectedApplications"`
	TotalMonthlyValue  float64 `json:"totalMonthlyValue"`
	TotalYearlyValue   float64 `json:"totalYearlyValue"`
}

// PaginationInfo is defined in announcement.go - reusing that definition
