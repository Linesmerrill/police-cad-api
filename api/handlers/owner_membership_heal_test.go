package handlers

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

// ownerLookup stubs the owner's user document. found=false plays a deleted
// account.
func ownerLookup(owner models.User, found bool) *mocks.UserDatabase {
	udb := &mocks.UserDatabase{}
	result := &mocks.SingleResultHelper{}
	if found {
		result.On("Decode", mock.Anything).Run(func(a mock.Arguments) {
			*(a.Get(0).(*models.User)) = owner
		}).Return(nil)
	} else {
		result.On("Decode", mock.Anything).Return(errors.New("mongo: no documents in result"))
	}
	udb.On("FindOne", mock.Anything, mock.Anything).Return(result)
	return udb
}

func communityOwnedBy(ownerID string) *models.Community {
	return &models.Community{ID: primitive.NewObjectID(), Details: models.CommunityDetails{OwnerID: ownerID}}
}

// The reported case: a 2020 community whose owner has no user.communities
// entry at all, and so was offered "Request to Join" on their own community.
func TestHealOwnerMembership_AddsAMissingOwner(t *testing.T) {
	ownerID := primitive.NewObjectID()
	community := communityOwnedBy(ownerID.Hex())

	udb := ownerLookup(models.User{ID: ownerID.Hex()}, true)
	var updates []bson.M
	udb.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).
		Run(func(a mock.Arguments) { updates = append(updates, a.Get(2).(bson.M)) }).
		Return(nil, nil)

	assert.True(t, healOwnerMembership(context.Background(), udb, community))

	added := updates[len(updates)-1]["$addToSet"].(bson.M)["user.communities"].(models.UserCommunity)
	assert.Equal(t, community.ID.Hex(), added.CommunityID)
	assert.Equal(t, "approved", added.Status)
}

// Every load of a healthy community runs this, so it must only read.
func TestHealOwnerMembership_LeavesAHealthyOwnerAlone(t *testing.T) {
	ownerID := primitive.NewObjectID()
	community := communityOwnedBy(ownerID.Hex())
	owner := models.User{ID: ownerID.Hex(), Details: models.UserDetails{
		Communities: []models.UserCommunity{{CommunityID: community.ID.Hex(), Status: "approved"}},
	}}

	udb := ownerLookup(owner, true)
	assert.False(t, healOwnerMembership(context.Background(), udb, community))
	udb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

// An owner stuck on a pending request to their own community is promoted,
// not given a second row.
func TestHealOwnerMembership_PromotesAPendingOwner(t *testing.T) {
	ownerID := primitive.NewObjectID()
	community := communityOwnedBy(ownerID.Hex())
	owner := models.User{ID: ownerID.Hex(), Details: models.UserDetails{
		Communities: []models.UserCommunity{{CommunityID: community.ID.Hex(), Status: "pending"}},
	}}

	udb := ownerLookup(owner, true)
	var updates []bson.M
	udb.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).
		Run(func(a mock.Arguments) { updates = append(updates, a.Get(2).(bson.M)) }).
		Return(nil, nil)

	assert.True(t, healOwnerMembership(context.Background(), udb, community))
	assert.Equal(t, []bson.M{{"$set": bson.M{"user.communities.$.status": "approved"}}}, updates)
}

// Orphans are a different problem: nothing to heal, and nothing is written.
func TestHealOwnerMembership_IgnoresOrphans(t *testing.T) {
	// The owner's account has been deleted.
	udb := ownerLookup(models.User{}, false)
	assert.False(t, healOwnerMembership(context.Background(), udb, communityOwnedBy(primitive.NewObjectID().Hex())))
	udb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)

	// No owner, or one that isn't an id: no lookup at all.
	for _, bad := range []string{"", "not-an-id"} {
		empty := &mocks.UserDatabase{}
		assert.False(t, healOwnerMembership(context.Background(), empty, communityOwnedBy(bad)))
		empty.AssertNotCalled(t, "FindOne", mock.Anything, mock.Anything)
	}

	assert.False(t, healOwnerMembership(context.Background(), nil, communityOwnedBy(primitive.NewObjectID().Hex())))
	assert.False(t, healOwnerMembership(context.Background(), &mocks.UserDatabase{}, nil))
}
