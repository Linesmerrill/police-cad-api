package models

import "go.mongodb.org/mongo-driver/bson/primitive"

// FormCounter is the per-community-per-template-per-year sequence used to
// generate report numbers (e.g. RR-2026-000123).
//
// One row per (communityID, slug, year). Mongo $inc with upsert keeps it
// atomic.
type FormCounter struct {
	ID          primitive.ObjectID `json:"_id" bson:"_id"`
	CommunityID string             `json:"communityID" bson:"communityID"`
	Slug        string             `json:"slug" bson:"slug"`
	Year        int                `json:"year" bson:"year"`
	Seq         int64              `json:"seq" bson:"seq"`
}
