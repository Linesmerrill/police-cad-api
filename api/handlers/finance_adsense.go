package handlers

// AdSense integration for the owner-only finance dashboard (see FINANCE.md).
//
// AdSense does not support service accounts, so this is a user-consent OAuth2
// flow: the owner connects their Google account once, the callback displays
// the refresh token ONCE, and the owner stores it as the Heroku config var
// ADSENSE_REFRESH_TOKEN. The summary endpoint then exchanges the refresh
// token for short-lived access tokens server-side.
//
// Env vars (fixed names): ADSENSE_CLIENT_ID, ADSENSE_CLIENT_SECRET,
// ADSENSE_REDIRECT_URI, ADSENSE_REFRESH_TOKEN.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	adsenseScope         = "https://www.googleapis.com/auth/adsense.readonly"
	adsenseAuthURL       = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenURL       = "https://oauth2.googleapis.com/token"
	adsenseAPIBase       = "https://adsense.googleapis.com/v2"
	adsenseStateTTL      = 10 * time.Minute
	adsenseHTTPTimeout   = 20 * time.Second
)

// ---------------------------------------------------------------------------
// OAuth state store (in-memory, single-use, 10-minute expiry)
// ---------------------------------------------------------------------------

type adsenseOAuthState struct {
	expiresAt time.Time
}

var adsenseStateStore = struct {
	sync.Mutex
	states map[string]adsenseOAuthState
}{states: make(map[string]adsenseOAuthState)}

func newAdSenseState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	state := hex.EncodeToString(b)
	adsenseStateStore.Lock()
	defer adsenseStateStore.Unlock()
	// Opportunistic cleanup of expired states.
	for s, st := range adsenseStateStore.states {
		if time.Now().After(st.expiresAt) {
			delete(adsenseStateStore.states, s)
		}
	}
	adsenseStateStore.states[state] = adsenseOAuthState{expiresAt: time.Now().Add(adsenseStateTTL)}
	return state, nil
}

// consumeAdSenseState verifies the state and deletes it (single-use).
func consumeAdSenseState(state string) error {
	adsenseStateStore.Lock()
	defer adsenseStateStore.Unlock()
	st, ok := adsenseStateStore.states[state]
	if !ok {
		return errors.New("invalid state")
	}
	delete(adsenseStateStore.states, state)
	if time.Now().After(st.expiresAt) {
		return errors.New("state expired")
	}
	return nil
}

// adsenseOAuthConfigured reports whether the OAuth client credentials are set
// (enough to start the consent flow).
func adsenseOAuthConfigured() bool {
	return os.Getenv("ADSENSE_CLIENT_ID") != "" &&
		os.Getenv("ADSENSE_CLIENT_SECRET") != "" &&
		os.Getenv("ADSENSE_REDIRECT_URI") != ""
}

// adsenseFullyConfigured reports whether the summary endpoint can pull
// AdSense earnings (client creds + stored refresh token).
func adsenseFullyConfigured() bool {
	return adsenseOAuthConfigured() && os.Getenv("ADSENSE_REFRESH_TOKEN") != ""
}

// AdSenseOAuthStartHandler implements
// GET /api/v1/admin/finance/adsense/oauth/start.
// Returns {"url": "<google consent url>"}.
func (f Finance) AdSenseOAuthStartHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if !adsenseOAuthConfigured() {
		writeFinanceError(w, http.StatusServiceUnavailable,
			"AdSense OAuth is not configured: set ADSENSE_CLIENT_ID, ADSENSE_CLIENT_SECRET and ADSENSE_REDIRECT_URI")
		return
	}

	state, err := newAdSenseState()
	if err != nil {
		writeFinanceError(w, http.StatusInternalServerError, "failed to generate OAuth state")
		return
	}

	q := url.Values{}
	q.Set("client_id", os.Getenv("ADSENSE_CLIENT_ID"))
	q.Set("redirect_uri", os.Getenv("ADSENSE_REDIRECT_URI"))
	q.Set("response_type", "code")
	q.Set("scope", adsenseScope)
	q.Set("access_type", "offline") // ask Google for a refresh token
	q.Set("prompt", "consent")     // force re-consent so a refresh token is issued
	q.Set("state", state)

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"url": adsenseAuthURL + "?" + q.Encode()})
}

// AdSenseOAuthCallbackHandler implements
// GET /api/v1/admin/finance/adsense/oauth/callback?code=&state=.
// It verifies the single-use state, exchanges the code for tokens, and
// displays the refresh token ONCE with instructions to store it as the
// Heroku config var ADSENSE_REFRESH_TOKEN. The token is never logged.
func (f Finance) AdSenseOAuthCallbackHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		writeFinanceError(w, http.StatusBadRequest, "missing code or state")
		return
	}
	if err := consumeAdSenseState(state); err != nil {
		writeFinanceError(w, http.StatusBadRequest, "invalid or expired state")
		return
	}
	if !adsenseOAuthConfigured() {
		writeFinanceError(w, http.StatusServiceUnavailable, "AdSense OAuth is not configured")
		return
	}

	form := url.Values{}
	form.Set("code", code)
	form.Set("client_id", os.Getenv("ADSENSE_CLIENT_ID"))
	form.Set("client_secret", os.Getenv("ADSENSE_CLIENT_SECRET"))
	form.Set("redirect_uri", os.Getenv("ADSENSE_REDIRECT_URI"))
	form.Set("grant_type", "authorization_code")

	token, err := exchangeGoogleTokens(r.Context(), form)
	if err != nil {
		writeFinanceError(w, http.StatusBadGateway, "failed to exchange authorization code: "+err.Error())
		return
	}
	if token.RefreshToken == "" {
		writeFinanceError(w, http.StatusBadGateway,
			"Google did not return a refresh token; revoke LPC access at https://myaccount.google.com/permissions and try again")
		return
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"refresh_token": token.RefreshToken,
		"message":       "Copy this refresh token now — it is displayed only once. Set it as the Heroku config var ADSENSE_REFRESH_TOKEN (never commit it to code).",
	})
}

type googleTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

// exchangeGoogleTokens posts a form to Google's token endpoint. Callers must
// never log the returned tokens.
func exchangeGoogleTokens(ctx context.Context, form url.Values) (*googleTokenResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, adsenseHTTPTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, googleTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token endpoint returned %d", resp.StatusCode)
	}
	var token googleTokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return nil, err
	}
	if token.AccessToken == "" {
		return nil, errors.New("token endpoint returned no access token")
	}
	return &token, nil
}

// adsenseAccessToken exchanges the stored refresh token for a short-lived
// access token.
func adsenseAccessToken(ctx context.Context) (string, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", os.Getenv("ADSENSE_CLIENT_ID"))
	form.Set("client_secret", os.Getenv("ADSENSE_CLIENT_SECRET"))
	form.Set("refresh_token", os.Getenv("ADSENSE_REFRESH_TOKEN"))
	token, err := exchangeGoogleTokens(ctx, form)
	if err != nil {
		return "", err
	}
	return token.AccessToken, nil
}

func adsenseGet(ctx context.Context, accessToken, path string, params url.Values) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, adsenseHTTPTimeout)
	defer cancel()

	u := adsenseAPIBase + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("AdSense API returned %d", resp.StatusCode)
	}
	return body, nil
}

type adsenseAccount struct {
	Name string `json:"name"` // e.g. "accounts/pub-1234567890"
}

type adsenseAccountsResponse struct {
	Accounts []adsenseAccount `json:"accounts"`
}

type adsenseReportCell struct {
	Value string `json:"value"`
}

type adsenseReportRow struct {
	Cells []adsenseReportCell `json:"cells"`
}

type adsenseReportResponse struct {
	Rows []adsenseReportRow `json:"rows"`
}

// fetchAdSenseEarnings pulls EARNINGS by DATE for every AdSense account over
// the requested month range and aggregates them into month -> earnings.
// It degrades gracefully: on ANY failure it returns (nil, false, []string)
// with a non-fatal warning so the summary still renders.
func fetchAdSenseEarnings(ctx context.Context, start, end time.Time) (map[string]float64, bool, []string) {
	if !adsenseFullyConfigured() {
		return nil, false, nil
	}

	accessToken, err := adsenseAccessToken(ctx)
	if err != nil {
		return nil, false, []string{"adsense: failed to refresh access token (" + err.Error() + ")"}
	}

	body, err := adsenseGet(ctx, accessToken, "/accounts", nil)
	if err != nil {
		return nil, false, []string{"adsense: failed to list accounts (" + err.Error() + ")"}
	}
	var accountsResp adsenseAccountsResponse
	if err := json.Unmarshal(body, &accountsResp); err != nil {
		return nil, false, []string{"adsense: failed to parse accounts response"}
	}
	if len(accountsResp.Accounts) == 0 {
		return nil, false, []string{"adsense: no AdSense accounts found"}
	}

	// The range is whole months; the report end is exclusive, so use the first
	// day of the month after `end`.
	reportEnd := end.AddDate(0, 1, 0).AddDate(0, 0, -1)

	byMonth := map[string]float64{}
	for _, acct := range accountsResp.Accounts {
		params := url.Values{}
		params.Set("dimensions", "DATE")
		params.Set("metrics", "EARNINGS")
		params.Set("currencyCode", "USD")
		params.Set("startDate.year", strconv.Itoa(start.Year()))
		params.Set("startDate.month", strconv.Itoa(int(start.Month())))
		params.Set("startDate.day", strconv.Itoa(start.Day()))
		params.Set("endDate.year", strconv.Itoa(reportEnd.Year()))
		params.Set("endDate.month", strconv.Itoa(int(reportEnd.Month())))
		params.Set("endDate.day", strconv.Itoa(reportEnd.Day()))

		body, err := adsenseGet(ctx, accessToken, "/"+acct.Name+"/reports:generate", params)
		if err != nil {
			return nil, false, []string{"adsense: failed to generate report (" + err.Error() + ")"}
		}
		var report adsenseReportResponse
		if err := json.Unmarshal(body, &report); err != nil {
			return nil, false, []string{"adsense: failed to parse report response"}
		}
		for _, row := range report.Rows {
			if len(row.Cells) < 2 {
				continue
			}
			dateStr := row.Cells[0].Value // YYYY-MM-DD
			t, err := time.Parse("2006-01-02", dateStr)
			if err != nil {
				continue
			}
			earnings, err := strconv.ParseFloat(row.Cells[1].Value, 64)
			if err != nil {
				continue
			}
			byMonth[t.UTC().Format("2006-01")] += earnings
		}
	}

	return byMonth, true, nil
}
