package scheduler

import (
	"context"
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

// fakeSessionDB serves a fixed set of active sessions and records every update
// the sweep writes, keyed by session id.
type fakeSessionDB struct {
	sessions []models.ClockSession
	updates  map[primitive.ObjectID]bson.M
}

func (f *fakeSessionDB) FindOne(context.Context, interface{}, ...*options.FindOneOptions) (*models.ClockSession, error) {
	return nil, mongo.ErrNoDocuments
}
func (f *fakeSessionDB) Find(context.Context, interface{}, ...*options.FindOptions) ([]models.ClockSession, error) {
	return f.sessions, nil
}
func (f *fakeSessionDB) InsertOne(context.Context, interface{}, ...*options.InsertOneOptions) (databases.InsertOneResultHelper, error) {
	return nil, nil
}
func (f *fakeSessionDB) UpdateOne(_ context.Context, filter interface{}, update interface{}, _ ...*options.UpdateOptions) error {
	id := filter.(bson.M)["_id"].(primitive.ObjectID)
	f.updates[id] = update.(bson.M)
	return nil
}
func (f *fakeSessionDB) FindOneAndUpdate(context.Context, interface{}, interface{}, ...*options.FindOneAndUpdateOptions) *mongo.SingleResult {
	return nil
}
func (f *fakeSessionDB) CountDocuments(context.Context, interface{}, ...*options.CountOptions) (int64, error) {
	return 0, nil
}

func sweepSession(source string, start time.Time) models.ClockSession {
	return models.ClockSession{
		ID:                primitive.NewObjectID(),
		UserID:            "user1",
		CivilianID:        primitive.NewObjectID().Hex(),
		Status:            "active",
		Source:            source,
		PayRateSnapshot:   3600, // one cent per second
		StartedAt:         primitive.NewDateTimeFromTime(start),
		LastHeartbeatAt:   primitive.NewDateTimeFromTime(start),
		LastPayoutAt:      primitive.NewDateTimeFromTime(start),
		MaxSessionMinutes: 120,
		AfkGraceSeconds:   60,
	}
}

func runSweep(t *testing.T, sessions ...models.ClockSession) *fakeSessionDB {
	t.Helper()
	sdb := &fakeSessionDB{sessions: sessions, updates: map[primitive.ObjectID]bson.M{}}
	lock := &mocks.SchedulerLockDatabase{}
	lock.On("TryAcquireLock", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(true, nil)
	lock.On("ReleaseLock", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	civ := &mocks.CivilianDatabase{}
	civ.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s := &Scheduler{
		LockDB:     lock,
		SessionDB:  sdb,
		CivDB:      civ,
		instanceID: "test.1",
		jobStats:   map[string]*JobStat{},
	}
	s.staleSessionSweep()
	return sdb
}

func TestStaleSessionSweep_DiscordShiftNotSweptForAfk(t *testing.T) {
	sess := sweepSession(models.ClockSourceDiscord, time.Now().Add(-10*time.Minute))
	sdb := runSweep(t, sess)
	assert.Empty(t, sdb.updates, "a Discord shift with no heartbeats must stay active")
}

func TestStaleSessionSweep_WebShiftStillAbandonedForAfk(t *testing.T) {
	sess := sweepSession("", time.Now().Add(-10*time.Minute))
	sdb := runSweep(t, sess)

	update, ok := sdb.updates[sess.ID]
	if !assert.True(t, ok, "web shift past the AFK grace must be swept") {
		return
	}
	assert.Equal(t, "abandoned", update["$set"].(bson.M)["status"])
	// Paid only up to last heartbeat + grace: 60 seconds at a cent a second.
	assert.Equal(t, int64(60), update["$inc"].(bson.M)["earnings"])
}

func TestStaleSessionSweep_DiscordShiftExpiresAtMaxSessionAndIsPaidToIt(t *testing.T) {
	sess := sweepSession(models.ClockSourceDiscord, time.Now().Add(-3*time.Hour))
	sdb := runSweep(t, sess)

	update, ok := sdb.updates[sess.ID]
	if !assert.True(t, ok, "Discord shift past max session must be swept") {
		return
	}
	assert.Equal(t, "expired", update["$set"].(bson.M)["status"])
	assert.Equal(t, int64(120*60), update["$inc"].(bson.M)["earnings"])
}

func TestStaleSessionSweep_BackgroundedMobileShiftNotSweptForAfk(t *testing.T) {
	sess := sweepSession(models.ClockSourceMobile, time.Now().Add(-10*time.Minute))
	sdb := runSweep(t, sess)
	assert.Empty(t, sdb.updates, "a mobile shift with the app backgrounded must stay active")
}

func TestStaleSessionSweep_MobileShiftExpiresAtMaxSessionAndIsPaidToIt(t *testing.T) {
	sess := sweepSession(models.ClockSourceMobile, time.Now().Add(-3*time.Hour))
	sdb := runSweep(t, sess)

	update, ok := sdb.updates[sess.ID]
	if !assert.True(t, ok, "mobile shift past max session must be swept") {
		return
	}
	assert.Equal(t, "expired", update["$set"].(bson.M)["status"])
	assert.Equal(t, int64(120*60), update["$inc"].(bson.M)["earnings"])
}
