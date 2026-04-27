package models

import "go.mongodb.org/mongo-driver/bson/primitive"

// BetaFeedback captures the reason a user disables a beta feature flag, so
// the product team can see why opt-outs happen. Stored per-event (not per
// user) — each disable creates a new document. UserID is retained for
// follow-up but the admin UI surfaces feedback anonymously.
type BetaFeedback struct {
	ID        primitive.ObjectID `json:"_id"       bson:"_id"`
	UserID    string             `json:"userId"    bson:"userId"`
	// Flag is the user-preferences field name being toggled off, e.g.
	// "betaCommandDashboard" or "betaCommandDispatch".
	Flag      string             `json:"flag"      bson:"flag"`
	// Reason is one of a small enum surfaced in the disable modal:
	//   too_buggy | look_and_feel | preferred_old | missing_features |
	//   performance | other
	Reason    string             `json:"reason"    bson:"reason"`
	// Feedback is an optional free-form comment from the user.
	Feedback  string             `json:"feedback"  bson:"feedback"`
	// Context is the page the user was on when they disabled (e.g.
	// "/dispatch-dashboard", "/command-dashboard"). Helps triage bug
	// reports by where the pain happened.
	Context   string             `json:"context"   bson:"context"`
	CreatedAt primitive.DateTime `json:"createdAt" bson:"createdAt"`
}

// CreateBetaFeedbackRequest is the POST /api/v1/beta-feedback body.
type CreateBetaFeedbackRequest struct {
	UserID   string `json:"userId"   validate:"required"`
	Flag     string `json:"flag"     validate:"required,oneof=betaCommandDashboard betaCommandDispatch betaCivDashboard"`
	Reason   string `json:"reason"   validate:"required,oneof=too_buggy look_and_feel preferred_old missing_features performance other"`
	Feedback string `json:"feedback" validate:"omitempty,max=2000"`
	Context  string `json:"context"  validate:"omitempty,max=200"`
}
