package models

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/bsontype"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Cents is a monetary amount in cents (hourly pay, etc.) that decodes
// DEFENSIVELY from BSON. A community once had a rank payRatePerHour of 1e32 (a
// bad value written via the website's Mongoose path) stored as a BSON double;
// decoding it into a plain int64 overflowed and made the ENTIRE community fail
// to decode — 500ing every community-scoped read (page load, panic alerts,
// units, signal-100, ...). Accept int32/int64/double/string and clamp anything
// non-finite or out of int64 range to 0, so a single bad value can never brick
// a community again. Underlying type stays int64, so callers convert with
// int64(...) and JSON still serializes as a plain number.
type Cents int64

// UnmarshalBSONValue implements defensive decoding for Cents. See the type doc.
func (c *Cents) UnmarshalBSONValue(t bsontype.Type, data []byte) error {
	rv := bson.RawValue{Type: t, Value: data}
	switch t {
	case bsontype.Int32:
		*c = Cents(rv.Int32())
	case bsontype.Int64:
		*c = Cents(rv.Int64())
	case bsontype.Double:
		d := rv.Double()
		if math.IsNaN(d) || math.IsInf(d, 0) || d > math.MaxInt64 || d < math.MinInt64 {
			*c = 0
		} else {
			*c = Cents(d)
		}
	case bsontype.String:
		s := strings.TrimSpace(strings.TrimPrefix(rv.StringValue(), "$"))
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			*c = Cents(n)
		} else {
			*c = 0
		}
	case bsontype.Null, bsontype.Undefined:
		*c = 0
	default:
		*c = 0
	}
	return nil
}

// Community holds the structure for the community collection in mongo
type Community struct {
	ID      primitive.ObjectID `json:"_id" bson:"_id"`
	Details CommunityDetails   `json:"community" bson:"community"`
	Version int32              `json:"__v" bson:"__v"`
}

// CommunityDetails holds the structure for the inner community collection in mongo
type CommunityDetails struct {
	Name                          string                  `json:"name" bson:"name"`
	OwnerID                       string                  `json:"ownerID" bson:"ownerID"`
	Code                          string                  `json:"code" bson:"code"`
	ActivePanics                  map[string]interface{}  `json:"activePanics" bson:"activePanics"`
	ActiveSignal100               bool                    `json:"activeSignal100" bson:"activeSignal100"`
	Signal100                     Signal100Data           `json:"signal100" bson:"signal100"`
	ImageLink                     string                  `json:"imageLink" bson:"imageLink"`
	MapLink                       string                  `json:"mapLink" bson:"mapLink"`
	Visibility                    string                  `json:"visibility" bson:"visibility"`
	PromotionalText               string                  `json:"promotionalText" bson:"promotionalText"`
	PromotionalDescription        string                  `json:"promotionalDescription" bson:"promotionalDescription"`
	InviteCodeIds                 []string                `json:"inviteCodeIds" bson:"inviteCodeIds"`
	Tags                          []string                `json:"tags" bson:"tags"`
	Roles                         []Role                  `json:"roles" bson:"roles"`
	BanList                       []string                `json:"banList" bson:"banList"`
	Description                   string                  `json:"description" bson:"description"`
	Members                       map[string]MemberDetail `json:"members" bson:"members"`
	MembersCount                  int                     `json:"membersCount" bson:"membersCount"`
	Events                        []Event                 `json:"events" bson:"events"`
	Departments                   []Department            `json:"departments" bson:"departments"`
	TenCodes                      []TenCodes              `json:"tenCodes" bson:"tenCodes"`
	Fines                         CommunityFine           `json:"fines" bson:"fines"`
	PenalCodes                    CommunityPenalCode      `json:"penalCodes" bson:"penalCodes"`
	Templates                     []Template              `json:"templates" bson:"templates"`
	Subscription                  Subscription            `json:"subscription" bson:"subscription"`
	SubscriptionCreatedBy         string                  `json:"subscriptionCreatedBy" bson:"subscriptionCreatedBy"`
	Analytics                     CommunityAnalytics      `json:"analytics" bson:"analytics"`
	ActivityLevel                 string                  `json:"activityLevel" bson:"activityLevel"`
	CivilianCreationLimitsEnabled bool                    `json:"civilianCreationLimitsEnabled" bson:"civilianCreationLimitsEnabled"`
	CivilianCreationLimit         int                     `json:"civilianCreationLimit" bson:"civilianCreationLimit"`
	VehicleCreationLimitsEnabled  bool                    `json:"vehicleCreationLimitsEnabled" bson:"vehicleCreationLimitsEnabled"`
	VehicleCreationLimit          int                     `json:"vehicleCreationLimit" bson:"vehicleCreationLimit"`
	FirearmCreationLimitsEnabled  bool                    `json:"firearmCreationLimitsEnabled" bson:"firearmCreationLimitsEnabled"`
	FirearmCreationLimit          int                     `json:"firearmCreationLimit" bson:"firearmCreationLimit"`
	CivilianApprovalSystemEnabled bool                    `json:"civilianApprovalSystemEnabled" bson:"civilianApprovalSystemEnabled"`
	ActivePanicAlerts             []PanicAlert            `json:"activePanicAlerts" bson:"activePanicAlerts"`
	CustomToneGroups              []CustomToneGroup       `json:"customToneGroups" bson:"customToneGroups"`
	CustomToneSounds              []CustomToneSound       `json:"customToneSounds" bson:"customToneSounds"`
	DefaultToneLeo                string                  `json:"defaultToneLeo,omitempty" bson:"defaultToneLeo,omitempty"`
	DefaultToneFd                 string                  `json:"defaultToneFd,omitempty" bson:"defaultToneFd,omitempty"`
	DefaultToneEms                string                  `json:"defaultToneEms,omitempty" bson:"defaultToneEms,omitempty"`
	DefaultPanicSound             string                  `json:"defaultPanicSound,omitempty" bson:"defaultPanicSound,omitempty"`
	DefaultSignal100Sound         string                  `json:"defaultSignal100Sound,omitempty" bson:"defaultSignal100Sound,omitempty"`
	WarrantApprovalMode           string                  `json:"warrantApprovalMode" bson:"warrantApprovalMode"`             // "auto-approve", "random", "require-judge"
	WarrantRandomApprovalRate     int                     `json:"warrantRandomApprovalRate" bson:"warrantRandomApprovalRate"` // 0-100 percentage, default 70
	MostWantedEnabled             bool                    `json:"mostWantedEnabled" bson:"mostWantedEnabled"`
	MostWantedSidebarName         string                  `json:"mostWantedSidebarName" bson:"mostWantedSidebarName"`
	MostWantedVisibleFields       []string                `json:"mostWantedVisibleFields" bson:"mostWantedVisibleFields"`
	MostWantedCustomFields        []MostWantedCustomField `json:"mostWantedCustomFields" bson:"mostWantedCustomFields"`
	Economy                       EconomySettings         `json:"economy" bson:"economy"`
	RankSettings                  RankSettings            `json:"rankSettings" bson:"rankSettings"`
	// RpPromotion holds the community's last "RP server promotion" Discord post
	// and the timestamp used to enforce the once-per-cooldown posting gate.
	// Pointer so legacy communities that never promoted serialize it as absent.
	RpPromotion *RpPromotion `json:"rpPromotion,omitempty" bson:"rpPromotion,omitempty"`
	// Soft-delete fields. When PendingDeletionAt is set, the community is hidden
	// from all customer-facing surfaces and the scheduler hard-deletes it once
	// ScheduledDeletionAt elapses. The community owner cannot self-restore;
	// staff handle restores through the admin console.
	PendingDeletionAt         *primitive.DateTime `json:"pendingDeletionAt,omitempty" bson:"pendingDeletionAt,omitempty"`
	ScheduledDeletionAt       *primitive.DateTime `json:"scheduledDeletionAt,omitempty" bson:"scheduledDeletionAt,omitempty"`
	PendingDeletionNotifiedAt *primitive.DateTime `json:"pendingDeletionNotifiedAt,omitempty" bson:"pendingDeletionNotifiedAt,omitempty"`
	DeletionRequestedBy       string              `json:"deletionRequestedBy,omitempty" bson:"deletionRequestedBy,omitempty"`
	CreatedAt                 primitive.DateTime  `json:"createdAt" bson:"createdAt"`
	UpdatedAt                 primitive.DateTime  `json:"updatedAt" bson:"updatedAt"`
}

// RpPromotion tracks a community's recruitment/promotion posts to the shared
// Discord rp-servers channel. LastPostedAt drives the posting cooldown;
// History keeps the most recent posts so an admin can review, reuse as a
// template, and (in a future iteration) edit a still-live message.
type RpPromotion struct {
	LastPostedAt *primitive.DateTime `json:"lastPostedAt,omitempty" bson:"lastPostedAt,omitempty"`
	History      []RpPromotionPost   `json:"history,omitempty" bson:"history,omitempty"`
}

// RpPromotionPost is a single posted promotion, retained in RpPromotion.History.
// MessageID is the Discord message ID captured from the webhook response — it
// is what a future "edit your live post" feature will PATCH.
type RpPromotionPost struct {
	ID           string             `json:"id" bson:"id"` // ObjectID hex, stable handle for the post
	PostedAt     primitive.DateTime `json:"postedAt" bson:"postedAt"`
	PostedBy     string             `json:"postedBy" bson:"postedBy"`                                   // user ID of the posting admin
	PostedByName string             `json:"postedByName,omitempty" bson:"postedByName,omitempty"`       // username captured at post time, for the history audit trail
	Tier         string             `json:"tier" bson:"tier"`                                           // boost tier key at post time
	MessageID string             `json:"messageId,omitempty" bson:"messageId,omitempty"` // Discord message ID
	ChannelID string             `json:"channelId,omitempty" bson:"channelId,omitempty"` // Discord channel ID, for building a jump link
	Data      RpPromotionData    `json:"data" bson:"data"`

	// Soft-removal fields, set when a staff admin removes a promotion from the
	// Discord channel via the moderation console. The entry is retained (not
	// $pull-ed) so it survives as evidence for any offense issued against the
	// poster. RemovedAt being non-nil means the live Discord message is gone.
	RemovedAt    *primitive.DateTime `json:"removedAt,omitempty" bson:"removedAt,omitempty"`
	RemovedBy    string              `json:"removedBy,omitempty" bson:"removedBy,omitempty"` // admin email
	RemoveReason string              `json:"removeReason,omitempty" bson:"removeReason,omitempty"`
}

// RpPromotionData is the structured content of an RP server promotion post.
// It is what the website form collects, what the Discord embed is built from,
// and what gets echoed back so the form can be pre-filled on the next visit.
type RpPromotionData struct {
	ServerName   string   `json:"serverName" bson:"serverName"`
	Consoles     []string `json:"consoles" bson:"consoles"` // e.g. "PS5", "Xbox", "PC"
	Game         string   `json:"game" bson:"game"`         // e.g. "GTA RP", "Roblox RP"
	Description  string   `json:"description" bson:"description"`
	Departments  []string `json:"departments" bson:"departments"`
	Features     []string `json:"features" bson:"features"`
	Requirements string   `json:"requirements,omitempty" bson:"requirements,omitempty"`
	InviteURL    string   `json:"inviteUrl" bson:"inviteUrl"`
	BannerImage  string   `json:"bannerImage,omitempty" bson:"bannerImage,omitempty"` // boosted only
	Images       []string `json:"images,omitempty" bson:"images,omitempty"`
}

// MostWantedCustomField holds the structure for a custom field on the most wanted list
type MostWantedCustomField struct {
	ID       primitive.ObjectID `json:"_id" bson:"_id"`
	Name     string             `json:"name" bson:"name"`
	Required bool               `json:"required" bson:"required"`
}

// CommunityAnalytics holds the structure for community analytics
type CommunityAnalytics struct {
	CardViews     int `json:"cardViews" bson:"cardViews"`
	HomePageViews int `json:"homePageViews" bson:"homePageViews"`
}

// CommunityFine holds the structure for community fines
type CommunityFine struct {
	Categories []Category `json:"categories" bson:"categories"`
	Currency   string     `json:"currency" bson:"currency"` // USD, EUR, etc.
}

// Category holds the structure for a category
type Category struct {
	Name  string        `json:"name" bson:"name"` // Traffic Citations, Misdeameanors, etc.
	Fines []FineDetails `json:"fines" bson:"fines"`
}

// FineDetails holds the structure for a fine detail
type FineDetails struct {
	Name   string `json:"name" bson:"name"`     // Speeding, Public Intoxication, etc.
	Amount int    `json:"amount" bson:"amount"` // 50, 100, etc.
}

// CurrencyOption holds a currency code and its display symbol
type CurrencyOption struct {
	Code    string `json:"code" bson:"code"`
	Symbol  string `json:"symbol" bson:"symbol"`
	Builtin bool   `json:"builtin" bson:"builtin"`
}

// CommunityPenalCode holds the structure for community penal codes
type CommunityPenalCode struct {
	Categories []PenalCodeCategory `json:"categories" bson:"categories"`
	Currency   string              `json:"currency" bson:"currency"`     // active currency code
	Currencies []CurrencyOption    `json:"currencies" bson:"currencies"` // available currencies
}

// PenalCodeCategory holds the structure for a category of penal code violations
type PenalCodeCategory struct {
	ID         string               `json:"id" bson:"id"`
	Name       string               `json:"name" bson:"name"`
	Subtitle   string               `json:"subtitle" bson:"subtitle"`
	Icon       string               `json:"icon" bson:"icon"`
	Color      string               `json:"color" bson:"color"`
	Columns    []string             `json:"columns" bson:"columns"`
	Violations []PenalCodeViolation `json:"violations" bson:"violations"`
}

// PenalCodeViolation holds the structure for a single penal code violation
type PenalCodeViolation struct {
	Name        string `json:"name" bson:"name"`
	JailTime    string `json:"jailTime" bson:"jailTime"`
	Fine        int    `json:"fine" bson:"fine"`
	Explanation string `json:"explanation,omitempty" bson:"explanation,omitempty"`
}

// UnmarshalBSONValue handles backward compatibility for the Fine field,
// which was previously stored as a string like "$500" and is now an int.
func (v *PenalCodeViolation) UnmarshalBSONValue(t bsontype.Type, data []byte) error {
	// Decode into a raw document so we can handle the fine field specially
	if t != bsontype.EmbeddedDocument {
		return fmt.Errorf("expected embedded document, got %v", t)
	}

	raw := bson.Raw(data)

	v.Name = raw.Lookup("name").StringValue()

	if jt, ok := raw.Lookup("jailTime").StringValueOK(); ok {
		v.JailTime = jt
	}

	if exp, ok := raw.Lookup("explanation").StringValueOK(); ok {
		v.Explanation = exp
	}

	// Handle fine: could be string ("$500") or int32/int64 (500) or missing
	fineVal := raw.Lookup("fine")
	switch fineVal.Type {
	case bsontype.String:
		s := fineVal.StringValue()
		s = strings.TrimPrefix(s, "$")
		s = strings.TrimSpace(s)
		if s != "" {
			if n, err := strconv.Atoi(s); err == nil {
				v.Fine = n
			}
		}
	case bsontype.Int32:
		v.Fine = int(fineVal.Int32())
	case bsontype.Int64:
		v.Fine = int(fineVal.AsInt64())
	case bsontype.Double:
		v.Fine = int(fineVal.Double())
	}

	return nil
}

// MemberDetail holds the structure for a member detail
type MemberDetail struct {
	DepartmentID         string            `json:"departmentID" bson:"departmentID"`
	TenCodeID            string            `json:"tenCodeID" bson:"tenCodeID"`
	IsOnline             bool              `json:"isOnline" bson:"isOnline"`
	ActiveDepartmentID   string            `json:"activeDepartmentId" bson:"activeDepartmentId"`
	ActiveDepartmentName string            `json:"activeDepartmentName" bson:"activeDepartmentName"`
	DepartmentCallSigns  map[string]string `json:"departmentCallSigns,omitempty" bson:"departmentCallSigns,omitempty"`
}

// RankRequirement defines a single threshold for rank eligibility.
// For tracked metrics: MetricType is a registry key (e.g. "citations_issued"), Threshold is the target value.
// For custom requirements: MetricType is "custom", CustomLabel is the free-text description, Threshold is ignored.
type RankRequirement struct {
	ID          string `json:"id,omitempty" bson:"id,omitempty"`                   // stable identifier for tracking completion
	MetricType  string `json:"metricType" bson:"metricType"`                       // e.g. "citations_issued" or "custom"
	Threshold   int    `json:"threshold" bson:"threshold"`                         // e.g. 50 (ignored for custom)
	CustomLabel string `json:"customLabel,omitempty" bson:"customLabel,omitempty"` // free-text label for custom requirements
}

// RankSettings holds community-wide rank/promotion configuration.
type RankSettings struct {
	// ResetStatsOnPromotion, when true, measures rank requirement progress from the
	// member's most recent rank assignment (MemberStatus.RankAssignedAt) instead of
	// all-time. Promotions then require a fresh count of metrics per rank, and met
	// custom requirements are cleared on promotion. Default false preserves the
	// historical all-time behavior so existing communities are unaffected.
	ResetStatsOnPromotion bool `json:"resetStatsOnPromotion" bson:"resetStatsOnPromotion"`
}

// Rank defines a configurable LEO rank within a department
type Rank struct {
	ID             primitive.ObjectID `json:"_id" bson:"_id"`
	Name           string             `json:"name" bson:"name"`                         // e.g. "Sergeant"
	Prefix         string             `json:"prefix,omitempty" bson:"prefix,omitempty"` // e.g. "PD", "SO"
	DisplayOrder   int                `json:"displayOrder" bson:"displayOrder"`         // lower = higher rank
	Requirements   []RankRequirement  `json:"requirements" bson:"requirements"`
	AutoPromote    bool               `json:"autoPromote" bson:"autoPromote"`
	CanViewStats   bool               `json:"canViewStats" bson:"canViewStats"`     // can view department metrics
	IsDefault      bool               `json:"isDefault" bson:"isDefault"`           // unranked members get this rank
	PayRatePerHour Cents              `json:"payRatePerHour" bson:"payRatePerHour"` // Economy: hourly pay in cents; 0 falls back to Department.BasePayPerHour. Cents = defensive decode (see type doc).
}

// Department holds the structure for a department
type Department struct {
	ID               primitive.ObjectID `json:"_id" bson:"_id"`
	Name             string             `json:"name" bson:"name"`
	Description      string             `json:"description" bson:"description"`
	Image            string             `json:"image" bson:"image"`
	ApprovalRequired bool               `json:"approvalRequired" bson:"approvalRequired"`
	Members          []MemberStatus     `json:"members" bson:"members"`
	Ranks            []Rank             `json:"ranks" bson:"ranks"`
	Template         Template           `json:"template" bson:"template"`                       // Legacy embedded template (for backward compatibility)
	TemplateRef      *TemplateReference `json:"templateRef" bson:"templateRef"`                 // New template reference system
	ToneSound        string             `json:"toneSound,omitempty" bson:"toneSound,omitempty"` // "leo", "fd", "ems", or "" (uses template default)
	// RestrictCivilianRecordDeletion gates civilian-initiated deletion of records
	// (citations, written warnings, arrest reports) issued by this department.
	// When true, only community owner / "administrator" / "manage records" can delete.
	// Stored as a pointer so we can distinguish "explicitly false" (allow) from
	// "missing on a legacy department" (also treated as allow, preserves prior behavior).
	// New departments are created with this set to true.
	RestrictCivilianRecordDeletion *bool `json:"restrictCivilianRecordDeletion,omitempty" bson:"restrictCivilianRecordDeletion,omitempty"`
	// Economy fields
	EconomyEnabled           bool               `json:"economyEnabled" bson:"economyEnabled"`
	BasePayPerHour           Cents              `json:"basePayPerHour" bson:"basePayPerHour"`                     // cents/hr, fallback if rank pay is 0. Cents = defensive decode (see type doc).
	MaxSessionMinutes        int                `json:"maxSessionMinutes" bson:"maxSessionMinutes"`               // default 120
	AfkPromptIntervalSeconds int                `json:"afkPromptIntervalSeconds" bson:"afkPromptIntervalSeconds"` // e.g. 600
	AfkGraceSeconds          int                `json:"afkGraceSeconds" bson:"afkGraceSeconds"`                   // e.g. 60
	PayoutMode               string             `json:"payoutMode" bson:"payoutMode"`                             // "on_heartbeat" | "on_clockout"
	CreatedAt                primitive.DateTime `json:"createdAt" bson:"createdAt"`
	UpdatedAt                primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
	OnlineMemberCount        int                `json:"onlineMemberCount" bson:"onlineMemberCount"`
}

// EconomySettings holds community-wide economy config.
type EconomySettings struct {
	Enabled                bool   `json:"enabled" bson:"enabled"`
	DefaultStartingBalance int64  `json:"defaultStartingBalance" bson:"defaultStartingBalance"` // cents
	FineMode               string `json:"fineMode" bson:"fineMode"`                             // "inbox" | "auto_debit"
	AllowNegativeBalance   bool   `json:"allowNegativeBalance" bson:"allowNegativeBalance"`
	DefaultDueDays         int    `json:"defaultDueDays" bson:"defaultDueDays"`             // days before inbox item flips delinquent
	ContestExtensionDays   int    `json:"contestExtensionDays" bson:"contestExtensionDays"` // days the due date is pushed when a civilian contests a fine
}

// TenCodes holds the structure for ten-codes used in departments
type TenCodes struct {
	ID          primitive.ObjectID `json:"_id" bson:"_id"`
	Code        string             `json:"code" bson:"code"`
	Description string             `json:"description" bson:"description"`
}

// Template holds the structure for a department template
type Template struct {
	ID          primitive.ObjectID `json:"_id" bson:"_id"`
	Name        string             `json:"name" bson:"name"`
	Description string             `json:"description" bson:"description"`
	Components  []Component        `json:"components" bson:"components"`
}

// Component holds the structure for a template component
type Component struct {
	ID      primitive.ObjectID `json:"_id" bson:"_id"`
	Name    string             `json:"name" bson:"name"`
	Enabled bool               `json:"enabled" bson:"enabled"`
}

// Event holds the structure for an event
type Event struct {
	ID            primitive.ObjectID `json:"_id" bson:"_id"`
	CommunityID   string             `json:"communityID" bson:"communityID"`
	Title         string             `json:"title" bson:"title"`
	ScheduledDate string             `json:"scheduledDate" bson:"scheduledDate"`
	Image         string             `json:"image" bson:"image"`
	Location      string             `json:"location" bson:"location"`
	Description   string             `json:"description" bson:"description"`
	Host          string             `json:"host" bson:"host"`
	CoHosts       []string           `json:"coHosts" bson:"coHosts"`
	Required      bool               `json:"required" bson:"required"`
	Attendance    Attendance         `json:"attendance" bson:"attendance"`
	CreatedAt     primitive.DateTime `json:"createdAt" bson:"createdAt"`
	UpdatedAt     primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
}

// MemberStatus holds the structure for a member status
type MemberStatus struct {
	UserID                string             `json:"userID" bson:"userID"`
	Status                string             `json:"status" bson:"status"`
	TenCodeID             string             `json:"tenCodeID" bson:"tenCodeID"`
	RankID                string             `json:"rankId,omitempty" bson:"rankId,omitempty"`
	RankAssignedAt        primitive.DateTime `json:"rankAssignedAt,omitempty" bson:"rankAssignedAt,omitempty"`
	RankAssignmentType    string             `json:"rankAssignmentType,omitempty" bson:"rankAssignmentType,omitempty"` // "auto" or "manual"
	CustomRequirementsMet []string           `json:"customRequirementsMet,omitempty" bson:"customRequirementsMet,omitempty"`
}

// Attendance holds the structure for attendance
type Attendance struct {
	Confirmed []string `json:"confirmed" bson:"confirmed"`
	Maybe     []string `json:"maybe" bson:"maybe"`
	Declined  []string `json:"declined" bson:"declined"`
}

// Role holds the structure for a role
type Role struct {
	ID          primitive.ObjectID `json:"_id" bson:"_id"`
	Name        string             `json:"name" bson:"name"`
	Members     []string           `json:"members" bson:"members"`
	Permissions []Permission       `json:"permissions" bson:"permissions"`
}

// Permission holds the structure for a permission
type Permission struct {
	ID          primitive.ObjectID `json:"_id" bson:"_id"`
	Name        string             `json:"name" bson:"name"`
	Description string             `json:"description" bson:"description"`
	Enabled     bool               `json:"enabled" bson:"enabled"`
}

// Signal100Data holds the structure for Signal 100 state on a community
type Signal100Data struct {
	Active                bool   `json:"active" bson:"active"`
	ActivatedByUserId     string `json:"activatedByUserId,omitempty" bson:"activatedByUserId,omitempty"`
	ActivatedByUsername   string `json:"activatedByUsername,omitempty" bson:"activatedByUsername,omitempty"`
	ActivatedByCallSign   string `json:"activatedByCallSign,omitempty" bson:"activatedByCallSign,omitempty"`
	ActivatedByDepartment string `json:"activatedByDepartment,omitempty" bson:"activatedByDepartment,omitempty"`
	ActivatedAt           string `json:"activatedAt,omitempty" bson:"activatedAt,omitempty"`
	ClearedByUserId       string `json:"clearedByUserId,omitempty" bson:"clearedByUserId,omitempty"`
	ClearedByUsername     string `json:"clearedByUsername,omitempty" bson:"clearedByUsername,omitempty"`
	ClearedByCallSign     string `json:"clearedByCallSign,omitempty" bson:"clearedByCallSign,omitempty"`
	ClearedAt             string `json:"clearedAt,omitempty" bson:"clearedAt,omitempty"`
}

// ToneLog represents a single dispatch tone event, stored in the tone_logs collection.
type ToneLog struct {
	ID                  primitive.ObjectID `json:"_id" bson:"_id,omitempty"`
	CommunityID         string             `json:"communityId" bson:"communityId"`
	ToneType            string             `json:"toneType" bson:"toneType"`
	ToneName            string             `json:"toneName" bson:"toneName"`
	TargetDeptIDs       []string           `json:"targetDeptIds" bson:"targetDeptIds"`
	TriggeredByID       string             `json:"triggeredById" bson:"triggeredById"`
	TriggeredByName     string             `json:"triggeredByName" bson:"triggeredByName"`
	TriggeredByCallSign string             `json:"triggeredByCallSign" bson:"triggeredByCallSign"`
	CreatedAt           primitive.DateTime `json:"createdAt" bson:"createdAt"`
}

// CustomToneGroup defines a custom tone group within a community.
type CustomToneGroup struct {
	ID            primitive.ObjectID `json:"_id" bson:"_id"`
	Name          string             `json:"name" bson:"name"`
	DepartmentIDs []string           `json:"departmentIds" bson:"departmentIds"`
	ToneSound     string             `json:"toneSound" bson:"toneSound"` // "leo", "fd", or "ems"
	CreatedBy     string             `json:"createdBy" bson:"createdBy"`
	CreatedAt     primitive.DateTime `json:"createdAt" bson:"createdAt"`
}

// CustomToneSound represents a community-uploaded custom tone sound file.
type CustomToneSound struct {
	Key       string             `json:"key" bson:"key"`   // unique identifier, e.g. "custom_abc123"
	Name      string             `json:"name" bson:"name"` // display name, e.g. "Station 5 Fire Tone"
	URL       string             `json:"url" bson:"url"`   // Cloudinary URL
	CreatedAt primitive.DateTime `json:"createdAt" bson:"createdAt"`
}

// PanicAlert holds the structure for a panic alert
type PanicAlert struct {
	AlertID        string              `json:"alertId" bson:"alertId"`
	UserID         string              `json:"userId" bson:"userId"`
	Username       string              `json:"username" bson:"username"`
	CallSign       string              `json:"callSign" bson:"callSign"`
	DepartmentType string              `json:"departmentType" bson:"departmentType"`
	CommunityID    string              `json:"communityId" bson:"communityId"`
	TriggeredAt    primitive.DateTime  `json:"triggeredAt" bson:"triggeredAt"`
	Status         string              `json:"status" bson:"status"` // "active", "cleared"
	ClearedBy      *string             `json:"clearedBy" bson:"clearedBy"`
	ClearedAt      *primitive.DateTime `json:"clearedAt" bson:"clearedAt"`
}
