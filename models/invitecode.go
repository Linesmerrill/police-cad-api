package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// InviteCode represents the structure of an invite code document in MongoDB
type InviteCode struct {
	ID            primitive.ObjectID `bson:"_id,omitempty"`
	Code          string             `bson:"code" index:"unique"`
	CommunityID   string             `bson:"communityId"`
	ExpiresAt     *time.Time         `bson:"expiresAt"`
	MaxUses       int                `bson:"maxUses"`
	RemainingUses int                `bson:"remainingUses"`
	Uses          int                `bson:"uses"`
	CreatedBy     string             `bson:"createdBy"`
	CreatedAt     time.Time          `bson:"createdAt"`
}
