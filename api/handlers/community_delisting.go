package handlers

import (
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.uber.org/zap"
)

const (
	listingSuspensionField      = "community.listingSuspension"
	listingSuspensionUntilField = "community.listingSuspension.until"
)

// notDelistedClause matches communities that are NOT under a moderation
// delisting, computed against now rather than read from a stored flag. There is
// no relisting cron: a delisting lifts itself the moment it elapses.
//
// A community is visible when it has no delisting at all, or when its delisting
// has already elapsed. A delisting with no `until` is permanent and never
// matches.
func notDelistedClause(now time.Time) bson.M {
	return bson.M{"$or": []bson.M{
		// $eq: nil matches both a missing field and an explicit null, which is
		// every community that has never been actioned.
		{listingSuspensionField: bson.M{"$eq": nil}},
		// Mongo brackets comparisons by BSON type, so $lte against a date
		// already skips a null or missing `until` (verified against the
		// server: a permanent delisting stays hidden either way). $type makes
		// that explicit rather than load-bearing implicit behaviour, and keeps
		// the clause correct if `until` is ever written as a string.
		{listingSuspensionUntilField: bson.M{
			"$type": "date",
			"$lte":  primitive.NewDateTimeFromTime(now),
		}},
	}}
}

// excludeDelistedCommunities adds the delisting check to a discovery filter.
//
// Every surface that lists communities to someone who is not already a member
// must compose this. It is a shared helper rather than a condition pasted into
// each query because there are nearly twenty such queries today, and the next
// discovery surface someone writes would otherwise leak delisted communities.
//
// The clause is appended under $and so it can never clobber an $or the caller
// already built.
func excludeDelistedCommunities(match bson.M) bson.M {
	if match == nil {
		match = bson.M{}
	}
	clause := notDelistedClause(time.Now())

	switch existing := match["$and"].(type) {
	case nil:
		match["$and"] = []bson.M{clause}
	case []bson.M:
		match["$and"] = append(existing, clause)
	case bson.A:
		match["$and"] = append(existing, clause)
	case []interface{}:
		match["$and"] = append(existing, clause)
	default:
		// An $and we do not recognize: wrap rather than drop the check.
		match["$and"] = []interface{}{existing, clause}
	}
	return match
}

// excludeDelistedCommunitiesD is the bson.D form, for the aggregation match
// stages that need ordered keys.
func excludeDelistedCommunitiesD(match bson.D) bson.D {
	clause := notDelistedClause(time.Now())
	for i, elem := range match {
		if elem.Key != "$and" {
			continue
		}
		switch existing := elem.Value.(type) {
		case []bson.M:
			match[i].Value = append(existing, clause)
		case bson.A:
			match[i].Value = append(existing, clause)
		case []interface{}:
			match[i].Value = append(existing, clause)
		default:
			zap.S().Warnw("delisting exclusion wrapped an unrecognized $and", "type", elem.Value)
			match[i].Value = []interface{}{existing, clause}
		}
		return match
	}
	return append(match, bson.E{Key: "$and", Value: []bson.M{clause}})
}
