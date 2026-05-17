package handlers

import (
	"encoding/json"
	"strings"
)

// stripeEnv maps Stripe's livemode bool to the audit-row environment string.
func stripeEnv(livemode bool) string {
	if livemode {
		return "PRODUCTION"
	}
	return "SANDBOX"
}

// extractStripeIdentifiers pulls the subscription id and customer id out of
// a Stripe webhook's raw data payload so we can attach the audit row to a
// user without coupling to the typed Stripe SDK structs (which vary by
// event type — Subscription vs Invoice vs Session). Returns ("", "") if
// the body is unparseable or the fields are absent.
func extractStripeIdentifiers(raw json.RawMessage) (subscriptionID, customerID string) {
	if len(raw) == 0 {
		return "", ""
	}
	var probe struct {
		ID           string `json:"id"`
		Object       string `json:"object"`
		Subscription string `json:"subscription"`
		Customer     string `json:"customer"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return "", ""
	}
	switch probe.Object {
	case "subscription":
		return probe.ID, probe.Customer
	case "invoice", "checkout.session":
		return probe.Subscription, probe.Customer
	default:
		// Fall back to whatever we got.
		if probe.Subscription != "" {
			return probe.Subscription, probe.Customer
		}
		return probe.ID, probe.Customer
	}
}

// mapStoreToSource converts a RevenueCat / Stripe store identifier into the
// `user.subscription.source` value we persist on the user document.
//
// RevenueCat sends store values in upper snake case
// ("PLAY_STORE", "APP_STORE", "STRIPE", "PROMOTIONAL", "MAC_APP_STORE", "AMAZON").
// The mobile app sends lower snake case ("play_store", "app_store").
//
// Returns "" for unknown stores so callers can omit the source update rather
// than overwriting it with garbage.
func mapStoreToSource(store string) string {
	switch strings.ToUpper(strings.TrimSpace(store)) {
	case "PLAY_STORE":
		return "play_store"
	case "APP_STORE", "MAC_APP_STORE":
		return "app_store"
	case "STRIPE":
		return "stripe"
	case "PROMOTIONAL":
		return "promotional"
	case "AMAZON":
		return "amazon"
	default:
		return ""
	}
}

// parseProductID extracts the plan ("base" | "premium" | "premium_plus") and
// billing cadence (isAnnual) from a RevenueCat / store product identifier.
//
// Accepted shapes (covering iOS + Play Store, including Play Store's colon-
// separated base:offer ids like "premium_plus_monthly:premium-plus-monthly"):
//
//	base_monthly, base_annual
//	premium_monthly, premium_annual
//	premium_plus_monthly, premium_plus_annual
//	premium_plus_monthly:premium-plus-monthly  (Play Store base:offer)
//	any of the above with a leading bundle/sku prefix
//
// Returns ("", false, false) if the id can't be parsed — callers must skip
// the plan/isAnnual update rather than downgrade the user.
func parseProductID(productID string) (plan string, isAnnual bool, ok bool) {
	id := strings.ToLower(strings.TrimSpace(productID))
	if id == "" {
		return "", false, false
	}

	// Play Store base:offer form — take base
	if i := strings.Index(id, ":"); i >= 0 {
		id = id[:i]
	}

	// Strip a leading bundle/sku prefix like "com.linesmerrill.lpc."
	if i := strings.LastIndex(id, "."); i >= 0 {
		id = id[i+1:]
	}

	switch {
	case strings.HasSuffix(id, "_annual"):
		isAnnual = true
		plan = strings.TrimSuffix(id, "_annual")
	case strings.HasSuffix(id, "_monthly"):
		isAnnual = false
		plan = strings.TrimSuffix(id, "_monthly")
	default:
		return "", false, false
	}

	switch plan {
	case "base", "premium", "premium_plus":
		return plan, isAnnual, true
	default:
		return "", false, false
	}
}
