package platforms

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Shapes taken from live responses: a real profile carries statusCode 0 with a
// uniqueId and followerCount, a missing handle carries statusCode 10221 with
// neither, and a block has no hydration blob at all. TikTok answers 200 in the
// first two cases, which is why the HTTP status cannot be the discriminator.
const (
	tiktokRealPage = `<html><script id="__UNIVERSAL_DATA_FOR_REHYDRATION__" type="application/json">` +
		`{"__DEFAULT_SCOPE__":{"webapp.user-detail":{"statusCode":0,"userInfo":{` +
		`"user":{"uniqueId":"daypixie","signature":"LPC-VERIFY-2WY32A"},` +
		`"stats":{"followerCount":1009,"heartCount":883}}}}}</script></html>`

	tiktokMissingPage = `<html><script id="__UNIVERSAL_DATA_FOR_REHYDRATION__" type="application/json">` +
		`{"__DEFAULT_SCOPE__":{"webapp.user-detail":{"statusCode":10221}}}</script></html>`

	tiktokCaptchaPage = `<html><body>Verify to continue</body></html>`
)

func tiktokServing(t *testing.T, status int, body string) TikTok {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return TikTok{BaseURL: srv.URL, Client: srv.Client()}
}

func TestTikTokFetch_ReadsBioAndFollowers(t *testing.T) {
	info, err := tiktokServing(t, 200, tiktokRealPage).Fetch(context.Background(), "daypixie")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.FollowerCount != 1009 {
		t.Errorf("followers = %d, want 1009", info.FollowerCount)
	}
	// The bio is where the verification code lives.
	if info.Description != "LPC-VERIFY-2WY32A" {
		t.Errorf("description = %q, want the bio", info.Description)
	}
	if info.Handle != "daypixie" {
		t.Errorf("handle = %q", info.Handle)
	}
}

func TestTikTokFetch_MissingHandleIsNotFoundNotBlocked(t *testing.T) {
	// 200 with statusCode 10221. Reporting this as blocked would send a warning
	// every time somebody mistypes their own username.
	_, err := tiktokServing(t, 200, tiktokMissingPage).Fetch(context.Background(), "nope")
	if !errors.Is(err, ErrChannelNotFound) {
		t.Fatalf("err = %v, want ErrChannelNotFound", err)
	}
}

func TestTikTokFetch_WallIsBlockedNotNotFound(t *testing.T) {
	// No hydration blob: a captcha or bot wall. Calling this "not found" would
	// fail an application over our own IP reputation.
	_, err := tiktokServing(t, 200, tiktokCaptchaPage).Fetch(context.Background(), "daypixie")
	if !errors.Is(err, ErrPlatformBlocked) {
		t.Fatalf("err = %v, want ErrPlatformBlocked", err)
	}
}

func TestTikTokFetch_NonOKIsBlocked(t *testing.T) {
	for _, status := range []int{403, 429, 500, 503} {
		_, err := tiktokServing(t, status, tiktokRealPage).Fetch(context.Background(), "daypixie")
		if !errors.Is(err, ErrPlatformBlocked) {
			t.Errorf("status %d: err = %v, want ErrPlatformBlocked", status, err)
		}
	}
}

func TestTikTokFetch_BioEscapesAreDecoded(t *testing.T) {
	// A code after a newline or an emoji is the common real-world case, and it
	// arrives escaped. Matching against the raw blob would miss it.
	page := `<html>__UNIVERSAL_DATA_FOR_REHYDRATION__ {"statusCode":0,` +
		`"uniqueId":"someone","signature":"gaming 🎮\nLPC-VERIFY-ABC123",` +
		`"followerCount":42}</html>`
	info, err := tiktokServing(t, 200, page).Fetch(context.Background(), "someone")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "gaming 🎮\nLPC-VERIFY-ABC123"; info.Description != want {
		t.Errorf("description = %q, want %q", info.Description, want)
	}
}

func TestNormalizeTikTokHandle(t *testing.T) {
	for in, want := range map[string]string{
		"daypixie":                             "daypixie",
		"@daypixie":                            "daypixie",
		"https://www.tiktok.com/@daypixie":     "daypixie",
		"https://tiktok.com/@daypixie?lang=en": "daypixie",
		"www.tiktok.com/@daypixie/video/123":   "daypixie",
		"  @daypixie  ":                        "daypixie",
		"":                                     "",
	} {
		if got := normalizeTikTokHandle(in); got != want {
			t.Errorf("normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTikTokFetch_EmptyHandleNeverHitsTheNetwork(t *testing.T) {
	// A blank handle must not become a request for https://www.tiktok.com/@
	tk := TikTok{BaseURL: "http://127.0.0.1:1", Client: &http.Client{}}
	if _, err := tk.Fetch(context.Background(), "   "); !errors.Is(err, ErrChannelNotFound) {
		t.Fatalf("err = %v, want ErrChannelNotFound", err)
	}
}
