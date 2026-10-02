package handlers

import (
	"crypto/subtle"
	"net/http"
	"os"
	"strings"

	"go.uber.org/zap"
)

// revenueCatWebhookAuthEnv names the shared secret RevenueCat sends in the
// Authorization header of every webhook delivery (set per webhook in the
// RevenueCat dashboard under "Authorization header value").
const revenueCatWebhookAuthEnv = "REVENUECAT_WEBHOOK_AUTH"

// authorizeRevenueCatWebhook rejects deliveries that don't carry the shared
// secret. Without it anyone could post an INITIAL_PURCHASE for their own
// user id and grant themselves a subscription. Fails closed: if the secret
// isn't configured, every delivery is refused (503) so RevenueCat retries
// once it is.
func authorizeRevenueCatWebhook(w http.ResponseWriter, r *http.Request) bool {
	want := strings.TrimSpace(os.Getenv(revenueCatWebhookAuthEnv))
	if want == "" {
		zap.S().Errorw("RevenueCat webhook refused: " + revenueCatWebhookAuthEnv + " is not set")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"message": "webhook not configured"}`))
		return false
	}
	got := strings.TrimSpace(r.Header.Get("Authorization"))
	if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		zap.S().Warnw("RevenueCat webhook refused: bad Authorization header",
			"remoteAddr", r.RemoteAddr, "hasHeader", got != "")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message": "unauthorized"}`))
		return false
	}
	return true
}
