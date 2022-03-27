package models

// Ems holds the structure for the ems collection in mongo
type Ems struct {
	ID      string     `json:"_id" bson:"_id"`
	Details EmsDetails `json:"ems" bson:"ems"`
	Version int32      `json:"__v" bson:"__v"`
}

// EmsDetails holds the structure for the inner user structure as
// defined in the ems collection in mongo
type EmsDetails struct {
	Email             string      `json:"email" bson:"email"`
	FirstName         string      `json:"firstName" bson:"firstName"`
	LastName          string      `json:"lastName" bson:"lastName"`
	Department        string      `json:"department" bson:"department"`
	AssignmentArea    string      `json:"assignmentArea" bson:"assignmentArea"`
	Station           string      `json:"station" bson:"station"`
	CallSign          string      `json:"callSign" bson:"callSign"`
	ActiveCommunityID string      `json:"activeCommunityID" bson:"activeCommunityID"`
	UserID            string      `json:"userID" bson:"userID"`
	CreatedAt         interface{} `json:"createdAt" bson:"createdAt"`
	UpdatedAt         interface{} `json:"updatedAt" bson:"updatedAt"`
}
