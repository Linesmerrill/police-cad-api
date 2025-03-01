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
	ActiveCommunity       string                `json:"activeCommunity" bson:"activeCommunity"` // will be deprecated, use lastAccessedCommunity and communities
	CallSign              string                `json:"callSign" bson:"callSign"`
	DispatchStatus        string                `json:"dispatchStatus" bson:"dispatchStatus"`
	DispatchStatusSetBy   string                `json:"dispatchStatusSetBy" bson:"dispatchStatusSetBy"`
	LastAccessedCommunity LastAccessedCommunity `json:"lastAccessedCommunity" bson:"lastAccessedCommunity"`
	Email                 string                `json:"email" bson:"email"`
	Name                  string                `json:"name" bson:"name"`
	Username              string                `json:"username" bson:"username"`
	Password              string                `json:"password" bson:"password"`
	ProfilePicture        string                `json:"profilePicture" bson:"profilePicture"`
	Friends               []Friend              `json:"friends" bson:"friends"`
	Notifications         []Notification        `json:"notifications" bson:"notifications"`
	Communities           []UserCommunity       `json:"communities" bson:"communities"`
	IsOnline              bool                  `json:"isOnline" bson:"isOnline"`
	ResetPasswordToken    string                `json:"resetPasswordToken" bson:"resetPasswordToken"`
	ResetPasswordExpires  interface{}           `json:"resetPasswordExpires" bson:"resetPasswordExpires"`
	CreatedAt             interface{}           `json:"createdAt" bson:"createdAt"`
	UpdatedAt             interface{}           `json:"updatedAt" bson:"updatedAt"`
}

// Friend holds the structure for a friend
type Friend struct {
	FriendID   string      `json:"friend_id" bson:"friend_id"`
	Status     string      `json:"status" bson:"status"` // e.g., "pending", "approved"
	LastOnline interface{} `json:"last_online" bson:"last_online"`
	IsOnline   bool        `json:"is_online" bson:"is_online"`
	CreatedAt  interface{} `json:"created_at" bson:"created_at"`
}

// Notification holds the structure for a notification
type Notification struct {
	ID         string      `json:"_id" bson:"_id"`
	SentFromID string      `json:"sentFromID" bson:"sentFromID"`
	SentToID   string      `json:"sentToID" bson:"sentToID"`
	Type       string      `json:"type" bson:"type"`
	Message    string      `json:"message" bson:"message"`
	Data1      string      `json:"data1" bson:"data1"`
	Data2      string      `json:"data2" bson:"data2"`
	Seen       bool        `json:"seen" bson:"seen"`
	CreatedAt  interface{} `json:"createdAt" bson:"createdAt"`
}

// UserCommunity holds the structure for a user community, mainly used to store the status of a community request for the user
type UserCommunity struct {
	ID          string `json:"_id" bson:"_id"`
	CommunityID string `json:"communityID" bson:"communityID"`
	Status      string `json:"status" bson:"status"`
}
