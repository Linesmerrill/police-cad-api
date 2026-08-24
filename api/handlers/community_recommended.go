package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.uber.org/zap"
)

// communitiesAlreadyJoined returns every community the user already holds an
// entry for, in any status. Pending counts: a recommendation to join somewhere
// you are already waiting on reads as the site having forgotten you asked.
//
// Any failure returns nil, which means "exclude nothing" — a recommendation
// list that still works is better than one that 500s because a user lookup
// failed.
func communitiesAlreadyJoined(ctx context.Context, udb databases.UserDatabase, userID string) []primitive.ObjectID {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil
	}
	uID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		return nil
	}
	var user models.User
	if err := udb.FindOne(ctx, bson.M{"_id": uID}).Decode(&user); err != nil {
		return nil
	}
	ids := make([]primitive.ObjectID, 0, len(user.Details.Communities))
	for _, membership := range user.Details.Communities {
		if oid, err := primitive.ObjectIDFromHex(membership.CommunityID); err == nil {
			ids = append(ids, oid)
		}
	}
	return ids
}

// RecommendedCommunitiesMinMembers is the floor for a community to be worth
// recommending to someone who has never joined one.
//
// 3,533 of the 5,824 public communities have exactly one member: the owner, who
// created it and never came back. Steering a brand-new player into an empty
// server is worse than showing them nothing, because they cannot tell the
// difference between "nobody is here" and "the site is broken" — and the whole
// point of the recommendation is that their first click should land somewhere
// with people in it.
const RecommendedCommunitiesMinMembers = 2

// recommendedCommunitiesDefaultLimit matches the other v2 community endpoints.
const recommendedCommunitiesDefaultLimit = 10

// BoostedPriorityMinMembers is how many members a boosted community needs before
// its boost buys it a place above larger free communities.
//
// Boosting is meant to surface a community, not to override the only signal a
// new player actually cares about. Active-boosted communities range from 1 to
// 2,472 members, and 12 of the 51 have fewer than 10. Without this floor a
// 2-member elite community outranks a 1,645-member free one for anybody
// filtering to PC, which is the exact "I joined and nobody was there" outcome
// this endpoint exists to prevent.
//
// At 10 the floor keeps 39 of 51 boosted communities in the priority block. The
// other 12 are still listed and still ranked by size — they just do not lead.
const BoostedPriorityMinMembers = 10

// subscriptionRankBranches ranks a community by its boost tier, but only while
// the subscription is actually active.
//
// The existing prioritized-communities pipeline switches on subscription.plan
// alone, so a community whose boost lapsed keeps its place at the top of every
// list forever. Effective tier is plan AND active; anything else is free.
func subscriptionRankBranches() bson.M {
	tiers := []struct {
		plan string
		rank int
	}{
		{"elite", 4},
		{"premium", 3},
		{"standard", 2},
		{"basic", 1},
	}
	branches := make([]bson.M, 0, len(tiers))
	for _, tier := range tiers {
		branches = append(branches, bson.M{
			"case": bson.M{"$and": []interface{}{
				bson.M{"$eq": []interface{}{"$community.subscription.plan", tier.plan}},
				bson.M{"$eq": []interface{}{"$community.subscription.active", true}},
			}},
			"then": tier.rank,
		})
	}
	return bson.M{"$switch": bson.M{"branches": branches, "default": 0}}
}

// subscriptionRankExpr is the ranking actually used by the pipeline: the tier
// ladder, but only for communities big enough for the boost to be worth
// honouring. Below the floor a boosted community ranks as free and takes its
// place in the size ordering with everyone else.
func subscriptionRankExpr() bson.M {
	return bson.M{"$cond": []interface{}{
		bson.M{"$gte": []interface{}{
			bson.M{"$ifNull": []interface{}{"$community.membersCount", 0}},
			BoostedPriorityMinMembers,
		}},
		subscriptionRankBranches(),
		0,
	}}
}

// demoCommunityIDs are our own communities. They stay public on purpose, as
// working examples people can look at, but they are not real servers to play in
// and must never be recommended to somebody looking for one.
//
// Both are elite/basic boosted and reasonably large, so without this they lead
// the PC results for every new player. Listed by ID rather than by owner: the
// same account could later run a genuine community, and excluding by owner would
// silently swallow it. Adding a future test community means adding its ID here.
var demoCommunityIDs = []string{
	"6803ecda576732cfdd465db9", // Lines Police CAD (Developers)
	"680405dccc4b8e684c686dbe", // Lines Police Server
}

// demoCommunityObjectIDs is the parsed form, built once at startup. A malformed
// entry is dropped rather than panicking: a typo here must not take the API down.
var demoCommunityObjectIDs = func() []primitive.ObjectID {
	ids := make([]primitive.ObjectID, 0, len(demoCommunityIDs))
	for _, hex := range demoCommunityIDs {
		oid, err := primitive.ObjectIDFromHex(hex)
		if err != nil {
			zap.S().Errorw("demo community id is not a valid object id; it will still be recommended",
				"id", hex, "error", err)
			continue
		}
		ids = append(ids, oid)
	}
	return ids
}()

// excludeDemoCommunities adds our own communities to a discovery filter's
// exclusion list, preserving anything already excluded.
//
// Applied to every surface that *suggests* a community to somebody: the elite
// carousel, discover, browse-by-tag, random and recommended. Deliberately NOT
// applied to search or to fetching one community by id — they stay public as
// working examples, so looking them up by name or opening a direct link must
// still work. The line is "we put it in front of you" versus "you asked for it
// by name".
func excludeDemoCommunities(match bson.M) bson.M {
	if len(demoCommunityObjectIDs) == 0 {
		return match
	}
	existing, _ := match["_id"].(bson.M)
	if existing == nil {
		match["_id"] = bson.M{"$nin": demoCommunityObjectIDs}
		return match
	}
	prior, _ := existing["$nin"].([]primitive.ObjectID)
	combined := make([]primitive.ObjectID, 0, len(prior)+len(demoCommunityObjectIDs))
	combined = append(combined, prior...)
	combined = append(combined, demoCommunityObjectIDs...)
	existing["$nin"] = combined
	match["_id"] = existing
	return match
}

// excludeDemoCommunitiesD is the bson.D form, for the browse-by-tag pipelines
// which build ordered match documents. If an _id condition is somehow already
// present it is left alone rather than appended to, since a bson.D with two _id
// keys silently drops one.
func excludeDemoCommunitiesD(match bson.D) bson.D {
	if len(demoCommunityObjectIDs) == 0 {
		return match
	}
	for _, elem := range match {
		if elem.Key == "_id" {
			zap.S().Warnw("demo community exclusion skipped: match already constrains _id")
			return match
		}
	}
	return append(match, bson.E{Key: "_id", Value: bson.M{"$nin": demoCommunityObjectIDs}})
}

// recommendedMatch builds the match stage. The count and the pipeline share it
// so a totalCount can never describe a different set than the page it labels.
func recommendedMatch(tag string, excludedIDs []primitive.ObjectID) bson.M {
	match := bson.M{
		"community.visibility":   "public",
		"community.membersCount": bson.M{"$gte": RecommendedCommunitiesMinMembers},
	}
	if tag != "" && !strings.EqualFold(tag, "all") {
		match["community.tags"] = tag
	}

	if len(excludedIDs) > 0 {
		match["_id"] = bson.M{"$nin": excludedIDs}
	}
	// Our own demo communities are excluded on top of whatever the caller is
	// already a member of.
	return excludeDemoCommunities(match)
}

// FetchRecommendedCommunitiesHandler returns public communities worth joining,
// best first, optionally filtered to one platform tag.
//
// GET /api/v2/communities/recommended?tag=&limit=&page=&userId=
//
// Ordering is active boost tier descending, then member count descending. That
// second key is the point of this endpoint: FetchCommunitiesByTagHandlerV2 sorts
// by community.name, so today the communities a new player is shown are
// whichever ones happen to start with "A".
//
// Passing userId excludes communities the user is already in or has already
// asked to join, so the recommendations never include somewhere they are still
// waiting on. It is only ever used to remove results, so a forged value can do
// nothing but hide communities from the caller themselves.
func (c Community) FetchRecommendedCommunitiesHandler(w http.ResponseWriter, r *http.Request) {
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit <= 0 {
		limit = recommendedCommunitiesDefaultLimit
	}
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 0 {
		page = 0
	}
	tag := strings.TrimSpace(r.URL.Query().Get("tag"))

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	excludedIDs := communitiesAlreadyJoined(ctx, c.UDB, r.URL.Query().Get("userId"))
	match := recommendedMatch(tag, excludedIDs)

	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: match}},
		{{Key: "$addFields", Value: bson.M{"subscriptionRank": subscriptionRankExpr()}}},
		// _id is the final key so the order is total: without it two communities
		// on the same tier and member count could swap places between pages and
		// the same community would appear twice or not at all.
		{{Key: "$sort", Value: bson.D{
			{Key: "subscriptionRank", Value: -1},
			{Key: "community.membersCount", Value: -1},
			{Key: "_id", Value: 1},
		}}},
		{{Key: "$skip", Value: int64(page * limit)}},
		{{Key: "$limit", Value: int64(limit)}},
		// Keep the payload to what a recommendation card renders. The full
		// document carries penal codes, ten-codes, templates and every
		// department, which is megabytes per page for fields nobody displays.
		{{Key: "$project", Value: bson.M{
			"community.name":                   1,
			"community.imageLink":              1,
			"community.description":            1,
			"community.membersCount":           1,
			"community.tags":                   1,
			"community.promotionalText":        1,
			"community.promotionalDescription": 1,
			"community.subscription":           1,
		}}},
	}

	cursor, err := c.DB.Aggregate(ctx, pipeline)
	if err != nil {
		config.ErrorStatus("failed to fetch recommended communities", http.StatusInternalServerError, w, err)
		return
	}
	defer cursor.Close(ctx)

	var decoded []struct {
		ID        primitive.ObjectID `bson:"_id"`
		Community struct {
			Name                   string   `bson:"name"`
			ImageLink              string   `bson:"imageLink"`
			Description            string   `bson:"description"`
			MembersCount           int      `bson:"membersCount"`
			Tags                   []string `bson:"tags"`
			PromotionalText        string   `bson:"promotionalText"`
			PromotionalDescription string   `bson:"promotionalDescription"`
			Subscription           struct {
				Active bool   `bson:"active"`
				Plan   string `bson:"plan"`
			} `bson:"subscription"`
		} `bson:"community"`
	}
	if err := cursor.All(ctx, &decoded); err != nil {
		config.ErrorStatus("failed to decode recommended communities", http.StatusInternalServerError, w, err)
		return
	}

	// Correct the displayed count from the users collection, since the stored
	// counter drifts. Sorting still uses the stored value: re-sorting the page in
	// Go would reorder rows within the page without changing which page they are
	// on, which is how a community ends up shown twice across two pages.
	ids := make([]string, 0, len(decoded))
	for _, item := range decoded {
		ids = append(ids, item.ID.Hex())
	}
	liveCounts := liveMemberCounts(ctx, c.UDB, ids)

	data := make([]map[string]interface{}, 0, len(decoded))
	for _, item := range decoded {
		membersCount := item.Community.MembersCount
		if live, ok := liveCounts[item.ID.Hex()]; ok {
			membersCount = live
		}
		tags := item.Community.Tags
		if tags == nil {
			tags = []string{}
		}
		data = append(data, map[string]interface{}{
			"_id":                    item.ID,
			"name":                   item.Community.Name,
			"imageLink":              item.Community.ImageLink,
			"description":            item.Community.Description,
			"membersCount":           membersCount,
			"tags":                   tags,
			"promotionalText":        item.Community.PromotionalText,
			"promotionalDescription": item.Community.PromotionalDescription,
			"subscription": map[string]interface{}{
				"active": item.Community.Subscription.Active,
				"plan":   item.Community.Subscription.Plan,
			},
		})
	}

	totalCount, err := c.DB.CountDocuments(ctx, match)
	if err != nil {
		config.ErrorStatus("failed to count recommended communities", http.StatusInternalServerError, w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"data":       data,
		"totalCount": totalCount,
		"page":       page,
		"limit":      limit,
	})
}
