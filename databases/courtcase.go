package databases

// go generate: mockery --name CourtCaseDatabase

import (
	"context"
	"fmt"
	"time"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const courtCaseName = "courtcases"
const caseCountersName = "casecounters"

// CourtCaseDatabase contains the methods to use with the court case database
type CourtCaseDatabase interface {
	FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.CourtCase, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.CourtCase, error)
	CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error)
	InsertOne(ctx context.Context, document interface{}, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error)
	UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error
	DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error
	NextCaseNumber(ctx context.Context, communityID string) (string, error)
	EnsureIndexes(ctx context.Context) error
}

type courtCaseDatabase struct {
	db DatabaseHelper
}

// NewCourtCaseDatabase initializes a new instance of court case database with the provided db connection
func NewCourtCaseDatabase(db DatabaseHelper) CourtCaseDatabase {
	return &courtCaseDatabase{
		db: db,
	}
}

func (c *courtCaseDatabase) FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.CourtCase, error) {
	courtCase := &models.CourtCase{}
	err := c.db.Collection(courtCaseName).FindOne(ctx, filter).Decode(&courtCase)
	if err != nil {
		return nil, err
	}
	return courtCase, nil
}

func (c *courtCaseDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.CourtCase, error) {
	var courtCases []models.CourtCase
	curr, err := c.db.Collection(courtCaseName).Find(ctx, filter, opts...)
	if err != nil {
		return nil, err
	}
	defer curr.Close(ctx)
	err = curr.All(ctx, &courtCases)
	if err != nil {
		return nil, err
	}
	return courtCases, nil
}

func (c *courtCaseDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	return c.db.Collection(courtCaseName).CountDocuments(ctx, filter, opts...)
}

func (c *courtCaseDatabase) InsertOne(ctx context.Context, document interface{}, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
	res, err := c.db.Collection(courtCaseName).InsertOne(ctx, document, opts...)
	return res, err
}

func (c *courtCaseDatabase) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error {
	_, err := c.db.Collection(courtCaseName).UpdateOne(ctx, filter, update, opts...)
	return err
}

func (c *courtCaseDatabase) DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error {
	return c.db.Collection(courtCaseName).DeleteOne(ctx, filter, opts...)
}

// NextCaseNumber atomically increments the per-community per-year sequence
// and returns the formatted case number (CC-YYYY-NNNNNN).
func (c *courtCaseDatabase) NextCaseNumber(ctx context.Context, communityID string) (string, error) {
	if communityID == "" {
		return "", fmt.Errorf("communityID is required")
	}
	year := time.Now().UTC().Year()
	counterID := fmt.Sprintf("%s:%d", communityID, year)

	coll := c.db.Collection(caseCountersName)
	resp := coll.FindOneAndUpdate(
		ctx,
		bson.M{"_id": counterID},
		bson.M{"$inc": bson.M{"seq": 1}},
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After),
	)

	var counter struct {
		Seq int64 `bson:"seq"`
	}
	if err := resp.Decode(&counter); err != nil {
		return "", fmt.Errorf("failed to increment case counter: %w", err)
	}
	return fmt.Sprintf("CC-%d-%06d", year, counter.Seq), nil
}

// EnsureIndexes creates the indexes required by the court-cases feature.
// Safe to call repeatedly: Mongo treats identical IndexModel as a no-op.
func (c *courtCaseDatabase) EnsureIndexes(ctx context.Context) error {
	coll, ok := c.db.Collection(courtCaseName).(interface {
		Indexes() mongo.IndexView
	})
	if !ok {
		return fmt.Errorf("collection helper does not expose Indexes()")
	}
	_, err := coll.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys: bson.D{
				{Key: "courtCase.communityID", Value: 1},
				{Key: "courtCase.caseNumber", Value: 1},
			},
			Options: options.Index().
				SetUnique(true).
				SetName("uniq_courtCase_communityID_caseNumber").
				SetPartialFilterExpression(bson.M{
					"courtCase.caseNumber": bson.M{"$exists": true, "$gt": ""},
				}),
		},
	})
	return err
}
