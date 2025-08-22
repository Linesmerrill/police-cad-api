package models

import (
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// MedicalReport holds the structure for the medical report collection in mongo
type MedicalReport struct {
	ID      primitive.ObjectID `json:"_id" bson:"_id"`
	Report  MedicalReportDetails `json:"report" bson:"report"`
	Version int32              `json:"__v" bson:"__v"`
}

// MedicalReportDetails holds the structure for the inner medical report structure
type MedicalReportDetails struct {
	Date               string             `json:"date" bson:"date"`
	Details            string             `json:"details" bson:"details"`
	CivilianID         string             `json:"civilianID" bson:"civilianID"`
	ReportingEmsID     string             `json:"reportingEmsID" bson:"reportingEmsID"`
	Hospitalized       bool               `json:"hospitalized" bson:"hospitalized"`
	Deceased           bool               `json:"deceased" bson:"deceased"`
	ActiveCommunityID  string             `json:"activeCommunityID" bson:"activeCommunityID"`
	UserID             string             `json:"userID" bson:"userID"`
	FirstName          string             `json:"firstName" bson:"firstName"`
	LastName           string             `json:"lastName" bson:"lastName"`
	DateOfBirth        string             `json:"dateOfBirth" bson:"dateOfBirth"`
	CreatedAt          primitive.DateTime `json:"createdAt" bson:"createdAt"`
	UpdatedAt          primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
}

// MedicalReportResponse represents the API response structure
type MedicalReportResponse struct {
	MedicalReports []MedicalReportWithEms `json:"medicalReports"`
	Pagination     Pagination             `json:"pagination"`
}

// MedicalReportWithEms includes EMS information for the response
type MedicalReportWithEms struct {
	ID                primitive.ObjectID `json:"_id" bson:"_id"`
	CivilianID        string             `json:"civilianID" bson:"civilianID"`
	ReportingEmsID    string             `json:"reportingEmsID" bson:"reportingEmsID"`
	ReportDate        string             `json:"reportDate" bson:"reportDate"`
	ReportTime        string             `json:"reportTime" bson:"reportTime"`
	Hospitalized      string             `json:"hospitalized" bson:"hospitalized"`
	Deceased          bool               `json:"deceased" bson:"deceased"`
	Details           string             `json:"details" bson:"details"`
	ActiveCommunityID string             `json:"activeCommunityID" bson:"activeCommunityID"`
	CreatedAt         primitive.DateTime `json:"createdAt" bson:"createdAt"`
	UpdatedAt         primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
	ReportingEms      EmsInfo            `json:"reportingEms" bson:"reportingEms"`
}

// EmsInfo contains basic EMS information for the response
type EmsInfo struct {
	FirstName string `json:"firstName" bson:"firstName"`
	LastName  string `json:"lastName" bson:"lastName"`
	Department string `json:"department" bson:"department"`
}

// Pagination represents pagination information
type Pagination struct {
	CurrentPage int64 `json:"currentPage"`
	TotalPages  int64 `json:"totalPages"`
	TotalRecords int64 `json:"totalRecords"`
	Limit       int64 `json:"limit"`
}
