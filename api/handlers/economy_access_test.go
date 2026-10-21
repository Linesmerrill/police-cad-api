package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// A civilian's wallet, inbox and active pick belong to the player who owns it.
// These tests pin who else may read them (community managers) and that nobody
// else may read, pay, contest or select them.

const (
	accessOwner    = "aaaaaaaaaaaaaaaaaaaaaaaa"
	accessStranger = "bbbbbbbbbbbbbbbbbbbbbbbb"
	accessManager  = "cccccccccccccccccccccccc"
)

// fakeAccessCivs serves one civilian by _id. Methods the handlers under test
// never call are left to the embedded interface and would panic if reached.
type fakeAccessCivs struct {
	databases.CivilianDatabase
	civ *models.Civilian
}

func (f *fakeAccessCivs) FindOne(_ context.Context, filter interface{}, _ ...*options.FindOneOptions) (*models.Civilian, error) {
	if f.civ != nil && filter.(bson.M)["_id"] == f.civ.ID {
		c := *f.civ
		return &c, nil
	}
	return nil, mongo.ErrNoDocuments
}

type fakeAccessCommunities struct {
	databases.CommunityDatabase
	community *models.Community
}

func (f *fakeAccessCommunities) FindOne(_ context.Context, filter interface{}) (*models.Community, error) {
	if f.community != nil && filter.(bson.M)["_id"] == f.community.ID {
		c := *f.community
		return &c, nil
	}
	return nil, mongo.ErrNoDocuments
}

// fakeAccessInbox serves one item by _id and records whether anything wrote.
type fakeAccessInbox struct {
	databases.InboxItemDatabase
	item       *models.InboxItem
	listFilter bson.M
	writes     int
}

func (f *fakeAccessInbox) FindOne(_ context.Context, filter interface{}, _ ...*options.FindOneOptions) (*models.InboxItem, error) {
	if f.item != nil && filter.(bson.M)["_id"] == f.item.ID {
		c := *f.item
		return &c, nil
	}
	return nil, mongo.ErrNoDocuments
}
func (f *fakeAccessInbox) Find(_ context.Context, filter interface{}, _ ...*options.FindOptions) ([]models.InboxItem, error) {
	f.listFilter = filter.(bson.M)
	return []models.InboxItem{}, nil
}
func (f *fakeAccessInbox) CountDocuments(context.Context, interface{}, ...*options.CountOptions) (int64, error) {
	return 0, nil
}
func (f *fakeAccessInbox) UpdateOne(context.Context, interface{}, interface{}, ...*options.UpdateOptions) error {
	f.writes++
	return nil
}

type fakeAccessActiveCivs struct {
	databases.UserActiveCivilianDatabase
	writes int
}

func (f *fakeAccessActiveCivs) UpdateOne(context.Context, interface{}, interface{}, ...*options.UpdateOptions) error {
	f.writes++
	return nil
}

// accessFixture is a community with a manager, and a civilian in it owned by
// accessOwner.
func accessFixture() (*models.Civilian, *models.Community) {
	community := &models.Community{ID: primitive.NewObjectID()}
	community.Details.OwnerID = "dddddddddddddddddddddddd"
	community.Details.Roles = []models.Role{{
		Name:        "Staff",
		Members:     []string{accessManager},
		Permissions: []models.Permission{{Name: "manage members", Enabled: true}},
	}}
	civ := &models.Civilian{ID: primitive.NewObjectID()}
	civ.Details.UserID = accessOwner
	civ.Details.ActiveCommunityID = community.ID.Hex()
	return civ, community
}

// asActor attaches an identity the way each client does: the mobile app with a
// verified bearer token (in context), the website and bot with ?userId=.
func asBearer(req *http.Request, userID string) *http.Request {
	return req.WithContext(api.WithAuthenticatedUserID(req.Context(), userID))
}

func TestCanViewCivilianEconomy(t *testing.T) {
	civ, community := accessFixture()

	assert.True(t, canViewCivilianEconomy(civ, community, accessOwner), "the owner")
	assert.True(t, canViewCivilianEconomy(civ, community, accessManager), "a member manager of the civilian's community")
	assert.True(t, canViewCivilianEconomy(civ, community, community.Details.OwnerID), "the community owner")
	assert.False(t, canViewCivilianEconomy(civ, community, accessStranger), "anyone else")
	assert.False(t, canViewCivilianEconomy(civ, community, ""), "no identity")
	assert.False(t, canViewCivilianEconomy(civ, nil, accessManager), "manager rights need the community")

	other := &models.Community{ID: primitive.NewObjectID(), Details: community.Details}
	assert.False(t, canViewCivilianEconomy(civ, other, accessManager), "managing a different community grants nothing")
}

func TestOwnsCivilianInCommunity(t *testing.T) {
	civ, community := accessFixture()
	assert.True(t, ownsCivilianInCommunity(civ, accessOwner, community.ID.Hex()))
	assert.False(t, ownsCivilianInCommunity(civ, accessStranger, community.ID.Hex()), "someone else's civilian")
	assert.False(t, ownsCivilianInCommunity(civ, accessOwner, primitive.NewObjectID().Hex()), "own civilian, other community")
	assert.False(t, ownsCivilianInCommunity(civ, accessManager, community.ID.Hex()), "managers cannot pick it as theirs")
	assert.False(t, ownsCivilianInCommunity(civ, "", community.ID.Hex()))
}

func walletRequest(e Economy, civID, query string, bearer string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v2/economy/wallet/"+civID+query, nil)
	req = mux.SetURLVars(req, map[string]string{"civilianId": civID})
	if bearer != "" {
		req = asBearer(req, bearer)
	}
	rec := httptest.NewRecorder()
	e.GetWalletHandler(rec, req)
	return rec
}

func TestGetWalletHandlerOwnership(t *testing.T) {
	civ, community := accessFixture()
	civ.Details.Balance = 12345
	civ.Details.BalanceInitialized = true
	e := Economy{
		CivDB:  &fakeAccessCivs{civ: civ},
		CommDB: &fakeAccessCommunities{community: community},
		IDB:    &fakeAccessInbox{},
	}
	id := civ.ID.Hex()

	assert.Equal(t, http.StatusOK, walletRequest(e, id, "?userId="+accessOwner, "").Code, "owner via ?userId=")
	assert.Equal(t, http.StatusOK, walletRequest(e, id, "", accessOwner).Code, "owner via bearer")
	assert.Equal(t, http.StatusOK, walletRequest(e, id, "?userId="+accessManager, "").Code, "member manager")

	assert.Equal(t, http.StatusForbidden, walletRequest(e, id, "?userId="+accessStranger, "").Code, "someone else")
	assert.Equal(t, http.StatusForbidden, walletRequest(e, id, "?userId="+accessOwner, accessStranger).Code,
		"a signed-in caller cannot claim another identity in the query")
	assert.Equal(t, http.StatusUnauthorized, walletRequest(e, id, "", "").Code, "no identity")

	rec := walletRequest(e, id, "?userId="+accessStranger, "")
	assert.NotContains(t, rec.Body.String(), "12345", "a refused wallet leaks no balance")
}

func inboxListRequest(e Economy, query string, bearer string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v2/economy/inbox"+query, nil)
	if bearer != "" {
		req = asBearer(req, bearer)
	}
	rec := httptest.NewRecorder()
	e.ListInboxHandler(rec, req)
	return rec
}

func TestListInboxHandlerOwnership(t *testing.T) {
	civ, community := accessFixture()
	inbox := &fakeAccessInbox{}
	e := Economy{
		CivDB:  &fakeAccessCivs{civ: civ},
		CommDB: &fakeAccessCommunities{community: community},
		IDB:    inbox,
	}
	civQ := "?civilianId=" + civ.ID.Hex()

	t.Run("the owner lists their civilian's inbox", func(t *testing.T) {
		assert.Equal(t, http.StatusOK, inboxListRequest(e, civQ+"&userId="+accessOwner, "").Code)
		assert.Equal(t, http.StatusOK, inboxListRequest(e, civQ, accessOwner).Code)
	})

	t.Run("with a civilian, userId identifies the caller and does not filter", func(t *testing.T) {
		assert.Equal(t, http.StatusOK, inboxListRequest(e, civQ+"&userId="+accessManager, "").Code)
		assert.Equal(t, bson.M{"civilianId": civ.ID.Hex()}, inbox.listFilter)
	})

	t.Run("someone else's civilian is refused", func(t *testing.T) {
		assert.Equal(t, http.StatusForbidden, inboxListRequest(e, civQ+"&userId="+accessStranger, "").Code)
		assert.Equal(t, http.StatusForbidden, inboxListRequest(e, civQ, accessStranger).Code)
	})

	t.Run("listing by user is only for yourself", func(t *testing.T) {
		assert.Equal(t, http.StatusOK, inboxListRequest(e, "?userId="+accessOwner, "").Code)
		assert.Equal(t, http.StatusForbidden, inboxListRequest(e, "?userId="+accessOwner, accessStranger).Code)
	})

	t.Run("no identity, or no target, is refused", func(t *testing.T) {
		assert.Equal(t, http.StatusUnauthorized, inboxListRequest(e, civQ, "").Code)
		assert.Equal(t, http.StatusBadRequest, inboxListRequest(e, "?communityId="+community.ID.Hex(), accessOwner).Code,
			"a whole community's fines are not listable")
	})

	t.Run("an unknown civilian is a 404", func(t *testing.T) {
		assert.Equal(t, http.StatusNotFound, inboxListRequest(e, "?civilianId="+primitive.NewObjectID().Hex(), accessOwner).Code)
	})
}

func inboxItemFixture(status string) *models.InboxItem {
	return &models.InboxItem{
		ID:          primitive.NewObjectID(),
		UserID:      accessOwner,
		CivilianID:  primitive.NewObjectID().Hex(),
		CommunityID: primitive.NewObjectID().Hex(),
		Status:      status,
		Amount:      5000,
	}
}

func inboxActionRequest(handler http.HandlerFunc, itemID, action, query, bearer string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"reason": "not me"})
	req := httptest.NewRequest(http.MethodPost, "/api/v2/economy/inbox/"+itemID+"/"+action+query, bytes.NewReader(body))
	req = mux.SetURLVars(req, map[string]string{"id": itemID})
	if bearer != "" {
		req = asBearer(req, bearer)
	}
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

func TestContestInboxItemHandlerOwnership(t *testing.T) {
	t.Run("someone else's fine cannot be contested", func(t *testing.T) {
		inbox := &fakeAccessInbox{item: inboxItemFixture("pending")}
		e := Economy{IDB: inbox, CommDB: &fakeAccessCommunities{}}
		id := inbox.item.ID.Hex()

		assert.Equal(t, http.StatusForbidden, inboxActionRequest(e.ContestInboxItemHandler, id, "contest", "?userId="+accessStranger, "").Code)
		assert.Equal(t, http.StatusForbidden, inboxActionRequest(e.ContestInboxItemHandler, id, "contest", "", accessStranger).Code)
		assert.Equal(t, http.StatusUnauthorized, inboxActionRequest(e.ContestInboxItemHandler, id, "contest", "", "").Code)
		assert.Zero(t, inbox.writes, "nothing was written: reason and due date untouched")
	})

	t.Run("the owner contests their own fine", func(t *testing.T) {
		inbox := &fakeAccessInbox{item: inboxItemFixture("pending")}
		e := Economy{IDB: inbox, CommDB: &fakeAccessCommunities{}}
		rec := inboxActionRequest(e.ContestInboxItemHandler, inbox.item.ID.Hex(), "contest", "?userId="+accessOwner, "")
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, 1, inbox.writes)
	})

	t.Run("ownership is checked before the fine's state is revealed", func(t *testing.T) {
		inbox := &fakeAccessInbox{item: inboxItemFixture("paid")}
		e := Economy{IDB: inbox, CommDB: &fakeAccessCommunities{}}
		rec := inboxActionRequest(e.ContestInboxItemHandler, inbox.item.ID.Hex(), "contest", "?userId="+accessStranger, "")
		assert.Equal(t, http.StatusForbidden, rec.Code)
	})
}

func TestPayInboxItemHandlerOwnership(t *testing.T) {
	t.Run("a paid item is not echoed back to someone else", func(t *testing.T) {
		item := inboxItemFixture("paid")
		item.Title = "Speeding"
		e := Economy{IDB: &fakeAccessInbox{item: item}}
		rec := inboxActionRequest(e.PayInboxItemHandler, item.ID.Hex(), "pay", "?userId="+accessStranger, "")
		assert.Equal(t, http.StatusForbidden, rec.Code)
		assert.NotContains(t, rec.Body.String(), "Speeding")
	})

	t.Run("the owner still gets the idempotent paid response", func(t *testing.T) {
		item := inboxItemFixture("paid")
		e := Economy{IDB: &fakeAccessInbox{item: item}}
		rec := inboxActionRequest(e.PayInboxItemHandler, item.ID.Hex(), "pay", "?userId="+accessOwner, "")
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("no identity never matches an item with no owner", func(t *testing.T) {
		item := inboxItemFixture("pending")
		item.UserID = ""
		e := Economy{IDB: &fakeAccessInbox{item: item}}
		rec := inboxActionRequest(e.PayInboxItemHandler, item.ID.Hex(), "pay", "", "")
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("someone else cannot pay a pending item", func(t *testing.T) {
		item := inboxItemFixture("pending")
		e := Economy{IDB: &fakeAccessInbox{item: item}}
		rec := inboxActionRequest(e.PayInboxItemHandler, item.ID.Hex(), "pay", "", accessStranger)
		assert.Equal(t, http.StatusForbidden, rec.Code)
	})
}

func TestGetInboxItemHandlerOwnership(t *testing.T) {
	item := inboxItemFixture("pending")
	e := Economy{IDB: &fakeAccessInbox{item: item}}
	get := func(query, bearer string) int {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/economy/inbox/"+item.ID.Hex()+query, nil)
		req = mux.SetURLVars(req, map[string]string{"id": item.ID.Hex()})
		if bearer != "" {
			req = asBearer(req, bearer)
		}
		rec := httptest.NewRecorder()
		e.GetInboxItemHandler(rec, req)
		return rec.Code
	}
	assert.Equal(t, http.StatusOK, get("?userId="+accessOwner, ""))
	assert.Equal(t, http.StatusOK, get("", accessOwner))
	assert.Equal(t, http.StatusNotFound, get("?userId="+accessStranger, ""))
	assert.Equal(t, http.StatusUnauthorized, get("", ""), "no identity no longer reads any item")
}

func TestSetActiveCivilianHandlerOwnership(t *testing.T) {
	civ, community := accessFixture()
	put := func(body map[string]string, bearer string) (int, *fakeAccessActiveCivs) {
		store := &fakeAccessActiveCivs{}
		u := User{ACDB: store, CivDB: &fakeAccessCivs{civ: civ}}
		b, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPut, "/api/v2/user/active-civilian", bytes.NewReader(b))
		if bearer != "" {
			req = asBearer(req, bearer)
		}
		rec := httptest.NewRecorder()
		u.SetActiveCivilianHandler(rec, req)
		return rec.Code, store
	}
	pick := func(userID, communityID string) map[string]string {
		return map[string]string{"userId": userID, "communityId": communityID, "civilianId": civ.ID.Hex()}
	}

	code, store := put(pick(accessOwner, community.ID.Hex()), "")
	assert.Equal(t, http.StatusOK, code, "own civilian in its community")
	assert.Equal(t, 1, store.writes)

	code, _ = put(pick(accessOwner, community.ID.Hex()), accessOwner)
	assert.Equal(t, http.StatusOK, code, "own civilian, bearer")

	code, store = put(pick(accessStranger, community.ID.Hex()), "")
	assert.Equal(t, http.StatusForbidden, code, "someone else's civilian")
	assert.Zero(t, store.writes)

	code, store = put(pick(accessOwner, primitive.NewObjectID().Hex()), "")
	assert.Equal(t, http.StatusForbidden, code, "own civilian pinned in a different community")
	assert.Zero(t, store.writes)

	code, store = put(pick(accessOwner, community.ID.Hex()), accessStranger)
	assert.Equal(t, http.StatusForbidden, code, "a signed-in caller cannot set someone else's pick")
	assert.Zero(t, store.writes)

	code, _ = put(map[string]string{"userId": accessOwner, "communityId": community.ID.Hex(), "civilianId": primitive.NewObjectID().Hex()}, "")
	assert.Equal(t, http.StatusNotFound, code, "a civilian that does not exist")
}
