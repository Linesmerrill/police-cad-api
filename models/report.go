package models

import "go.mongodb.org/mongo-driver/bson/primitive"

// Report represents a player report
type Report struct {
	ID         primitive.ObjectID `bson:"_id,omitempty" json:"id,omitempty"`
	UserID     string             `bson:"userId" json:"userId"`
	ReportType string             `bson:"reportType" json:"reportType"`
	CreatedAt  primitive.DateTime `bson:"createdAt" json:"createdAt"`
	ReportedBy string             `bson:"reportedBy" json:"reportedBy"`
}
