package handlers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// Resolving a report's target: loading the content it points at, checking the
// reporter can see it, and copying what it said.
//
// The snapshot is taken from the content itself, never from anything the
// client sent. A client-supplied copy would let a reporter invent what someone
// wrote, and would be erased the moment the author edited it.

// targetResolver holds what the resolvers need to read.
type targetResolver struct {
	db databases.DatabaseHelper
}

// resolvedTarget is a snapshot plus who must be able to see it.
type resolvedTarget struct {
	snapshot models.ReportSnapshot
	// requiresMembershipOf is a community the reporter must belong to, or
	// empty when the content is public.
	requiresMembershipOf string
}

// errTargetGone is returned when the content is not there any more. Reporting
// something that has already been deleted is not an error worth a stack trace:
// there is simply nothing to act on.
var errTargetGone = fmt.Errorf("that content is no longer there")

// resolveTarget loads the reported content and snapshots the named fields.
func (t targetResolver) resolveTarget(ctx context.Context, target models.ReportTarget) (resolvedTarget, error) {
	kind, ok := models.LookupReportableKind(target.Kind)
	if !ok {
		return resolvedTarget{}, fmt.Errorf("%s is not something that can be reported", target.Kind)
	}
	if err := kind.ValidateFields(target.Fields); err != nil {
		return resolvedTarget{}, err
	}

	switch kind.Kind {
	case models.TargetCommunity:
		return t.resolveCommunity(ctx, kind, target)
	case models.TargetUserProfile:
		return t.resolveUserProfile(ctx, kind, target)
	case models.TargetAnnouncement, models.TargetAnnouncementComment:
		return t.resolveAnnouncement(ctx, kind, target)
	case models.TargetCommunityEvent:
		return t.resolveCommunityEvent(ctx, kind, target)
	case models.TargetFeatureRequest, models.TargetFeatureRequestReply:
		return t.resolveFeatureRequest(ctx, kind, target)
	case models.TargetRpPromotion:
		return t.resolveRpPromotion(ctx, kind, target)
	case models.TargetContentCreator:
		return t.resolveContentCreator(ctx, kind, target)
	case models.TargetCivilian, models.TargetVehicle, models.TargetFirearm:
		return t.resolveRoleplayRecord(ctx, kind, target)
	default:
		// A kind in the registry with no resolver here cannot be snapshotted,
		// so it must not be reportable. A test keeps the two in step.
		return resolvedTarget{}, fmt.Errorf("%s cannot be reported yet", kind.Kind)
	}
}

// snapshotOf builds the snapshot for the named fields, or for every field of
// the kind when none were named.
func snapshotOf(kind models.ReportableKind, target models.ReportTarget, values map[string]string, images map[string][]string) models.ReportSnapshot {
	names := target.Fields
	if len(names) == 0 {
		for _, f := range kind.Fields {
			names = append(names, f.Name)
		}
	}

	snap := models.ReportSnapshot{
		Label:      kind.Label,
		CapturedAt: primitive.NewDateTimeFromTime(time.Now()),
	}
	for _, name := range names {
		field, ok := kind.Field(name)
		if !ok {
			continue
		}
		if field.Image {
			snap.ImageURLs = append(snap.ImageURLs, images[field.Name]...)
			if v := values[field.Name]; v != "" {
				snap.ImageURLs = append(snap.ImageURLs, v)
			}
			continue
		}
		value := values[field.Name]
		if strings.TrimSpace(value) == "" {
			// An empty field is recorded as empty rather than skipped: "they
			// reported the description and it was blank" is itself an answer.
			value = ""
		}
		snap.Text = append(snap.Text, models.SnapshotField{Field: field.Name, Label: field.Label, Value: value})
	}
	return snap
}

func objectID(hex string) (primitive.ObjectID, error) {
	return primitive.ObjectIDFromHex(strings.TrimSpace(hex))
}

func (t targetResolver) resolveCommunity(ctx context.Context, kind models.ReportableKind, target models.ReportTarget) (resolvedTarget, error) {
	oid, err := objectID(target.ID)
	if err != nil {
		return resolvedTarget{}, fmt.Errorf("that community could not be found")
	}
	var doc models.Community
	if err := t.db.Collection("communities").FindOne(ctx, bson.M{"_id": oid}).Decode(&doc); err != nil {
		return resolvedTarget{}, errTargetGone
	}
	c := doc.Details
	snap := snapshotOf(kind, target, map[string]string{
		"name":                   c.Name,
		"description":            c.Description,
		"promotionalText":        c.PromotionalText,
		"promotionalDescription": c.PromotionalDescription,
		"imageLink":              c.ImageLink,
	}, nil)
	// A community's profile is written by whoever runs it, so the strike goes
	// to its owner.
	snap.AuthorID = c.OwnerID
	snap.CommunityID = target.ID
	return resolvedTarget{snapshot: snap}, nil
}

func (t targetResolver) resolveUserProfile(ctx context.Context, kind models.ReportableKind, target models.ReportTarget) (resolvedTarget, error) {
	oid, err := objectID(target.ID)
	if err != nil {
		return resolvedTarget{}, fmt.Errorf("that player could not be found")
	}
	var doc models.User
	if err := t.db.Collection("users").FindOne(ctx, bson.M{"_id": oid}).Decode(&doc); err != nil {
		return resolvedTarget{}, errTargetGone
	}
	u := doc.Details
	snap := snapshotOf(kind, target, map[string]string{
		"username":        u.Username,
		"name":            u.Name,
		"profilePicture":  u.ProfilePicture,
		"backgroundImage": u.BackgroundImage,
	}, nil)
	snap.AuthorID = target.ID
	snap.AuthorName = u.Username
	return resolvedTarget{snapshot: snap}, nil
}

func (t targetResolver) resolveAnnouncement(ctx context.Context, kind models.ReportableKind, target models.ReportTarget) (resolvedTarget, error) {
	// A comment names the announcement holding it; the announcement names
	// itself.
	holderID := target.ID
	if kind.Kind == models.TargetAnnouncementComment {
		holderID = target.ParentID
	}
	oid, err := objectID(holderID)
	if err != nil {
		return resolvedTarget{}, errTargetGone
	}
	var doc models.Announcement
	if err := t.db.Collection("announcements").FindOne(ctx, bson.M{"_id": oid}).Decode(&doc); err != nil {
		return resolvedTarget{}, errTargetGone
	}

	community := doc.Community.Hex()
	if kind.Kind == models.TargetAnnouncement {
		snap := snapshotOf(kind, target, map[string]string{"title": doc.Title, "content": doc.Content}, nil)
		snap.AuthorID = doc.Creator.Hex()
		snap.CommunityID = community
		return resolvedTarget{snapshot: snap, requiresMembershipOf: community}, nil
	}

	commentID, err := objectID(target.ID)
	if err != nil {
		return resolvedTarget{}, errTargetGone
	}
	for _, c := range doc.Comments {
		if c.ID != commentID {
			continue
		}
		snap := snapshotOf(kind, target, map[string]string{"content": c.Content}, nil)
		snap.AuthorID = c.User.Hex()
		snap.CommunityID = community
		return resolvedTarget{snapshot: snap, requiresMembershipOf: community}, nil
	}
	return resolvedTarget{}, errTargetGone
}

func (t targetResolver) resolveCommunityEvent(ctx context.Context, kind models.ReportableKind, target models.ReportTarget) (resolvedTarget, error) {
	oid, err := objectID(target.ParentID)
	if err != nil {
		return resolvedTarget{}, errTargetGone
	}
	var doc models.Community
	if err := t.db.Collection("communities").FindOne(ctx, bson.M{"_id": oid}).Decode(&doc); err != nil {
		return resolvedTarget{}, errTargetGone
	}
	eventID, err := objectID(target.ID)
	if err != nil {
		return resolvedTarget{}, errTargetGone
	}
	for _, e := range doc.Details.Events {
		if e.ID != eventID {
			continue
		}
		snap := snapshotOf(kind, target, map[string]string{
			"title": e.Title, "description": e.Description, "image": e.Image,
		}, nil)
		// Events record a host by name, not id, so the strike lands on the
		// community's owner unless staff say otherwise.
		snap.AuthorID = doc.Details.OwnerID
		snap.AuthorName = e.Host
		snap.CommunityID = target.ParentID
		return resolvedTarget{snapshot: snap, requiresMembershipOf: target.ParentID}, nil
	}
	return resolvedTarget{}, errTargetGone
}

func (t targetResolver) resolveFeatureRequest(ctx context.Context, kind models.ReportableKind, target models.ReportTarget) (resolvedTarget, error) {
	holderID := target.ID
	if kind.Kind == models.TargetFeatureRequestReply {
		holderID = target.ParentID
	}
	oid, err := objectID(holderID)
	if err != nil {
		return resolvedTarget{}, errTargetGone
	}
	var doc models.FeatureRequest
	if err := t.db.Collection("featureRequests").FindOne(ctx, bson.M{"_id": oid}).Decode(&doc); err != nil {
		return resolvedTarget{}, errTargetGone
	}

	if kind.Kind == models.TargetFeatureRequest {
		snap := snapshotOf(kind, target,
			map[string]string{"title": doc.Title, "description": doc.Description},
			map[string][]string{"imageUrls": doc.ImageURLs})
		snap.AuthorID = doc.Author.Hex()
		return resolvedTarget{snapshot: snap}, nil
	}

	commentID, err := objectID(target.ID)
	if err != nil {
		return resolvedTarget{}, errTargetGone
	}
	for _, c := range doc.Comments {
		if c.ID != commentID {
			continue
		}
		snap := snapshotOf(kind, target,
			map[string]string{"content": c.Content},
			map[string][]string{"imageUrls": c.ImageURLs})
		snap.AuthorID = c.User.Hex()
		return resolvedTarget{snapshot: snap}, nil
	}
	return resolvedTarget{}, errTargetGone
}

// reporterCanSee checks a members-only piece of content really is visible to
// the reporter, so nobody can file reports about a community they have never
// been in.
func (t targetResolver) reporterCanSee(ctx context.Context, reporterID, communityID string) bool {
	if communityID == "" {
		return true
	}
	oid, err := objectID(reporterID)
	if err != nil {
		return false
	}
	var user models.User
	if err := t.db.Collection("users").FindOne(ctx, bson.M{"_id": oid}).Decode(&user); err != nil {
		return false
	}
	for _, c := range user.Details.Communities {
		if c.CommunityID == communityID && c.Status == "approved" {
			return true
		}
	}
	return false
}

func (t targetResolver) resolveRpPromotion(ctx context.Context, kind models.ReportableKind, target models.ReportTarget) (resolvedTarget, error) {
	oid, err := objectID(target.ParentID)
	if err != nil {
		return resolvedTarget{}, errTargetGone
	}
	var doc models.Community
	if err := t.db.Collection("communities").FindOne(ctx, bson.M{"_id": oid}).Decode(&doc); err != nil {
		return resolvedTarget{}, errTargetGone
	}
	if doc.Details.RpPromotion == nil {
		return resolvedTarget{}, errTargetGone
	}
	wanted := strings.TrimSpace(target.ID)
	for _, post := range doc.Details.RpPromotion.History {
		if post.ID != wanted {
			continue
		}
		d := post.Data
		snap := snapshotOf(kind, target, map[string]string{
			"serverName":   d.ServerName,
			"description":  d.Description,
			"features":     strings.Join(d.Features, ", "),
			"requirements": d.Requirements,
			"bannerImage":  d.BannerImage,
		}, map[string][]string{"bannerImage": d.Images})
		// The promo is the work of whoever posted it, not of the community.
		snap.AuthorID = post.PostedBy
		snap.AuthorName = post.PostedByName
		snap.CommunityID = target.ParentID
		return resolvedTarget{snapshot: snap}, nil
	}
	return resolvedTarget{}, errTargetGone
}

func (t targetResolver) resolveContentCreator(ctx context.Context, kind models.ReportableKind, target models.ReportTarget) (resolvedTarget, error) {
	oid, err := objectID(target.ID)
	if err != nil {
		return resolvedTarget{}, errTargetGone
	}
	var doc models.ContentCreator
	if err := t.db.Collection("content_creators").FindOne(ctx, bson.M{"_id": oid}).Decode(&doc); err != nil {
		return resolvedTarget{}, errTargetGone
	}
	snap := snapshotOf(kind, target, map[string]string{
		"displayName":  doc.DisplayName,
		"bio":          doc.Bio,
		"profileImage": doc.ProfileImage,
	}, nil)
	if doc.UserID != nil {
		snap.AuthorID = doc.UserID.Hex()
	}
	snap.AuthorName = doc.DisplayName
	return resolvedTarget{snapshot: snap}, nil
}

// roleplayCollections maps each roleplay record to its collection and the
// field its details hang off, since each wraps its own document.
var roleplayCollections = map[string]struct{ collection, wrapper string }{
	models.TargetCivilian: {"civilians", "civilian"},
	models.TargetVehicle:  {"vehicles", "vehicle"},
	models.TargetFirearm:  {"firearms", "firearm"},
}

// resolveRoleplayRecord snapshots a character, vehicle or firearm.
//
// These are read as a loose document rather than through their typed models,
// because all three share this one resolver and only a handful of fields are
// reportable. Every other field on the record, the roleplay itself, is left
// alone: it is the community's own fiction and none of our business.
func (t targetResolver) resolveRoleplayRecord(ctx context.Context, kind models.ReportableKind, target models.ReportTarget) (resolvedTarget, error) {
	spec, ok := roleplayCollections[kind.Kind]
	if !ok {
		return resolvedTarget{}, errTargetGone
	}
	oid, err := objectID(target.ID)
	if err != nil {
		return resolvedTarget{}, errTargetGone
	}

	var doc bson.M
	if err := t.db.Collection(spec.collection).FindOne(ctx, bson.M{"_id": oid}).Decode(&doc); err != nil {
		return resolvedTarget{}, errTargetGone
	}
	details, _ := doc[spec.wrapper].(bson.M)
	if details == nil {
		return resolvedTarget{}, errTargetGone
	}

	text := func(field string) string {
		v, _ := details[field].(string)
		return v
	}
	snap := snapshotOf(kind, target, map[string]string{
		"firstName": text("firstName"),
		"lastName":  text("lastName"),
		"plate":     text("plate"),
		"model":     text("model"),
		"name":      text("name"),
		"image":     text("image"),
	}, nil)

	// The record belongs to whoever created it, so the strike lands on them
	// and not on the community it was played in.
	snap.AuthorID = text("userID")
	community := text("activeCommunityID")
	snap.CommunityID = community
	return resolvedTarget{snapshot: snap, requiresMembershipOf: community}, nil
}
