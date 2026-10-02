package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func postRevenueCatWebhook(auth string) *httptest.ResponseRecorder {
	body := `{"event":{"id":"evt_1","type":"INITIAL_PURCHASE","app_user_id":"64b000000000000000000001"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhook-subscription-deleted", strings.NewReader(body))
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	// A zero User has no databases: a request that gets past the auth
	// check would panic, so these tests prove the check runs first.
	User{}.HandleRevenueCatWebhook(rec, req)
	return rec
}

func TestRevenueCatWebhook_RefusedWhenSecretNotConfigured(t *testing.T) {
	t.Setenv(revenueCatWebhookAuthEnv, "")
	assert.Equal(t, http.StatusServiceUnavailable, postRevenueCatWebhook("Bearer anything").Code)
}

func TestRevenueCatWebhook_RefusedWithoutHeader(t *testing.T) {
	t.Setenv(revenueCatWebhookAuthEnv, "Bearer s3cret")
	assert.Equal(t, http.StatusUnauthorized, postRevenueCatWebhook("").Code)
}

func TestRevenueCatWebhook_RefusedWithWrongHeader(t *testing.T) {
	t.Setenv(revenueCatWebhookAuthEnv, "Bearer s3cret")
	assert.Equal(t, http.StatusUnauthorized, postRevenueCatWebhook("Bearer guess").Code)
}

func TestRevenueCatWebhook_AuthorizeAcceptsMatchingHeader(t *testing.T) {
	t.Setenv(revenueCatWebhookAuthEnv, "Bearer s3cret")
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	assert.True(t, authorizeRevenueCatWebhook(httptest.NewRecorder(), req))
}
