package models

import (
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// ArrestReport represents the main arrest report structure
type ArrestReport struct {
	ID      primitive.ObjectID  `json:"_id" bson:"_id"`
	Details ArrestReportDetails `json:"arrestReport" bson:"arrestReport"`
	Version int32               `json:"__v" bson:"__v"`
}

// ArrestReportDetails holds the structure for the inner arrest report details
type ArrestReportDetails struct {
	ReportNumber     string             `json:"reportNumber" bson:"reportNumber"`
	ArrestDate       string             `json:"arrestDate" bson:"arrestDate"` // Format: MM/DD/YYYY
	ArrestTime       string             `json:"arrestTime" bson:"arrestTime"` // Format: HH:MM (24-hour)
	ArrestLocation   string             `json:"arrestLocation" bson:"arrestLocation"`
	IncidentDate     string             `json:"incidentDate" bson:"incidentDate"` // Format: MM/DD/YYYY
	IncidentTime     string             `json:"incidentTime" bson:"incidentTime"` // Format: HH:MM (24-hour)
	IncidentLocation string             `json:"incidentLocation" bson:"incidentLocation"`
	Arrestee         Arrestee           `json:"arrestee" bson:"arrestee"`
	Officer          Officer            `json:"officer" bson:"officer"`
	Charges          string             `json:"charges" bson:"charges"`
	Narrative        string             `json:"narrative" bson:"narrative"`
	Witnesses        string             `json:"witnesses" bson:"witnesses"`
	ForceUsed        bool               `json:"forceUsed" bson:"forceUsed"`
	AttachedForms    []AttachedForm     `json:"attachedForms" bson:"attachedForms"`
	CreatedAt        primitive.DateTime `json:"createdAt" bson:"createdAt"`
	UpdatedAt        primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
}

// Arrestee represents the arrestee's details
type Arrestee struct {
	ID        string `json:"id" bson:"id"`
	Name      string `json:"name" bson:"name"`
	DOB       string `json:"dob" bson:"dob"` // Format: MM/DD/YYYY
	Address   string `json:"address" bson:"address"`
	Height    string `json:"height" bson:"height"` // e.g., "5'10\""
	Weight    string `json:"weight" bson:"weight"` // e.g., "180"
	EyeColor  string `json:"eyeColor" bson:"eyeColor"`
	HairColor string `json:"hairColor" bson:"hairColor"`
	Phone     string `json:"phone" bson:"phone"`
}

// Officer represents the arresting officer's details
type Officer struct {
	Name        string `json:"name" bson:"name"`
	BadgeNumber string `json:"badgeNumber" bson:"badgeNumber"`
}

// AttachedForm represents a form attached to the arrest report
type AttachedForm struct {
	FormID string      `json:"formId" bson:"formId"`
	Type   string      `json:"type" bson:"type"` // e.g., "evidence_booking", "tow_form"
	Data   interface{} `json:"data" bson:"data"` // Dynamic data based on form type
}

// EvidenceBookingForm represents the data for an evidence booking form
type EvidenceBookingForm struct {
	Description string `json:"description" bson:"description"`
	Location    string `json:"location" bson:"location"`
}

// TowForm represents the data for a tow form
type TowForm struct {
	Make         string `json:"make" bson:"make"`
	Model        string `json:"model" bson:"model"`
	LicensePlate string `json:"licensePlate" bson:"licensePlate"`
	TowCompany   string `json:"towCompany" bson:"towCompany"`
}

// PropertyReportForm represents the data for a property report form (placeholder)
type PropertyReportForm struct {
	Description  string `json:"description" bson:"description"`
	Location     string `json:"location" bson:"location"`
	Value        string `json:"value" bson:"value"`
	Owner        string `json:"owner" bson:"owner"`
	OwnerContact string `json:"ownerContact" bson:"ownerContact"`
}
