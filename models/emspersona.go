package models

import (
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// EMSPersona holds the structure for the ems collection in mongo
type EMSPersona struct {
	ID       primitive.ObjectID `json:"_id" bson:"_id"`
	Persona  PersonaDetails     `json:"persona" bson:"persona"`
	Version  int32              `json:"__v" bson:"__v"`
}

// PersonaDetails holds the structure for the inner persona structure
type PersonaDetails struct {
	FirstName          string             `json:"firstName" bson:"firstName"`
	LastName           string             `json:"lastName" bson:"lastName"`
	Department         string             `json:"department" bson:"department"`
	AssignmentArea     string             `json:"assignmentArea" bson:"assignmentArea"`
	Station            string             `json:"station" bson:"station"`
	CallSign           string             `json:"callSign" bson:"callSign"`
	ActiveCommunityID  string             `json:"activeCommunityID" bson:"activeCommunityID"`
	UserID             string             `json:"userID" bson:"userID"`
	CreatedAt          primitive.DateTime `json:"createdAt" bson:"createdAt"`
	UpdatedAt          primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
}

// EMSPersonaResponse represents the API response structure
type EMSPersonaResponse struct {
	Personas   []EMSPersonaWithDetails `json:"personas"`
	Pagination Pagination               `json:"pagination"`
}

// EMSPersonaWithDetails includes additional details for the response
type EMSPersonaWithDetails struct {
	ID                primitive.ObjectID `json:"_id" bson:"_id"`
	FirstName         string             `json:"firstName" bson:"firstName"`
	LastName          string             `json:"lastName" bson:"lastName"`
	Department        string             `json:"department" bson:"department"`
	AssignmentArea    string             `json:"assignmentArea" bson:"assignmentArea"`
	Station           string             `json:"station" bson:"station"`
	CallSign          string             `json:"callSign" bson:"callSign"`
	ActiveCommunityID string             `json:"activeCommunityID" bson:"activeCommunityID"`
	UserID            string             `json:"userID" bson:"userID"`
	CreatedAt         primitive.DateTime `json:"createdAt" bson:"createdAt"`
	UpdatedAt         primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
}
