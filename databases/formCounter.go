package databases

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const formCounterName = "formCounters"

// FormCounterDatabase atomically increments and returns a per-(community, slug, year) sequence
// used to generate unique form submission report numbers.
type FormCounterDatabase interface {
	NextSeq(ctx context.Context, communityID, slug string, year int) (int64, error)
	// CatchUp bumps the (community, slug, year) counter to at least
	// minSeq using $max — used to recover from cross-slug collisions
	// (the unique index on submissions is community-wide while the
	// counter is per-slug, so a brand-new template starts at seq=1
	// and collides with the existing template's first reports). Idempotent.
	CatchUp(ctx context.Context, communityID, slug string, year int, minSeq int64) error
}

type formCounterDatabase struct {
	db DatabaseHelper
}

// NewFormCounterDatabase initializes a new instance of the form counter database
func NewFormCounterDatabase(db DatabaseHelper) FormCounterDatabase {
	return &formCounterDatabase{db: db}
}

// NextSeq atomically increments the (communityID, slug, year) counter and returns the new value.
// Uses FindOneAndUpdate with $inc + upsert + ReturnDocument=After.
func (c *formCounterDatabase) NextSeq(ctx context.Context, communityID, slug string, year int) (int64, error) {
	filter := bson.M{
		"communityID": communityID,
		"slug":        slug,
		"year":        year,
	}
	update := bson.M{
		"$inc": bson.M{"seq": int64(1)},
		"$setOnInsert": bson.M{
			"communityID": communityID,
			"slug":        slug,
			"year":        year,
		},
	}
	after := options.After
	opts := options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(after)

	res := c.db.Collection(formCounterName).FindOneAndUpdate(ctx, filter, update, opts)
	if err := res.Err(); err != nil {
		return 0, err
	}

	var doc struct {
		Seq int64 `bson:"seq"`
	}
	if err := res.Decode(&doc); err != nil {
		return 0, err
	}
	return doc.Seq, nil
}

// CatchUp ensures the (communityID, slug, year) counter is at or
// above minSeq. Uses $max so concurrent writes never roll the
// counter backward. The next NextSeq call returns at least minSeq+1.
func (c *formCounterDatabase) CatchUp(ctx context.Context, communityID, slug string, year int, minSeq int64) error {
	filter := bson.M{
		"communityID": communityID,
		"slug":        slug,
		"year":        year,
	}
	update := bson.M{
		"$max": bson.M{"seq": minSeq},
		"$setOnInsert": bson.M{
			"communityID": communityID,
			"slug":        slug,
			"year":        year,
		},
	}
	opts := options.Update().SetUpsert(true)
	_, err := c.db.Collection(formCounterName).UpdateOne(ctx, filter, update, opts)
	return err
}
