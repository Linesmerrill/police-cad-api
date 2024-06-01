package models

// Spotlight holds the structure for the spotlight collection in mongo
type Spotlight struct {
	ID      string           `json:"_id" bson:"_id"`
	Details SpotlightDetails `json:"spotlight" bson:"spotlight"`
	Version int32            `json:"__v" bson:"__v"`
}

// SpotlightDetails holds the structure for the inner user structure as
// defined in the spotlight collection in mongo
type SpotlightDetails struct {
	Image     string      `json:"image" bson:"image"`
	Title     string      `json:"title" bson:"title"`
	Time      string      `json:"time" bson:"time"`
	CreatedAt interface{} `json:"createdAt" bson:"createdAt"`
	UpdatedAt interface{} `json:"updatedAt" bson:"updatedAt"`
}
