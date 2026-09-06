package platforms

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// TikTok reads a public profile page.
//
// TikTok has no public API for a bio or a follower count, but the profile page
// ships both in a JSON blob it hydrates the client from, and that page needs no
// login. So we read it, and we are honest about the ways that can fail.
//
// Verified against live responses while building this:
//
//	real handle    -> statusCode 0,     uniqueId + followerCount present
//	missing handle -> statusCode 10221, neither present
//	blocked        -> the blob is absent entirely
//
// The status code is the discriminator, not the HTTP code: TikTok answers 200
// for a handle that does not exist, so a bare 4xx check would call every
// missing account "reachable".
type TikTok struct {
	// BaseURL and Client exist so tests can serve fixtures instead of hitting
	// the network. Zero values use the real thing.
	BaseURL string
	Client  *http.Client
}

// ErrPlatformBlocked means the platform refused to serve us, rather than the
// handle being wrong. Distinct from ErrChannelNotFound on purpose: one is the
// applicant's typo and theirs to fix, the other is our problem and must never
// fail an application. Callers fall back to manual review and raise a warning.
var ErrPlatformBlocked = fmt.Errorf("platform blocked the request")

const (
	tiktokDefaultBaseURL = "https://www.tiktok.com"
	tiktokTimeout        = 12 * time.Second
	// Enough to cover the hydration blob without buffering an unbounded page.
	tiktokMaxBody = 3 << 20 // 3 MB
	// TikTok serves a stripped page to anything that looks automated.
	tiktokUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
		"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
)

var (
	tiktokStatusRe    = regexp.MustCompile(`"statusCode":\s*(\d+)`)
	tiktokFollowersRe = regexp.MustCompile(`"followerCount":\s*(\d+)`)
	tiktokUniqueIDRe  = regexp.MustCompile(`"uniqueId":\s*"([^"]{1,64})"`)
	tiktokSignatureRe = regexp.MustCompile(`"signature":\s*"((?:[^"\\]|\\.){0,1000})"`)
	// Present on every real profile page, missing when we are served a wall.
	tiktokHydrationMarker = "__UNIVERSAL_DATA_FOR_REHYDRATION__"
)

// normalizeTikTokHandle accepts what people actually paste: a bare handle, an
// @handle, or a full profile URL with or without a query string.
func normalizeTikTokHandle(handle string) string {
	h := strings.TrimSpace(handle)
	if h == "" {
		return ""
	}
	if i := strings.Index(h, "tiktok.com/"); i >= 0 {
		h = h[i+len("tiktok.com/"):]
	}
	if i := strings.IndexAny(h, "?#"); i >= 0 {
		h = h[:i]
	}
	h = strings.Trim(h, "/")
	h = strings.TrimPrefix(h, "@")
	if i := strings.Index(h, "/"); i >= 0 {
		h = h[:i]
	}
	return strings.TrimSpace(h)
}

func (t TikTok) baseURL() string {
	if strings.TrimSpace(t.BaseURL) != "" {
		return strings.TrimRight(t.BaseURL, "/")
	}
	return tiktokDefaultBaseURL
}

func (t TikTok) client() *http.Client {
	if t.Client != nil {
		return t.Client
	}
	return &http.Client{Timeout: tiktokTimeout}
}

// Fetch reads the public profile page for handle.
func (t TikTok) Fetch(ctx context.Context, handle string) (ChannelInfo, error) {
	h := normalizeTikTokHandle(handle)
	if h == "" {
		return ChannelInfo{}, ErrChannelNotFound
	}

	profileURL := t.baseURL() + "/@" + h
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, profileURL, nil)
	if err != nil {
		return ChannelInfo{}, ErrPlatformBlocked
	}
	req.Header.Set("User-Agent", tiktokUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := t.client().Do(req)
	if err != nil {
		// Timeout, DNS, connection reset. Ours, not theirs.
		return ChannelInfo{}, ErrPlatformBlocked
	}
	defer resp.Body.Close()

	// 404 would be honest, but TikTok does not send one. Anything that is not
	// 200 here is a refusal: 403 and 429 are the ones a datacenter IP earns.
	if resp.StatusCode != http.StatusOK {
		return ChannelInfo{}, ErrPlatformBlocked
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, tiktokMaxBody))
	if err != nil {
		return ChannelInfo{}, ErrPlatformBlocked
	}
	page := string(body)

	// No hydration blob means we were not served the profile page at all: a
	// captcha interstitial, a consent wall, or a stripped bot response.
	if !strings.Contains(page, tiktokHydrationMarker) {
		return ChannelInfo{}, ErrPlatformBlocked
	}

	// A non-zero status inside the blob is TikTok telling us the handle does not
	// resolve, even though the HTTP status said 200.
	if m := tiktokStatusRe.FindStringSubmatch(page); m != nil && m[1] != "0" {
		return ChannelInfo{}, ErrChannelNotFound
	}

	unique := firstGroup(tiktokUniqueIDRe, page)
	followers, hasFollowers := firstInt(tiktokFollowersRe, page)
	if unique == "" || !hasFollowers {
		// The page rendered but carries no profile. Treat as missing rather than
		// blocked: the blob was there, it just had nobody in it.
		return ChannelInfo{}, ErrChannelNotFound
	}

	return ChannelInfo{
		Handle: unique,
		// The bio. This is where the applicant is asked to put the code.
		Description:   decodeJSONString(firstGroup(tiktokSignatureRe, page)),
		FollowerCount: followers,
		ProfileURL:    profileURL,
	}, nil
}

func firstGroup(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

func firstInt(re *regexp.Regexp, s string) (int, bool) {
	m := re.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	var n int
	if _, err := fmt.Sscanf(m[1], "%d", &n); err != nil {
		return 0, false
	}
	return n, true
}

// decodeJSONString unescapes a raw JSON string body. Bios routinely contain
// newlines and emoji, which arrive as \n and \uXXXX; a verification code sitting
// after one of those would otherwise never match.
func decodeJSONString(raw string) string {
	if raw == "" {
		return ""
	}
	var out string
	if err := json.Unmarshal([]byte(`"`+raw+`"`), &out); err != nil {
		return raw
	}
	return out
}
