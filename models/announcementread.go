package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// AnnouncementRead tracks which users have read which announcements
type AnnouncementRead struct {
	ID           primitive.ObjectID `json:"_id" bson:"_id"`
	Announcement primitive.ObjectID `json:"announcement" bson:"announcement"`
	User         primitive.ObjectID `json:"user" bson:"user"`
	ReadAt       time.Time          `json:"readAt" bson:"readAt"`
}

// MarkAsReadRequest holds the structure for marking an announcement as read
type MarkAsReadRequest struct {
	UserID string `json:"userId" validate:"required"`
}

// MarkAsReadResponse holds the structure for mark as read response
type MarkAsReadResponse struct {
	Success        bool `json:"success"`
	WasAlreadyRead bool `json:"wasAlreadyRead"`
	ViewCount      int  `json:"viewCount"`
}
