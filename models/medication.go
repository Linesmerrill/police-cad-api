package models

import (
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Medication holds the structure for the medication collection in mongo
type Medication struct {
	ID          primitive.ObjectID `json:"_id" bson:"_id"`
	Medication  MedicationDetails  `json:"medication" bson:"medication"`
	Version     int32              `json:"__v" bson:"__v"`
}

// MedicationDetails holds the structure for the inner medication structure
type MedicationDetails struct {
	StartDate          string             `json:"startDate" bson:"startDate"`
	Name               string             `json:"name" bson:"name"`
	Dosage             string             `json:"dosage" bson:"dosage"`
	Frequency          string             `json:"frequency" bson:"frequency"`
	CivilianID         string             `json:"civilianID" bson:"civilianID"`
	ActiveCommunityID  string             `json:"activeCommunityID" bson:"activeCommunityID"`
	UserID             string             `json:"userID" bson:"userID"`
	FirstName          string             `json:"firstName" bson:"firstName"`
	LastName           string             `json:"lastName" bson:"lastName"`
	DateOfBirth        string             `json:"dateOfBirth" bson:"dateOfBirth"`
	CreatedAt          primitive.DateTime `json:"createdAt" bson:"createdAt"`
	UpdatedAt          primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
}

// MedicationResponse represents the API response structure
type MedicationResponse struct {
	Medications []MedicationWithDetails `json:"medications"`
	Pagination Pagination               `json:"pagination"`
}

// MedicationWithDetails includes additional details for the response
type MedicationWithDetails struct {
	ID                primitive.ObjectID `json:"_id" bson:"_id"`
	StartDate         string             `json:"startDate" bson:"startDate"`
	Name              string             `json:"name" bson:"name"`
	Dosage            string             `json:"dosage" bson:"dosage"`
	Frequency         string             `json:"frequency" bson:"frequency"`
	CivilianID        string             `json:"civilianID" bson:"civilianID"`
	ActiveCommunityID string             `json:"activeCommunityID" bson:"activeCommunityID"`
	UserID            string             `json:"userID" bson:"userID"`
	FirstName         string             `json:"firstName" bson:"firstName"`
	LastName          string             `json:"lastName" bson:"lastName"`
	DateOfBirth       string             `json:"dateOfBirth" bson:"dateOfBirth"`
	CreatedAt         primitive.DateTime `json:"createdAt" bson:"createdAt"`
	UpdatedAt         primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
}
