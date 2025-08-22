package models

import (
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// EMSVehicle holds the structure for the emsvehicles collection in mongo
type EMSVehicle struct {
	ID      primitive.ObjectID `json:"_id" bson:"_id"`
	Vehicle EMSVehicleDetails  `json:"vehicle" bson:"vehicle"`
	Version int32              `json:"__v" bson:"__v"`
}

// EMSVehicleDetails holds the structure for the inner vehicle structure
type EMSVehicleDetails struct {
	Plate             string             `json:"plate" bson:"plate"`
	Model             string             `json:"model" bson:"model"`
	EngineNumber      string             `json:"engineNumber" bson:"engineNumber"`
	Color             string             `json:"color" bson:"color"`
	RegisteredOwner   string             `json:"registeredOwner" bson:"registeredOwner"`
	ActiveCommunityID string             `json:"activeCommunityID" bson:"activeCommunityID"`
	UserID            string             `json:"userID" bson:"userID"`
	CreatedAt         primitive.DateTime `json:"createdAt" bson:"createdAt"`
	UpdatedAt         primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
}

// EMSVehicleResponse represents the API response structure
type EMSVehicleResponse struct {
	Vehicles   []EMSVehicleWithDetails `json:"vehicles"`
	Pagination Pagination              `json:"pagination"`
}

// EMSVehicleWithDetails includes additional details for the response
type EMSVehicleWithDetails struct {
	ID                primitive.ObjectID `json:"_id" bson:"_id"`
	Plate             string             `json:"plate" bson:"plate"`
	Model             string             `json:"model" bson:"model"`
	EngineNumber      string             `json:"engineNumber" bson:"engineNumber"`
	Color             string             `json:"color" bson:"color"`
	RegisteredOwner   string             `json:"registeredOwner" bson:"registeredOwner"`
	ActiveCommunityID string             `json:"activeCommunityID" bson:"activeCommunityID"`
	UserID            string             `json:"userID" bson:"userID"`
	CreatedAt         primitive.DateTime `json:"createdAt" bson:"createdAt"`
	UpdatedAt         primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
}

// ValidVehicleModels contains the list of valid vehicle model options
var ValidVehicleModels = []string{
	"FireTruck",
	"Fire Dept. Vehicle",
	"Fire Dept. Chief Vehicle",
	"Ambulance",
	"Medical Dept. Vehicle",
	"Medical Dept. Chief Vehicle",
	"LifeGuard Patrol Vehicle",
	"LifeGuard Boat",
}

// Legacy types for backward compatibility
type EmsVehicle struct {
	ID      string            `json:"_id" bson:"_id"`
	Details EmsVehicleDetails `json:"emsVehicle" bson:"emsVehicle"`
	Version int32             `json:"__v" bson:"__v"`
}

type EmsVehicleDetails struct {
	Email               string      `json:"email" bson:"email"`
	Plate               string      `json:"plate" bson:"plate"`
	Model               string      `json:"model" bson:"model"`
	EngineNumber        string      `json:"engineNumber" bson:"engineNumber"`
	Color               string      `json:"color" bson:"color"`
	DispatchStatus      string      `json:"dispatchStatus" bson:"dispatchStatus"`
	DispatchStatusSetBy string      `json:"dispatchStatusSetBy" bson:"dispatchStatusSetBy"`
	DispatchOnDuty      string      `json:"dispatchOnDuty" bson:"dispatchOnDuty"`
	RegisteredOwner     string      `json:"registeredOwner" bson:"registeredOwner"`
	ActiveCommunityID   string      `json:"activeCommunityID" bson:"activeCommunityID"`
	UserID              string      `json:"userID" bson:"userID"`
	CreatedAt           interface{} `json:"createdAt" bson:"createdAt"`
	UpdatedAt           interface{} `json:"updatedAt" bson:"updatedAt"`
}
