package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
	"github.com/linesmerrill/police-cad-api/platforms"
)

// These endpoints change the two things the whole program rests on: whether a
// channel is proved to be the creator's, and the follower count the 500 minimum
// is judged on. So most of what follows is about what must NOT happen.
//
// A platform that refuses to answer must leave every stored field exactly as it
// was. A code that is not found must leave the old handle live AND the proposed
// one waiting. A count below the minimum must raise a flag for a human and
// never revoke anything or start a clock. And the profile endpoint must not be
// able to assert any of it.
//
// The fake below is not Mongo. It applies the dotted $set paths these handlers
// actually send, field by field, because writing the right path is the
// behaviour under test: an update that silently went nowhere would pass a test
// that only counted calls.

// recordingFetcher serves one canned answer and remembers what it was asked
// for, so "no fetch was attempted" is something a test can assert rather than
// assume.
type recordingFetcher struct {
	info  platforms.ChannelInfo
	err   error
	calls []string
}

func (f *recordingFetcher) Fetch(_ context.Context, handle string) (platforms.ChannelInfo, error) {
	f.calls = append(f.calls, handle)
	return f.info, f.err
}

type resyncWorld struct {
	creator *models.ContentCreator
	userID  primitive.ObjectID

	fetcher    *recordingFetcher
	resolveErr error // what platforms.For stands in as returning

	snapshots []models.ContentCreatorFollowerSnapshot
	updates   int
}

func newResyncWorld(ps ...models.ContentCreatorPlatform) *resyncWorld {
	userID := primitive.NewObjectID()
	return &resyncWorld{
		creator: &models.ContentCreator{
			ID:          primitive.NewObjectID(),
			UserID:      &userID,
			DisplayName: "Day Pixie",
			Status:      "active",
			Platforms:   ps,
		},
		userID:  userID,
		fetcher: &recordingFetcher{},
	}
}

func ytPlatform(handle string, count int) models.ContentCreatorPlatform {
	return models.ContentCreatorPlatform{
		Type: "youtube", Handle: handle, URL: "https://youtube.com/@" + handle,
		FollowerCount: count, VerificationStatus: models.PlatformVerified,
		VerificationMethod: "api",
	}
}

// applySet applies the dotted paths these handlers write. Anything unhandled
// fails loudly: a handler that starts writing a field this does not know about
// would otherwise look like it wrote nothing.
func (w *resyncWorld) applySet(t *testing.T, set bson.M) {
	t.Helper()
	for key, value := range set {
		if idx, field, ok := platformPath(key); ok {
			if idx >= len(w.creator.Platforms) {
				t.Fatalf("update wrote platform index %d, creator has %d", idx, len(w.creator.Platforms))
			}
			p := &w.creator.Platforms[idx]
			switch field {
			case "followerCount":
				p.FollowerCount = value.(int)
			case "lastSyncedAt":
				p.LastSyncedAt = dateTimePtr(value)
			case "handle":
				p.Handle = value.(string)
			case "url":
				p.URL = value.(string)
			case "pendingHandle":
				p.PendingHandle = value.(string)
			case "pendingUrl":
				p.PendingURL = value.(string)
			case "verificationStatus":
				p.VerificationStatus = value.(string)
			case "verificationMethod":
				p.VerificationMethod = value.(string)
			case "verificationError":
				p.VerificationError = value.(string)
			case "verificationCode":
				p.VerificationCode = value.(string)
			case "verificationCodeExpiresAt":
				p.VerificationCodeExpiresAt = dateTimePtr(value)
			case "verifiedAt":
				p.VerifiedAt = dateTimePtr(value)
			case "verifiedByAdmin":
				p.VerifiedByAdmin = value.(bool)
			case "verifiedBy":
				if value == nil {
					p.VerifiedBy = nil
				} else {
					id := value.(primitive.ObjectID)
					p.VerifiedBy = &id
				}
			case "verificationLastCheckedAt":
				p.VerificationLastCheckedAt = dateTimePtr(value)
			case "verificationCheckCount":
				p.VerificationCheckCount = value.(int)
			case "verificationCheckWindowFrom":
				p.VerificationCheckWindowFrom = dateTimePtr(value)
			default:
				t.Fatalf("unhandled platform field in update: %s", field)
			}
			continue
		}

		switch key {
		case "platforms":
			w.creator.Platforms = value.([]models.ContentCreatorPlatform)
		case "displayName":
			w.creator.DisplayName = value.(string)
		case "bio":
			w.creator.Bio = value.(string)
		case "themeColor":
			w.creator.ThemeColor = value.(string)
		case "profileImage":
			w.creator.ProfileImage = value.(string)
		case "lastSyncedAt":
			w.creator.LastSyncedAt = dateTimePtr(value)
		case "followerReviewFlaggedAt":
			w.creator.FollowerReviewFlaggedAt = dateTimePtr(value)
		case "followerReviewReason":
			w.creator.FollowerReviewReason = value.(string)
		case "status":
			w.creator.Status = value.(string)
		case "warningReason":
			w.creator.WarningReason = value.(string)
		case "warningMessage":
			w.creator.WarningMessage = value.(string)
		case "warnedAt":
			w.creator.WarnedAt = dateTimePtr(value)
		case "gracePeriodStartedAt":
			w.creator.GracePeriodStartedAt = dateTimePtr(value)
		case "gracePeriodEndsAt":
			w.creator.GracePeriodEndsAt = dateTimePtr(value)
		case "gracePeriodNotifiedAt":
			w.creator.GracePeriodNotifiedAt = dateTimePtr(value)
		case "updatedAt":
		default:
			t.Fatalf("unhandled field in update: %s", key)
		}
	}
}

// platformPath splits "platforms.2.handle" into its index and field.
func platformPath(key string) (int, string, bool) {
	const prefix = "platforms."
	if len(key) <= len(prefix) || key[:len(prefix)] != prefix {
		return 0, "", false
	}
	rest := key[len(prefix):]
	dot := -1
	for i := 0; i < len(rest); i++ {
		if rest[i] == '.' {
			dot = i
			break
		}
	}
	if dot <= 0 {
		return 0, "", false
	}
	idx, err := strconv.Atoi(rest[:dot])
	if err != nil {
		return 0, "", false
	}
	return idx, rest[dot+1:], true
}

func dateTimePtr(value interface{}) *primitive.DateTime {
	if value == nil {
		return nil
	}
	dt := value.(primitive.DateTime)
	return &dt
}

func (w *resyncWorld) handler(t *testing.T) ContentCreator {
	t.Helper()
	creatorDB := &mocks.ContentCreatorDatabase{}
	snapDB := &mocks.ContentCreatorSnapshotDatabase{}

	creatorDB.On("FindOne", mock.Anything, mock.Anything).Return(
		func(_ context.Context, filter interface{}) (*models.ContentCreator, error) {
			if w.creator == nil {
				return nil, mongo.ErrNoDocuments
			}
			f, _ := filter.(bson.M)
			// The creator-facing gate resolves by the caller's own user id and
			// only for a live creator. Both halves are the authorization.
			if id, ok := f["userId"].(primitive.ObjectID); ok {
				if w.creator.UserID == nil || *w.creator.UserID != id {
					return nil, mongo.ErrNoDocuments
				}
				if w.creator.Status != "active" && w.creator.Status != "warned" {
					return nil, mongo.ErrNoDocuments
				}
			}
			if id, ok := f["_id"].(primitive.ObjectID); ok && id != w.creator.ID {
				return nil, mongo.ErrNoDocuments
			}
			copied := *w.creator
			copied.Platforms = append([]models.ContentCreatorPlatform(nil), w.creator.Platforms...)
			return &copied, nil
		})

	creatorDB.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).Return(
		func(_ context.Context, filter interface{}, update interface{}, _ ...*options.UpdateOptions) error {
			if id, ok := filter.(bson.M)["_id"].(primitive.ObjectID); ok && id != w.creator.ID {
				return mongo.ErrNoDocuments
			}
			w.updates++
			w.applySet(t, setFields(update))
			return nil
		})

	snapDB.On("InsertOne", mock.Anything, mock.Anything).Return(
		func(_ context.Context, snapshot models.ContentCreatorFollowerSnapshot, _ ...*options.InsertOneOptions) (databases.InsertOneResultHelper, error) {
			w.snapshots = append(w.snapshots, snapshot)
			return nil, nil
		})

	return ContentCreator{
		CCDB:   creatorDB,
		SnapDB: snapDB,
		fetcherFor: func(string) (platforms.Fetcher, error) {
			if w.resolveErr != nil {
				return nil, w.resolveErr
			}
			return w.fetcher, nil
		},
	}
}

// call drives one of these handlers the way the router would, with the platform
// index as a path variable.
func (w *resyncWorld) call(h http.HandlerFunc, index, userID, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/content-creators/me/platforms/"+index+"/resync", bytes.NewReader([]byte(body)))
	req = mux.SetURLVars(req, map[string]string{"index": index})
	if userID != "" {
		req.Header.Set("X-User-ID", userID)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

// reason digs the message out of the API's error envelope.
func reason(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var envelope struct {
		Response struct {
			Message string `json:"message"`
		} `json:"response"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("response is not JSON: %v (%s)", err, rec.Body.String())
	}
	return envelope.Response.Message
}

func ago(d time.Duration) *primitive.DateTime {
	dt := primitive.NewDateTimeFromTime(time.Now().Add(-d))
	return &dt
}

func ahead(d time.Duration) *primitive.DateTime {
	dt := primitive.NewDateTimeFromTime(time.Now().Add(d))
	return &dt
}

// --- the gate ---

func TestResync_RequiresAnApprovedCreator(t *testing.T) {
	t.Run("no caller", func(t *testing.T) {
		w := newResyncWorld(ytPlatform("cryptic", 30000))
		rec := w.call(w.handler(t).ResyncPlatformHandler, "0", "", "")
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
		assert.Empty(t, w.fetcher.calls)
	})

	t.Run("a user who is not a creator", func(t *testing.T) {
		w := newResyncWorld(ytPlatform("cryptic", 30000))
		rec := w.call(w.handler(t).ResyncPlatformHandler, "0", primitive.NewObjectID().Hex(), "")
		assert.Equal(t, http.StatusForbidden, rec.Code)
		assert.Empty(t, w.fetcher.calls, "a stranger must not be able to make us call a platform")
		assert.Equal(t, 0, w.updates)
	})

	t.Run("a removed creator", func(t *testing.T) {
		w := newResyncWorld(ytPlatform("cryptic", 30000))
		w.creator.Status = "removed"
		rec := w.call(w.handler(t).ResyncPlatformHandler, "0", w.userID.Hex(), "")
		assert.Equal(t, http.StatusForbidden, rec.Code)
		assert.Equal(t, 0, w.updates)
	})
}

// Another creator's platform is not a case the gate has to detect: the creator
// is resolved from the caller's own id, so somebody else's index can only read
// as one past the end of the caller's own list.
func TestResync_IndexOutOfBounds(t *testing.T) {
	w := newResyncWorld(ytPlatform("cryptic", 30000))
	h := w.handler(t)

	rec := w.call(h.ResyncPlatformHandler, "7", w.userID.Hex(), "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Empty(t, w.fetcher.calls)
	assert.Equal(t, 0, w.updates)

	rec = w.call(h.ResyncPlatformHandler, "-1", w.userID.Hex(), "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = w.call(h.ResyncPlatformHandler, "banana", w.userID.Hex(), "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// --- the budget ---

// The budget exists to stop one creator spending a platform quota everybody
// shares, so the order matters: refused BEFORE the call, not after it.
func TestResync_ExhaustedBudgetSpendsNoPlatformCall(t *testing.T) {
	p := ytPlatform("cryptic", 30000)
	p.VerificationCheckCount = models.VerifyCheckDailyCap
	p.VerificationCheckWindowFrom = ago(time.Hour)
	p.VerificationLastCheckedAt = ago(10 * time.Minute)
	w := newResyncWorld(p)
	w.fetcher.info = platforms.ChannelInfo{FollowerCount: 40000}

	rec := w.call(w.handler(t).ResyncPlatformHandler, "0", w.userID.Hex(), "")

	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.NotEmpty(t, rec.Header().Get("Retry-After"), "a client needs to be told when to come back")
	assert.Empty(t, w.fetcher.calls, "the budget must be checked before the platform is called, not after")
	assert.Equal(t, 0, w.updates, "a refused request writes nothing at all")
	assert.Equal(t, 30000, w.creator.Platforms[0].FollowerCount)
}

func TestResync_CooldownRefusesTheSecondPressInstantly(t *testing.T) {
	p := ytPlatform("cryptic", 30000)
	p.VerificationLastCheckedAt = ago(2 * time.Second)
	w := newResyncWorld(p)

	rec := w.call(w.handler(t).ResyncPlatformHandler, "0", w.userID.Hex(), "")
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.Empty(t, w.fetcher.calls)
}

// --- failed reads change nothing ---

func TestResync_BlockedPlatformChangesNothing(t *testing.T) {
	p := models.ContentCreatorPlatform{
		Type: "tiktok", Handle: "daypixie", FollowerCount: 12000,
		VerificationStatus: models.PlatformVerified,
	}
	w := newResyncWorld(p)
	w.fetcher.err = platforms.ErrPlatformBlocked

	rec := w.call(w.handler(t).ResyncPlatformHandler, "0", w.userID.Hex(), "")

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code,
		"the platform refusing US is our outage, not a verification failure")
	msg := reason(t, rec)
	assert.Contains(t, msg, "TikTok", "the creator should be told which platform would not answer")
	assert.Contains(t, msg, "Nothing was changed")

	assert.Equal(t, 12000, w.creator.Platforms[0].FollowerCount)
	assert.Equal(t, models.PlatformVerified, w.creator.Platforms[0].VerificationStatus)
	assert.Nil(t, w.creator.Platforms[0].LastSyncedAt)
	assert.Nil(t, w.creator.LastSyncedAt)
	assert.Nil(t, w.creator.FollowerReviewFlaggedAt, "an outage of ours must never flag a creator")
	assert.Empty(t, w.snapshots, "nothing was measured, so there is nothing to record")
}

func TestResync_ChannelNotFoundChangesNothing(t *testing.T) {
	w := newResyncWorld(ytPlatform("cryptic", 30000))
	w.fetcher.err = platforms.ErrChannelNotFound

	rec := w.call(w.handler(t).ResyncPlatformHandler, "0", w.userID.Hex(), "")

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, reason(t, rec), "could not find that channel")
	assert.Equal(t, 30000, w.creator.Platforms[0].FollowerCount,
		"a handle that no longer resolves is not evidence the audience went away")
	assert.Equal(t, models.PlatformVerified, w.creator.Platforms[0].VerificationStatus)
	assert.Empty(t, w.snapshots)
}

func TestResync_ManualOnlyPlatformIsRefusedBeforeAnythingIsSpent(t *testing.T) {
	w := newResyncWorld(models.ContentCreatorPlatform{
		Type: "other", Handle: "me", URL: "https://example.com/me", FollowerCount: 900,
	})
	w.resolveErr = platforms.ErrManualOnly

	rec := w.call(w.handler(t).ResyncPlatformHandler, "0", w.userID.Hex(), "")

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, 0, w.updates, "a platform we never call must not spend a unit of the check budget")
	assert.Equal(t, 900, w.creator.Platforms[0].FollowerCount)
}

// --- the happy path ---

func TestResync_StoresTheCountWeRead(t *testing.T) {
	w := newResyncWorld(ytPlatform("cryptic", 30000))
	w.fetcher.info = platforms.ChannelInfo{
		Handle: "cryptic", FollowerCount: 41234, ProfileURL: "https://youtube.com/@cryptic",
	}

	rec := w.call(w.handler(t).ResyncPlatformHandler, "0", w.userID.Hex(), "")
	assert.Equal(t, http.StatusOK, rec.Code)

	body := decodeBody(t, rec)
	assert.Equal(t, true, body["success"])
	assert.Equal(t, float64(30000), body["previousFollowerCount"])
	assert.Equal(t, false, body["belowMinimum"])
	platform := body["platform"].(map[string]interface{})
	assert.Equal(t, float64(41234), platform["followerCount"])

	assert.Equal(t, []string{"cryptic"}, w.fetcher.calls, "the live handle is what gets read")
	assert.Equal(t, 41234, w.creator.Platforms[0].FollowerCount)
	assert.NotNil(t, w.creator.Platforms[0].LastSyncedAt)
	assert.NotNil(t, w.creator.LastSyncedAt)

	// Ownership is not re-proved by a resync, so nothing about it may move.
	assert.Equal(t, models.PlatformVerified, w.creator.Platforms[0].VerificationStatus)
	assert.Equal(t, "api", w.creator.Platforms[0].VerificationMethod)
	assert.Equal(t, "cryptic", w.creator.Platforms[0].Handle)

	if assert.Len(t, w.snapshots, 1) {
		assert.Equal(t, "api", w.snapshots[0].Source, "a number we read is not a manual entry")
		assert.Equal(t, 41234, w.snapshots[0].MaxFollowers)
	}
}

// --- below the minimum ---

func TestResync_BelowMinimumFlagsForReviewAndRevokesNothing(t *testing.T) {
	w := newResyncWorld(models.ContentCreatorPlatform{
		Type: "tiktok", Handle: "daypixie", FollowerCount: 1200,
		VerificationStatus: models.PlatformVerified,
	})
	w.fetcher.info = platforms.ChannelInfo{Handle: "daypixie", FollowerCount: 80}

	rec := w.call(w.handler(t).ResyncPlatformHandler, "0", w.userID.Hex(), "")
	assert.Equal(t, http.StatusOK, rec.Code)

	body := decodeBody(t, rec)
	assert.Equal(t, true, body["belowMinimum"])
	assert.Contains(t, body["message"], "below the 500 minimum")
	assert.Contains(t, body["message"], "an admin will take a look")
	assert.NotContains(t, body["message"], "removed", "nothing was removed, so the copy must not say so")

	assert.NotNil(t, w.creator.FollowerReviewFlaggedAt, "an admin has to be able to find this")
	assert.Contains(t, w.creator.FollowerReviewReason, "500")

	// The things this endpoint must never do.
	assert.Equal(t, "active", w.creator.Status)
	assert.Nil(t, w.creator.GracePeriodStartedAt)
	assert.Nil(t, w.creator.GracePeriodEndsAt)
	assert.Nil(t, w.creator.WarnedAt)
	assert.Equal(t, models.PlatformVerified, w.creator.Platforms[0].VerificationStatus,
		"being small is not the same as being unproved")
	assert.Equal(t, 80, w.creator.Platforms[0].FollowerCount, "the real number is still the real number")
}

func TestResync_AtOrAboveTheMinimumClearsTheFlag(t *testing.T) {
	w := newResyncWorld(models.ContentCreatorPlatform{
		Type: "tiktok", Handle: "daypixie", FollowerCount: 80,
		VerificationStatus: models.PlatformVerified,
	})
	w.creator.FollowerReviewFlaggedAt = ago(72 * time.Hour)
	w.creator.FollowerReviewReason = "read 80 followers, below the 500 minimum"
	w.fetcher.info = platforms.ChannelInfo{Handle: "daypixie", FollowerCount: 900}

	rec := w.call(w.handler(t).ResyncPlatformHandler, "0", w.userID.Hex(), "")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, false, decodeBody(t, rec)["belowMinimum"])

	assert.Nil(t, w.creator.FollowerReviewFlaggedAt, "a creator who recovered must not stay on the admin's list")
	assert.Empty(t, w.creator.FollowerReviewReason)
}

// A second platform still above the bar is why the flag is judged on the
// creator's highest count: flagging on one low channel would be manual triage
// for an admin over somebody who was never near the line.
func TestResync_ASecondQualifyingPlatformMeansNoFlag(t *testing.T) {
	w := newResyncWorld(
		ytPlatform("cryptic", 48000),
		models.ContentCreatorPlatform{Type: "tiktok", Handle: "daypixie", FollowerCount: 600},
	)
	w.fetcher.info = platforms.ChannelInfo{Handle: "daypixie", FollowerCount: 90}

	rec := w.call(w.handler(t).ResyncPlatformHandler, "1", w.userID.Hex(), "")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, false, decodeBody(t, rec)["belowMinimum"])
	assert.Nil(t, w.creator.FollowerReviewFlaggedAt)
	assert.Equal(t, 90, w.creator.Platforms[1].FollowerCount)
}

// --- re-verifying a changed handle ---

func TestReverifyStart_HoldsTheNewHandleAsideAndLeavesTheOldOneLive(t *testing.T) {
	w := newResyncWorld(models.ContentCreatorPlatform{
		Type: "tiktok", Handle: "daypixie", URL: "https://tiktok.com/@daypixie",
		FollowerCount: 12000, VerificationStatus: models.PlatformVerified,
	})

	rec := w.call(w.handler(t).ReverifyStartHandler, "0", w.userID.Hex(),
		`{"handle":"https://www.tiktok.com/@daypixie.rp?lang=en"}`)
	assert.Equal(t, http.StatusOK, rec.Code)

	body := decodeBody(t, rec)
	code := body["code"].(string)
	assert.Contains(t, code, "LPC-VERIFY-")
	assert.Equal(t, "daypixie.rp", body["pendingHandle"], "a pasted URL is normalized to a handle")
	assert.Equal(t, "daypixie", body["currentHandle"])
	assert.Contains(t, body["instruction"], "TikTok bio")
	assert.NotContains(t, body["instruction"], "team",
		"TikTok is read automatically now; promising a human review is the old, wrong copy")

	p := w.creator.Platforms[0]
	assert.Equal(t, "daypixie.rp", p.PendingHandle)
	assert.Equal(t, code, p.VerificationCode)
	assert.NotNil(t, p.VerificationCodeExpiresAt)

	// The live channel is untouched: a creator who starts this and walks away
	// still has a working, verified profile.
	assert.Equal(t, "daypixie", p.Handle)
	assert.Equal(t, "https://tiktok.com/@daypixie", p.URL)
	assert.Equal(t, 12000, p.FollowerCount)
	assert.Equal(t, models.PlatformVerified, p.VerificationStatus)
}

func TestReverifyStart_RefusesNothingAndTheHandleItAlreadyHas(t *testing.T) {
	w := newResyncWorld(ytPlatform("cryptic", 30000))
	h := w.handler(t)

	rec := w.call(h.ReverifyStartHandler, "0", w.userID.Hex(), `{"handle":"   "}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = w.call(h.ReverifyStartHandler, "0", w.userID.Hex(), `{"handle":"@Cryptic"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code,
		"the handle it already has, in any case, is nothing to re-verify")
	assert.Equal(t, 0, w.updates)
}

// An UNVERIFIED entry may run the flow against its own handle: that is how a
// platform added on the profile page gets verified at all, now that the profile
// endpoint cannot assert it.
func TestReverifyStart_AnUnverifiedPlatformMayProveItsOwnHandle(t *testing.T) {
	w := newResyncWorld(models.ContentCreatorPlatform{
		Type: "youtube", Handle: "freshchannel", VerificationStatus: models.PlatformUnverified,
	})
	rec := w.call(w.handler(t).ReverifyStartHandler, "0", w.userID.Hex(), `{"handle":"freshchannel"}`)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "freshchannel", w.creator.Platforms[0].PendingHandle)
}

func pendingTikTok(code string, expires *primitive.DateTime) models.ContentCreatorPlatform {
	return models.ContentCreatorPlatform{
		Type: "tiktok", Handle: "daypixie", URL: "https://tiktok.com/@daypixie",
		FollowerCount: 12000, VerificationStatus: models.PlatformVerified,
		PendingHandle: "daypixie.rp", PendingURL: "https://tiktok.com/@daypixie.rp",
		VerificationCode: code, VerificationCodeExpiresAt: expires,
	}
}

func TestReverifyCheck_CodeFoundPromotesThePendingHandle(t *testing.T) {
	const code = "LPC-VERIFY-2WY32A"
	w := newResyncWorld(pendingTikTok(code, ahead(time.Hour)))
	w.fetcher.info = platforms.ChannelInfo{
		Handle: "daypixie.rp", Description: "roleplay 🚓\n" + code,
		FollowerCount: 13400, ProfileURL: "https://www.tiktok.com/@daypixie.rp",
	}

	rec := w.call(w.handler(t).ReverifyCheckHandler, "0", w.userID.Hex(), "")
	assert.Equal(t, http.StatusOK, rec.Code)

	body := decodeBody(t, rec)
	assert.Equal(t, true, body["verified"])
	assert.Equal(t, true, body["codeFound"])
	assert.Equal(t, "daypixie", body["previousHandle"])

	assert.Equal(t, []string{"daypixie.rp"}, w.fetcher.calls,
		"the PENDING handle is the one being proved")

	p := w.creator.Platforms[0]
	assert.Equal(t, "daypixie.rp", p.Handle)
	assert.Equal(t, "https://www.tiktok.com/@daypixie.rp", p.URL)
	assert.Equal(t, 13400, p.FollowerCount)
	assert.Equal(t, models.PlatformVerified, p.VerificationStatus)
	assert.Equal(t, "api", p.VerificationMethod)
	assert.NotNil(t, p.VerifiedAt)
	assert.NotNil(t, p.LastSyncedAt)

	// Everything about the attempt is gone, so a stale code cannot be replayed
	// and the dashboard has no half-finished state to render.
	assert.Empty(t, p.PendingHandle)
	assert.Empty(t, p.PendingURL)
	assert.Empty(t, p.VerificationCode)
	assert.Nil(t, p.VerificationCodeExpiresAt)
	assert.Empty(t, p.VerificationError)

	if assert.Len(t, w.snapshots, 1) {
		assert.Equal(t, "api", w.snapshots[0].Source)
	}
}

func TestReverifyCheck_CodeMissingLeavesBothHandlesExactlyAsTheyWere(t *testing.T) {
	const code = "LPC-VERIFY-2WY32A"
	w := newResyncWorld(pendingTikTok(code, ahead(time.Hour)))
	w.fetcher.info = platforms.ChannelInfo{
		Handle: "daypixie.rp", Description: "roleplay, no code here", FollowerCount: 13400,
	}

	rec := w.call(w.handler(t).ReverifyCheckHandler, "0", w.userID.Hex(), "")

	// 200 with codeFound false, the same shape the application check answers
	// with: the request worked, the answer is "not yet".
	assert.Equal(t, http.StatusOK, rec.Code)
	body := decodeBody(t, rec)
	assert.Equal(t, false, body["verified"])
	assert.Equal(t, false, body["codeFound"])
	assert.Equal(t, true, body["channelFound"])
	assert.Equal(t, "daypixie", body["currentHandle"])
	assert.Equal(t, "daypixie.rp", body["pendingHandle"])

	p := w.creator.Platforms[0]
	assert.Equal(t, "daypixie", p.Handle, "the live handle keeps working")
	assert.Equal(t, 12000, p.FollowerCount, "an unproved channel's count must not be adopted")
	assert.Equal(t, "daypixie.rp", p.PendingHandle, "the attempt is still open")
	assert.Equal(t, code, p.VerificationCode, "a new code would mean editing the bio again")
	assert.Equal(t, models.PlatformVerified, p.VerificationStatus)
	assert.Empty(t, w.snapshots)
}

func TestReverifyCheck_ExpiredCodeAsksForAFreshOne(t *testing.T) {
	w := newResyncWorld(pendingTikTok("LPC-VERIFY-2WY32A", ago(time.Minute)))

	rec := w.call(w.handler(t).ReverifyCheckHandler, "0", w.userID.Hex(), "")

	assert.Equal(t, http.StatusGone, rec.Code)
	assert.Contains(t, reason(t, rec), "expired")
	assert.Empty(t, w.fetcher.calls, "an expired code is decided without calling anyone")
	assert.Equal(t, 0, w.updates)
	assert.Equal(t, "daypixie.rp", w.creator.Platforms[0].PendingHandle,
		"the proposed handle survives an expiry; only the code has to be reissued")
}

func TestReverifyCheck_NothingPending(t *testing.T) {
	w := newResyncWorld(ytPlatform("cryptic", 30000))
	rec := w.call(w.handler(t).ReverifyCheckHandler, "0", w.userID.Hex(), "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Empty(t, w.fetcher.calls)
}

func TestReverifyCheck_BlockedPlatformKeepsThePendingHandleWaiting(t *testing.T) {
	const code = "LPC-VERIFY-2WY32A"
	w := newResyncWorld(pendingTikTok(code, ahead(time.Hour)))
	w.fetcher.err = platforms.ErrPlatformBlocked

	rec := w.call(w.handler(t).ReverifyCheckHandler, "0", w.userID.Hex(), "")

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, reason(t, rec), "TikTok")

	p := w.creator.Platforms[0]
	assert.Equal(t, "daypixie", p.Handle)
	assert.Equal(t, "daypixie.rp", p.PendingHandle, "nothing promoted and nothing lost")
	assert.Equal(t, code, p.VerificationCode)
}

// --- the admin fallback ---

func TestAdminVerifyCreatorPlatform_PromotesThePendingHandleByHand(t *testing.T) {
	w := newResyncWorld(pendingTikTok("LPC-VERIFY-2WY32A", ahead(time.Hour)))
	adminID := primitive.NewObjectID()

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/admin/content-creators/"+w.creator.ID.Hex()+"/platforms/0/verify",
		bytes.NewReader([]byte(`{"followerCount":13000}`)))
	req = mux.SetURLVars(req, map[string]string{"id": w.creator.ID.Hex(), "index": "0"})
	req.Header.Set("X-User-ID", adminID.Hex())
	rec := httptest.NewRecorder()
	w.handler(t).AdminVerifyCreatorPlatformHandler(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "daypixie.rp", decodeBody(t, rec)["handle"])

	p := w.creator.Platforms[0]
	assert.Equal(t, "daypixie.rp", p.Handle)
	assert.Equal(t, "https://tiktok.com/@daypixie.rp", p.URL)
	assert.Equal(t, models.PlatformVerified, p.VerificationStatus)
	assert.Equal(t, "admin", p.VerificationMethod)
	assert.True(t, p.VerifiedByAdmin)
	assert.Equal(t, &adminID, p.VerifiedBy, "a manual vouch has to say who made it")
	assert.Equal(t, 13000, p.FollowerCount)
	assert.Empty(t, p.PendingHandle)
	assert.Empty(t, p.VerificationCode)
}

// --- the profile endpoint: the hole this PR closes ---

// Before this, PUT /content-creators/me wrote req.Platforms straight through.
// verificationStatus, verifiedByAdmin and followerCount ARE the basis for "this
// channel is yours and it clears the minimum", and the program grants a
// top-tier subscription on them, so a creator being able to set them was a
// self-serve path to the benefits.
func TestUpdateMyProfile_CannotSetVerificationOrFollowerFields(t *testing.T) {
	w := newResyncWorld(ytPlatform("cryptic", 30000))
	w.creator.Platforms[0].VerifiedByAdmin = false

	body, _ := json.Marshal(map[string]interface{}{
		"displayName": "Cryptic RP",
		"platforms": []map[string]interface{}{{
			"type":               "youtube",
			"handle":             "cryptic",
			"url":                "https://youtube.com/@cryptic",
			"followerCount":      5000000,
			"verificationStatus": models.PlatformVerified,
			"verifiedByAdmin":    true,
			"verifiedBy":         primitive.NewObjectID().Hex(),
			"verificationMethod": "admin",
		}},
	})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/content-creators/me", bytes.NewReader(body))
	req.Header.Set("X-User-ID", w.userID.Hex())
	rec := httptest.NewRecorder()
	w.handler(t).UpdateMyProfileHandler(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "the display fields must still work")
	assert.Equal(t, "Cryptic RP", w.creator.DisplayName)

	p := w.creator.Platforms[0]
	assert.Equal(t, 30000, p.FollowerCount, "a creator cannot type their way past the follower minimum")
	assert.False(t, p.VerifiedByAdmin, "nobody can tick an admin's box for them")
	assert.Equal(t, "api", p.VerificationMethod, "the stored entry is carried over, not rewritten")
	assert.Nil(t, p.VerifiedBy)
}

func TestUpdateMyProfile_ANewPlatformStartsUnverifiedWithNoFollowers(t *testing.T) {
	w := newResyncWorld(ytPlatform("cryptic", 30000))

	body, _ := json.Marshal(map[string]interface{}{
		"platforms": []map[string]interface{}{
			{"type": "youtube", "handle": "cryptic", "followerCount": 30000},
			{
				"type": "twitch", "handle": "@crypticlive", "followerCount": 900000,
				"verificationStatus": models.PlatformVerified, "verifiedByAdmin": true,
			},
		},
	})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/content-creators/me", bytes.NewReader(body))
	req.Header.Set("X-User-ID", w.userID.Hex())
	rec := httptest.NewRecorder()
	w.handler(t).UpdateMyProfileHandler(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "adding a platform must still be possible")
	if assert.Len(t, w.creator.Platforms, 2) {
		added := w.creator.Platforms[1]
		assert.Equal(t, "crypticlive", added.Handle, "the handle is normalized on the way in")
		assert.Equal(t, 0, added.FollowerCount)
		assert.False(t, added.VerifiedByAdmin)
		assert.Equal(t, models.PlatformUnverified, added.VerificationStatus,
			"a platform nobody has proved starts unproved, and goes through the code flow")

		// The existing verified entry is carried over untouched.
		assert.Equal(t, 30000, w.creator.Platforms[0].FollowerCount)
		assert.Equal(t, models.PlatformVerified, w.creator.Platforms[0].VerificationStatus)
	}
}

// Editing the handle in the profile form would otherwise read as "remove the
// verified one, add an unverified one", quietly throwing away a verification
// and a real follower count.
func TestUpdateMyProfile_HandleChangeIsSentToTheReverifyFlow(t *testing.T) {
	w := newResyncWorld(ytPlatform("cryptic", 30000))

	body, _ := json.Marshal(map[string]interface{}{
		"platforms": []map[string]interface{}{{"type": "youtube", "handle": "crypticrp"}},
	})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/content-creators/me", bytes.NewReader(body))
	req.Header.Set("X-User-ID", w.userID.Hex())
	rec := httptest.NewRecorder()
	w.handler(t).UpdateMyProfileHandler(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, reason(t, rec), "Change handle")
	assert.Equal(t, 0, w.updates)
	assert.Equal(t, "cryptic", w.creator.Platforms[0].Handle)
	assert.Equal(t, 30000, w.creator.Platforms[0].FollowerCount)
}

func TestUpdateMyProfile_RemovingAPlatformStillWorks(t *testing.T) {
	w := newResyncWorld(ytPlatform("cryptic", 30000),
		models.ContentCreatorPlatform{Type: "twitch", Handle: "crypticlive"})

	body, _ := json.Marshal(map[string]interface{}{
		"platforms": []map[string]interface{}{{"type": "youtube", "handle": "cryptic"}},
	})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/content-creators/me", bytes.NewReader(body))
	req.Header.Set("X-User-ID", w.userID.Hex())
	rec := httptest.NewRecorder()
	w.handler(t).UpdateMyProfileHandler(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Len(t, w.creator.Platforms, 1)
}

// --- the follower sync ---

// The sync endpoint can clear a low-follower grace period, and it used to take
// the number that decides that from the request body.
func TestSyncFollowers_IgnoresABodyCountForAPlatformWeCanRead(t *testing.T) {
	w := newResyncWorld(ytPlatform("cryptic", 1000))
	w.fetcher.info = platforms.ChannelInfo{Handle: "cryptic", FollowerCount: 2500}

	body, _ := json.Marshal(map[string]interface{}{
		"platforms": []map[string]interface{}{{"type": "youtube", "followerCount": 999999}},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/content-creators/me/sync", bytes.NewReader(body))
	req.Header.Set("X-User-ID", w.userID.Hex())
	rec := httptest.NewRecorder()
	w.handler(t).SyncFollowersHandler(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []string{"cryptic"}, w.fetcher.calls)
	assert.Equal(t, 2500, w.creator.Platforms[0].FollowerCount,
		"the number we read wins; the typed one is not evidence of anything")
	assert.Equal(t, float64(2500), decodeBody(t, rec)["maxFollowers"])
	if assert.Len(t, w.snapshots, 1) {
		assert.Equal(t, "api", w.snapshots[0].Source)
		assert.Equal(t, 2500, w.snapshots[0].MaxFollowers)
	}
}

// A read we could not make is not a licence to believe the body instead: that
// would make "break the fetch" the way to set your own follower count.
func TestSyncFollowers_AFailedReadKeepsTheStoredCount(t *testing.T) {
	w := newResyncWorld(ytPlatform("cryptic", 1000))
	w.fetcher.err = platforms.ErrPlatformBlocked

	body, _ := json.Marshal(map[string]interface{}{
		"platforms": []map[string]interface{}{{"type": "youtube", "followerCount": 999999}},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/content-creators/me/sync", bytes.NewReader(body))
	req.Header.Set("X-User-ID", w.userID.Hex())
	rec := httptest.NewRecorder()
	w.handler(t).SyncFollowersHandler(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 1000, w.creator.Platforms[0].FollowerCount)
	if assert.Len(t, w.snapshots, 1) {
		assert.Equal(t, "manual", w.snapshots[0].Source, "nothing in this snapshot was measured")
	}
}

// Where nothing reads the platform, the creator's own number is the only one
// there will ever be, so it is still accepted.
func TestSyncFollowers_KeepsTheBodyCountForAPlatformNothingReads(t *testing.T) {
	w := newResyncWorld(models.ContentCreatorPlatform{
		Type: "other", Handle: "me", URL: "https://example.com/me", FollowerCount: 700,
	})
	w.resolveErr = platforms.ErrManualOnly

	body, _ := json.Marshal(map[string]interface{}{
		"platforms": []map[string]interface{}{{"type": "other", "followerCount": 1800}},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/content-creators/me/sync", bytes.NewReader(body))
	req.Header.Set("X-User-ID", w.userID.Hex())
	rec := httptest.NewRecorder()
	w.handler(t).SyncFollowersHandler(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, w.fetcher.calls)
	assert.Equal(t, 1800, w.creator.Platforms[0].FollowerCount)
	if assert.Len(t, w.snapshots, 1) {
		assert.Equal(t, "manual", w.snapshots[0].Source)
	}
}

// The public creator profile serializes platform entries directly, so the
// re-verify fields had to be kept off it: a live code is the proof of ownership
// while it is live, and a pending handle is a rename nobody announced.
func TestPublicPlatformsKeepsVerificationInternalsOffAPublicPage(t *testing.T) {
	admin := primitive.NewObjectID()
	public := models.PublicPlatforms([]models.ContentCreatorPlatform{{
		Type: "tiktok", URL: "https://tiktok.com/@daypixie", Handle: "daypixie",
		FollowerCount: 12000, VerificationStatus: models.PlatformVerified,
		VerificationMethod: "api", VerifiedByAdmin: true, VerifiedBy: &admin,
		VerificationCode: "LPC-VERIFY-2WY32A", VerificationCodeExpiresAt: ahead(time.Hour),
		PendingHandle: "daypixie.rp", PendingURL: "https://tiktok.com/@daypixie.rp",
		VerificationCheckCount: 4, ReportedFollowerCount: 99000,
	}})

	if assert.Len(t, public, 1) {
		p := public[0]
		assert.Empty(t, p.VerificationCode)
		assert.Nil(t, p.VerificationCodeExpiresAt)
		assert.Empty(t, p.PendingHandle)
		assert.Empty(t, p.PendingURL)
		assert.Nil(t, p.VerifiedBy)
		assert.Zero(t, p.VerificationCheckCount)
		assert.Zero(t, p.ReportedFollowerCount)

		// What a profile page actually renders is all still there.
		assert.Equal(t, "daypixie", p.Handle)
		assert.Equal(t, "https://tiktok.com/@daypixie", p.URL)
		assert.Equal(t, 12000, p.FollowerCount)
		assert.Equal(t, models.PlatformVerified, p.VerificationStatus)
		assert.True(t, p.VerifiedByAdmin)
	}
}
