package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/shaj13/go-guardian/auth"
	"github.com/shaj13/go-guardian/store"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/databases"
)

// authTokenCollection is the MongoDB collection that persists bearer tokens.
const authTokenCollection = "auth_tokens"

// defaultTokenTTL is how long a persisted bearer token stays valid. The mobile
// app rotates its token roughly every 10 minutes and the website re-issues one
// per session, so a generous window avoids logging active users out while a TTL
// index keeps the collection from growing forever.
const defaultTokenTTL = 30 * 24 * time.Hour

// tokenL1TTL is the lifetime of the in-process cache that sits in front of
// Mongo. It keeps hot tokens fast while still letting a revocation made on
// another dyno take effect within this window.
const tokenL1TTL = 60 * time.Second

// tokenDoc is the persisted shape of a bearer token.
type tokenDoc struct {
	Token     string    `bson:"_id"`
	UserName  string    `bson:"userName"`
	UserID    string    `bson:"userID"`
	ExpiresAt time.Time `bson:"expiresAt"`
}

// mongoTokenStore is a go-guardian store.Cache backed by MongoDB so bearer
// tokens survive API restarts and are shared across dynos. A small in-process
// FIFO acts as an L1 cache to avoid a DB read on every authenticated request.
//
// This replaces the previous in-memory-only FIFO, which lost every token on
// restart and was never shared across dynos — meaning any enforcement of token
// validity would have rejected active users after each deploy.
type mongoTokenStore struct {
	db  databases.DatabaseHelper
	l1  *store.FIFO
	ttl time.Duration
}

func newMongoTokenStore(db databases.DatabaseHelper) *mongoTokenStore {
	ttl := defaultTokenTTL
	if v := os.Getenv("AUTH_TOKEN_TTL_HOURS"); v != "" {
		if h, err := strconv.Atoi(v); err == nil && h > 0 {
			ttl = time.Duration(h) * time.Hour
		}
	}
	s := &mongoTokenStore{
		db:  db,
		l1:  store.NewFIFO(context.Background(), tokenL1TTL),
		ttl: ttl,
	}
	s.ensureIndexes()
	return s
}

// ensureIndexes creates a TTL index so Mongo auto-purges expired tokens.
// Best-effort: a failure here only means stale docs linger (Load still ignores
// expired tokens), so we log and continue.
func (s *mongoTokenStore) ensureIndexes() {
	coll, ok := s.db.Collection(authTokenCollection).(interface {
		Indexes() mongo.IndexView
	})
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	// Best-effort and idempotent; ignore the result.
	_, _ = coll.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "expiresAt", Value: 1}},
		Options: options.Index().SetExpireAfterSeconds(0).SetName("ttl_expiresAt"),
	})
}

// Load returns the auth.Info for a token, checking the L1 cache before Mongo.
func (s *mongoTokenStore) Load(key string, r *http.Request) (interface{}, bool, error) {
	if v, ok, _ := s.l1.Load(key, r); ok {
		return v, true, nil
	}

	ctx, cancel := WithQueryTimeout(contextOrBackground(r))
	defer cancel()

	doc := tokenDoc{}
	err := s.db.Collection(authTokenCollection).FindOne(ctx, bson.M{"_id": key}).Decode(&doc)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, false, nil
		}
		return nil, false, err
	}

	// Defensive: honour expiry even if the TTL index hasn't swept the doc yet.
	if !doc.ExpiresAt.IsZero() && time.Now().After(doc.ExpiresAt) {
		return nil, false, nil
	}

	info := auth.NewDefaultUser(doc.UserName, doc.UserID, nil, nil)
	_ = s.l1.Store(key, info, r)
	return info, true, nil
}

// Store persists a token and mirrors it into the L1 cache.
func (s *mongoTokenStore) Store(key string, value interface{}, r *http.Request) error {
	info, ok := value.(auth.Info)
	if !ok {
		return fmt.Errorf("auth token store: unexpected value type %T", value)
	}

	ctx, cancel := WithQueryTimeout(contextOrBackground(r))
	defer cancel()

	_, err := s.db.Collection(authTokenCollection).UpdateOne(
		ctx,
		bson.M{"_id": key},
		bson.M{"$set": bson.M{
			"userName":  info.UserName(),
			"userID":    info.ID(),
			"expiresAt": time.Now().Add(s.ttl),
		}},
		options.Update().SetUpsert(true),
	)
	if err != nil {
		return err
	}

	_ = s.l1.Store(key, info, r)
	return nil
}

// Delete removes a token from both Mongo and the L1 cache (e.g. on logout).
func (s *mongoTokenStore) Delete(key string, r *http.Request) error {
	_ = s.l1.Delete(key, r)

	ctx, cancel := WithQueryTimeout(contextOrBackground(r))
	defer cancel()
	return s.db.Collection(authTokenCollection).DeleteOne(ctx, bson.M{"_id": key})
}

// Keys returns the keys currently held in the L1 cache. The full set lives in
// Mongo; go-guardian's auth flow never calls Keys, so we avoid scanning the
// collection here.
func (s *mongoTokenStore) Keys() []string {
	return s.l1.Keys()
}

func contextOrBackground(r *http.Request) context.Context {
	if r != nil {
		return r.Context()
	}
	return context.Background()
}
