package models

import "go.mongodb.org/mongo-driver/bson/primitive"

// Report represents a player report
type Report struct {
	ID                primitive.ObjectID `bson:"_id,omitempty" json:"id,omitempty"`
	ItemID            string             `bson:"itemId" json:"itemId"`               // The ID of the item being reported, userID, communityID, etc.
	ItemType          string             `bson:"itemType" json:"itemType"`           // The database name of the item being reported, user, community, etc.
	ReportType        ReportType         `bson:"reportType" json:"reportType"`       // USER_REPORT, AD_REPORT, etc.
	ReportedIssue     string             `bson:"reportedIssue" json:"reportedIssue"` // hate, scan, etc.
	AdditionalDetails string             `bson:"additionalDetails" json:"additionalDetails"`
	ReportedByID      string             `bson:"reportedById" json:"reportedById"`
	Active            bool               `bson:"active" json:"active"`
	ActionTaken       string             `bson:"actionTaken" json:"actionTaken"` // warning, ban, etc.
	CreatedAt         primitive.DateTime `bson:"createdAt" json:"createdAt"`
}
