package platforms

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// Twitch reads public channel data through Helix.
//
// Uses an app access token (client credentials), not user OAuth: the bio and
// follower total are public, so nobody needs to grant us anything.
type Twitch struct{}

const (
	twitchTokenURL     = "https://id.twitch.tv/oauth2/token"
	twitchUsersURL     = "https://api.twitch.tv/helix/users"
	twitchFollowersURL = "https://api.twitch.tv/helix/channels/followers"
)

// App tokens last ~60 days, so caching one avoids a token round trip on every
// verification check.
var (
	twitchTokenMu      sync.Mutex
	twitchToken        string
	twitchTokenExpires time.Time
)

func twitchCredentials() (id, secret string, err error) {
	id = strings.TrimSpace(os.Getenv("TWITCH_CLIENT_ID"))
	secret = strings.TrimSpace(os.Getenv("TWITCH_CLIENT_SECRET"))
	if id == "" || secret == "" {
		return "", "", ErrNotConfigured
	}
	return id, secret, nil
}

func twitchAppToken(ctx context.Context, clientID, clientSecret string) (string, error) {
	twitchTokenMu.Lock()
	defer twitchTokenMu.Unlock()

	// Refresh a minute early so a token cannot expire mid-request.
	if twitchToken != "" && time.Now().Add(time.Minute).Before(twitchTokenExpires) {
		return twitchToken, nil
	}

	form := url.Values{}
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)
	form.Set("grant_type", "client_credentials")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, twitchTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: twitch token returned %d", ErrNotConfigured, resp.StatusCode)
	}

	var parsed struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", err
	}

	twitchToken = parsed.AccessToken
	twitchTokenExpires = time.Now().Add(time.Duration(parsed.ExpiresIn) * time.Second)
	return twitchToken, nil
}

// Fetch resolves a Twitch channel by login name.
func (t Twitch) Fetch(ctx context.Context, handle string) (ChannelInfo, error) {
	clientID, clientSecret, err := twitchCredentials()
	if err != nil {
		return ChannelInfo{}, err
	}

	login := NormalizeHandle("twitch", handle)
	if login == "" {
		return ChannelInfo{}, ErrChannelNotFound
	}

	token, err := twitchAppToken(ctx, clientID, clientSecret)
	if err != nil {
		return ChannelInfo{}, err
	}

	call := func(endpoint string, q url.Values, out interface{}) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+q.Encode(), nil)
		if err != nil {
			return err
		}
		req.Header.Set("Client-Id", clientID)
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("twitch api returned %d", resp.StatusCode)
		}
		return json.NewDecoder(resp.Body).Decode(out)
	}

	var users struct {
		Data []struct {
			ID          string `json:"id"`
			Login       string `json:"login"`
			Description string `json:"description"`
		} `json:"data"`
	}
	q := url.Values{}
	q.Set("login", login)
	if err := call(twitchUsersURL, q, &users); err != nil {
		return ChannelInfo{}, err
	}
	if len(users.Data) == 0 {
		return ChannelInfo{}, ErrChannelNotFound
	}
	user := users.Data[0]

	// Follower total is a separate endpoint. A failure here should not sink the
	// ownership check, which is what the description is for.
	var followers struct {
		Total int `json:"total"`
	}
	fq := url.Values{}
	fq.Set("broadcaster_id", user.ID)
	_ = call(twitchFollowersURL, fq, &followers)

	return ChannelInfo{
		Handle:        user.Login,
		Description:   user.Description,
		FollowerCount: followers.Total,
		ProfileURL:    "https://twitch.tv/" + user.Login,
	}, nil
}
