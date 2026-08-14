package models

import (
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// ContentCreatorApplication represents an application to the Content Creator Program
type ContentCreatorApplication struct {
	ID              primitive.ObjectID       `json:"_id" bson:"_id"`
	UserID          primitive.ObjectID       `json:"userId" bson:"userId"`
	DisplayName     string                   `json:"displayName" bson:"displayName"`
	PrimaryPlatform string                   `json:"primaryPlatform" bson:"primaryPlatform"` // twitch, youtube, tiktok, other
	Platforms       []ContentCreatorPlatform `json:"platforms" bson:"platforms"`
	Description     string                   `json:"description" bson:"description"` // for admin evaluation
	Bio             string                   `json:"bio" bson:"bio"`                 // for public profile (max 500 chars)
	Status          string                   `json:"status" bson:"status"`           // submitted, under_review, approved, rejected, withdrawn
	RejectionReason string                   `json:"rejectionReason,omitempty" bson:"rejectionReason,omitempty"`
	AdminNotes      string                   `json:"adminNotes,omitempty" bson:"adminNotes,omitempty"`
	Feedback        string                   `json:"feedback,omitempty" bson:"feedback,omitempty"`
	FirstApprovalBy *primitive.ObjectID      `json:"firstApprovalBy,omitempty" bson:"firstApprovalBy,omitempty"`
	FirstApprovalAt *primitive.DateTime      `json:"firstApprovalAt,omitempty" bson:"firstApprovalAt,omitempty"`
	ReviewedBy      *primitive.ObjectID      `json:"reviewedBy,omitempty" bson:"reviewedBy,omitempty"`
	ReviewedAt      *primitive.DateTime      `json:"reviewedAt,omitempty" bson:"reviewedAt,omitempty"`
	CreatorID       *primitive.ObjectID      `json:"creatorId,omitempty" bson:"creatorId,omitempty"` // set when approved
	CreatedAt       primitive.DateTime       `json:"createdAt" bson:"createdAt"`
	UpdatedAt       primitive.DateTime       `json:"updatedAt" bson:"updatedAt"`

	// Automated screening. A scheduled job re-runs the machine-checkable parts
	// of review (does the channel exist, is it owned by the applicant, does it
	// clear the follower minimum) so human attention is only spent on
	// applications that already passed. Admins are notified when Checks flip to
	// passed, NOT on submit — a bogus application should never reach an inbox.
	Checks          []ApplicationCheck  `json:"checks,omitempty" bson:"checks,omitempty"`
	ChecksPassed    bool                `json:"checksPassed" bson:"checksPassed"`
	ChecksLastRunAt *primitive.DateTime `json:"checksLastRunAt,omitempty" bson:"checksLastRunAt,omitempty"`
	CheckAttempts   int                 `json:"checkAttempts,omitempty" bson:"checkAttempts,omitempty"`
	// AdminNotifiedAt makes the review email fire exactly once.
	AdminNotifiedAt *primitive.DateTime `json:"adminNotifiedAt,omitempty" bson:"adminNotifiedAt,omitempty"`
	// ApplicantNotifiedAt marks that the applicant has been told their checks
	// failed. Without it an application that fails screening notifies nobody:
	// admins are only emailed on pass, and the applicant is only emailed on
	// rejection, so a failed-but-not-rejected application sat silently forever.
	ApplicantNotifiedAt *primitive.DateTime `json:"applicantNotifiedAt,omitempty" bson:"applicantNotifiedAt,omitempty"`
	// ChecksSettled marks screening as finished either way, so "nobody has been
	// told about this" is a state that cannot persist.
	ChecksSettled bool `json:"checksSettled" bson:"checksSettled"`

	// ReviewChecklist is the human half of review: the judgement calls no
	// automated check can make. Stored on the application so a second reviewer
	// sees what the first already confirmed, and every toggle is written to the
	// admin audit log.
	ReviewChecklist []ReviewChecklistItem `json:"reviewChecklist,omitempty" bson:"reviewChecklist,omitempty"`
}

// ReviewChecklistItem is one thing a human has to confirm before approving.
type ReviewChecklistItem struct {
	Key       string              `json:"key" bson:"key"`
	Checked   bool                `json:"checked" bson:"checked"`
	CheckedBy *primitive.ObjectID `json:"checkedBy,omitempty" bson:"checkedBy,omitempty"`
	CheckedAt *primitive.DateTime `json:"checkedAt,omitempty" bson:"checkedAt,omitempty"`
}

// The reviewer checklist. Keys are stable; the wording lives with the UI so it
// can be reworded without migrating stored data.
const (
	ReviewCheckAutomated = "automated_passed"
	ReviewCheckRPContent = "rp_content"
	ReviewCheckGenuine   = "genuine_content"
)

// ReviewChecklistKeys is the canonical set, in the order a reviewer works
// through them.
var ReviewChecklistKeys = []string{
	ReviewCheckAutomated,
	ReviewCheckRPContent,
	ReviewCheckGenuine,
}

// IsReviewChecklistKey guards the override endpoint against arbitrary keys
// being written into the document.
func IsReviewChecklistKey(key string) bool {
	for _, k := range ReviewChecklistKeys {
		if k == key {
			return true
		}
	}
	return false
}

// ReviewChecklistComplete reports whether every item has been confirmed.
func ReviewChecklistComplete(items []ReviewChecklistItem) bool {
	done := map[string]bool{}
	for _, it := range items {
		if it.Checked {
			done[it.Key] = true
		}
	}
	for _, k := range ReviewChecklistKeys {
		if !done[k] {
			return false
		}
	}
	return true
}

// Automated check keys and statuses.
const (
	CheckChannelResolves = "channel_resolves"
	CheckOwnership       = "ownership"
	CheckFollowers       = "followers"

	CheckPending = "pending" // still working, will retry
	CheckPassed  = "passed"
	CheckFailed  = "failed" // applicant must act; reason explains what
	CheckManual  = "manual" // no public API, a human confirms

	// MinFollowers is the program's follower requirement. Previously a bare 500
	// repeated across the scheduler.
	MinFollowers = 500
)

// ApplicationCheck is one automated check against one platform entry. The
// applicant sees these on their dashboard, so Reason is written for them, not
// for a log.
type ApplicationCheck struct {
	Key       string              `json:"key" bson:"key"`
	Platform  string              `json:"platform" bson:"platform"`
	Handle    string              `json:"handle" bson:"handle"`
	Status    string              `json:"status" bson:"status"`
	Reason    string              `json:"reason,omitempty" bson:"reason,omitempty"`
	CheckedAt *primitive.DateTime `json:"checkedAt,omitempty" bson:"checkedAt,omitempty"`
}

// ContentCreator represents an approved content creator in the program
type ContentCreator struct {
	ID              primitive.ObjectID       `json:"_id" bson:"_id"`
	UserID          *primitive.ObjectID      `json:"userId,omitempty" bson:"userId,omitempty"` // nullable if creator left platform
	ApplicationID   primitive.ObjectID       `json:"applicationId" bson:"applicationId"`
	DisplayName     string                   `json:"displayName" bson:"displayName"`
	Slug            string                   `json:"slug" bson:"slug"` // unique URL-friendly identifier
	ProfileImage    string                   `json:"profileImage,omitempty" bson:"profileImage,omitempty"`
	Bio             string                   `json:"bio" bson:"bio"`
	ThemeColor      string                   `json:"themeColor" bson:"themeColor"` // hex color for profile (e.g. #fbbf24)
	PrimaryPlatform string                   `json:"primaryPlatform" bson:"primaryPlatform"`
	Platforms       []ContentCreatorPlatform `json:"platforms" bson:"platforms"`
	Status          string                   `json:"status" bson:"status"` // active, warned, pending_removal, removed
	Featured        bool                     `json:"featured" bson:"featured"`
	WarnedAt        *primitive.DateTime      `json:"warnedAt,omitempty" bson:"warnedAt,omitempty"`
	WarningMessage  string                   `json:"warningMessage,omitempty" bson:"warningMessage,omitempty"`
	WarningReason   string                   `json:"warningReason,omitempty" bson:"warningReason,omitempty"`
	RemovalReason   string                   `json:"removalReason,omitempty" bson:"removalReason,omitempty"`
	JoinedAt        primitive.DateTime       `json:"joinedAt" bson:"joinedAt"`
	RemovedAt       *primitive.DateTime      `json:"removedAt,omitempty" bson:"removedAt,omitempty"`
	// Grace period fields for low follower tracking
	GracePeriodStartedAt  *primitive.DateTime `json:"gracePeriodStartedAt,omitempty" bson:"gracePeriodStartedAt,omitempty"`
	GracePeriodEndsAt     *primitive.DateTime `json:"gracePeriodEndsAt,omitempty" bson:"gracePeriodEndsAt,omitempty"`
	GracePeriodNotifiedAt *primitive.DateTime `json:"gracePeriodNotifiedAt,omitempty" bson:"gracePeriodNotifiedAt,omitempty"` // For reminder email tracking
	LastSyncedAt          *primitive.DateTime `json:"lastSyncedAt,omitempty" bson:"lastSyncedAt,omitempty"`
	CreatedAt             primitive.DateTime  `json:"createdAt" bson:"createdAt"`
	UpdatedAt             primitive.DateTime  `json:"updatedAt" bson:"updatedAt"`
}

// ContentCreatorPlatform represents a social media platform connection
type ContentCreatorPlatform struct {
	Type   string `json:"type" bson:"type"` // twitch, youtube, tiktok, other
	URL    string `json:"url" bson:"url"`
	Handle string `json:"handle" bson:"handle"`
	// FollowerCount is the trusted count. Until the channel is verified it holds
	// whatever the applicant typed; verification overwrites it with the real
	// figure read from the platform.
	FollowerCount   int  `json:"followerCount" bson:"followerCount"`
	VerifiedByAdmin bool `json:"verifiedByAdmin" bson:"verifiedByAdmin"`

	// Channel ownership verification. The applicant puts VerificationCode in
	// their channel description, we read the public channel and look for it.
	// Without this every field above is self-asserted — someone applied with a
	// channel they did not own, and the follower count was equally unchecked.
	VerificationCode          string              `json:"verificationCode,omitempty" bson:"verificationCode,omitempty"`
	VerificationCodeExpiresAt *primitive.DateTime `json:"verificationCodeExpiresAt,omitempty" bson:"verificationCodeExpiresAt,omitempty"`
	VerificationStatus        string              `json:"verificationStatus,omitempty" bson:"verificationStatus,omitempty"` // unverified | pending | verified | failed
	VerificationError         string              `json:"verificationError,omitempty" bson:"verificationError,omitempty"`
	VerifiedAt                *primitive.DateTime `json:"verifiedAt,omitempty" bson:"verifiedAt,omitempty"`
	VerificationMethod        string              `json:"verificationMethod,omitempty" bson:"verificationMethod,omitempty"` // api | admin
	VerifiedBy                *primitive.ObjectID `json:"verifiedBy,omitempty" bson:"verifiedBy,omitempty"`                 // admin who vouched, when method is admin
	// ReportedFollowerCount preserves what the applicant claimed, so a large gap
	// between claim and reality is visible to reviewers instead of overwritten.
	ReportedFollowerCount int `json:"reportedFollowerCount,omitempty" bson:"reportedFollowerCount,omitempty"`

	// Manual check budget. Every Check press spends a unit of a SHARED daily
	// platform API quota, so one impatient or malicious applicant must not be
	// able to exhaust it for everyone. Tracked per platform entry and keyed to
	// the application, which an IP-based limiter at the proxy cannot be — that
	// one is bypassed by rotating IPs and punishes users behind a shared NAT.
	VerificationLastCheckedAt   *primitive.DateTime `json:"verificationLastCheckedAt,omitempty" bson:"verificationLastCheckedAt,omitempty"`
	VerificationCheckCount      int                 `json:"verificationCheckCount,omitempty" bson:"verificationCheckCount,omitempty"`
	VerificationCheckWindowFrom *primitive.DateTime `json:"verificationCheckWindowFrom,omitempty" bson:"verificationCheckWindowFrom,omitempty"`
}

// Manual verification check budget, per platform entry.
//
// A real applicant saves their channel then checks: a short cooldown is
// invisible to them. The daily cap bounds the worst case so a single
// application cannot spend a meaningful share of the platform quota.
const (
	VerifyCheckCooldown  = 30 * time.Second
	VerifyCheckDailyCap  = 25
	VerifyCheckCapWindow = 24 * time.Hour
)

// VerifyCheckBudget reports whether a manual check may run now.
//
// Returns ok=false with a retryAfter and a reason written for the applicant.
// Callers must not spend a platform API call when this says no.
func (p ContentCreatorPlatform) VerifyCheckBudget(now time.Time) (ok bool, retryAfter time.Duration, reason string) {
	if p.VerificationLastCheckedAt != nil {
		since := now.Sub(p.VerificationLastCheckedAt.Time())
		if since < VerifyCheckCooldown {
			wait := VerifyCheckCooldown - since
			return false, wait, fmt.Sprintf(
				"Please wait %d seconds before checking again.", int(wait.Seconds())+1)
		}
	}

	// The cap is a rolling window: an expired window resets the count, so a
	// legitimate applicant coming back the next day is never stuck.
	if p.VerificationCheckWindowFrom != nil &&
		now.Sub(p.VerificationCheckWindowFrom.Time()) < VerifyCheckCapWindow &&
		p.VerificationCheckCount >= VerifyCheckDailyCap {
		wait := VerifyCheckCapWindow - now.Sub(p.VerificationCheckWindowFrom.Time())
		return false, wait, "You have checked this channel a lot today. We keep checking automatically every few hours, so you can leave the code in place and we will pick it up."
	}

	return true, 0, ""
}

// NextVerifyCheckCounters returns the counters to persist after a check runs,
// rolling the window when the previous one has expired.
func (p ContentCreatorPlatform) NextVerifyCheckCounters(now time.Time) (count int, windowFrom time.Time) {
	if p.VerificationCheckWindowFrom == nil ||
		now.Sub(p.VerificationCheckWindowFrom.Time()) >= VerifyCheckCapWindow {
		return 1, now
	}
	return p.VerificationCheckCount + 1, p.VerificationCheckWindowFrom.Time()
}

// Platform verification statuses.
const (
	PlatformUnverified = "unverified"
	PlatformPending    = "pending"
	PlatformVerified   = "verified"
	PlatformFailed     = "failed"
)

// IsVerified reports whether a platform entry has cleared ownership checks by
// either route. Treats a legacy VerifiedByAdmin tick as verified so records
// predating this system are not retroactively blocked.
func (p ContentCreatorPlatform) IsVerified() bool {
	return p.VerificationStatus == PlatformVerified || p.VerifiedByAdmin
}

// ContentCreatorEntitlement represents a plan entitlement granted to a creator
type ContentCreatorEntitlement struct {
	ID               primitive.ObjectID  `json:"_id" bson:"_id"`
	ContentCreatorID primitive.ObjectID  `json:"contentCreatorId" bson:"contentCreatorId"`
	TargetType       string              `json:"targetType" bson:"targetType"` // "user" or "community"
	TargetID         primitive.ObjectID  `json:"targetId" bson:"targetId"`
	Plan             string              `json:"plan" bson:"plan"`     // "base"
	Source           string              `json:"source" bson:"source"` // "content_creator_program"
	Active           bool                `json:"active" bson:"active"`
	GrantedAt        primitive.DateTime  `json:"grantedAt" bson:"grantedAt"`
	GrantedBy        primitive.ObjectID  `json:"grantedBy" bson:"grantedBy"`
	RevokedAt        *primitive.DateTime `json:"revokedAt,omitempty" bson:"revokedAt,omitempty"`
	RevokedBy        *primitive.ObjectID `json:"revokedBy,omitempty" bson:"revokedBy,omitempty"`
	RevokeReason     string              `json:"revokeReason,omitempty" bson:"revokeReason,omitempty"`
	CreatedAt        primitive.DateTime  `json:"createdAt" bson:"createdAt"`
	UpdatedAt        primitive.DateTime  `json:"updatedAt" bson:"updatedAt"`
}

// ContentCreatorFollowerSnapshot stores historical follower counts for compliance tracking
type ContentCreatorFollowerSnapshot struct {
	ID               primitive.ObjectID       `json:"_id" bson:"_id"`
	ContentCreatorID primitive.ObjectID       `json:"contentCreatorId" bson:"contentCreatorId"`
	Platforms        []ContentCreatorPlatform `json:"platforms" bson:"platforms"`
	TotalFollowers   int                      `json:"totalFollowers" bson:"totalFollowers"`
	MaxFollowers     int                      `json:"maxFollowers" bson:"maxFollowers"` // highest single platform count
	Source           string                   `json:"source" bson:"source"`             // "manual", "api", "admin"
	RecordedAt       primitive.DateTime       `json:"recordedAt" bson:"recordedAt"`
	RecordedBy       *primitive.ObjectID      `json:"recordedBy,omitempty" bson:"recordedBy,omitempty"`
}

// ContentCreatorSettings stores program configuration
type ContentCreatorSettings struct {
	ID                primitive.ObjectID `json:"_id" bson:"_id"`
	FollowerThreshold int                `json:"followerThreshold" bson:"followerThreshold"` // default 500
	GracePeriodDays   int                `json:"gracePeriodDays" bson:"gracePeriodDays"`     // default 60
	CheckFrequency    string             `json:"checkFrequency" bson:"checkFrequency"`       // "daily", "weekly", "monthly"
	UpdatedAt         primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
	UpdatedBy         primitive.ObjectID `json:"updatedBy" bson:"updatedBy"`
}

// --- Request DTOs ---

// CreateContentCreatorApplicationRequest is the request body for submitting an application
type CreateContentCreatorApplicationRequest struct {
	DisplayName     string                   `json:"displayName" validate:"required,min=2,max=50"`
	PrimaryPlatform string                   `json:"primaryPlatform" validate:"required,oneof=twitch youtube tiktok other"`
	Platforms       []ContentCreatorPlatform `json:"platforms" validate:"required,min=1,max=5"`
	Description     string                   `json:"description" validate:"required,min=50,max=1000"` // for admin evaluation
	// Bio is optional here and collected after acceptance, on the creator's own
	// profile. Most applications are declined, and writing public-profile copy
	// for a profile that never exists is wasted effort.
	Bio string `json:"bio" validate:"omitempty,max=500"`
}

// ReviewApplicationRequest is the request body for approving/rejecting an application
type ReviewApplicationRequest struct {
	Status          string `json:"status" validate:"required,oneof=approved rejected"`
	RejectionReason string `json:"rejectionReason,omitempty"`
	Feedback        string `json:"feedback,omitempty"`
	AdminNotes      string `json:"adminNotes,omitempty"`
}

// UpdateContentCreatorRequest is the request body for updating a creator profile
type UpdateContentCreatorRequest struct {
	DisplayName  string                   `json:"displayName,omitempty"`
	Bio          string                   `json:"bio,omitempty"`
	ThemeColor   string                   `json:"themeColor,omitempty"`
	ProfileImage string                   `json:"profileImage,omitempty"`
	Platforms    []ContentCreatorPlatform `json:"platforms,omitempty"`
	Featured     *bool                    `json:"featured,omitempty"`
}

// WarnCreatorRequest is the request body for issuing a warning
type WarnCreatorRequest struct {
	Reason  string `json:"reason" validate:"required"`
	Message string `json:"message" validate:"required"`
}

// RemoveCreatorRequest is the request body for removing a creator
type RemoveCreatorRequest struct {
	Reason string `json:"reason" validate:"required"`
}

// GrantEntitlementRequest is the request body for granting an entitlement
type GrantEntitlementRequest struct {
	TargetType string `json:"targetType" validate:"required,oneof=user community"`
	TargetID   string `json:"targetId" validate:"required"`
	Plan       string `json:"plan" validate:"required,oneof=base"`
}

// SyncFollowersRequest is the request body for syncing follower counts
type SyncFollowersRequest struct {
	Platforms []SyncPlatformEntry `json:"platforms" validate:"required,min=1,max=5"`
}

// SyncPlatformEntry represents a platform entry for syncing
type SyncPlatformEntry struct {
	Type          string `json:"type" validate:"required,oneof=twitch youtube tiktok other"`
	FollowerCount int    `json:"followerCount" validate:"required,min=0"`
}

// --- Response DTOs ---

// ContentCreatorPublicResponse is the public response for a content creator
type ContentCreatorPublicResponse struct {
	ID              primitive.ObjectID       `json:"_id"`
	DisplayName     string                   `json:"displayName"`
	Slug            string                   `json:"slug"`
	ProfileImage    string                   `json:"profileImage,omitempty"`
	Bio             string                   `json:"bio"`
	ThemeColor      string                   `json:"themeColor"`
	PrimaryPlatform string                   `json:"primaryPlatform"`
	Platforms       []ContentCreatorPlatform `json:"platforms"`
	Featured        bool                     `json:"featured"`
	JoinedAt        primitive.DateTime       `json:"joinedAt"`
}

// ContentCreatorPagination holds pagination metadata for content creator endpoints
type ContentCreatorPagination struct {
	CurrentPage int  `json:"currentPage"`
	TotalPages  int  `json:"totalPages"`
	TotalItems  int  `json:"totalItems"`
	HasNextPage bool `json:"hasNextPage"`
	HasPrevPage bool `json:"hasPrevPage"`
}

// ContentCreatorsListResponse is the response for listing content creators
type ContentCreatorsListResponse struct {
	Success    bool                           `json:"success"`
	Creators   []ContentCreatorPublicResponse `json:"creators"`
	Pagination ContentCreatorPagination       `json:"pagination"`
}

// ContentCreatorApplicationResponse is the response for an application
type ContentCreatorApplicationResponse struct {
	ID              primitive.ObjectID       `json:"_id"`
	DisplayName     string                   `json:"displayName"`
	PrimaryPlatform string                   `json:"primaryPlatform"`
	Platforms       []ContentCreatorPlatform `json:"platforms"`
	Description     string                   `json:"description"`
	Bio             string                   `json:"bio"`
	Status          string                   `json:"status"`
	Feedback        string                   `json:"feedback,omitempty"`
	CreatedAt       primitive.DateTime       `json:"createdAt"`
	ReviewedAt      *primitive.DateTime      `json:"reviewedAt,omitempty"`
	// What the automated checks found. Every check Reason is written for the
	// applicant to read, so these go back to them: the dashboard shows each
	// check with its reason, and without them the page has to guess at the
	// state from the platform records alone.
	Checks          []ApplicationCheck `json:"checks,omitempty"`
	ChecksPassed    bool               `json:"checksPassed"`
	RejectionReason string             `json:"rejectionReason,omitempty"`
}

// ContentCreatorMeResponse is the response for the current user's creator status
type ContentCreatorMeResponse struct {
	Success     bool                               `json:"success"`
	Application *ContentCreatorApplicationResponse `json:"application,omitempty"`
	Creator     *ContentCreatorPrivateResponse     `json:"creator,omitempty"`
}

// ContentCreatorPrivateResponse is the private response for a content creator (includes entitlements)
type ContentCreatorPrivateResponse struct {
	ID                   primitive.ObjectID       `json:"_id"`
	DisplayName          string                   `json:"displayName"`
	Slug                 string                   `json:"slug"`
	ProfileImage         string                   `json:"profileImage,omitempty"`
	Bio                  string                   `json:"bio"`
	ThemeColor           string                   `json:"themeColor"`
	PrimaryPlatform      string                   `json:"primaryPlatform"`
	Platforms            []ContentCreatorPlatform `json:"platforms"`
	Status               string                   `json:"status"`
	Featured             bool                     `json:"featured"`
	WarnedAt             *primitive.DateTime      `json:"warnedAt,omitempty"`
	WarningMessage       string                   `json:"warningMessage,omitempty"`
	JoinedAt             primitive.DateTime       `json:"joinedAt"`
	Entitlements         EntitlementsSummary      `json:"entitlements"`
	GracePeriodStartedAt *primitive.DateTime      `json:"gracePeriodStartedAt,omitempty"`
	GracePeriodEndsAt    *primitive.DateTime      `json:"gracePeriodEndsAt,omitempty"`
	LastSyncedAt         *primitive.DateTime      `json:"lastSyncedAt,omitempty"`
}

// EntitlementsSummary summarizes a creator's entitlements
type EntitlementsSummary struct {
	PersonalPlan         bool                 `json:"personalPlan"`
	PersonalPlanFallback bool                 `json:"personalPlanFallback"`      // true if user has higher plan, entitlement acts as fallback
	CurrentUserPlan      string               `json:"currentUserPlan,omitempty"` // user's current subscription plan
	CommunityPlan        CommunityPlanSummary `json:"communityPlan"`
}

// CommunityPlanSummary summarizes community plan entitlement
type CommunityPlanSummary struct {
	Active        bool   `json:"active"`
	CommunityName string `json:"communityName,omitempty"`
	CommunityID   string `json:"communityId,omitempty"`
}

// ContentCreatorAnalytics is the analytics response for admin
type ContentCreatorAnalytics struct {
	TotalCreators        int     `json:"totalCreators"`
	ActiveCreators       int     `json:"activeCreators"`
	WarnedCreators       int     `json:"warnedCreators"`
	PendingRemoval       int     `json:"pendingRemoval"`
	TotalApplications    int     `json:"totalApplications"`
	PendingApplications  int     `json:"pendingApplications"`
	ApprovedApplications int     `json:"approvedApplications"`
	RejectedApplications int     `json:"rejectedApplications"`
	TotalMonthlyValue    float64 `json:"totalMonthlyValue"`
	TotalYearlyValue     float64 `json:"totalYearlyValue"`
}

// PaginationInfo is defined in announcement.go - reusing that definition
