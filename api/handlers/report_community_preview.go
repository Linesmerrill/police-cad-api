package handlers

import (
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/models"
)

// A staff, read-only view of what a community shows the world.
//
// A report that says "they copied my community" can only be judged by looking:
// the logo, the description, the department names and images. Staff could not
// see a community's home page from the report, because that page is the live
// CAD, which needs membership and is full of controls that act on the
// community as whoever is signed in. This returns only what the home page and
// the community listing display, so the website can draw it with nothing to
// press.
//
// Everything here is presentation. No members, roles, invite codes, ban list or
// settings: a reviewer comparing two communities needs none of it, and a staff
// tool that hands out less is one that leaks less.

// previewEventsMax is how many events the preview carries. The home page leads
// with the next few; a reviewer looking for copied art needs no more.
const previewEventsMax = 6

type communityPreviewDepartment struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Image       string `json:"image,omitempty"`
}

type communityPreviewEvent struct {
	Title         string `json:"title"`
	ScheduledDate string `json:"scheduledDate,omitempty"`
	Image         string `json:"image,omitempty"`
	Description   string `json:"description,omitempty"`
}

type communityPreviewOwner struct {
	ID       string `json:"id"`
	Username string `json:"username,omitempty"`
}

type communityPreview struct {
	ID                     string                       `json:"id"`
	Name                   string                       `json:"name"`
	ImageLink              string                       `json:"imageLink,omitempty"`
	Description            string                       `json:"description,omitempty"`
	PromotionalText        string                       `json:"promotionalText,omitempty"`
	PromotionalDescription string                       `json:"promotionalDescription,omitempty"`
	Tags                   []string                     `json:"tags"`
	Visibility             string                       `json:"visibility,omitempty"`
	MembersCount           int                          `json:"membersCount"`
	CreatedAt              *time.Time                   `json:"createdAt,omitempty"`
	DiscordInviteURL       string                       `json:"discordInviteUrl,omitempty"`
	OnboardingSteps        []string                     `json:"onboardingSteps"`
	BoostPlan              string                       `json:"boostPlan,omitempty"`
	Owner                  *communityPreviewOwner       `json:"owner,omitempty"`
	PendingDeletionAt      *time.Time                   `json:"pendingDeletionAt,omitempty"`
	ListingSuspended       bool                         `json:"listingSuspended,omitempty"`
	Departments            []communityPreviewDepartment `json:"departments"`
	Events                 []communityPreviewEvent      `json:"events"`
}

// AdminCommunityPreviewHandler returns a community's public presentation for
// staff review.
//
// GET /api/v1/admin/community-preview/{id}
//
// The route variable is {id} on purpose: the pending-deletion gate short
// circuits any route whose variable is named community_id or communityId, and
// a community already being deleted is exactly one a reviewer may need to see.
func (ra ReportAdmin) AdminCommunityPreviewHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var currentUser map[string]interface{}
	if raw := r.URL.Query().Get("roles"); raw != "" {
		currentUser = map[string]interface{}{"roles": strings.Split(raw, ",")}
	}
	if !authorizeReportAdmin(w, r, currentUser) {
		return
	}

	oid, err := primitive.ObjectIDFromHex(mux.Vars(r)["id"])
	if err != nil {
		config.InfoStatus("invalid community id", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	community, err := ra.CDB.FindOneIncludingPending(ctx, bson.M{"_id": oid})
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			config.InfoStatus("community not found", http.StatusNotFound, w, err)
			return
		}
		config.ErrorStatus("failed to load community", http.StatusInternalServerError, w, err)
		return
	}

	preview := buildCommunityPreview(community)
	if owner := community.Details.OwnerID; owner != "" {
		names := ra.userNames(ctx, map[string]bool{owner: true})
		preview.Owner = &communityPreviewOwner{ID: owner, Username: names[owner]}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"community": preview})
}

// buildCommunityPreview copies out the fields a community shows the world.
func buildCommunityPreview(community *models.Community) communityPreview {
	d := community.Details
	preview := communityPreview{
		ID:                     community.ID.Hex(),
		Name:                   d.Name,
		ImageLink:              d.ImageLink,
		Description:            d.Description,
		PromotionalText:        d.PromotionalText,
		PromotionalDescription: d.PromotionalDescription,
		Tags:                   nonNilStrings(d.Tags),
		Visibility:             d.Visibility,
		MembersCount:           d.MembersCount,
		DiscordInviteURL:       d.DiscordInviteURL,
		OnboardingSteps:        nonNilStrings(d.OnboardingSteps),
		ListingSuspended:       d.ListingSuspension != nil,
		Departments:            []communityPreviewDepartment{},
		Events:                 []communityPreviewEvent{},
	}
	if d.CreatedAt != 0 {
		t := d.CreatedAt.Time()
		preview.CreatedAt = &t
	}
	if d.PendingDeletionAt != nil {
		t := d.PendingDeletionAt.Time()
		preview.PendingDeletionAt = &t
	}
	if d.Subscription.Active && d.Subscription.Plan != "" {
		preview.BoostPlan = d.Subscription.Plan
	}

	for _, dept := range d.Departments {
		preview.Departments = append(preview.Departments, communityPreviewDepartment{
			Name:        dept.Name,
			Description: dept.Description,
			Image:       dept.Image,
		})
	}

	// Newest first, the order the home page puts a community's events in.
	events := append([]models.Event(nil), d.Events...)
	sort.SliceStable(events, func(i, j int) bool { return events[i].CreatedAt > events[j].CreatedAt })
	for _, e := range events {
		if len(preview.Events) == previewEventsMax {
			break
		}
		preview.Events = append(preview.Events, communityPreviewEvent{
			Title:         e.Title,
			ScheduledDate: e.ScheduledDate,
			Image:         e.Image,
			Description:   e.Description,
		})
	}
	return preview
}

func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}
