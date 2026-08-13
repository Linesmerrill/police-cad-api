package platforms

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// YouTube reads public channel data through Data API v3.
//
// Uses a plain API key rather than OAuth: reading a public channel needs no user
// consent, which avoids Google's sensitive-scope app review entirely.
type YouTube struct{}

const youTubeAPI = "https://www.googleapis.com/youtube/v3/channels"

type youTubeResponse struct {
	Items []struct {
		ID      string `json:"id"`
		Snippet struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			CustomURL   string `json:"customUrl"`
		} `json:"snippet"`
		Statistics struct {
			// Counts come back as strings in this API.
			SubscriberCount       string `json:"subscriberCount"`
			HiddenSubscriberCount bool   `json:"hiddenSubscriberCount"`
		} `json:"statistics"`
	} `json:"items"`
}

// Fetch resolves a channel by @handle, legacy username, or raw channel id.
//
// The API needs a different parameter for each, and a handle is by far the most
// common thing an applicant supplies, so it is tried first.
func (y YouTube) Fetch(ctx context.Context, handle string) (ChannelInfo, error) {
	key := strings.TrimSpace(os.Getenv("YOUTUBE_API_KEY"))
	if key == "" {
		return ChannelInfo{}, ErrNotConfigured
	}

	h := NormalizeHandle("youtube", handle)
	if h == "" {
		return ChannelInfo{}, ErrChannelNotFound
	}

	// forHandle covers @names, forUsername the legacy /user/ form, and id a raw
	// UC... channel id. Try in order of how often applicants supply each.
	attempts := []struct{ param, value string }{
		{"forHandle", "@" + h},
		{"forUsername", h},
	}
	if strings.HasPrefix(h, "UC") && len(h) == 24 {
		attempts = append([]struct{ param, value string }{{"id", h}}, attempts...)
	}

	var lastErr error
	for _, attempt := range attempts {
		info, err := y.fetchBy(ctx, key, attempt.param, attempt.value)
		if err == nil {
			return info, nil
		}
		if err != ErrChannelNotFound {
			return ChannelInfo{}, err // real failure, do not keep trying
		}
		lastErr = err
	}
	return ChannelInfo{}, lastErr
}

func (y YouTube) fetchBy(ctx context.Context, key, param, value string) (ChannelInfo, error) {
	q := url.Values{}
	q.Set("part", "snippet,statistics")
	q.Set(param, value)
	q.Set("key", key)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, youTubeAPI+"?"+q.Encode(), nil)
	if err != nil {
		return ChannelInfo{}, err
	}

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return ChannelInfo{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden {
		// Quota exhausted or key rejected. Ours to fix, not the applicant's.
		return ChannelInfo{}, fmt.Errorf("%w: youtube returned 403", ErrNotConfigured)
	}
	if resp.StatusCode != http.StatusOK {
		return ChannelInfo{}, fmt.Errorf("youtube api returned %d", resp.StatusCode)
	}

	var parsed youTubeResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return ChannelInfo{}, err
	}
	if len(parsed.Items) == 0 {
		return ChannelInfo{}, ErrChannelNotFound
	}

	item := parsed.Items[0]
	// A hidden subscriber count reports as 0; keep it 0 rather than inventing a
	// number, and let the reviewer see the channel is hiding it.
	count := 0
	if !item.Statistics.HiddenSubscriberCount {
		count, _ = strconv.Atoi(item.Statistics.SubscriberCount)
	}

	profile := "https://www.youtube.com/channel/" + item.ID
	if item.Snippet.CustomURL != "" {
		profile = "https://www.youtube.com/" + item.Snippet.CustomURL
	}

	return ChannelInfo{
		Handle:        item.Snippet.CustomURL,
		Description:   item.Snippet.Description,
		FollowerCount: count,
		ProfileURL:    profile,
	}, nil
}
