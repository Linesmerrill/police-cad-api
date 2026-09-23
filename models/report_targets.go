package models

import "strings"

// What can be reported, and which parts of it.
//
// A report has to point at one piece of content we host. Reporting a person
// with nothing attached cannot be verified and is most of what has been filed.
// The kinds here are the content real people write that other people can see.
const (
	TargetCommunity           = "community"
	TargetUserProfile         = "user_profile"
	TargetAnnouncement        = "announcement"
	TargetAnnouncementComment = "announcement_comment"
	TargetCommunityEvent      = "community_event"
	TargetFeatureRequest      = "feature_request"
	TargetFeatureRequestReply = "feature_request_comment"
	TargetRpPromotion         = "rp_promotion"
	TargetContentCreator      = "content_creator"
)

// ReportableField is one part of a piece of content that can be reported on
// its own, such as a community's name as distinct from its description.
type ReportableField struct {
	// Name is what the client sends and the snapshot records.
	Name string `json:"name"`
	// Label is how it reads on screen, in the reporter's words.
	Label string `json:"label"`
	// Image marks a field whose value is a picture rather than wording, so the
	// console knows not to print it and never to open it for a child-safety
	// report.
	Image bool `json:"image,omitempty"`
}

// ReportableKind describes one sort of content.
type ReportableKind struct {
	Kind string `json:"kind"`
	// Label is how the console describes it.
	Label string `json:"label"`
	// Fields are the parts a reporter can point at. A report may name several.
	Fields []ReportableField `json:"fields"`
	// MembersOnly marks content only a community's members can see. Reporting
	// it requires being one, so nobody can file about a community they have
	// never been in.
	MembersOnly bool `json:"membersOnly,omitempty"`
}

// reportableKinds is the registry. Keep it in step with the resolvers in
// api/handlers/report_targets.go: a kind here with no resolver cannot be
// reported, which the tests check.
var reportableKinds = map[string]ReportableKind{
	TargetCommunity: {
		Kind: TargetCommunity, Label: "Community profile",
		Fields: []ReportableField{
			{Name: "name", Label: "Its name"},
			{Name: "description", Label: "Its description"},
			{Name: "promotionalText", Label: "Its promo text"},
			{Name: "promotionalDescription", Label: "Its promo description"},
			{Name: "imageLink", Label: "Its logo or banner", Image: true},
		},
	},
	TargetUserProfile: {
		Kind: TargetUserProfile, Label: "Player profile",
		Fields: []ReportableField{
			{Name: "username", Label: "Their username"},
			{Name: "name", Label: "Their display name"},
			{Name: "profilePicture", Label: "Their profile picture", Image: true},
			{Name: "backgroundImage", Label: "Their background image", Image: true},
		},
	},
	TargetAnnouncement: {
		Kind: TargetAnnouncement, Label: "Announcement", MembersOnly: true,
		Fields: []ReportableField{
			{Name: "title", Label: "Its title"},
			{Name: "content", Label: "What it says"},
		},
	},
	TargetAnnouncementComment: {
		Kind: TargetAnnouncementComment, Label: "Comment on an announcement", MembersOnly: true,
		Fields: []ReportableField{{Name: "content", Label: "What it says"}},
	},
	TargetCommunityEvent: {
		Kind: TargetCommunityEvent, Label: "Community event", MembersOnly: true,
		Fields: []ReportableField{
			{Name: "title", Label: "Its title"},
			{Name: "description", Label: "Its description"},
			{Name: "image", Label: "Its image", Image: true},
		},
	},
	TargetFeatureRequest: {
		Kind: TargetFeatureRequest, Label: "Feature request",
		Fields: []ReportableField{
			{Name: "title", Label: "Its title"},
			{Name: "description", Label: "What it says"},
			{Name: "imageUrls", Label: "Its images", Image: true},
		},
	},
	TargetFeatureRequestReply: {
		Kind: TargetFeatureRequestReply, Label: "Comment on a feature request",
		Fields: []ReportableField{
			{Name: "content", Label: "What it says"},
			{Name: "imageUrls", Label: "Its images", Image: true},
		},
	},
	TargetRpPromotion: {
		Kind: TargetRpPromotion, Label: "Server promotion",
		Fields: []ReportableField{
			{Name: "serverName", Label: "The server name"},
			{Name: "description", Label: "Its description"},
			{Name: "features", Label: "Its features"},
			{Name: "requirements", Label: "Its requirements"},
			{Name: "bannerImage", Label: "Its banner", Image: true},
		},
	},
	TargetContentCreator: {
		Kind: TargetContentCreator, Label: "Creator profile",
		Fields: []ReportableField{
			{Name: "displayName", Label: "Their name"},
			{Name: "bio", Label: "Their bio"},
			{Name: "profileImage", Label: "Their picture", Image: true},
		},
	},
}

// ReportableKinds returns the registry, for a client to render from.
func ReportableKinds() map[string]ReportableKind { return reportableKinds }

// LookupReportableKind returns one kind.
func LookupReportableKind(kind string) (ReportableKind, bool) {
	k, ok := reportableKinds[strings.ToLower(strings.TrimSpace(kind))]
	return k, ok
}

// Field returns one field of a kind.
func (k ReportableKind) Field(name string) (ReportableField, bool) {
	for _, f := range k.Fields {
		if strings.EqualFold(f.Name, name) {
			return f, true
		}
	}
	return ReportableField{}, false
}

// ValidateFields checks the reported fields belong to the kind. No fields
// means the content as a whole, which is allowed: a reporter who cannot say
// which part is wrong still has a real complaint.
func (k ReportableKind) ValidateFields(fields []string) error {
	for _, name := range fields {
		if _, ok := k.Field(name); !ok {
			return &UnknownFieldError{Kind: k.Kind, Field: name}
		}
	}
	return nil
}

// UnknownFieldError is returned for a field that is not part of the kind.
type UnknownFieldError struct{ Kind, Field string }

func (e *UnknownFieldError) Error() string {
	return e.Field + " is not part of a " + e.Kind
}

// HasImageField reports whether any of the named fields is a picture. Used to
// keep a child-safety image out of the console until a person chooses to act.
func (k ReportableKind) HasImageField(fields []string) bool {
	for _, name := range fields {
		if f, ok := k.Field(name); ok && f.Image {
			return true
		}
	}
	return false
}
