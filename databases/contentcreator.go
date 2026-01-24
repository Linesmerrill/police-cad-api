package databases

// go generate: mockery --name ContentCreatorApplicationDatabase
// go generate: mockery --name ContentCreatorDatabase
// go generate: mockery --name ContentCreatorEntitlementDatabase

import (
	"context"
	"time"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	contentCreatorApplicationCollectionName = "content_creator_applications"
	contentCreatorCollectionName            = "content_creators"
	contentCreatorEntitlementCollectionName = "content_creator_entitlements"
	contentCreatorSnapshotCollectionName    = "content_creator_follower_snapshots"
	schedulerLockCollectionName             = "scheduler_locks"
)

// --- Content Creator Application Database ---

// ContentCreatorApplicationDatabase contains the methods to use with the content creator application database
type ContentCreatorApplicationDatabase interface {
	FindOne(ctx context.Context, filter interface{}) (*models.ContentCreatorApplication, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (MongoCursor, error)
	InsertOne(ctx context.Context, application models.ContentCreatorApplication, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error)
	UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error
	CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error)
	FindOneAndUpdate(ctx context.Context, filter interface{}, update interface{}, opts ...*options.FindOneAndUpdateOptions) (*models.ContentCreatorApplication, error)
}

type contentCreatorApplicationDatabase struct {
	db DatabaseHelper
}

// NewContentCreatorApplicationDatabase initializes a new instance of content creator application database
func NewContentCreatorApplicationDatabase(db DatabaseHelper) ContentCreatorApplicationDatabase {
	return &contentCreatorApplicationDatabase{
		db: db,
	}
}

func (c *contentCreatorApplicationDatabase) FindOne(ctx context.Context, filter interface{}) (*models.ContentCreatorApplication, error) {
	application := &models.ContentCreatorApplication{}
	err := c.db.Collection(contentCreatorApplicationCollectionName).FindOne(ctx, filter).Decode(application)
	if err != nil {
		return nil, err
	}
	return application, nil
}

func (c *contentCreatorApplicationDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (MongoCursor, error) {
	cursor, err := c.db.Collection(contentCreatorApplicationCollectionName).Find(ctx, filter, opts...)
	if err != nil {
		return MongoCursor{}, err
	}
	return *cursor, err
}

func (c *contentCreatorApplicationDatabase) InsertOne(ctx context.Context, application models.ContentCreatorApplication, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
	res, err := c.db.Collection(contentCreatorApplicationCollectionName).InsertOne(ctx, application, opts...)
	return res, err
}

func (c *contentCreatorApplicationDatabase) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error {
	_, err := c.db.Collection(contentCreatorApplicationCollectionName).UpdateOne(ctx, filter, update, opts...)
	return err
}

func (c *contentCreatorApplicationDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	return c.db.Collection(contentCreatorApplicationCollectionName).CountDocuments(ctx, filter, opts...)
}

func (c *contentCreatorApplicationDatabase) FindOneAndUpdate(ctx context.Context, filter interface{}, update interface{}, opts ...*options.FindOneAndUpdateOptions) (*models.ContentCreatorApplication, error) {
	application := &models.ContentCreatorApplication{}
	err := c.db.Collection(contentCreatorApplicationCollectionName).FindOneAndUpdate(ctx, filter, update, opts...).Decode(&application)
	if err != nil {
		return nil, err
	}
	return application, nil
}

// --- Content Creator Database ---

// ContentCreatorDatabase contains the methods to use with the content creator database
type ContentCreatorDatabase interface {
	FindOne(ctx context.Context, filter interface{}) (*models.ContentCreator, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (MongoCursor, error)
	InsertOne(ctx context.Context, creator models.ContentCreator, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error)
	UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error
	DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error
	CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error)
	Aggregate(ctx context.Context, pipeline mongo.Pipeline, opts ...*options.AggregateOptions) (*MongoCursor, error)
	FindOneAndUpdate(ctx context.Context, filter interface{}, update interface{}, opts ...*options.FindOneAndUpdateOptions) (*models.ContentCreator, error)
}

type contentCreatorDatabase struct {
	db DatabaseHelper
}

// NewContentCreatorDatabase initializes a new instance of content creator database
func NewContentCreatorDatabase(db DatabaseHelper) ContentCreatorDatabase {
	return &contentCreatorDatabase{
		db: db,
	}
}

func (c *contentCreatorDatabase) FindOne(ctx context.Context, filter interface{}) (*models.ContentCreator, error) {
	creator := &models.ContentCreator{}
	err := c.db.Collection(contentCreatorCollectionName).FindOne(ctx, filter).Decode(creator)
	if err != nil {
		return nil, err
	}
	return creator, nil
}

func (c *contentCreatorDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (MongoCursor, error) {
	cursor, err := c.db.Collection(contentCreatorCollectionName).Find(ctx, filter, opts...)
	if err != nil {
		return MongoCursor{}, err
	}
	return *cursor, err
}

func (c *contentCreatorDatabase) InsertOne(ctx context.Context, creator models.ContentCreator, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
	res, err := c.db.Collection(contentCreatorCollectionName).InsertOne(ctx, creator, opts...)
	return res, err
}

func (c *contentCreatorDatabase) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error {
	_, err := c.db.Collection(contentCreatorCollectionName).UpdateOne(ctx, filter, update, opts...)
	return err
}

func (c *contentCreatorDatabase) DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error {
	return c.db.Collection(contentCreatorCollectionName).DeleteOne(ctx, filter, opts...)
}

func (c *contentCreatorDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	return c.db.Collection(contentCreatorCollectionName).CountDocuments(ctx, filter, opts...)
}

func (c *contentCreatorDatabase) Aggregate(ctx context.Context, pipeline mongo.Pipeline, opts ...*options.AggregateOptions) (*MongoCursor, error) {
	return c.db.Collection(contentCreatorCollectionName).Aggregate(ctx, pipeline, opts...)
}

func (c *contentCreatorDatabase) FindOneAndUpdate(ctx context.Context, filter interface{}, update interface{}, opts ...*options.FindOneAndUpdateOptions) (*models.ContentCreator, error) {
	creator := &models.ContentCreator{}
	err := c.db.Collection(contentCreatorCollectionName).FindOneAndUpdate(ctx, filter, update, opts...).Decode(&creator)
	if err != nil {
		return nil, err
	}
	return creator, nil
}

// --- Content Creator Entitlement Database ---

// ContentCreatorEntitlementDatabase contains the methods to use with the content creator entitlement database
type ContentCreatorEntitlementDatabase interface {
	FindOne(ctx context.Context, filter interface{}) (*models.ContentCreatorEntitlement, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (MongoCursor, error)
	InsertOne(ctx context.Context, entitlement models.ContentCreatorEntitlement, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error)
	UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error
	UpdateMany(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error
	DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error
	CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error)
}

type contentCreatorEntitlementDatabase struct {
	db DatabaseHelper
}

// NewContentCreatorEntitlementDatabase initializes a new instance of content creator entitlement database
func NewContentCreatorEntitlementDatabase(db DatabaseHelper) ContentCreatorEntitlementDatabase {
	return &contentCreatorEntitlementDatabase{
		db: db,
	}
}

func (c *contentCreatorEntitlementDatabase) FindOne(ctx context.Context, filter interface{}) (*models.ContentCreatorEntitlement, error) {
	entitlement := &models.ContentCreatorEntitlement{}
	err := c.db.Collection(contentCreatorEntitlementCollectionName).FindOne(ctx, filter).Decode(entitlement)
	if err != nil {
		return nil, err
	}
	return entitlement, nil
}

func (c *contentCreatorEntitlementDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (MongoCursor, error) {
	cursor, err := c.db.Collection(contentCreatorEntitlementCollectionName).Find(ctx, filter, opts...)
	if err != nil {
		return MongoCursor{}, err
	}
	return *cursor, err
}

func (c *contentCreatorEntitlementDatabase) InsertOne(ctx context.Context, entitlement models.ContentCreatorEntitlement, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
	res, err := c.db.Collection(contentCreatorEntitlementCollectionName).InsertOne(ctx, entitlement, opts...)
	return res, err
}

func (c *contentCreatorEntitlementDatabase) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error {
	_, err := c.db.Collection(contentCreatorEntitlementCollectionName).UpdateOne(ctx, filter, update, opts...)
	return err
}

func (c *contentCreatorEntitlementDatabase) UpdateMany(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error {
	_, err := c.db.Collection(contentCreatorEntitlementCollectionName).UpdateMany(ctx, filter, update, opts...)
	return err
}

func (c *contentCreatorEntitlementDatabase) DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error {
	return c.db.Collection(contentCreatorEntitlementCollectionName).DeleteOne(ctx, filter, opts...)
}

func (c *contentCreatorEntitlementDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	return c.db.Collection(contentCreatorEntitlementCollectionName).CountDocuments(ctx, filter, opts...)
}

// --- Content Creator Follower Snapshot Database ---

// ContentCreatorSnapshotDatabase contains the methods for follower snapshots
type ContentCreatorSnapshotDatabase interface {
	InsertOne(ctx context.Context, snapshot models.ContentCreatorFollowerSnapshot, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (MongoCursor, error)
	FindOne(ctx context.Context, filter interface{}) (*models.ContentCreatorFollowerSnapshot, error)
}

type contentCreatorSnapshotDatabase struct {
	db DatabaseHelper
}

// NewContentCreatorSnapshotDatabase initializes a new instance of content creator snapshot database
func NewContentCreatorSnapshotDatabase(db DatabaseHelper) ContentCreatorSnapshotDatabase {
	return &contentCreatorSnapshotDatabase{
		db: db,
	}
}

func (c *contentCreatorSnapshotDatabase) InsertOne(ctx context.Context, snapshot models.ContentCreatorFollowerSnapshot, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
	res, err := c.db.Collection(contentCreatorSnapshotCollectionName).InsertOne(ctx, snapshot, opts...)
	return res, err
}

func (c *contentCreatorSnapshotDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (MongoCursor, error) {
	cursor, err := c.db.Collection(contentCreatorSnapshotCollectionName).Find(ctx, filter, opts...)
	if err != nil {
		return MongoCursor{}, err
	}
	return *cursor, err
}

func (c *contentCreatorSnapshotDatabase) FindOne(ctx context.Context, filter interface{}) (*models.ContentCreatorFollowerSnapshot, error) {
	snapshot := &models.ContentCreatorFollowerSnapshot{}
	err := c.db.Collection(contentCreatorSnapshotCollectionName).FindOne(ctx, filter).Decode(snapshot)
	if err != nil {
		return nil, err
	}
	return snapshot, nil
}

// --- Scheduler Lock Database ---

// SchedulerLockDatabase handles distributed locking for scheduled jobs
type SchedulerLockDatabase interface {
	// TryAcquireLock attempts to acquire a lock for a job. Returns true if acquired, false if already locked.
	TryAcquireLock(ctx context.Context, jobName, instanceID string, ttl time.Duration) (bool, error)
	// ReleaseLock releases a lock held by this instance
	ReleaseLock(ctx context.Context, jobName, instanceID string) error
}

type schedulerLockDatabase struct {
	db DatabaseHelper
}

// NewSchedulerLockDatabase initializes a new scheduler lock database
func NewSchedulerLockDatabase(db DatabaseHelper) SchedulerLockDatabase {
	return &schedulerLockDatabase{
		db: db,
	}
}

// SchedulerLock represents a distributed lock document
type SchedulerLock struct {
	ID        string    `bson:"_id"`       // job name as the ID
	LockedBy  string    `bson:"lockedBy"`  // pod/instance identifier
	LockedAt  time.Time `bson:"lockedAt"`  // when the lock was acquired
	ExpiresAt time.Time `bson:"expiresAt"` // when the lock expires (TTL)
}

func (s *schedulerLockDatabase) TryAcquireLock(ctx context.Context, jobName, instanceID string, ttl time.Duration) (bool, error) {
	now := time.Now()
	expiresAt := now.Add(ttl)

	// Try to insert a new lock OR update an expired one
	// This uses MongoDB's upsert with a filter that either:
	// 1. The document doesn't exist, OR
	// 2. The document exists but is expired
	filter := map[string]interface{}{
		"$or": []map[string]interface{}{
			{"_id": jobName, "expiresAt": map[string]interface{}{"$lt": now}},
		},
	}

	update := map[string]interface{}{
		"$set": map[string]interface{}{
			"_id":       jobName,
			"lockedBy":  instanceID,
			"lockedAt":  now,
			"expiresAt": expiresAt,
		},
	}

	opts := options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)

	result := s.db.Collection(schedulerLockCollectionName).FindOneAndUpdate(ctx, filter, update, opts)

	// Check if we got the lock
	var lock SchedulerLock
	err := result.Decode(&lock)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			// Lock already held by another instance
			return false, nil
		}
		// Try a simple insert for the case where the document doesn't exist
		_, insertErr := s.db.Collection(schedulerLockCollectionName).InsertOne(ctx, SchedulerLock{
			ID:        jobName,
			LockedBy:  instanceID,
			LockedAt:  now,
			ExpiresAt: expiresAt,
		})
		if insertErr != nil {
			// Check if it's a duplicate key error (lock already exists)
			if mongo.IsDuplicateKeyError(insertErr) {
				return false, nil
			}
			return false, insertErr
		}
		return true, nil
	}

	// Check if we are the owner of the lock
	return lock.LockedBy == instanceID, nil
}

func (s *schedulerLockDatabase) ReleaseLock(ctx context.Context, jobName, instanceID string) error {
	filter := map[string]interface{}{
		"_id":      jobName,
		"lockedBy": instanceID,
	}
	return s.db.Collection(schedulerLockCollectionName).DeleteOne(ctx, filter)
}
