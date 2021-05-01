package models

// User holds the structure for the community collection in mongo
type User struct {
	ID        string    `json:"_id" bson:"_id"`
	UserInner UserInner `json:"user" bson:"user"`
	Version   int32     `json:"__v" bson:"__v"`
}

type UserInner struct {
	Address              string      `json:"address" bson:"address"`
	ActiveCommunity      string      `json:"activeCommunity" bson:"activeCommunity"`
	CallSign             string      `json:"callSign" bson:"callSign"`
	DispatchStatus       string      `json:"dispatchStatus" bson:"dispatchStatus"`
	DispatchStatusSetBy  string      `json:"dispatchStatusSetBy" bson:"dispatchStatusSetBy"`
	Email                string      `json:"email" bson:"email"`
	Name                 string      `json:"name" bson:"name"`
	Username             string      `json:"username" bson:"username"`
	Password             string      `json:"password" bson:"password"`
	ResetPasswordToken   string      `json:"resetPasswordToken" bson:"resetPasswordToken"`
	ResetPasswordExpires interface{} `json:"resetPasswordExpires" bson:"resetPasswordExpires"`
	CreatedAt            interface{} `json:"createdAt" bson:"createdAt"`
	UpdatedAt            interface{} `json:"updatedAt" bson:"updatedAt"`
}
