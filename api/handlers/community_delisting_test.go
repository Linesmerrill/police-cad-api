package handlers

import (
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
)

func clauseBranches(t *testing.T, clause bson.M) []bson.M {
	t.Helper()
	branches, ok := clause["$or"].([]bson.M)
	if !ok {
		t.Fatalf("clause is not an $or of bson.M: %#v", clause)
	}
	return branches
}

// Verified against the server with $documents: a community is visible when it
// has never been actioned, when the field is null, or when its delisting has
// elapsed; a running delisting and both permanent shapes (until null, until
// missing) stay hidden.
//
// Mongo's type bracketing already keeps $lte from matching a null `until`, so
// the $type guard is explicitness rather than the thing holding this up. It is
// pinned because dropping it would leave the clause depending on that
// behaviour silently.
func TestNotDelistedClause_Shape(t *testing.T) {
	branches := clauseBranches(t, notDelistedClause(time.Now()))
	if len(branches) != 2 {
		t.Fatalf("expected 2 branches, got %d", len(branches))
	}

	elapsed, ok := branches[1][listingSuspensionUntilField].(bson.M)
	if !ok {
		t.Fatalf("second branch does not constrain %s: %#v", listingSuspensionUntilField, branches[1])
	}
	if elapsed["$type"] != "date" {
		t.Errorf("$type guard missing: the clause would rely on Mongo's type bracketing implicitly")
	}
	if _, ok := elapsed["$lte"]; !ok {
		t.Error("expected an $lte on the expiry")
	}

	absent, ok := branches[0][listingSuspensionField].(bson.M)
	if !ok {
		t.Fatalf("first branch does not constrain %s", listingSuspensionField)
	}
	// $eq: nil matches a missing field as well as an explicit null, which is
	// every community that has never been actioned.
	if v, ok := absent["$eq"]; !ok || v != nil {
		t.Errorf("first branch should be {$eq: nil}, got %#v", absent)
	}
}

func TestExcludeDelistedCommunities_AddsTheClause(t *testing.T) {
	got := excludeDelistedCommunities(bson.M{"community.visibility": "public"})
	if got["community.visibility"] != "public" {
		t.Error("the caller's own conditions were dropped")
	}
	and, ok := got["$and"].([]bson.M)
	if !ok || len(and) != 1 {
		t.Fatalf("$and = %#v, want one clause", got["$and"])
	}
	clauseBranches(t, and[0])
}

func TestExcludeDelistedCommunities_HandlesNilMap(t *testing.T) {
	got := excludeDelistedCommunities(nil)
	if got == nil {
		t.Fatal("returned a nil filter")
	}
	if _, ok := got["$and"]; !ok {
		t.Error("clause was not added to an empty filter")
	}
}

// The clause goes under $and precisely so it cannot clobber an $or the caller
// already built. search.go's community filters are exactly this shape.
func TestExcludeDelistedCommunities_DoesNotClobberAnExistingOr(t *testing.T) {
	caller := bson.M{"$or": []bson.M{
		{"community.name": "a"},
		{"community.code": "a"},
	}}
	got := excludeDelistedCommunities(caller)

	or, ok := got["$or"].([]bson.M)
	if !ok || len(or) != 2 {
		t.Fatalf("the caller's $or was modified: %#v", got["$or"])
	}
	if _, ok := got["$and"]; !ok {
		t.Error("clause was not added alongside the caller's $or")
	}
}

func TestExcludeDelistedCommunities_AppendsToAnExistingAnd(t *testing.T) {
	caller := bson.M{"$and": []bson.M{{"community.name": bson.M{"$regex": "x"}}}}
	got := excludeDelistedCommunities(caller)

	and, ok := got["$and"].([]bson.M)
	if !ok || len(and) != 2 {
		t.Fatalf("$and = %#v, want the caller's clause plus ours", got["$and"])
	}
	if _, ok := and[0]["community.name"]; !ok {
		t.Error("the caller's first clause was overwritten")
	}
}

func TestExcludeDelistedCommunities_AppendsToABsonAAnd(t *testing.T) {
	caller := bson.M{"$and": bson.A{bson.M{"community.name": "x"}}}
	got := excludeDelistedCommunities(caller)

	and, ok := got["$and"].(bson.A)
	if !ok || len(and) != 2 {
		t.Fatalf("$and = %#v, want two entries", got["$and"])
	}
}

func TestExcludeDelistedCommunitiesD_AppendsWhenAbsent(t *testing.T) {
	got := excludeDelistedCommunitiesD(bson.D{{Key: "community.visibility", Value: "public"}})
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Key != "community.visibility" {
		t.Error("the caller's key order was disturbed")
	}
	if got[1].Key != "$and" {
		t.Errorf("appended key = %q, want $and", got[1].Key)
	}
}

func TestExcludeDelistedCommunitiesD_MergesWithAnExistingAnd(t *testing.T) {
	got := excludeDelistedCommunitiesD(bson.D{
		{Key: "community.visibility", Value: "public"},
		{Key: "$and", Value: []bson.M{{"community.tags": "pc"}}},
	})
	// A bson.D may not carry the same key twice.
	seen := 0
	for _, e := range got {
		if e.Key == "$and" {
			seen++
			if and, ok := e.Value.([]bson.M); !ok || len(and) != 2 {
				t.Fatalf("$and = %#v, want two clauses", e.Value)
			}
		}
	}
	if seen != 1 {
		t.Errorf("$and appears %d times, want exactly 1", seen)
	}
}
