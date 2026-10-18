package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

// Covers the Discord /clock-in fix: the bot records source "discord" so the
// shift is paid to clock-out (capped at max session) instead of being cut off
// a minute in for missing heartbeats, and clock-in refuses a civilian that is
// gone or belongs to someone else.

// fakeClockSessions is an in-memory ClockSessionDatabase: one stored session
// that FindOne returns when the filter's _id matches it, every insert and
// update recorded.
type fakeClockSessions struct {
	stored   *models.ClockSession
	inserted []models.ClockSession
	updates  []bson.M
}

func (f *fakeClockSessions) FindOne(_ context.Context, filter interface{}, _ ...*options.FindOneOptions) (*models.ClockSession, error) {
	if id, ok := filter.(bson.M)["_id"]; ok && f.stored != nil && f.stored.ID == id {
		copy := *f.stored
		return &copy, nil
	}
	return nil, mongo.ErrNoDocuments
}
func (f *fakeClockSessions) Find(context.Context, interface{}, ...*options.FindOptions) ([]models.ClockSession, error) {
	return nil, nil
}
func (f *fakeClockSessions) InsertOne(_ context.Context, doc interface{}, _ ...*options.InsertOneOptions) (databases.InsertOneResultHelper, error) {
	f.inserted = append(f.inserted, doc.(models.ClockSession))
	return nil, nil
}
func (f *fakeClockSessions) UpdateOne(_ context.Context, _ interface{}, update interface{}, _ ...*options.UpdateOptions) error {
	f.updates = append(f.updates, update.(bson.M))
	return nil
}
func (f *fakeClockSessions) FindOneAndUpdate(context.Context, interface{}, interface{}, ...*options.FindOneAndUpdateOptions) *mongo.SingleResult {
	return nil
}
func (f *fakeClockSessions) CountDocuments(context.Context, interface{}, ...*options.CountOptions) (int64, error) {
	return 0, nil
}

const clockTestUser = "507f1f77bcf86cd799439011"

func clockInCommunity(deptID primitive.ObjectID) *models.Community {
	c := &models.Community{ID: primitive.NewObjectID()}
	c.Details.Economy.Enabled = true
	c.Details.Departments = []models.Department{{
		ID:               deptID,
		Name:             "LSPD",
		EconomyEnabled:   true,
		ApprovalRequired: true,
		BasePayPerHour:   models.Cents(3600),
		Members:          []models.MemberStatus{{UserID: clockTestUser, Status: "approved"}},
	}}
	return c
}

// clockIn runs ClockInHandler against a community the user is a member of,
// with the given civilian lookup result, and returns the recorder and store.
func clockIn(t *testing.T, body map[string]string, gatewayKey string, civ *models.Civilian, civErr error) (*httptest.ResponseRecorder, *fakeClockSessions) {
	t.Helper()
	deptID := primitive.NewObjectID()
	community := clockInCommunity(deptID)
	body["communityId"] = community.ID.Hex()
	body["departmentId"] = deptID.Hex()

	commDB := &mocks.CommunityDatabase{}
	commDB.On("FindOne", mock.Anything, mock.Anything).Return(community, nil)
	civDB := &mocks.CivilianDatabase{}
	civDB.On("FindOne", mock.Anything, mock.Anything).Return(civ, civErr).Maybe()
	sdb := &fakeClockSessions{}

	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v2/economy/clock-in?userId="+clockTestUser, bytes.NewReader(raw))
	if gatewayKey != "" {
		req.Header.Set(apiGatewayHeader, gatewayKey)
	}
	rec := httptest.NewRecorder()
	Economy{SDB: sdb, CivDB: civDB, CommDB: commDB}.ClockInHandler(rec, req)
	return rec, sdb
}

func ownedCivilian(userID string) *models.Civilian {
	civ := &models.Civilian{ID: primitive.NewObjectID()}
	civ.Details.UserID = userID
	return civ
}

func TestClockInRecordsDiscordSourceFromTheBot(t *testing.T) {
	t.Setenv(apiGatewayKeyEnv, "s3cret")
	civ := ownedCivilian(clockTestUser)

	rec, sdb := clockIn(t, map[string]string{"civilianId": civ.ID.Hex(), "source": "discord"}, "s3cret", civ, nil)

	assert.Equal(t, http.StatusCreated, rec.Code)
	if assert.Len(t, sdb.inserted, 1) {
		assert.Equal(t, models.ClockSourceDiscord, sdb.inserted[0].Source)
	}
}

func TestClockInIgnoresDiscordSourceWithoutGatewaySecret(t *testing.T) {
	t.Setenv(apiGatewayKeyEnv, "s3cret")
	civ := ownedCivilian(clockTestUser)

	// A browser or app asking to skip the AFK rules gets an ordinary shift.
	rec, sdb := clockIn(t, map[string]string{"civilianId": civ.ID.Hex(), "source": "discord"}, "", civ, nil)

	assert.Equal(t, http.StatusCreated, rec.Code)
	if assert.Len(t, sdb.inserted, 1) {
		assert.Equal(t, "", sdb.inserted[0].Source)
		assert.True(t, sdb.inserted[0].TracksAfk())
	}
}

func TestClockInWithoutSourceIsAfkTracked(t *testing.T) {
	civ := ownedCivilian(clockTestUser)
	rec, sdb := clockIn(t, map[string]string{"civilianId": civ.ID.Hex()}, "", civ, nil)

	assert.Equal(t, http.StatusCreated, rec.Code)
	if assert.Len(t, sdb.inserted, 1) {
		assert.Equal(t, "", sdb.inserted[0].Source)
	}
}

func TestClockInRejectsDeletedCivilian(t *testing.T) {
	rec, sdb := clockIn(t, map[string]string{"civilianId": primitive.NewObjectID().Hex()}, "", nil, mongo.ErrNoDocuments)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "civilian not found")
	assert.Empty(t, sdb.inserted)
}

func TestClockInRejectsSomeoneElsesCivilian(t *testing.T) {
	civ := ownedCivilian("someone-else")
	rec, sdb := clockIn(t, map[string]string{"civilianId": civ.ID.Hex()}, "", civ, nil)

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "does not belong to you")
	assert.Empty(t, sdb.inserted)
}

func TestClockInRejectsMalformedCivilianID(t *testing.T) {
	rec, sdb := clockIn(t, map[string]string{"civilianId": "not-an-id"}, "", nil, nil)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Empty(t, sdb.inserted)
}

// clockOut runs ClockOutHandler for a session started `elapsed` ago that has
// never sent a heartbeat, paid one cent per second, and returns the credit.
func clockOut(t *testing.T, source string, elapsed time.Duration) int64 {
	t.Helper()
	start := time.Now().Add(-elapsed)
	sess := &models.ClockSession{
		ID:                primitive.NewObjectID(),
		UserID:            clockTestUser,
		CivilianID:        primitive.NewObjectID().Hex(),
		Status:            "active",
		Source:            source,
		PayoutMode:        "on_clockout",
		PayRateSnapshot:   3600,
		StartedAt:         primitive.NewDateTimeFromTime(start),
		LastHeartbeatAt:   primitive.NewDateTimeFromTime(start),
		LastPayoutAt:      primitive.NewDateTimeFromTime(start),
		MaxSessionMinutes: 120,
		AfkGraceSeconds:   60,
	}
	sdb := &fakeClockSessions{stored: sess}
	civDB := &mocks.CivilianDatabase{}
	civDB.On("FindOneAndUpdate", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(mongo.NewSingleResultFromDocument(models.Civilian{ID: primitive.NewObjectID()}, nil, nil))

	raw, _ := json.Marshal(map[string]string{"sessionId": sess.ID.Hex()})
	req := httptest.NewRequest(http.MethodPost, "/api/v2/economy/clock-out?userId="+clockTestUser, bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	Economy{SDB: sdb, CivDB: civDB}.ClockOutHandler(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		CreditedAmount int64 `json:"creditedAmount"`
	}
	assert.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	if assert.Len(t, sdb.updates, 1) {
		assert.Equal(t, "ended", sdb.updates[0]["$set"].(bson.M)["status"])
	}
	return resp.CreditedAmount
}

func TestClockOutPaysDiscordShiftToClockOutTime(t *testing.T) {
	// 45 minutes at a cent a second; allow a second of drift for test runtime.
	assert.InDelta(t, 45*60, clockOut(t, models.ClockSourceDiscord, 45*time.Minute), 1)
}

func TestClockOutCapsDiscordShiftAtMaxSession(t *testing.T) {
	assert.Equal(t, int64(120*60), clockOut(t, models.ClockSourceDiscord, 5*time.Hour))
}

func TestClockOutCapsWebShiftAtLastHeartbeat(t *testing.T) {
	assert.Equal(t, int64(60), clockOut(t, "", 45*time.Minute))
}

func TestGetLastClosedSessionValidatesUserID(t *testing.T) {
	h := Economy{SDB: &fakeClockSessions{}}

	rec := httptest.NewRecorder()
	h.GetLastClosedSessionHandler(rec, httptest.NewRequest(http.MethodGet,
		`/api/v2/economy/session/last-closed?userId={"$ne":""}`, nil))
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = httptest.NewRecorder()
	h.GetLastClosedSessionHandler(rec, httptest.NewRequest(http.MethodGet,
		"/api/v2/economy/session/last-closed?userId="+clockTestUser, nil))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "null", rec.Body.String())
}
