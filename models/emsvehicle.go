package models

// EmsVehicle holds the structure for the emsVehicle collection in mongo
type EmsVehicle struct {
	ID      string            `json:"_id" bson:"_id"`
	Details EmsVehicleDetails `json:"emsVehicle" bson:"emsVehicle"`
	Version int32             `json:"__v" bson:"__v"`
}

// EmsVehicleDetails holds the structure for the inner user structure as
// defined in the emsVehicle collection in mongo
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
