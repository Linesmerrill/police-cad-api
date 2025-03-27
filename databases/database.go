package databases

import (
	"context"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/config"
)

// DatabaseHelper contains the collection and client to be used to access the methods
// defined below
type DatabaseHelper interface {
	Collection(name string) *MongoCollection
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
	coll *mongo.Collection
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
	c, err := mongo.NewClient(options.Client().ApplyURI(conf.URL))

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
	return mc.cl.Connect(nil)
}

func (md *mongoDatabase) Collection(name string) *MongoCollection {
	collection := md.db.Collection(name)
	return &MongoCollection{coll: collection}
}

func (md *mongoDatabase) Client() ClientHelper {
	client := md.db.Client()
	return &mongoClient{cl: client}
}

// FindOne returns a single result from the collection
func (mc *MongoCollection) FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) SingleResultHelper {
	singleResult := mc.coll.FindOne(ctx, filter, opts...)
	return &mongoSingleResult{sr: singleResult}
}

// InsertOne inserts a single document into the collection
func (mc *MongoCollection) InsertOne(ctx context.Context, document interface{}, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
	insertOneResult, err := mc.coll.InsertOne(ctx, document, opts...)
	if err != nil {
		return &mongoInsertOneResult{}, err
	}
	return &mongoInsertOneResult{ior: insertOneResult}, err
}

// Find returns a cursor to iterate over the collection
func (mc *MongoCollection) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (*MongoCursor, error) {
	cursor, err := mc.coll.Find(ctx, filter, opts...)
	if err != nil {
		println(err)
	}
	return &MongoCursor{cr: cursor}, err
}

// Aggregate returns a cursor to iterate over the collection
func (mc *MongoCollection) Aggregate(ctx context.Context, pipeline interface{}, opts ...*options.AggregateOptions) (*MongoCursor, error) {
	cursor, err := mc.coll.Aggregate(ctx, pipeline, opts...)
	if err != nil {
		println(err)
	}
	return &MongoCursor{cr: cursor}, err
}

// DeleteOne deletes a single document from the collection
func (mc *MongoCollection) DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error {
	_, err := mc.coll.DeleteOne(ctx, filter, opts...)
	return err
}

// UpdateOne updates a single document in the collection
func (mc *MongoCollection) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	res, err := mc.coll.UpdateOne(ctx, filter, update, opts...)
	if err != nil {
		println(err)
	}
	return res, err
}

// CountDocuments returns the number of documents in the collection
func (mc *MongoCollection) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	count, err := mc.coll.CountDocuments(ctx, filter, opts...)
	if err != nil {
		println(err)
	}
	return count, err
}

// UpdateMany updates multiple documents in the collection
func (mc *MongoCollection) UpdateMany(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	res, err := mc.coll.UpdateMany(ctx, filter, update, opts...)
	if err != nil {
		println(err)
	}
	return res, err
}

func (sr *mongoSingleResult) Decode(v interface{}) error {
	return sr.sr.Decode(v)
}

func (ior *mongoInsertOneResult) Decode() interface{} {
	return ior.ior.InsertedID
}

// Decode decodes the cursor into the provided interface
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

// CursorHelper defines the methods required for cursor operations
type CursorHelper interface {
	Next(ctx context.Context) bool
	Close(ctx context.Context) error
	Decode(val interface{}) error
	Err() error
}
