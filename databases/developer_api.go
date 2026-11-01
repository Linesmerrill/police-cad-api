package databases

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/models"
)

// Collections behind the Developer API. See docs/DEVELOPER_API.md.
const (
	developerAccountCollectionName   = "developer_accounts"
	developerAPIKeyCollectionName    = "developer_api_keys"
	developerGrantCollectionName     = "developer_community_grants"
	developerUsageCollectionName     = "developer_api_usage"
	developerRateCollectionName      = "developer_api_rate"
	playerIdentityCollectionName     = "player_identities"
	developerClaimCodeCollectionName = "developer_claim_codes"
	authIdentityCollectionName       = "auth_identities"
)

// DeveloperAccountDatabase is one LPC user's developer registration.
type DeveloperAccountDatabase interface {
	FindOne(ctx context.Context, filter interface{}) (*models.DeveloperAccount, error)
	InsertOne(ctx context.Context, account models.DeveloperAccount) (InsertOneResultHelper, error)
	UpdateOne(ctx context.Context, filter, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error)
}

type developerAccountDatabase struct{ db DatabaseHelper }

// NewDeveloperAccountDatabase creates a developer account database wrapper.
func NewDeveloperAccountDatabase(db DatabaseHelper) DeveloperAccountDatabase {
	return &developerAccountDatabase{db: db}
}

func (d *developerAccountDatabase) FindOne(ctx context.Context, filter interface{}) (*models.DeveloperAccount, error) {
	account := &models.DeveloperAccount{}
	if err := d.db.Collection(developerAccountCollectionName).FindOne(ctx, filter).Decode(account); err != nil {
		return nil, err
	}
	return account, nil
}

func (d *developerAccountDatabase) InsertOne(ctx context.Context, account models.DeveloperAccount) (InsertOneResultHelper, error) {
	return d.db.Collection(developerAccountCollectionName).InsertOne(ctx, account)
}

func (d *developerAccountDatabase) UpdateOne(ctx context.Context, filter, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	return d.db.Collection(developerAccountCollectionName).UpdateOne(ctx, filter, update, opts...)
}

// DeveloperAPIKeyDatabase stores issued keys. Lookup on the request path is by
// keyHash, which is unique and indexed.
type DeveloperAPIKeyDatabase interface {
	FindOne(ctx context.Context, filter interface{}) (*models.DeveloperAPIKey, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (*MongoCursor, error)
	InsertOne(ctx context.Context, key models.DeveloperAPIKey) (InsertOneResultHelper, error)
	UpdateOne(ctx context.Context, filter, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error)
	CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error)
}

type developerAPIKeyDatabase struct{ db DatabaseHelper }

// NewDeveloperAPIKeyDatabase creates a developer API key database wrapper.
func NewDeveloperAPIKeyDatabase(db DatabaseHelper) DeveloperAPIKeyDatabase {
	return &developerAPIKeyDatabase{db: db}
}

func (d *developerAPIKeyDatabase) FindOne(ctx context.Context, filter interface{}) (*models.DeveloperAPIKey, error) {
	key := &models.DeveloperAPIKey{}
	if err := d.db.Collection(developerAPIKeyCollectionName).FindOne(ctx, filter).Decode(key); err != nil {
		return nil, err
	}
	return key, nil
}

func (d *developerAPIKeyDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (*MongoCursor, error) {
	return d.db.Collection(developerAPIKeyCollectionName).Find(ctx, filter, opts...)
}

func (d *developerAPIKeyDatabase) InsertOne(ctx context.Context, key models.DeveloperAPIKey) (InsertOneResultHelper, error) {
	return d.db.Collection(developerAPIKeyCollectionName).InsertOne(ctx, key)
}

func (d *developerAPIKeyDatabase) UpdateOne(ctx context.Context, filter, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	return d.db.Collection(developerAPIKeyCollectionName).UpdateOne(ctx, filter, update, opts...)
}

func (d *developerAPIKeyDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	return d.db.Collection(developerAPIKeyCollectionName).CountDocuments(ctx, filter, opts...)
}

// DeveloperGrantDatabase ties keys to the communities they may act in.
type DeveloperGrantDatabase interface {
	FindOne(ctx context.Context, filter interface{}) (*models.DeveloperCommunityGrant, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (*MongoCursor, error)
	InsertOne(ctx context.Context, grant models.DeveloperCommunityGrant) (InsertOneResultHelper, error)
	UpdateOne(ctx context.Context, filter, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error)
	UpdateMany(ctx context.Context, filter, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error)
	CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error)
}

type developerGrantDatabase struct{ db DatabaseHelper }

// NewDeveloperGrantDatabase creates a developer community grant wrapper.
func NewDeveloperGrantDatabase(db DatabaseHelper) DeveloperGrantDatabase {
	return &developerGrantDatabase{db: db}
}

func (d *developerGrantDatabase) FindOne(ctx context.Context, filter interface{}) (*models.DeveloperCommunityGrant, error) {
	grant := &models.DeveloperCommunityGrant{}
	if err := d.db.Collection(developerGrantCollectionName).FindOne(ctx, filter).Decode(grant); err != nil {
		return nil, err
	}
	return grant, nil
}

func (d *developerGrantDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (*MongoCursor, error) {
	return d.db.Collection(developerGrantCollectionName).Find(ctx, filter, opts...)
}

func (d *developerGrantDatabase) InsertOne(ctx context.Context, grant models.DeveloperCommunityGrant) (InsertOneResultHelper, error) {
	return d.db.Collection(developerGrantCollectionName).InsertOne(ctx, grant)
}

func (d *developerGrantDatabase) UpdateOne(ctx context.Context, filter, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	return d.db.Collection(developerGrantCollectionName).UpdateOne(ctx, filter, update, opts...)
}

func (d *developerGrantDatabase) UpdateMany(ctx context.Context, filter, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	return d.db.Collection(developerGrantCollectionName).UpdateMany(ctx, filter, update, opts...)
}

func (d *developerGrantDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	return d.db.Collection(developerGrantCollectionName).CountDocuments(ctx, filter, opts...)
}

// DeveloperUsageDatabase holds the per-key-per-day counters that serve both the
// dashboard and the daily cap.
//
// FindOneAndIncrement is the hot path: one _id-targeted upsert that returns the
// document after the increment, so a single round trip both records the request
// and tells the caller whether it is over the limit. Doing it in two steps
// would let two dynos read the same count and both allow the request.
type DeveloperUsageDatabase interface {
	FindOneAndIncrement(ctx context.Context, id string, inc bson.M, set bson.M) (*models.DeveloperAPIUsage, error)
	FindOne(ctx context.Context, filter interface{}) (*models.DeveloperAPIUsage, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (*MongoCursor, error)
	UpdateOne(ctx context.Context, filter, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error)
}

type developerUsageDatabase struct{ db DatabaseHelper }

// NewDeveloperUsageDatabase creates a developer usage database wrapper.
func NewDeveloperUsageDatabase(db DatabaseHelper) DeveloperUsageDatabase {
	return &developerUsageDatabase{db: db}
}

func (d *developerUsageDatabase) FindOneAndIncrement(ctx context.Context, id string, inc bson.M, set bson.M) (*models.DeveloperAPIUsage, error) {
	update := bson.M{"$inc": inc}
	if len(set) > 0 {
		update["$set"] = set
	}
	opts := options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)
	usage := &models.DeveloperAPIUsage{}
	res := d.db.Collection(developerUsageCollectionName).FindOneAndUpdate(ctx, bson.M{"_id": id}, update, opts)
	if err := res.Decode(usage); err != nil {
		return nil, err
	}
	return usage, nil
}

func (d *developerUsageDatabase) FindOne(ctx context.Context, filter interface{}) (*models.DeveloperAPIUsage, error) {
	usage := &models.DeveloperAPIUsage{}
	if err := d.db.Collection(developerUsageCollectionName).FindOne(ctx, filter).Decode(usage); err != nil {
		return nil, err
	}
	return usage, nil
}

func (d *developerUsageDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (*MongoCursor, error) {
	return d.db.Collection(developerUsageCollectionName).Find(ctx, filter, opts...)
}

func (d *developerUsageDatabase) UpdateOne(ctx context.Context, filter, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	return d.db.Collection(developerUsageCollectionName).UpdateOne(ctx, filter, update, opts...)
}

// DeveloperRateDatabase holds the per-minute burst counters. One tiny document
// per key per active minute, swept by a TTL index.
type DeveloperRateDatabase interface {
	IncrementAndCount(ctx context.Context, id string, expiresAt interface{}) (int, error)
}

type developerRateDatabase struct{ db DatabaseHelper }

// NewDeveloperRateDatabase creates a developer rate counter wrapper.
func NewDeveloperRateDatabase(db DatabaseHelper) DeveloperRateDatabase {
	return &developerRateDatabase{db: db}
}

// IncrementAndCount bumps the minute's counter and returns the new value.
//
// One round trip, on purpose. Reading then writing would let both dynos see the
// same count and both decide the request was inside the limit, which is exactly
// the failure an in-memory limiter has and this store exists to avoid.
func (d *developerRateDatabase) IncrementAndCount(ctx context.Context, id string, expiresAt interface{}) (int, error) {
	update := bson.M{
		"$inc":         bson.M{"n": 1},
		"$setOnInsert": bson.M{"expiresAt": expiresAt},
	}
	opts := options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)
	var doc struct {
		N int `bson:"n"`
	}
	res := d.db.Collection(developerRateCollectionName).FindOneAndUpdate(ctx, bson.M{"_id": id}, update, opts)
	if err := res.Decode(&doc); err != nil {
		return 0, err
	}
	return doc.N, nil
}

// PlayerIdentityDatabase maps game identifiers to LPC users.
type PlayerIdentityDatabase interface {
	FindOne(ctx context.Context, filter interface{}) (*models.PlayerIdentity, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (*MongoCursor, error)
	InsertOne(ctx context.Context, identity models.PlayerIdentity) (InsertOneResultHelper, error)
	UpdateOne(ctx context.Context, filter, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error)
	UpdateMany(ctx context.Context, filter, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error)
	CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error)
}

type playerIdentityDatabase struct{ db DatabaseHelper }

// NewPlayerIdentityDatabase creates a player identity database wrapper.
func NewPlayerIdentityDatabase(db DatabaseHelper) PlayerIdentityDatabase {
	return &playerIdentityDatabase{db: db}
}

func (p *playerIdentityDatabase) FindOne(ctx context.Context, filter interface{}) (*models.PlayerIdentity, error) {
	identity := &models.PlayerIdentity{}
	if err := p.db.Collection(playerIdentityCollectionName).FindOne(ctx, filter).Decode(identity); err != nil {
		return nil, err
	}
	return identity, nil
}

func (p *playerIdentityDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (*MongoCursor, error) {
	return p.db.Collection(playerIdentityCollectionName).Find(ctx, filter, opts...)
}

func (p *playerIdentityDatabase) InsertOne(ctx context.Context, identity models.PlayerIdentity) (InsertOneResultHelper, error) {
	return p.db.Collection(playerIdentityCollectionName).InsertOne(ctx, identity)
}

func (p *playerIdentityDatabase) UpdateOne(ctx context.Context, filter, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	return p.db.Collection(playerIdentityCollectionName).UpdateOne(ctx, filter, update, opts...)
}

func (p *playerIdentityDatabase) UpdateMany(ctx context.Context, filter, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	return p.db.Collection(playerIdentityCollectionName).UpdateMany(ctx, filter, update, opts...)
}

func (p *playerIdentityDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	return p.db.Collection(playerIdentityCollectionName).CountDocuments(ctx, filter, opts...)
}

// DeveloperClaimCodeDatabase holds the one-time codes a player redeems to take
// ownership of an account a mod made for them.
type DeveloperClaimCodeDatabase interface {
	FindOne(ctx context.Context, filter interface{}) (*models.DeveloperClaimCode, error)
	InsertOne(ctx context.Context, code models.DeveloperClaimCode) (InsertOneResultHelper, error)
	UpdateOne(ctx context.Context, filter, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error)
	DeleteMany(ctx context.Context, filter interface{}) (int64, error)
}

type developerClaimCodeDatabase struct{ db DatabaseHelper }

// NewDeveloperClaimCodeDatabase creates a claim code database wrapper.
func NewDeveloperClaimCodeDatabase(db DatabaseHelper) DeveloperClaimCodeDatabase {
	return &developerClaimCodeDatabase{db: db}
}

func (d *developerClaimCodeDatabase) FindOne(ctx context.Context, filter interface{}) (*models.DeveloperClaimCode, error) {
	code := &models.DeveloperClaimCode{}
	if err := d.db.Collection(developerClaimCodeCollectionName).FindOne(ctx, filter).Decode(code); err != nil {
		return nil, err
	}
	return code, nil
}

func (d *developerClaimCodeDatabase) InsertOne(ctx context.Context, code models.DeveloperClaimCode) (InsertOneResultHelper, error) {
	return d.db.Collection(developerClaimCodeCollectionName).InsertOne(ctx, code)
}

func (d *developerClaimCodeDatabase) UpdateOne(ctx context.Context, filter, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	return d.db.Collection(developerClaimCodeCollectionName).UpdateOne(ctx, filter, update, opts...)
}

func (d *developerClaimCodeDatabase) DeleteMany(ctx context.Context, filter interface{}) (int64, error) {
	return d.db.Collection(developerClaimCodeCollectionName).DeleteMany(ctx, filter)
}
