package api

import (
	"context"
	"testing"
	"time"

	"github.com/shaj13/go-guardian/auth"
	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/databases"
)

// fakeSingleResult lets a test control what Decode returns.
type fakeSingleResult struct {
	doc *tokenDoc
	err error
}

func (f *fakeSingleResult) Decode(v interface{}) error {
	if f.err != nil {
		return f.err
	}
	if td, ok := v.(*tokenDoc); ok && f.doc != nil {
		*td = *f.doc
	}
	return nil
}

// fakeCollection implements databases.MongoCollectionHelper. Only the methods
// the token store uses do anything; the rest satisfy the interface.
type fakeCollection struct {
	findResult  *fakeSingleResult
	updateCalls int
	deleteCalls int
	lastUpdate  interface{}
}

func (c *fakeCollection) FindOne(context.Context, interface{}, ...*options.FindOneOptions) databases.SingleResultHelper {
	if c.findResult != nil {
		return c.findResult
	}
	return &fakeSingleResult{err: mongo.ErrNoDocuments}
}
func (c *fakeCollection) UpdateOne(_ context.Context, _ interface{}, update interface{}, _ ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	c.updateCalls++
	c.lastUpdate = update
	return &mongo.UpdateResult{}, nil
}
func (c *fakeCollection) DeleteOne(context.Context, interface{}, ...*options.DeleteOptions) error {
	c.deleteCalls++
	return nil
}
func (c *fakeCollection) Find(context.Context, interface{}, ...*options.FindOptions) (*databases.MongoCursor, error) {
	return nil, nil
}
func (c *fakeCollection) CountDocuments(context.Context, interface{}, ...*options.CountOptions) (int64, error) {
	return 0, nil
}
func (c *fakeCollection) InsertOne(context.Context, interface{}, ...*options.InsertOneOptions) (databases.InsertOneResultHelper, error) {
	return nil, nil
}
func (c *fakeCollection) Aggregate(context.Context, interface{}, ...*options.AggregateOptions) (*databases.MongoCursor, error) {
	return nil, nil
}
func (c *fakeCollection) DeleteMany(context.Context, interface{}, ...*options.DeleteOptions) (int64, error) {
	return 0, nil
}
func (c *fakeCollection) UpdateMany(context.Context, interface{}, interface{}, ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	return nil, nil
}
func (c *fakeCollection) FindOneAndUpdate(context.Context, interface{}, interface{}, ...*options.FindOneAndUpdateOptions) *mongo.SingleResult {
	return nil
}

type fakeDB struct{ coll *fakeCollection }

func (d *fakeDB) Collection(string) databases.MongoCollectionHelper { return d.coll }
func (d *fakeDB) Client() databases.ClientHelper                    { return nil }

func newStoreWithFakeDB(t *testing.T) (*mongoTokenStore, *fakeCollection) {
	t.Helper()
	coll := &fakeCollection{}
	return newMongoTokenStore(&fakeDB{coll: coll}), coll
}

func TestMongoTokenStore_StoreThenLoadHitsL1(t *testing.T) {
	s, coll := newStoreWithFakeDB(t)

	info := auth.NewDefaultUser("user@example.com", "uid-1", nil, nil)
	if err := s.Store("tok-1", info, nil); err != nil {
		t.Fatalf("store: %v", err)
	}
	assert.Equal(t, 1, coll.updateCalls)

	// Served from L1 — to prove it, make any DB read fail.
	coll.findResult = &fakeSingleResult{err: mongo.ErrNoDocuments}
	got, ok, err := s.Load("tok-1", nil)
	assert.NoError(t, err)
	assert.True(t, ok)
	gotInfo := got.(auth.Info)
	assert.Equal(t, "uid-1", gotInfo.ID())
	assert.Equal(t, "user@example.com", gotInfo.UserName())
}

func TestMongoTokenStore_LoadFromMongo(t *testing.T) {
	s, coll := newStoreWithFakeDB(t)
	coll.findResult = &fakeSingleResult{doc: &tokenDoc{
		Token:     "tok-db",
		UserName:  "db@example.com",
		UserID:    "uid-db",
		ExpiresAt: time.Now().Add(time.Hour),
	}}

	got, ok, err := s.Load("tok-db", nil)
	assert.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "uid-db", got.(auth.Info).ID())
}

func TestMongoTokenStore_LoadExpiredIsMiss(t *testing.T) {
	s, coll := newStoreWithFakeDB(t)
	coll.findResult = &fakeSingleResult{doc: &tokenDoc{
		Token:     "tok-old",
		UserID:    "uid-old",
		ExpiresAt: time.Now().Add(-time.Hour),
	}}

	_, ok, err := s.Load("tok-old", nil)
	assert.NoError(t, err)
	assert.False(t, ok)
}

func TestMongoTokenStore_DeleteEvictsL1(t *testing.T) {
	s, coll := newStoreWithFakeDB(t)
	_ = s.Store("tok-2", auth.NewDefaultUser("u@x.com", "uid-2", nil, nil), nil)

	if err := s.Delete("tok-2", nil); err != nil {
		t.Fatalf("delete: %v", err)
	}
	assert.Equal(t, 1, coll.deleteCalls)
	assert.NotContains(t, s.Keys(), "tok-2")
}

func TestMongoTokenStore_TTLFromEnv(t *testing.T) {
	t.Setenv("AUTH_TOKEN_TTL_HOURS", "48")
	s, _ := newStoreWithFakeDB(t)
	assert.Equal(t, 48*time.Hour, s.ttl)
}
