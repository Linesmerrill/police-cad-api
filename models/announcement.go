package models

import (
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Announcement holds the structure for the announcement collection in mongo
type Announcement struct {
	ID          primitive.ObjectID `json:"_id" bson:"_id"`
	Community   primitive.ObjectID `json:"community" bson:"community"`
	Creator     primitive.ObjectID `json:"creator" bson:"creator"`
	Type        string             `json:"type" bson:"type"` // 'main', 'session', 'training'
	Title       string             `json:"title" bson:"title"`
	Content     string             `json:"content" bson:"content"`
	Priority    string             `json:"priority" bson:"priority"` // 'low', 'medium', 'high', 'urgent'
	IsActive    bool               `json:"isActive" bson:"isActive"`
	IsPinned    bool               `json:"isPinned" bson:"isPinned"`
	StartTime   *primitive.DateTime `json:"startTime,omitempty" bson:"startTime,omitempty"`
	EndTime     *primitive.DateTime `json:"endTime,omitempty" bson:"endTime,omitempty"`
	Reactions   []Reaction         `json:"reactions" bson:"reactions"`
	Comments    []Comment          `json:"comments" bson:"comments"`
	ViewCount   int                `json:"viewCount" bson:"viewCount"`
	CreatedAt   primitive.DateTime `json:"createdAt" bson:"createdAt"`
	UpdatedAt   primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
}

// Reaction holds the structure for a reaction on an announcement
type Reaction struct {
	User      primitive.ObjectID `json:"user" bson:"user"`
	Emoji     string             `json:"emoji" bson:"emoji"`
	Timestamp primitive.DateTime `json:"timestamp" bson:"timestamp"`
}

// Comment holds the structure for a comment on an announcement
type Comment struct {
	ID        primitive.ObjectID `json:"_id" bson:"_id"`
	User      primitive.ObjectID `json:"user" bson:"user"`
	Content   string             `json:"content" bson:"content"`
	Timestamp primitive.DateTime `json:"timestamp" bson:"timestamp"`
	Edited    bool               `json:"edited" bson:"edited"`
	EditedAt  *primitive.DateTime `json:"editedAt,omitempty" bson:"editedAt,omitempty"`
}

// CreateAnnouncementRequest holds the structure for creating a new announcement
type CreateAnnouncementRequest struct {
	UserID    string             `json:"userId" validate:"required"`
	Type      string             `json:"type" validate:"required,oneof=main session training"`
	Title     string             `json:"title" validate:"required,min=1,max=200"`
	Content   string             `json:"content" validate:"required,min=1"`
	Priority  string             `json:"priority" validate:"required,oneof=low medium high urgent"`
	StartTime *primitive.DateTime `json:"startTime,omitempty"`
	EndTime   *primitive.DateTime `json:"endTime,omitempty"`
	IsPinned  bool               `json:"isPinned"`
}

// UpdateAnnouncementRequest holds the structure for updating an announcement
type UpdateAnnouncementRequest struct {
	UserID    string              `json:"userId" validate:"required"`
	Type      *string             `json:"type,omitempty" validate:"omitempty,oneof=main session training"`
	Title     *string             `json:"title,omitempty" validate:"omitempty,min=1,max=200"`
	Content   *string             `json:"content,omitempty" validate:"omitempty,min=1"`
	Priority  *string             `json:"priority,omitempty" validate:"omitempty,oneof=low medium high urgent"`
	IsActive  *bool               `json:"isActive,omitempty"`
	IsPinned  *bool               `json:"isPinned,omitempty"`
	StartTime *primitive.DateTime `json:"startTime,omitempty"`
	EndTime   *primitive.DateTime `json:"endTime,omitempty"`
}

// AddReactionRequest holds the structure for adding a reaction
type AddReactionRequest struct {
	UserID string `json:"userId" validate:"required"`
	Emoji  string `json:"emoji" validate:"required"`
}

// RemoveReactionRequest holds the structure for removing a reaction
type RemoveReactionRequest struct {
	UserID string `json:"userId" validate:"required"`
	Emoji  string `json:"emoji" validate:"required"`
}

// AddCommentRequest holds the structure for adding a comment
type AddCommentRequest struct {
	UserID  string `json:"userId" validate:"required"`
	Content string `json:"content" validate:"required,min=1,max=1000"`
}

// UpdateCommentRequest holds the structure for updating a comment
type UpdateCommentRequest struct {
	UserID  string `json:"userId" validate:"required"`
	Content string `json:"content" validate:"required,min=1,max=1000"`
}

// AnnouncementResponse holds the structure for announcement responses with populated user data
type AnnouncementResponse struct {
	ID          primitive.ObjectID     `json:"_id" bson:"_id"`
	Community   primitive.ObjectID     `json:"community" bson:"community"`
	Creator     UserSummary            `json:"creator" bson:"creator"`
	Type        string                 `json:"type" bson:"type"`
	Title       string                 `json:"title" bson:"title"`
	Content     string                 `json:"content" bson:"content"`
	Priority    string                 `json:"priority" bson:"priority"`
	IsActive    bool                   `json:"isActive" bson:"isActive"`
	IsPinned    bool                   `json:"isPinned" bson:"isPinned"`
	StartTime   *primitive.DateTime    `json:"startTime,omitempty" bson:"startTime,omitempty"`
	EndTime     *primitive.DateTime    `json:"endTime,omitempty" bson:"endTime,omitempty"`
	Reactions   []ReactionResponse     `json:"reactions" bson:"reactions"`
	Comments    []CommentResponse      `json:"comments" bson:"comments"`
	ViewCount   int                    `json:"viewCount" bson:"viewCount"`
	IsRead      bool                   `json:"isRead" bson:"isRead"`
	CreatedAt   primitive.DateTime     `json:"createdAt" bson:"createdAt"`
	UpdatedAt   primitive.DateTime     `json:"updatedAt" bson:"updatedAt"`
}

// UserSummary holds basic user information for responses
type UserSummary struct {
	ID             primitive.ObjectID `json:"_id" bson:"_id"`
	Username       string             `json:"username" bson:"username"`
	ProfilePicture *string            `json:"profilePicture,omitempty" bson:"profilePicture,omitempty"`
}

// ReactionResponse holds the structure for reaction responses with populated user data
type ReactionResponse struct {
	User      UserSummary        `json:"user" bson:"user"`
	Emoji     string             `json:"emoji" bson:"emoji"`
	Timestamp primitive.DateTime `json:"timestamp" bson:"timestamp"`
}

// CommentResponse holds the structure for comment responses with populated user data
type CommentResponse struct {
	ID        primitive.ObjectID `json:"_id" bson:"_id"`
	User      UserSummary        `json:"user" bson:"user"`
	Content   string             `json:"content" bson:"content"`
	Timestamp primitive.DateTime `json:"timestamp" bson:"timestamp"`
	Edited    bool               `json:"edited" bson:"edited"`
	EditedAt  *primitive.DateTime `json:"editedAt,omitempty" bson:"editedAt,omitempty"`
}

// PaginatedAnnouncementsResponse holds the structure for paginated announcements response
type PaginatedAnnouncementsResponse struct {
	Success       bool                   `json:"success"`
	Announcements []AnnouncementResponse `json:"announcements"`
	Pagination    PaginationInfo         `json:"pagination"`
}

// PaginationInfo holds pagination metadata
type PaginationInfo struct {
	CurrentPage        int  `json:"currentPage"`
	TotalPages         int  `json:"totalPages"`
	TotalAnnouncements int  `json:"totalAnnouncements"`
	HasNextPage        bool `json:"hasNextPage"`
	HasPrevPage        bool `json:"hasPrevPage"`
} 