package handlers

import (
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/models"
)

// A duplicate is the same server advertised twice inside the posting cooldown.
// The window is what separates someone spreading one server over two
// communities to dodge the per-community cooldown from a community that simply
// re-promoted weeks later, which the cooldown allows.

var (
	dupCommunityA = primitive.NewObjectID()
	dupCommunityB = primitive.NewObjectID()
	dupOwnerOne   = primitive.NewObjectID().Hex()
	dupOwnerTwo   = primitive.NewObjectID().Hex()
)

// dupRow builds one aggregated promotion row.
func dupRow(community primitive.ObjectID, ownerID, serverName, invite string, postedAt time.Time) rpPromoAggRow {
	return rpPromoAggRow{
		CommunityID: community,
		OwnerID:     ownerID,
		Post: models.RpPromotionPost{
			ID:       primitive.NewObjectID().Hex(),
			PostedAt: primitive.NewDateTimeFromTime(postedAt),
			Data: models.RpPromotionData{
				ServerName: serverName,
				InviteURL:  invite,
			},
		},
	}
}

func removedRow(row rpPromoAggRow, at time.Time) rpPromoAggRow {
	removed := primitive.NewDateTimeFromTime(at)
	row.Post.RemovedAt = &removed
	return row
}

func noOwnerIgnored(string) bool { return false }

// dupSets groups rows with the production cooldown and reports which rows ended
// up flagged, by either key.
func dupSets(t *testing.T, rows []rpPromoAggRow) map[int]rpPromoDupGroup {
	t.Helper()
	invite, name := rpPromoDuplicateSets(rows, noOwnerIgnored, rpPromotionCooldown())
	flagged := map[int]rpPromoDupGroup{}
	for i := range rows {
		if g, ok := invite[i]; ok {
			flagged[i] = g
			continue
		}
		if g, ok := name[i]; ok {
			flagged[i] = g
		}
	}
	return flagged
}

// The report that prompted the window: DOJ HQ Network and chilling hangout
// shared an invite 43 days apart and were grouped as a possible duplicate.
func TestRpPromoDuplicateSets_SameInviteLongApartIsNotADuplicate(t *testing.T) {
	now := time.Now().UTC()
	rows := []rpPromoAggRow{
		dupRow(dupCommunityA, dupOwnerOne, "DOJ HQ Network", "https://discord.gg/MDJnW5wFz", now),
		dupRow(dupCommunityB, dupOwnerTwo, "chilling hangout", "https://discord.gg/MDJnW5wFz", now.Add(-43*24*time.Hour)),
	}
	if flagged := dupSets(t, rows); len(flagged) != 0 {
		t.Fatalf("posts 43 days apart must not be flagged, got %v", flagged)
	}
}

func TestRpPromoDuplicateSets_SameInviteWithinWindowIsADuplicate(t *testing.T) {
	now := time.Now().UTC()
	rows := []rpPromoAggRow{
		dupRow(dupCommunityA, dupOwnerOne, "Night City RP", "https://discord.gg/abc123", now),
		dupRow(dupCommunityB, dupOwnerTwo, "Totally Different Name", "discord.gg/abc123/", now.Add(-3*time.Hour)),
	}
	flagged := dupSets(t, rows)
	if len(flagged) != 2 {
		t.Fatalf("expected both posts flagged, got %v", flagged)
	}
	if flagged[0].ID != flagged[1].ID {
		t.Errorf("both posts belong in one set, got %q and %q", flagged[0].ID, flagged[1].ID)
	}
	if flagged[0].CommunityCount != 2 {
		t.Errorf("expected 2 distinct communities, got %d", flagged[0].CommunityCount)
	}
}

// Same community re-promoting inside the window is the cooldown's business, not
// a cross-community duplicate.
func TestRpPromoDuplicateSets_SameCommunityWithinWindowIsNotADuplicate(t *testing.T) {
	now := time.Now().UTC()
	rows := []rpPromoAggRow{
		dupRow(dupCommunityA, dupOwnerOne, "Night City RP", "https://discord.gg/abc123", now),
		dupRow(dupCommunityA, dupOwnerOne, "Night City RP", "https://discord.gg/abc123", now.Add(-2*time.Hour)),
	}
	if flagged := dupSets(t, rows); len(flagged) != 0 {
		t.Fatalf("one community re-promoting must not be flagged, got %v", flagged)
	}
}

// Removing one post is how staff action a set, so it must stop being flagged.
func TestRpPromoDuplicateSets_RemovedPostDropsTheSet(t *testing.T) {
	now := time.Now().UTC()
	rows := []rpPromoAggRow{
		dupRow(dupCommunityA, dupOwnerOne, "Night City RP", "https://discord.gg/abc123", now),
		removedRow(dupRow(dupCommunityB, dupOwnerTwo, "Night City RP", "https://discord.gg/abc123", now.Add(-3*time.Hour)), now),
	}
	if flagged := dupSets(t, rows); len(flagged) != 0 {
		t.Fatalf("a set with only one live post must not be flagged, got %v", flagged)
	}
}

func TestRpPromoDuplicateSets_SameNameSameOwnerWithinWindowIsADuplicate(t *testing.T) {
	now := time.Now().UTC()
	rows := []rpPromoAggRow{
		dupRow(dupCommunityA, dupOwnerOne, "San Andreas State Roleplay", "https://discord.gg/one", now),
		dupRow(dupCommunityB, dupOwnerOne, "san-andreas state  roleplay", "https://discord.gg/two", now.Add(-5*time.Hour)),
	}
	flagged := dupSets(t, rows)
	if len(flagged) != 2 {
		t.Fatalf("expected both posts flagged on name, got %v", flagged)
	}
	if flagged[0].ID != flagged[1].ID {
		t.Errorf("both posts belong in one set, got %q and %q", flagged[0].ID, flagged[1].ID)
	}
}

// Generic names collide between unrelated communities constantly, so a name
// match only means something when one person is behind both.
func TestRpPromoDuplicateSets_SameNameDifferentOwnersIsNotADuplicate(t *testing.T) {
	now := time.Now().UTC()
	rows := []rpPromoAggRow{
		dupRow(dupCommunityA, dupOwnerOne, "San Andreas State Roleplay", "https://discord.gg/one", now),
		dupRow(dupCommunityB, dupOwnerTwo, "San Andreas State Roleplay", "https://discord.gg/two", now.Add(-5*time.Hour)),
	}
	if flagged := dupSets(t, rows); len(flagged) != 0 {
		t.Fatalf("same name under different owners must not be flagged, got %v", flagged)
	}
}

// Posts chain, so a drip-feed shows staff one incident rather than several
// overlapping pairs, while a wide gap still ends the set.
func TestRpPromoDupGroups_PostsChainThroughTheWindow(t *testing.T) {
	base := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	candidates := map[string][]rpPromoDupCandidate{
		"abc123": {
			{Row: 0, CommunityID: "a", PostedAt: base},
			{Row: 1, CommunityID: "b", PostedAt: base.Add(20 * time.Hour)},
			{Row: 2, CommunityID: "a", PostedAt: base.Add(40 * time.Hour)},
			// 10 days later: a new incident, and on its own not a duplicate.
			{Row: 3, CommunityID: "b", PostedAt: base.Add(10 * 24 * time.Hour)},
		},
	}
	groups := rpPromoDupGroups(candidates, "inv:", 24*time.Hour)
	if len(groups) != 3 {
		t.Fatalf("expected the three chained posts flagged, got %v", groups)
	}
	if groups[0].ID != groups[1].ID || groups[1].ID != groups[2].ID {
		t.Errorf("chained posts belong in one set, got %v", groups)
	}
	if groups[0].CommunityCount != 2 {
		t.Errorf("expected 2 distinct communities in the chain, got %d", groups[0].CommunityCount)
	}
	if _, flagged := groups[3]; flagged {
		t.Error("the post 10 days later must not be flagged")
	}
}

// ---- the post-time Discord alert ----
//
// The alert must agree with the console: same window, same normalization.

// otherCommunity builds a community carrying one promotion history entry.
func otherCommunity(id primitive.ObjectID, name, ownerID string, post models.RpPromotionPost) models.Community {
	return models.Community{
		ID: id,
		Details: models.CommunityDetails{
			Name:        name,
			OwnerID:     ownerID,
			RpPromotion: &models.RpPromotion{History: []models.RpPromotionPost{post}},
		},
	}
}

func alertPost(serverName, invite string, postedAt time.Time) models.RpPromotionPost {
	return models.RpPromotionPost{
		ID:       primitive.NewObjectID().Hex(),
		PostedAt: primitive.NewDateTimeFromTime(postedAt),
		Data:     models.RpPromotionData{ServerName: serverName, InviteURL: invite},
	}
}

func TestRpPromoRecentDuplicateMatch_OldPostRaisesNothing(t *testing.T) {
	now := time.Now().UTC()
	others := []models.Community{
		otherCommunity(dupCommunityB, "chilling hangout", dupOwnerTwo,
			alertPost("chilling hangout", "https://discord.gg/MDJnW5wFz", now.Add(-43*24*time.Hour))),
	}
	data := models.RpPromotionData{ServerName: "DOJ HQ Network", InviteURL: "https://discord.gg/MDJnW5wFz"}

	kind, names := rpPromoRecentDuplicateMatch(dupCommunityA.Hex(), dupOwnerOne, data, others, now, rpPromotionCooldown())
	if kind != "" {
		t.Fatalf("a 43-day-old post must not alert, got %q %v", kind, names)
	}
}

// The invites differ in scheme and trailing slash, which is why both sides are
// normalized before comparison.
func TestRpPromoRecentDuplicateMatch_RecentInviteAlertsAcrossURLForms(t *testing.T) {
	now := time.Now().UTC()
	others := []models.Community{
		otherCommunity(dupCommunityB, "Second Server", dupOwnerTwo,
			alertPost("Something Else", "discord.gg/abc123/", now.Add(-2*time.Hour))),
	}
	data := models.RpPromotionData{ServerName: "Night City RP", InviteURL: "https://discord.gg/ABC123"}

	kind, names := rpPromoRecentDuplicateMatch(dupCommunityA.Hex(), dupOwnerOne, data, others, now, rpPromotionCooldown())
	if kind != "same invite" {
		t.Fatalf("expected an invite match, got %q", kind)
	}
	if len(names) != 1 || names[0] != "Second Server" {
		t.Errorf("expected the other community named, got %v", names)
	}
}

func TestRpPromoRecentDuplicateMatch_RemovedPostRaisesNothing(t *testing.T) {
	now := time.Now().UTC()
	removed := primitive.NewDateTimeFromTime(now)
	post := alertPost("Night City RP", "https://discord.gg/abc123", now.Add(-2*time.Hour))
	post.RemovedAt = &removed
	others := []models.Community{otherCommunity(dupCommunityB, "Second Server", dupOwnerTwo, post)}
	data := models.RpPromotionData{ServerName: "Night City RP", InviteURL: "https://discord.gg/abc123"}

	if kind, _ := rpPromoRecentDuplicateMatch(dupCommunityA.Hex(), dupOwnerOne, data, others, now, rpPromotionCooldown()); kind != "" {
		t.Fatalf("a removed post must not alert, got %q", kind)
	}
}

func TestRpPromoRecentDuplicateMatch_SameNameNeedsTheSameOwner(t *testing.T) {
	now := time.Now().UTC()
	data := models.RpPromotionData{ServerName: "San Andreas State Roleplay", InviteURL: "https://discord.gg/one"}
	post := alertPost("san andreas state roleplay", "https://discord.gg/two", now.Add(-4*time.Hour))

	sameOwner := []models.Community{otherCommunity(dupCommunityB, "Their Other Server", dupOwnerOne, post)}
	if kind, _ := rpPromoRecentDuplicateMatch(dupCommunityA.Hex(), dupOwnerOne, data, sameOwner, now, rpPromotionCooldown()); kind != "same name" {
		t.Fatalf("expected a name match under the same owner, got %q", kind)
	}

	otherOwner := []models.Community{otherCommunity(dupCommunityB, "Unrelated Server", dupOwnerTwo, post)}
	if kind, _ := rpPromoRecentDuplicateMatch(dupCommunityA.Hex(), dupOwnerOne, data, otherOwner, now, rpPromotionCooldown()); kind != "" {
		t.Fatalf("same name under a different owner must not alert, got %q", kind)
	}
}

// The community being posted to is never its own duplicate, even though the
// query that feeds this already excludes it.
func TestRpPromoRecentDuplicateMatch_IgnoresTheCommunityBeingPostedTo(t *testing.T) {
	now := time.Now().UTC()
	others := []models.Community{
		otherCommunity(dupCommunityA, "Night City RP", dupOwnerOne,
			alertPost("Night City RP", "https://discord.gg/abc123", now.Add(-1*time.Hour))),
	}
	data := models.RpPromotionData{ServerName: "Night City RP", InviteURL: "https://discord.gg/abc123"}

	if kind, _ := rpPromoRecentDuplicateMatch(dupCommunityA.Hex(), dupOwnerOne, data, others, now, rpPromotionCooldown()); kind != "" {
		t.Fatalf("a community's own earlier post must not alert, got %q", kind)
	}
}
