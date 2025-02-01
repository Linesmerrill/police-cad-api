package models

// LastAccessedCommunity holds the structure for the lastAccessedCommunity for "Jump Back In" feature
type LastAccessedCommunity struct {
	CommunityID string      `json:"communityID" bson:"communityID"`
	CreatedAt   interface{} `json:"createdAt" bson:"createdAt"`
}
