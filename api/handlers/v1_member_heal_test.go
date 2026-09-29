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

func v1Member(communityID string, entries ...models.UserCommunity) *models.User {
	return &models.User{ID: primitive.NewObjectID().Hex(), Details: models.UserDetails{
		ActiveCommunity: communityID, Communities: entries,
	}}
}

func communityLookup(c *models.Community, err error) *mocks.CommunityDatabase {
	cdb := &mocks.CommunityDatabase{}
	cdb.On("FindOne", mock.Anything, mock.Anything).Return(c, err)
	return cdb
}

func recordingUserDB() (*mocks.UserDatabase, *[]bson.M) {
	udb := &mocks.UserDatabase{}
	var updates []bson.M
	udb.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).
		Run(func(a mock.Arguments) { updates = append(updates, a.Get(2).(bson.M)) }).
		Return(nil, nil)
	return udb, &updates
}

// The V1 player: activeCommunity set, nothing in communities. Knowing the
// code was the approval in V1, so they come back approved.
func TestHealV1Membership_BringsAV1MemberBack(t *testing.T) {
	cid := primitive.NewObjectID()
	user := v1Member(cid.Hex())
	udb, updates := recordingUserDB()

	changed := healV1Membership(context.Background(), udb,
		communityLookup(&models.Community{ID: cid}, nil), user)

	assert.True(t, changed)
	added := (*updates)[len(*updates)-1]["$addToSet"].(bson.M)["user.communities"].(models.UserCommunity)
	assert.Equal(t, cid.Hex(), added.CommunityID)
	assert.Equal(t, "approved", added.Status)
}

// Anything decided since V1 wins, whatever it was: a pending request is not
// promoted, and a decline or ban is not undone.
func TestHealV1Membership_NeverOverridesALaterDecision(t *testing.T) {
	cid := primitive.NewObjectID().Hex()
	for _, status := range []string{"approved", "pending", "declined", "banned"} {
		udb := &mocks.UserDatabase{}
		cdb := &mocks.CommunityDatabase{}
		user := v1Member(cid, models.UserCommunity{CommunityID: cid, Status: status})

		assert.False(t, healV1Membership(context.Background(), udb, cdb, user), status)
		cdb.AssertNotCalled(t, "FindOne", mock.Anything, mock.Anything)
		udb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
	}
}

func TestHealV1Membership_RespectsTheBanList(t *testing.T) {
	cid := primitive.NewObjectID()
	user := v1Member(cid.Hex())
	udb := &mocks.UserDatabase{}
	community := &models.Community{ID: cid, Details: models.CommunityDetails{BanList: []string{user.ID}}}

	assert.False(t, healV1Membership(context.Background(), udb, communityLookup(community, nil), user))
	udb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

// Deleted, or pending deletion (FindOne excludes those): nothing to rejoin.
func TestHealV1Membership_SkipsACommunityThatIsGone(t *testing.T) {
	user := v1Member(primitive.NewObjectID().Hex())
	udb := &mocks.UserDatabase{}

	assert.False(t, healV1Membership(context.Background(), udb,
		communityLookup(nil, errors.New("mongo: no documents in result")), user))
	udb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

// This runs on every login and token refresh, so the common case must not
// touch the database at all.
func TestHealV1Membership_CostsNothingWithoutAV1Community(t *testing.T) {
	for _, ac := range []string{"", "not-an-id"} {
		udb := &mocks.UserDatabase{}
		cdb := &mocks.CommunityDatabase{}
		assert.False(t, healV1Membership(context.Background(), udb, cdb, v1Member(ac)))
		cdb.AssertNotCalled(t, "FindOne", mock.Anything, mock.Anything)
	}
	assert.False(t, healV1Membership(context.Background(), nil, &mocks.CommunityDatabase{}, v1Member(primitive.NewObjectID().Hex())))
	assert.False(t, healV1Membership(context.Background(), &mocks.UserDatabase{}, &mocks.CommunityDatabase{}, nil))
}
