package handlers

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/databases"
)

// decodeTolerantly drains a cursor into a slice, skipping any document that
// fails to decode instead of failing the whole page.
//
// cursor.All is all-or-nothing. One stored document whose shape no longer
// matches the model — an ObjectID sitting where Notification.ID or
// UserCommunity.ID expect a string, a date that will not parse — fails the
// entire batch. On a search endpoint that means a single bad record takes
// search down for every user whose results happen to include it, and the error
// they see says nothing about which record or that it is even a data problem.
//
// Skipping the bad one keeps search working for everybody else and logs the
// offending _id, which is the thing you actually need to repair it. The counter
// is logged separately so a sudden rise is visible without reading every line.
//
// Deliberately tolerant only on READ paths that render a list. Anywhere a
// missing record would change a decision — permissions, balances, membership —
// a hard failure is correct and All is the right call.
func decodeTolerantly[T any](ctx context.Context, cursor databases.MongoCursor, where string) []T {
	results := make([]T, 0)
	skipped := 0

	for cursor.Next(ctx) {
		var item T
		if err := cursor.DecodeCurrent(&item); err != nil {
			skipped++
			// The cursor stays on the same document until Next, so this second
			// decode is safe and gets us the _id for the log.
			var raw bson.M
			id := "unknown"
			if rawErr := cursor.DecodeCurrent(&raw); rawErr == nil {
				if v, ok := raw["_id"]; ok {
					id = toIDString(v)
				}
			}
			zap.S().Warnw("skipped undecodable document",
				"where", where, "id", id, "error", err)
			continue
		}
		results = append(results, item)
	}

	if skipped > 0 {
		zap.S().Warnw("search skipped undecodable documents",
			"where", where, "skipped", skipped, "returned", len(results))
	}
	return results
}

// toIDString renders a Mongo _id for a log line without assuming its type.
func toIDString(v interface{}) string {
	switch id := v.(type) {
	case string:
		return id
	case interface{ Hex() string }:
		return id.Hex()
	default:
		return "unprintable"
	}
}
