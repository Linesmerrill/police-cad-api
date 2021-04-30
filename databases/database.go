package databases

import (
	"context"

	"github.com/linesmerrill/police-cad-api/config"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// DatabaseHelper contains the collection and client to be used to access the methods
// defined below
type DatabaseHelper interface {
	Collection(name string) CollectionHelper
	Client() ClientHelper
}

// CollectionHelper contains all the methods defined for collections in this project
type CollectionHelper interface {
	FindOne(context.Context, interface{}) SingleResultHelper
}

// SingleResultHelper contains a single method to decode the result
type SingleResultHelper interface {
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

type mongoSession struct {
	mongo.Session
}

// NewClient uses the values from the config and returns a mongo client
func NewClient(conf *config.Config) (ClientHelper, error) {
	c, err := mongo.NewClient(options.Client().ApplyURI(conf.Url))

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

func (mc *mongoCollection) FindOne(ctx context.Context, filter interface{}) SingleResultHelper {
	singleResult := mc.coll.FindOne(ctx, filter)
	return &mongoSingleResult{sr: singleResult}
}

func (sr *mongoSingleResult) Decode(v interface{}) error {
	return sr.sr.Decode(v)
}
