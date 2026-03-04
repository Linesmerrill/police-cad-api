package models

import (
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// FeatureRequest holds the structure for the featureRequests collection in mongo
type FeatureRequest struct {
	ID           primitive.ObjectID `json:"_id" bson:"_id"`
	Title        string             `json:"title" bson:"title"`
	Description  string             `json:"description" bson:"description"`
	Author       primitive.ObjectID `json:"author" bson:"author"`
	Status       string             `json:"status" bson:"status"` // "open", "under_review", "planned", "in_progress", "released", "declined", "merged"
	ImageURLs    []string           `json:"imageUrls" bson:"imageUrls"`
	UpvoteCount  int                `json:"upvoteCount" bson:"upvoteCount"`
	CommentCount int                `json:"commentCount" bson:"commentCount"`
	MergedInto   *primitive.ObjectID  `json:"mergedInto,omitempty" bson:"mergedInto,omitempty"`
	MergedFrom   []primitive.ObjectID `json:"mergedFrom,omitempty" bson:"mergedFrom,omitempty"`
	Comments     []FeatureComment   `json:"comments" bson:"comments"`
	CreatedAt    primitive.DateTime `json:"createdAt" bson:"createdAt"`
	UpdatedAt    primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
}

// FeatureComment holds the structure for a comment on a feature request
type FeatureComment struct {
	ID        primitive.ObjectID  `json:"_id" bson:"_id"`
	User      primitive.ObjectID  `json:"user" bson:"user"`
	Content   string              `json:"content" bson:"content"`
	ImageURLs []string            `json:"imageUrls" bson:"imageUrls"`
	Edited    bool                `json:"edited" bson:"edited"`
	EditedAt  *primitive.DateTime `json:"editedAt,omitempty" bson:"editedAt,omitempty"`
	CreatedAt primitive.DateTime  `json:"createdAt" bson:"createdAt"`
}

// CreateFeatureRequestRequest holds the structure for creating a new feature request
type CreateFeatureRequestRequest struct {
	Title       string   `json:"title" validate:"required,min=1,max=200"`
	Description string   `json:"description" validate:"required,min=1,max=5000"`
	ImageURLs   []string `json:"imageUrls"`
}

// UpdateFeatureRequestRequest holds the structure for updating a feature request
type UpdateFeatureRequestRequest struct {
	Title       *string  `json:"title,omitempty" validate:"omitempty,min=1,max=200"`
	Description *string  `json:"description,omitempty" validate:"omitempty,min=1,max=5000"`
	ImageURLs   []string `json:"imageUrls,omitempty"`
}

// UpdateFeatureRequestStatusRequest holds the structure for updating a feature request status (admin only)
type UpdateFeatureRequestStatusRequest struct {
	Status string `json:"status" validate:"required,oneof=open under_review planned in_progress released declined"`
}

// AddFeatureCommentRequest holds the structure for adding a comment to a feature request
type AddFeatureCommentRequest struct {
	Content   string   `json:"content" validate:"required,min=1,max=2000"`
	ImageURLs []string `json:"imageUrls"`
}

// UpdateFeatureCommentRequest holds the structure for updating a comment
type UpdateFeatureCommentRequest struct {
	Content string `json:"content" validate:"required,min=1,max=2000"`
}

// MergeFeatureRequestRequest holds the structure for merging a feature request into a target
type MergeFeatureRequestRequest struct {
	SourceID string `json:"sourceId"`
}

// MergedRequestSummary holds minimal info for displaying merged request references
type MergedRequestSummary struct {
	ID    primitive.ObjectID `json:"_id"`
	Title string             `json:"title"`
}

// FeatureRequestResponse holds the structure for feature request responses with populated user data
type FeatureRequestResponse struct {
	ID           primitive.ObjectID       `json:"_id"`
	Title        string                   `json:"title"`
	Description  string                   `json:"description"`
	Author       UserSummary              `json:"author"`
	Status       string                   `json:"status"`
	ImageURLs    []string                 `json:"imageUrls"`
	UpvoteCount  int                      `json:"upvoteCount"`
	CommentCount int                      `json:"commentCount"`
	HasVoted     bool                     `json:"hasVoted"`
	MergedInto   *MergedRequestSummary    `json:"mergedInto,omitempty"`
	MergedFrom   []MergedRequestSummary   `json:"mergedFrom,omitempty"`
	Comments     []FeatureCommentResponse `json:"comments"`
	CreatedAt    primitive.DateTime       `json:"createdAt"`
	UpdatedAt    primitive.DateTime       `json:"updatedAt"`
}

// FeatureCommentResponse holds the structure for comment responses with populated user data
type FeatureCommentResponse struct {
	ID        primitive.ObjectID  `json:"_id"`
	User      UserSummary         `json:"user"`
	Content   string              `json:"content"`
	ImageURLs []string            `json:"imageUrls"`
	Edited    bool                `json:"edited"`
	EditedAt  *primitive.DateTime `json:"editedAt,omitempty"`
	CreatedAt primitive.DateTime  `json:"createdAt"`
}

// FeatureRequestListResponse holds the structure for paginated feature request list response
type FeatureRequestListResponse struct {
	Data       []FeatureRequestResponse `json:"data"`
	TotalCount int64                    `json:"totalCount"`
	Page       int                      `json:"page"`
	Limit      int                      `json:"limit"`
}
