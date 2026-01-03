package databases

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"

	"github.com/linesmerrill/police-cad-api/config"
)

// DBQueryRecorder is a function type for recording DB queries
// This avoids import cycles by allowing the api package to register a recorder
var DBQueryRecorder func(context.Context, string, string, time.Duration, error)

// SetDBQueryRecorder sets the function to record DB queries
func SetDBQueryRecorder(recorder func(context.Context, string, string, time.Duration, error)) {
	DBQueryRecorder = recorder
}

// MongoCollectionHelper defines the methods required for collection operations (for real and mock collections)
type MongoCollectionHelper interface {
	FindOne(context.Context, interface{}, ...*options.FindOneOptions) SingleResultHelper
	Find(context.Context, interface{}, ...*options.FindOptions) (*MongoCursor, error)
	CountDocuments(context.Context, interface{}, ...*options.CountOptions) (int64, error)
	InsertOne(context.Context, interface{}, ...*options.InsertOneOptions) (InsertOneResultHelper, error)
	Aggregate(context.Context, interface{}, ...*options.AggregateOptions) (*MongoCursor, error)
	UpdateOne(context.Context, interface{}, interface{}, ...*options.UpdateOptions) (*mongo.UpdateResult, error)
	DeleteOne(context.Context, interface{}, ...*options.DeleteOptions) error
	UpdateMany(context.Context, interface{}, interface{}, ...*options.UpdateOptions) (*mongo.UpdateResult, error)
	FindOneAndUpdate(context.Context, interface{}, interface{}, ...*options.FindOneAndUpdateOptions) *mongo.SingleResult
}

// DatabaseHelper contains the collection and client to be used to access the methods
type DatabaseHelper interface {
	Collection(name string) MongoCollectionHelper
	Client() ClientHelper
}

// CollectionHelper contains all the methods defined for collections in this project
type CollectionHelper interface {
	FindOne(context.Context, interface{}, ...*options.FindOneOptions) SingleResultHelper
	Find(context.Context, interface{}, ...*options.FindOptions) CursorHelper
	CountDocuments(context.Context, interface{}, ...*options.CountOptions) (int64, error)
	InsertOne(context.Context, interface{}, ...*options.InsertOneOptions) InsertOneResultHelper
	Aggregate(context.Context, interface{}, ...*options.AggregateOptions) (*mongo.Cursor, error)
	UpdateOne(context.Context, interface{}, interface{}, ...*options.UpdateOptions) (*mongo.UpdateResult, error)
}

// SingleResultHelper contains a single method to decode the result
type SingleResultHelper interface {
	Decode(v interface{}) error
}

// InsertOneResultHelper contains a single method to decode the result
type InsertOneResultHelper interface {
	Decode() interface{}
}

// ClientHelper defined to help at client creation inside main.go
type ClientHelper interface {
	Database(string) DatabaseHelper
	Connect() error
	StartSession() (mongo.Session, error)
}

// SessionHelper defined to help at session creation inside main.go
type SessionHelper interface {
	EndSession() error
	// Add other methods as needed
}

type mongoClient struct {
	cl *mongo.Client
}

type mongoDatabase struct {
	db *mongo.Database
}

// MongoCollection contains the collection to be used to access the methods
type MongoCollection struct {
	coll           *mongo.Collection
	collectionName string
}

type mongoSingleResult struct {
	sr *mongo.SingleResult
}

type mongoInsertOneResult struct {
	ior *mongo.InsertOneResult
}

// MongoCursor contains the cursor to be used to access the methods
type MongoCursor struct {
	cr *mongo.Cursor
}

// mongoSession contains the session to be used to access the methods
type mongoSession struct {
	mongo.Session
}

// NewClient uses the values from the config and returns a mongo client
func NewClient(conf *config.Config) (ClientHelper, error) {
	// Configure connection pool for optimal performance
	// Optimized for fast queries (<100ms) and high concurrency
	clientOptions := options.Client().ApplyURI(conf.URL).
		SetMaxPoolSize(400).                    // Maximum connections per dyno (400 × 2 dynos = 800 total, 53% of M10's 1,500 per node limit)
		// M10 cluster: 1,500 concurrent connections per node
		// With 2 dynos: 800 total connections = 53% of limit (leaves 700 buffer for spikes)
		// Reduced from 600 to 400: Lower overhead, faster connection establishment
		// With 5s query timeouts, connections release quickly, so 400 per dyno is sufficient
		// If you add more dynos, consider: 300 per dyno × 3 dynos = 900 total
		SetMinPoolSize(50).                     // Increased from 20: More warm connections for faster queries
		SetMaxConnecting(20).                   // Increased from 10: Faster connection establishment during spikes
		SetMaxConnIdleTime(15 * time.Second).  // Reduced from 30s: Release idle connections faster (frees up pool)
		SetServerSelectionTimeout(10 * time.Second). // Reduced from 30s: Faster failure detection
		SetSocketTimeout(6 * time.Second).     // Reduced from 30s: Match query timeout (5s) + 1s network overhead
		SetConnectTimeout(5 * time.Second).     // Keep at 5s: Fast connection attempts
		SetRetryWrites(true).                   // Enable retry writes for transient failures
		SetRetryReads(true).                    // Enable retry reads for transient failures
		SetHeartbeatInterval(10 * time.Second). // Check server status every 10 seconds
		SetReadPreference(readpref.PrimaryPreferred()) // Use PrimaryPreferred for better load distribution
		// Changed back to PrimaryPreferred() to distribute load across PRIMARY and SECONDARY
		// If replication lag is still high (>1s), MongoDB will automatically prefer PRIMARY
		// This provides better resilience and load distribution when secondaries are healthy

	c, err := mongo.NewClient(clientOptions)

	return &mongoClient{cl: c}, err
}

// NewDatabase uses the client from NewClient and sets the database name
func NewDatabase(conf *config.Config, client ClientHelper) DatabaseHelper {
	return client.Database(conf.DatabaseName)
}

func (mc *mongoClient) Database(dbName string) DatabaseHelper {
	db := mc.cl.Database(dbName)
	return &mongoDatabase{db: db}
}

func (mc *mongoClient) StartSession() (mongo.Session, error) {
	session, err := mc.cl.StartSession()
	return &mongoSession{session}, err
}

func (mc *mongoClient) Connect() error {
	// Use context with timeout for connection
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return mc.cl.Connect(ctx)
}

func (md *mongoDatabase) Collection(name string) MongoCollectionHelper {
	collection := md.db.Collection(name)
	return &MongoCollection{coll: collection, collectionName: name}
}

func (md *mongoDatabase) Client() ClientHelper {
	client := md.db.Client()
	return &mongoClient{cl: client}
}

// FindOne returns a single result from the collection
func (mc *MongoCollection) FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) SingleResultHelper {
	start := time.Now()
	singleResult := mc.coll.FindOne(ctx, filter, opts...)
	duration := time.Since(start)
	if DBQueryRecorder != nil {
		DBQueryRecorder(ctx, "FindOne", mc.collectionName, duration, nil)
	}
	return &mongoSingleResult{sr: singleResult}
}

// InsertOne inserts a single document into the collection
func (mc *MongoCollection) InsertOne(ctx context.Context, document interface{}, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
	start := time.Now()
	insertOneResult, err := mc.coll.InsertOne(ctx, document, opts...)
	duration := time.Since(start)
	if DBQueryRecorder != nil {
		DBQueryRecorder(ctx, "InsertOne", mc.collectionName, duration, err)
	}
	if err != nil {
		return &mongoInsertOneResult{}, err
	}
	return &mongoInsertOneResult{ior: insertOneResult}, err
}

// Find returns a cursor to iterate over the collection
func (mc *MongoCollection) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (*MongoCursor, error) {
	start := time.Now()
	cursor, err := mc.coll.Find(ctx, filter, opts...)
	duration := time.Since(start)
	if DBQueryRecorder != nil {
		DBQueryRecorder(ctx, "Find", mc.collectionName, duration, err)
	}
	if err != nil {
		println(err)
	}
	return &MongoCursor{cr: cursor}, err
}

// Aggregate returns a cursor to iterate over the collection
func (mc *MongoCollection) Aggregate(ctx context.Context, pipeline interface{}, opts ...*options.AggregateOptions) (*MongoCursor, error) {
	start := time.Now()
	cursor, err := mc.coll.Aggregate(ctx, pipeline, opts...)
	duration := time.Since(start)
	if DBQueryRecorder != nil {
		DBQueryRecorder(ctx, "Aggregate", mc.collectionName, duration, err)
	}
	if err != nil {
		println(err)
	}
	return &MongoCursor{cr: cursor}, err
}

// DeleteOne deletes a single document from the collection
func (mc *MongoCollection) DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error {
	start := time.Now()
	_, err := mc.coll.DeleteOne(ctx, filter, opts...)
	duration := time.Since(start)
	if DBQueryRecorder != nil {
		DBQueryRecorder(ctx, "DeleteOne", mc.collectionName, duration, err)
	}
	return err
}

// FindOneAndUpdate updates a single document and returns the result
func (mc *MongoCollection) FindOneAndUpdate(ctx context.Context, filter interface{}, update interface{}, opts ...*options.FindOneAndUpdateOptions) *mongo.SingleResult {
	start := time.Now()
	result := mc.coll.FindOneAndUpdate(ctx, filter, update, opts...)
	duration := time.Since(start)
	if DBQueryRecorder != nil {
		DBQueryRecorder(ctx, "FindOneAndUpdate", mc.collectionName, duration, result.Err())
	}
	return result
}

// UpdateOne updates a single document in the collection
func (mc *MongoCollection) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	start := time.Now()
	res, err := mc.coll.UpdateOne(ctx, filter, update, opts...)
	duration := time.Since(start)
	if DBQueryRecorder != nil {
		DBQueryRecorder(ctx, "UpdateOne", mc.collectionName, duration, err)
	}
	if err != nil {
		println(err)
	}
	return res, err
}

// CountDocuments returns the number of documents in the collection
func (mc *MongoCollection) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	start := time.Now()
	count, err := mc.coll.CountDocuments(ctx, filter, opts...)
	duration := time.Since(start)
	if DBQueryRecorder != nil {
		DBQueryRecorder(ctx, "CountDocuments", mc.collectionName, duration, err)
	}
	if err != nil {
		println(err)
	}
	return count, err
}

// UpdateMany updates multiple documents in the collection
func (mc *MongoCollection) UpdateMany(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	start := time.Now()
	res, err := mc.coll.UpdateMany(ctx, filter, update, opts...)
	duration := time.Since(start)
	if DBQueryRecorder != nil {
		DBQueryRecorder(ctx, "UpdateMany", mc.collectionName, duration, err)
	}
	if err != nil {
		println(err)
	}
	return res, err
}

func (sr *mongoSingleResult) Decode(v interface{}) error {
	// Note: Decode timing is included in the operation timing
	// since it's part of the same query execution
	return sr.sr.Decode(v)
}

func (ior *mongoInsertOneResult) Decode() interface{} {
	return ior.ior.InsertedID
}

// Decode decodes the cursor into the provided interface
// NOTE: This method uses context.Background() which may not have timeout or trace tracking.
// Prefer using All(ctx, v) directly with a proper context.
func (cr *MongoCursor) Decode(v interface{}) error {
	return cr.All(context.Background(), v)
}

// All decodes the cursor into the provided interface
func (cr *MongoCursor) All(ctx context.Context, results interface{}) error {
	return cr.cr.All(ctx, results)
}

// Next returns true if there are more documents to iterate over
func (cr *MongoCursor) Next(ctx context.Context) bool { return cr.cr.Next(ctx) }

// Close closes the cursor
func (cr *MongoCursor) Close(ctx context.Context) error { return cr.cr.Close(ctx) }

// Err returns the error from the cursor
func (cr *MongoCursor) Err() error { return cr.cr.Err() }

// DecodeCurrent decodes the current document from the cursor
func (cr *MongoCursor) DecodeCurrent(v interface{}) error {
	return cr.cr.Decode(v)
}

// CursorHelper defines the methods required for cursor operations
type CursorHelper interface {
	Next(ctx context.Context) bool
	Close(ctx context.Context) error
	Decode(val interface{}) error
	Err() error
}
