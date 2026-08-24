package handlers

import (
	"bytes"
	"encoding/json"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Production survey (Aug 2026): 5,824 public communities, of which 3,533 have
// exactly one member — the owner. Only 48 carry an active boost of any tier
// (basic 28, elite 11, premium 5, standard 4), so the recommendable pool is
// overwhelmingly free communities ranked by size.

func TestRecommendedMatchFiltersOutOneMemberCommunities(t *testing.T) {
	match := recommendedMatch("", nil)

	if match["community.visibility"] != "public" {
		t.Errorf("visibility = %v, want public", match["community.visibility"])
	}

	members, ok := match["community.membersCount"].(bson.M)
	if !ok {
		t.Fatalf("membersCount filter missing: %#v", match["community.membersCount"])
	}
	if members["$gte"] != RecommendedCommunitiesMinMembers {
		t.Errorf("$gte = %v, want %d", members["$gte"], RecommendedCommunitiesMinMembers)
	}
	if RecommendedCommunitiesMinMembers < 2 {
		t.Error("a floor below 2 would recommend communities holding only their owner")
	}
}

func TestRecommendedMatchTagHandling(t *testing.T) {
	t.Run("a platform tag is applied", func(t *testing.T) {
		if got := recommendedMatch("Xbox", nil)["community.tags"]; got != "Xbox" {
			t.Errorf("tags = %v, want Xbox", got)
		}
	})

	// "all" is the convention the existing tag endpoint and both clients' filter
	// strips already use for "no filter".
	t.Run("all means no tag filter", func(t *testing.T) {
		for _, tag := range []string{"all", "All", "ALL"} {
			if _, ok := recommendedMatch(tag, nil)["community.tags"]; ok {
				t.Errorf("tag %q should not filter", tag)
			}
		}
	})

	t.Run("empty means no tag filter", func(t *testing.T) {
		if _, ok := recommendedMatch("", nil)["community.tags"]; ok {
			t.Error("empty tag should not filter")
		}
	})
}

func excludedIDsFrom(t *testing.T, match bson.M) []primitive.ObjectID {
	t.Helper()
	filter, ok := match["_id"].(bson.M)
	if !ok {
		t.Fatalf("_id filter missing: %#v", match["_id"])
	}
	nin, ok := filter["$nin"].([]primitive.ObjectID)
	if !ok {
		t.Fatalf("$nin is not an id slice: %#v", filter["$nin"])
	}
	return nin
}

func TestRecommendedMatchExclusions(t *testing.T) {
	t.Run("caller exclusions are applied", func(t *testing.T) {
		id := primitive.NewObjectID()
		nin := excludedIDsFrom(t, recommendedMatch("", []primitive.ObjectID{id}))
		found := false
		for _, excluded := range nin {
			if excluded == id {
				found = true
			}
		}
		if !found {
			t.Errorf("$nin = %#v, want it to contain the caller's excluded id", nin)
		}
	})

	t.Run("caller exclusions do not displace the demo ones", func(t *testing.T) {
		id := primitive.NewObjectID()
		nin := excludedIDsFrom(t, recommendedMatch("", []primitive.ObjectID{id}))
		if len(nin) != 1+len(demoCommunityObjectIDs) {
			t.Errorf("got %d exclusions, want the caller's plus %d demo communities",
				len(nin), len(demoCommunityObjectIDs))
		}
	})
}

// Our own communities stay public as working examples, but they are not real
// servers to play in. Both are boosted and sizeable, so before this they led the
// PC results for every new player.
func TestRecommendedMatchAlwaysExcludesDemoCommunities(t *testing.T) {
	if len(demoCommunityObjectIDs) != len(demoCommunityIDs) {
		t.Fatalf("only %d of %d demo ids parsed; the rest would still be recommended",
			len(demoCommunityObjectIDs), len(demoCommunityIDs))
	}
	if len(demoCommunityObjectIDs) == 0 {
		t.Fatal("expected at least one demo community to be excluded")
	}

	// Excluded on every entry point into the match, with or without a caller
	// exclusion list and with or without a tag filter.
	for _, tc := range []struct {
		name     string
		tag      string
		excluded []primitive.ObjectID
	}{
		{name: "no tag, no caller exclusions"},
		{name: "platform tag", tag: "PC"},
		{name: "all tag", tag: "all"},
		{name: "with caller exclusions", excluded: []primitive.ObjectID{primitive.NewObjectID()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nin := excludedIDsFrom(t, recommendedMatch(tc.tag, tc.excluded))
			for _, demo := range demoCommunityObjectIDs {
				found := false
				for _, excluded := range nin {
					if excluded == demo {
						found = true
					}
				}
				if !found {
					t.Errorf("demo community %s is not excluded", demo.Hex())
				}
			}
		})
	}
}

// A typo here must not take the API down, and must not silently pass either.
func TestDemoCommunityIDsAreValidObjectIDs(t *testing.T) {
	for _, hex := range demoCommunityIDs {
		if _, err := primitive.ObjectIDFromHex(hex); err != nil {
			t.Errorf("demo community id %q is not a valid object id: %v", hex, err)
		}
	}
}

func TestExcludeDemoCommunities(t *testing.T) {
	t.Run("adds the exclusion to a filter with no _id condition", func(t *testing.T) {
		got := excludeDemoCommunities(bson.M{"community.visibility": "public"})
		if len(excludedIDsFrom(t, got)) != len(demoCommunityObjectIDs) {
			t.Errorf("got %#v", got["_id"])
		}
		if got["community.visibility"] != "public" {
			t.Error("the original conditions must survive")
		}
	})

	t.Run("merges with an existing $nin instead of replacing it", func(t *testing.T) {
		existing := primitive.NewObjectID()
		got := excludeDemoCommunities(bson.M{"_id": bson.M{"$nin": []primitive.ObjectID{existing}}})
		nin := excludedIDsFrom(t, got)
		if len(nin) != 1+len(demoCommunityObjectIDs) {
			t.Fatalf("got %d exclusions, want %d", len(nin), 1+len(demoCommunityObjectIDs))
		}
		if nin[0] != existing {
			t.Error("the caller's exclusion was dropped")
		}
	})
}

func TestExcludeDemoCommunitiesD(t *testing.T) {
	t.Run("appends the exclusion", func(t *testing.T) {
		got := excludeDemoCommunitiesD(bson.D{{Key: "community.visibility", Value: "public"}})
		if len(got) != 2 || got[1].Key != "_id" {
			t.Fatalf("got %#v", got)
		}
		if got[0].Key != "community.visibility" {
			t.Error("the original conditions must survive, in order")
		}
	})

	// A bson.D carrying two _id keys silently drops one, so appending blindly
	// would be worse than not excluding at all.
	t.Run("leaves an existing _id condition alone", func(t *testing.T) {
		id := primitive.NewObjectID()
		input := bson.D{{Key: "_id", Value: id}}
		got := excludeDemoCommunitiesD(input)
		if len(got) != 1 {
			t.Errorf("got %#v, want the input untouched rather than a duplicate _id key", got)
		}
	})
}

// The tier ladder is basic < standard < premium < elite, and a community's
// effective tier only counts while the subscription is active. The existing
// prioritized-communities pipeline switches on plan alone, so a lapsed elite
// keeps top billing forever; this endpoint must not inherit that.
func TestSubscriptionRankRequiresAnActiveSubscription(t *testing.T) {
	raw, err := json.Marshal(subscriptionRankBranches())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded struct {
		Switch struct {
			Branches []struct {
				Case struct {
					And []map[string]interface{} `json:"$and"`
				} `json:"case"`
				Then int `json:"then"`
			} `json:"branches"`
			Default int `json:"default"`
		} `json:"$switch"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(decoded.Switch.Branches) != 4 {
		t.Fatalf("got %d branches, want one per paid tier", len(decoded.Switch.Branches))
	}
	if decoded.Switch.Default != 0 {
		t.Errorf("default rank = %d, want 0 (free)", decoded.Switch.Default)
	}

	wantRanks := []int{4, 3, 2, 1} // elite, premium, standard, basic
	for i, branch := range decoded.Switch.Branches {
		if branch.Then != wantRanks[i] {
			t.Errorf("branch %d rank = %d, want %d", i, branch.Then, wantRanks[i])
		}
		if len(branch.Case.And) != 2 {
			t.Errorf("branch %d has %d conditions, want plan AND active", i, len(branch.Case.And))
		}
	}

	if !bytes.Contains(raw, []byte("subscription.active")) {
		t.Error("ranking must require subscription.active; a lapsed boost is not a boost")
	}
}

// A boost surfaces a community; it does not make a 2-member server a better
// recommendation than a 1,645-member one. Active-boosted communities run from 1
// to 2,472 members and 12 of 51 have fewer than 10, so without this floor the
// top PC result for a brand-new player was a community with two people in it.
func TestSubscriptionRankExprGatesBoostOnLiveness(t *testing.T) {
	raw, err := json.Marshal(subscriptionRankExpr())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded struct {
		Cond []json.RawMessage `json:"$cond"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(decoded.Cond) != 3 {
		t.Fatalf("got %d $cond operands, want condition/then/else", len(decoded.Cond))
	}

	// Below the floor a boosted community ranks as free rather than being hidden.
	var fallback int
	if err := json.Unmarshal(decoded.Cond[2], &fallback); err != nil {
		t.Fatalf("else branch is not a rank: %v", err)
	}
	if fallback != 0 {
		t.Errorf("below-floor rank = %d, want 0 so it sorts with the free tier", fallback)
	}

	if !bytes.Contains(decoded.Cond[0], []byte("membersCount")) {
		t.Error("the gate must test membersCount")
	}
	if BoostedPriorityMinMembers <= RecommendedCommunitiesMinMembers {
		t.Errorf("boost floor (%d) must exceed the listing floor (%d), or it gates nothing",
			BoostedPriorityMinMembers, RecommendedCommunitiesMinMembers)
	}
}

// The count and the page must describe the same set, or totalCount silently
// labels a different result than the one being paged through.
func TestRecommendedMatchIsSharedByCountAndPipeline(t *testing.T) {
	id := primitive.NewObjectID()
	first := recommendedMatch("PC", []primitive.ObjectID{id})
	second := recommendedMatch("PC", []primitive.ObjectID{id})

	firstJSON, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	secondJSON, err := json.Marshal(second)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(firstJSON) != string(secondJSON) {
		t.Errorf("match is not deterministic:\n%s\n%s", firstJSON, secondJSON)
	}
}
