package models

import "go.mongodb.org/mongo-driver/bson/primitive"

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
	Notes                 []Note                `json:"notes" bson:"notes"`
	Username              string                `json:"username" bson:"username"`
	Password              string                `json:"password" bson:"password"`
	ProfilePicture        string                `json:"profilePicture" bson:"profilePicture"`
	BackgroundImage       string                `json:"backgroundImage" bson:"backgroundImage"`
	Friends               []Friend              `json:"friends" bson:"friends"`
	Notifications         []Notification        `json:"notifications" bson:"notifications"`
	Communities           []UserCommunity       `json:"communities" bson:"communities"`
	IsOnline              bool                  `json:"isOnline" bson:"isOnline"`
	Subscription          Subscription          `json:"subscription" bson:"subscription"`
	ResetPasswordToken    string                `json:"resetPasswordToken" bson:"resetPasswordToken"`
	ResetPasswordExpires  interface{}           `json:"resetPasswordExpires" bson:"resetPasswordExpires"`
	CreatedAt             interface{}           `json:"createdAt" bson:"createdAt"`
	UpdatedAt             interface{}           `json:"updatedAt" bson:"updatedAt"`
}

// Note holds the structure for a note
type Note struct {
	ID        primitive.ObjectID `json:"_id" bson:"_id"`
	Title     string             `json:"title" bson:"title"`
	Content   string             `json:"content" bson:"content"`
	CreatedAt primitive.DateTime `json:"createdAt" bson:"createdAt"`
	UpdatedAt primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
}

// Subscription holds the structure for a user's subscription
type Subscription struct {
	ID                 string             `json:"id" bson:"id"`
	Plan               string             `json:"plan" bson:"plan"`
	Active             bool               `json:"active" bson:"active"`
	CancelAt           primitive.DateTime `json:"cancelAt" bson:"cancelAt"`
	CurrentPeriodStart primitive.DateTime `json:"currentPeriodStart" bson:"currentPeriodStart"`
	CurrentPeriodEnd   primitive.DateTime `json:"currentPeriodEnd" bson:"currentPeriodEnd"`
	IsAnnual           bool               `json:"isAnnual" bson:"isAnnual"`
	CreatedAt          primitive.DateTime `json:"createdAt" bson:"createdAt"`
	UpdatedAt          primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
	// Ads               string             `json:"ads" bson:"ads"`
	// CustomDepartments bool               `json:"customDepartments" bson:"customDepartments"`
	// Verified          bool               `json:"verified" bson:"verified"`
	// CommunitiesLimit  int                `json:"communitiesLimit" bson:"communitiesLimit"`
	// ExpiresAt         primitive.DateTime `json:"expiresAt" bson:"expiresAt"`
}

// Friend holds the structure for a friend
type Friend struct {
	FriendID  string      `json:"friend_id" bson:"friend_id"`
	Status    string      `json:"status" bson:"status"` // e.g., "pending", "approved"
	CreatedAt interface{} `json:"created_at" bson:"created_at"`
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
	Data3      string      `json:"data3" bson:"data3"`
	Data4      string      `json:"data4" bson:"data4"`
	Seen       bool        `json:"seen" bson:"seen"`
	CreatedAt  interface{} `json:"createdAt" bson:"createdAt"`
}

// UserCommunity holds the structure for a user community, mainly used to store the status of a community request for the user
type UserCommunity struct {
	ID          string `json:"_id" bson:"_id"`
	CommunityID string `json:"communityId" bson:"communityId"`
	Status      string `json:"status" bson:"status"`
}
