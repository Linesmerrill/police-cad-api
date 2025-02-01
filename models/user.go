package models

// User holds the structure for the user collection in mongo
type User struct {
	ID      string      `json:"_id" bson:"_id"`
	Details UserDetails `json:"user" bson:"user"`
	Version int32       `json:"__v" bson:"__v"`
}

// UserDetails holds the structure for the inner user structure as defined in the user collection in mongo
type UserDetails struct {
	Address               string                `json:"address" bson:"address"`
	ActiveCommunity       string                `json:"activeCommunity" bson:"activeCommunity"`
	CallSign              string                `json:"callSign" bson:"callSign"`
	DispatchStatus        string                `json:"dispatchStatus" bson:"dispatchStatus"`
	DispatchStatusSetBy   string                `json:"dispatchStatusSetBy" bson:"dispatchStatusSetBy"`
	LastAccessedCommunity LastAccessedCommunity `json:"lastAccessedCommunity" bson:"lastAccessedCommunity"`
	Email                 string                `json:"email" bson:"email"`
	Name                  string                `json:"name" bson:"name"`
	Username              string                `json:"username" bson:"username"`
	Password              string                `json:"password" bson:"password"`
	ProfilePicture        string                `json:"profilePicture" bson:"profilePicture"`
	ResetPasswordToken    string                `json:"resetPasswordToken" bson:"resetPasswordToken"`
	ResetPasswordExpires  interface{}           `json:"resetPasswordExpires" bson:"resetPasswordExpires"`
	CreatedAt             interface{}           `json:"createdAt" bson:"createdAt"`
	UpdatedAt             interface{}           `json:"updatedAt" bson:"updatedAt"`
}
