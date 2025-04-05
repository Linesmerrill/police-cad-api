package models

import "go.mongodb.org/mongo-driver/bson/primitive"

// Bolo holds the structure for the bolo collection in MongoDB
type Bolo struct {
	ID      primitive.ObjectID `json:"_id" bson:"_id"`
	Details BoloDetails        `json:"bolo" bson:"bolo"`
	Version int32              `json:"__v" bson:"__v"`
}

// BoloDetails holds the structure for the inner bolo details
type BoloDetails struct {
	Title        string             `json:"title" bson:"title"`
	Location     string             `json:"location" bson:"location"`
	CommunityID  string             `json:"communityID" bson:"communityID"`
	DepartmentID string             `json:"departmentID" bson:"departmentID"`
	Scope        string             `json:"scope" bson:"scope"`
	Description  string             `json:"description" bson:"description"`
	ReportedByID string             `json:"reportedByID" bson:"reportedByID"`
	Status       bool               `json:"status" bson:"status"`
	CreatedAt    primitive.DateTime `json:"createdAt" bson:"createdAt"`
}
