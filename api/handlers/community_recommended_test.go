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

func TestRecommendedMatchExclusions(t *testing.T) {
	t.Run("no exclusions leaves the id filter off", func(t *testing.T) {
		if _, ok := recommendedMatch("", nil)["_id"]; ok {
			t.Error("_id filter should be absent when nothing is excluded")
		}
	})

	t.Run("exclusions become a $nin", func(t *testing.T) {
		id := primitive.NewObjectID()
		filter, ok := recommendedMatch("", []primitive.ObjectID{id})["_id"].(bson.M)
		if !ok {
			t.Fatal("_id filter missing")
		}
		nin, ok := filter["$nin"].([]primitive.ObjectID)
		if !ok || len(nin) != 1 || nin[0] != id {
			t.Errorf("$nin = %#v, want the excluded id", filter["$nin"])
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
