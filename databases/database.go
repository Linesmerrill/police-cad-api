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
	Collection(name string) CollectionHelper
	Client() ClientHelper
}

// CollectionHelper contains all the methods defined for collections in this project
type CollectionHelper interface {
	FindOne(context.Context, interface{}, ...*options.FindOneOptions) SingleResultHelper
	Find(context.Context, interface{}, ...*options.FindOptions) CursorHelper
	InsertOne(context.Context, interface{}, ...*options.InsertOneOptions) InsertOneResultHelper
	Aggregate(context.Context, interface{}, ...*options.AggregateOptions) (CursorHelper, error)
}

// SingleResultHelper contains a single method to decode the result
type SingleResultHelper interface {
	Decode(v interface{}) error
}

// InsertOneResultHelper contains a single method to decode the result
type InsertOneResultHelper interface {
	Decode() interface{}
}

// CursorHelper contains a method to decode the cursor
type CursorHelper interface {
	Decode(v interface{}) error
}

// ClientHelper defined to help at client creation inside main.go
type ClientHelper interface {
	Database(string) DatabaseHelper
	Connect() error
	StartSession() (mongo.Session, error)
}

type mongoClient struct {
	cl *mongo.Client
}

type mongoDatabase struct {
	db *mongo.Database
}

type mongoCollection struct {
	coll *mongo.Collection
}

type mongoSingleResult struct {
	sr *mongo.SingleResult
}

type mongoInsertOneResult struct {
	ior *mongo.InsertOneResult
}

type mongoCursor struct {
	cr *mongo.Cursor
}

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

func (md *mongoDatabase) Collection(colName string) CollectionHelper {
	collection := md.db.Collection(colName)
	return &mongoCollection{coll: collection}
}

func (md *mongoDatabase) Client() ClientHelper {
	client := md.db.Client()
	return &mongoClient{cl: client}
}

func (mc *mongoCollection) FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) SingleResultHelper {
	singleResult := mc.coll.FindOne(ctx, filter, opts...)
	return &mongoSingleResult{sr: singleResult}
}

func (mc *mongoCollection) InsertOne(ctx context.Context, document interface{}, opts ...*options.InsertOneOptions) InsertOneResultHelper {
	insertOneResult, err := mc.coll.InsertOne(ctx, document, opts...)
	if err != nil {
		println(err)
	}
	return &mongoInsertOneResult{ior: insertOneResult}
}

func (mc *mongoCollection) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) CursorHelper {
	cursor, err := mc.coll.Find(ctx, filter, opts...)
	if err != nil {
		println(err)
	}
	return &mongoCursor{cr: cursor}
}

func (mc *mongoCollection) Aggregate(ctx context.Context, pipeline interface{}, opts ...*options.AggregateOptions) (CursorHelper, error) {
	cursor, err := mc.coll.Aggregate(ctx, pipeline, opts...)
	if err != nil {
		println(err)
	}
	return &mongoCursor{cr: cursor}, err
}

func (sr *mongoSingleResult) Decode(v interface{}) error {
	return sr.sr.Decode(v)
}

func (ior *mongoInsertOneResult) Decode() interface{} {
	return ior.ior.InsertedID
}

func (cr *mongoCursor) Decode(v interface{}) error {
	return cr.All(context.Background(), v)
}

func (cr *mongoCursor) All(ctx context.Context, results interface{}) error {
	return cr.cr.All(ctx, results)
}
