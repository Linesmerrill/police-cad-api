package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

// fakeRpWebhook stands in for the rp-servers Discord webhook and records which
// messages were deleted. failFor makes a given message ID answer 500.
type fakeRpWebhook struct {
	mu      sync.Mutex
	deleted []string
	failFor map[string]bool
}

func newFakeRpWebhook(t *testing.T) *fakeRpWebhook {
	t.Helper()
	f := &fakeRpWebhook{failFor: map[string]bool{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Method != http.MethodDelete {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if f.failFor[id] {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		f.deleted = append(f.deleted, id)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	t.Setenv("DISCORD_RP_SERVERS_WEBHOOK_URL", server.URL+"/api/webhooks/1/token")
	return f
}

func promoPost(id, messageID string, removed bool) models.RpPromotionPost {
	p := models.RpPromotionPost{
		ID:        id,
		MessageID: messageID,
		PostedAt:  primitive.NewDateTimeFromTime(time.Now().Add(-time.Hour)),
		Data:      models.RpPromotionData{ServerName: "Chicago xbox roleplay"},
	}
	if removed {
		at := primitive.NewDateTimeFromTime(time.Now().Add(-30 * time.Minute))
		p.RemovedAt = &at
	}
	return p
}

// promoRemovalUpdate matches the update that marks promotions removed because
// their community was deleted.
func promoRemovalUpdate() interface{} {
	return mock.MatchedBy(func(update bson.M) bool {
		set, ok := update["$set"].(bson.M)
		if !ok {
			return false
		}
		return set["community.rpPromotion.history.$[elem].removeReason"] == "community deleted" &&
			set["community.rpPromotion.history.$[elem].removedBy"] == "system"
	})
}

func deleteRequest() *http.Request {
	return newCommunityRequest(http.MethodDelete, "/api/v1/community/"+softDeleteCommunityID, nil,
		map[string]string{"community_id": softDeleteCommunityID})
}

// Deleting a community takes its live promotions down from Discord and marks
// them removed. Posts already removed are left alone, and a post with no
// Discord message is marked without a Discord call.
func TestDeleteCommunity_RemovesLivePromotions(t *testing.T) {
	webhook := newFakeRpWebhook(t)
	cID, _ := primitive.ObjectIDFromHex(softDeleteCommunityID)

	cdb := &mocks.CommunityDatabase{}
	udb := &mocks.UserDatabase{}
	aldb := &mocks.AuditLogDatabase{}

	cdb.On("FindOneIncludingPending", mock.Anything, bson.M{"_id": cID}).Return(&models.Community{
		ID: cID,
		Details: models.CommunityDetails{
			Name: "Chicago xbox roleplay",
			RpPromotion: &models.RpPromotion{History: []models.RpPromotionPost{
				promoPost("live-1", "msg-1", false),
				promoPost("never-posted", "", false),
				promoPost("old", "msg-old", true),
			}},
		},
	}, nil)
	// The soft delete itself.
	cdb.On("UpdateOne", mock.Anything, bson.M{"_id": cID}, mock.MatchedBy(func(update bson.M) bool {
		set, _ := update["$set"].(bson.M)
		_, ok := set["community.pendingDeletionAt"]
		return ok
	})).Return(nil).Once()

	var targeted []string
	cdb.On("UpdateOne", mock.Anything, bson.M{"_id": cID}, promoRemovalUpdate(), mock.Anything).
		Run(func(args mock.Arguments) {
			targeted = arrayFilterIDs(t, args)
		}).Return(nil).Once()
	aldb.On("InsertOne", mock.Anything, mock.Anything).Return(nil, nil).Maybe()

	c := newSoftDeleteHandler(cdb, udb, aldb)
	rr := httptest.NewRecorder()
	http.HandlerFunc(c.DeleteCommunityByIDHandler).ServeHTTP(rr, deleteRequest())

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, []string{"msg-1"}, webhook.deleted, "only the live post with a Discord message is deleted there")
	assert.ElementsMatch(t, []string{"live-1", "never-posted"}, targeted)
	cdb.AssertExpectations(t)
}

// A promotion Discord would not delete stays live in the console for a person
// to remove, and the owner's deletion still goes through.
func TestDeleteCommunity_PromotionCleanupNeverBlocksDeletion(t *testing.T) {
	webhook := newFakeRpWebhook(t)
	webhook.failFor["msg-bad"] = true
	cID, _ := primitive.ObjectIDFromHex(softDeleteCommunityID)

	cdb := &mocks.CommunityDatabase{}
	udb := &mocks.UserDatabase{}
	aldb := &mocks.AuditLogDatabase{}

	cdb.On("FindOneIncludingPending", mock.Anything, bson.M{"_id": cID}).Return(&models.Community{
		ID: cID,
		Details: models.CommunityDetails{
			Name: "Two posts",
			RpPromotion: &models.RpPromotion{History: []models.RpPromotionPost{
				promoPost("bad", "msg-bad", false),
				promoPost("good", "msg-good", false),
			}},
		},
	}, nil)
	cdb.On("UpdateOne", mock.Anything, bson.M{"_id": cID}, mock.MatchedBy(func(update bson.M) bool {
		set, _ := update["$set"].(bson.M)
		_, ok := set["community.pendingDeletionAt"]
		return ok
	})).Return(nil).Once()

	var targeted []string
	cdb.On("UpdateOne", mock.Anything, bson.M{"_id": cID}, promoRemovalUpdate(), mock.Anything).
		Run(func(args mock.Arguments) { targeted = arrayFilterIDs(t, args) }).
		Return(nil).Once()
	aldb.On("InsertOne", mock.Anything, mock.Anything).Return(nil, nil).Maybe()

	c := newSoftDeleteHandler(cdb, udb, aldb)
	rr := httptest.NewRecorder()
	http.HandlerFunc(c.DeleteCommunityByIDHandler).ServeHTTP(rr, deleteRequest())

	assert.Equal(t, http.StatusOK, rr.Code, "a promotion that would not come down is no reason to refuse the deletion")
	assert.Equal(t, []string{"good"}, targeted, "the post still in Discord is not marked removed")
	cdb.AssertExpectations(t)
}

// With every post already removed there is nothing to do: no Discord call and
// no second update.
func TestDeleteCommunity_NothingLiveNothingToDo(t *testing.T) {
	webhook := newFakeRpWebhook(t)
	cID, _ := primitive.ObjectIDFromHex(softDeleteCommunityID)

	cdb := &mocks.CommunityDatabase{}
	udb := &mocks.UserDatabase{}
	aldb := &mocks.AuditLogDatabase{}

	cdb.On("FindOneIncludingPending", mock.Anything, bson.M{"_id": cID}).Return(&models.Community{
		ID: cID,
		Details: models.CommunityDetails{
			Name:        "Quiet",
			RpPromotion: &models.RpPromotion{History: []models.RpPromotionPost{promoPost("old", "msg-old", true)}},
		},
	}, nil)
	cdb.On("UpdateOne", mock.Anything, bson.M{"_id": cID}, mock.Anything).Return(nil).Once()
	aldb.On("InsertOne", mock.Anything, mock.Anything).Return(nil, nil).Maybe()

	c := newSoftDeleteHandler(cdb, udb, aldb)
	rr := httptest.NewRecorder()
	http.HandlerFunc(c.DeleteCommunityByIDHandler).ServeHTTP(rr, deleteRequest())

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Empty(t, webhook.deleted)
	cdb.AssertNumberOfCalls(t, "UpdateOne", 1)
}

// arrayFilterIDs pulls the post IDs out of the update's array filter, which is
// what decides which history entries get marked removed.
func arrayFilterIDs(t *testing.T, args mock.Arguments) []string {
	t.Helper()
	opts, ok := args.Get(3).(*options.UpdateOptions)
	if !ok || opts == nil || opts.ArrayFilters == nil || len(opts.ArrayFilters.Filters) != 1 {
		t.Fatalf("expected exactly one array filter on the removal update, got %#v", args.Get(3))
	}
	filter, ok := opts.ArrayFilters.Filters[0].(bson.M)
	if !ok {
		t.Fatalf("array filter is %T, want bson.M", opts.ArrayFilters.Filters[0])
	}
	in, ok := filter["elem.id"].(bson.M)
	if !ok {
		t.Fatalf("array filter does not select on elem.id: %#v", filter)
	}
	ids, ok := in["$in"].([]string)
	if !ok {
		t.Fatalf("elem.id is not an $in of strings: %#v", in)
	}
	return ids
}
