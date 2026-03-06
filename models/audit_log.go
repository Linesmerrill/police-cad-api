package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// AuditLog represents a single audit log entry for a community action.
type AuditLog struct {
	ID          primitive.ObjectID     `json:"_id" bson:"_id,omitempty"`
	CommunityID primitive.ObjectID     `json:"communityId" bson:"communityId"`
	Action      string                 `json:"action" bson:"action"`
	Category    string                 `json:"category" bson:"category"`
	ActorID     string                 `json:"actorId" bson:"actorId"`
	ActorName   string                 `json:"actorName" bson:"actorName"`
	TargetID    string                 `json:"targetId,omitempty" bson:"targetId,omitempty"`
	TargetName  string                 `json:"targetName,omitempty" bson:"targetName,omitempty"`
	Details     map[string]interface{} `json:"details,omitempty" bson:"details,omitempty"`
	CreatedAt   time.Time              `json:"createdAt" bson:"createdAt"`
}
