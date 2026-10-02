package handlers

import (
	"context"
	"sort"
	"strings"
	"unicode"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/models"
)

// Who the reporter and the reported account are, so staff can check a claim
// like "they're using my community's name" without leaving the report: each
// side's username, the communities they own and the ones they're in, plus any
// names that look alike across the two.

const reportPeopleCommunityLimit = 25

type reportPersonCommunity struct {
	ID        string      `json:"id"`
	Name      string      `json:"name"`
	Members   int         `json:"members"`
	CreatedAt interface{} `json:"createdAt,omitempty"`
}

type reportPerson struct {
	ID             string                  `json:"id"`
	Username       string                  `json:"username"`
	ProfilePicture string                  `json:"profilePicture,omitempty"`
	CreatedAt      interface{}             `json:"createdAt,omitempty"`
	Owned          []reportPersonCommunity `json:"owned"`
	Joined         []reportPersonCommunity `json:"joined"`
	JoinedTotal    int                     `json:"joinedTotal"`
}

// reportNameMatch is a reporter-side name and a reported-side name that look
// alike. Kind says what each one is: "username", "community" or "claimed"
// (the name the reporter typed as being impersonated).
type reportNameMatch struct {
	ReporterName string `json:"reporterName"`
	ReporterKind string `json:"reporterKind"`
	ReportedName string `json:"reportedName"`
	ReportedKind string `json:"reportedKind"`
}

type reportPeople struct {
	Reporter    *reportPerson     `json:"reporter,omitempty"`
	Reported    *reportPerson     `json:"reported,omitempty"`
	NameMatches []reportNameMatch `json:"nameMatches"`
}

// reportedUserID is the account a report is about: the user, or the owner of
// a reported community.
func (ra ReportAdmin) reportedUserID(ctx context.Context, report models.Report) string {
	if report.ItemType != reportItemTypeCommunity {
		return report.ItemID
	}
	oid, err := primitive.ObjectIDFromHex(report.ItemID)
	if err != nil || ra.CDB == nil {
		return ""
	}
	community, err := ra.CDB.FindOneIncludingPending(ctx, bson.M{"_id": oid})
	if err != nil || community == nil {
		return ""
	}
	return community.Details.OwnerID
}

func (ra ReportAdmin) peopleFor(ctx context.Context, report models.Report) reportPeople {
	people := reportPeople{
		Reporter:    ra.reportPerson(ctx, report.ReportedByID),
		Reported:    ra.reportPerson(ctx, ra.reportedUserID(ctx, report)),
		NameMatches: []reportNameMatch{},
	}
	people.NameMatches = findNameMatches(people.Reporter, people.Reported, report.ImpersonatedName)
	return people
}

func (ra ReportAdmin) reportPerson(ctx context.Context, userID string) *reportPerson {
	oid, err := primitive.ObjectIDFromHex(userID)
	if err != nil || ra.UDB == nil {
		return nil
	}
	var user models.User
	if err := ra.UDB.FindOne(ctx, bson.M{"_id": oid}).Decode(&user); err != nil {
		return nil
	}
	person := &reportPerson{
		ID:             userID,
		Username:       user.Details.Username,
		ProfilePicture: user.Details.ProfilePicture,
		CreatedAt:      user.Details.CreatedAt,
		Owned:          []reportPersonCommunity{},
		Joined:         []reportPersonCommunity{},
	}
	if ra.CDB == nil {
		return person
	}

	// Ownership is community.ownerID; user.communities is membership and
	// doesn't reliably include communities the user owns.
	person.Owned = ra.reportCommunities(ctx, bson.M{"community.ownerID": userID})
	owned := map[string]bool{}
	for _, c := range person.Owned {
		owned[c.ID] = true
	}

	var joinedIDs []primitive.ObjectID
	for _, membership := range user.Details.Communities {
		if membership.Status != "approved" || owned[membership.CommunityID] {
			continue
		}
		if cid, err := primitive.ObjectIDFromHex(membership.CommunityID); err == nil {
			joinedIDs = append(joinedIDs, cid)
		}
	}
	person.JoinedTotal = len(joinedIDs)
	if len(joinedIDs) > 0 {
		person.Joined = ra.reportCommunities(ctx, bson.M{"_id": bson.M{"$in": joinedIDs}})
	}
	return person
}

// reportCommunities lists matching communities, biggest first, capped.
// Pending-deletion communities are included: a reported owner may have
// already started deleting the evidence.
func (ra ReportAdmin) reportCommunities(ctx context.Context, filter bson.M) []reportPersonCommunity {
	out := []reportPersonCommunity{}
	cursor, err := ra.CDB.FindIncludingPending(ctx, filter, options.Find().
		SetSort(bson.D{{Key: "community.membersCount", Value: -1}, {Key: "_id", Value: 1}}).
		SetLimit(reportPeopleCommunityLimit).
		SetProjection(bson.M{"community.name": 1, "community.membersCount": 1, "community.createdAt": 1}))
	if err != nil {
		zap.S().Warnw("report people: failed to list communities", "error", err)
		return out
	}
	defer cursor.Close(ctx)
	var communities []models.Community
	if err := cursor.All(ctx, &communities); err != nil {
		zap.S().Warnw("report people: failed to decode communities", "error", err)
		return out
	}
	for _, c := range communities {
		out = append(out, reportPersonCommunity{
			ID:        c.ID.Hex(),
			Name:      c.Details.Name,
			Members:   c.Details.MembersCount,
			CreatedAt: c.Details.CreatedAt,
		})
	}
	return out
}

type reportName struct {
	name string
	kind string
}

func personNames(p *reportPerson) []reportName {
	if p == nil {
		return nil
	}
	names := []reportName{{p.Username, "username"}}
	for _, c := range p.Owned {
		names = append(names, reportName{c.Name, "community"})
	}
	return names
}

// findNameMatches pairs reporter-side names (username, owned communities and
// the claimed name) with reported-side names (username, owned communities)
// that look alike after ignoring case, spaces and punctuation.
func findNameMatches(reporter, reported *reportPerson, claimed string) []reportNameMatch {
	matches := []reportNameMatch{}
	mine := personNames(reporter)
	if strings.TrimSpace(claimed) != "" {
		mine = append(mine, reportName{strings.TrimSpace(claimed), "claimed"})
	}
	theirs := personNames(reported)
	seen := map[string]bool{}
	for _, a := range mine {
		for _, b := range theirs {
			if !namesLookAlike(a.name, b.name) {
				continue
			}
			key := a.kind + "\x00" + a.name + "\x00" + b.kind + "\x00" + b.name
			if seen[key] {
				continue
			}
			seen[key] = true
			matches = append(matches, reportNameMatch{
				ReporterName: a.name, ReporterKind: a.kind,
				ReportedName: b.name, ReportedKind: b.kind,
			})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].ReportedName < matches[j].ReportedName })
	return matches
}

func normalizeName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// namesLookAlike: equal once normalized, one contains the other (when the
// shorter has at least 4 characters, so "pd" doesn't match everything), or
// within a small edit distance (about one typo per five characters).
func namesLookAlike(a, b string) bool {
	na, nb := normalizeName(a), normalizeName(b)
	if na == "" || nb == "" {
		return false
	}
	if na == nb {
		return true
	}
	short, long := na, nb
	if len([]rune(short)) > len([]rune(long)) {
		short, long = long, short
	}
	if len([]rune(short)) >= 4 && strings.Contains(long, short) {
		return true
	}
	limit := len([]rune(short)) / 5
	if limit < 1 || len([]rune(short)) < 5 {
		return false
	}
	return editDistance(na, nb) <= limit
}

func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(min(prev[j]+1, cur[j-1]+1), prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}
