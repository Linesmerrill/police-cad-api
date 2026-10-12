// Package platforms reads public channel data from creator platforms so channel
// ownership can be verified and follower counts can be trusted rather than
// taken from whatever an applicant typed.
//
// Only data that is public is read. Nothing here needs a user to grant access;
// verification works by the applicant placing a code in their own channel
// description, which we then look for.
package platforms

import (
	"context"
	"errors"
	"strings"
)

// ChannelInfo is the subset of a channel we need: enough text to find a
// verification code in, and the follower count.
type ChannelInfo struct {
	Handle string
	// Description is whatever public free-text the platform exposes (YouTube
	// channel description, Twitch bio). The verification code is searched here.
	Description   string
	FollowerCount int
	// ProfileURL is the canonical link, useful for admin review.
	ProfileURL string
}

var (
	// ErrNotConfigured means the credentials for this platform are absent, so
	// verification cannot run. Callers should surface this rather than treating
	// it as a failed verification — it is our problem, not the applicant's.
	ErrNotConfigured = errors.New("platform credentials not configured")

	// ErrManualOnly means the platform exposes no usable public API, so an admin
	// has to confirm the code by eye. "other" is the case: an arbitrary link we
	// know nothing about.
	//
	// TikTok used to be here too, on the grounds that scraping from a server IP
	// gets blocked and breaks. Both halves of that are still true, which is why
	// the TikTok fetcher reports ErrPlatformBlocked distinctly, the caller keeps
	// the manual path as its fallback, and a warning goes to Discord when it
	// fires. Trying and degrading loudly beats not trying.
	ErrManualOnly = errors.New("platform requires manual verification")

	// ErrChannelNotFound means the handle does not resolve. Usually a typo, but
	// also what a made-up channel looks like.
	ErrChannelNotFound = errors.New("channel not found")
)

// Fetcher reads a channel by handle.
type Fetcher interface {
	Fetch(ctx context.Context, handle string) (ChannelInfo, error)
}

// For returns the fetcher for a platform type, or ErrManualOnly when the
// platform cannot be checked automatically.
func For(platformType string) (Fetcher, error) {
	switch strings.ToLower(strings.TrimSpace(platformType)) {
	case "youtube":
		return YouTube{}, nil
	case "twitch":
		return Twitch{}, nil
	case "tiktok":
		// Reads the public profile page. Falls back to manual review when
		// TikTok blocks us, rather than failing the applicant. See tiktok.go.
		return TikTok{}, nil
	case "other", "":
		return nil, ErrManualOnly
	default:
		return nil, ErrManualOnly
	}
}

// Measurable reports whether we can RELY on a follower count WE read from this
// platform. Where we can, the applicant is not asked for a number at all — so a
// zero from one of these is the absence of a question, not a claim of zero, and
// must never be validated as though the applicant typed it.
//
// TikTok is listed because the figure the profile page gives us has held up
// against live responses: when the read succeeds the number is real, and a
// number we read is what the 500 minimum is meant to be judged on. What TikTok
// still cannot promise is that a read succeeds at all — a datacenter IP earns a
// captcha or a refusal often enough that the fetcher reports ErrPlatformBlocked
// distinctly. That is a question about availability, which every caller already
// answers by falling back to the manual path, and not a question about whether
// the number can be trusted once we have it.
//
// Deliberately NOT derived from For(): "other" has no fetcher and a platform
// could gain one that returns a figure we would not stake a revocation on.
//
// Independent of credentials on purpose: a missing API key is an outage, and an
// outage must not change what an applicant is allowed to submit.
func Measurable(platformType string) bool {
	switch strings.ToLower(strings.TrimSpace(platformType)) {
	case "youtube", "twitch", "tiktok":
		return true
	default:
		return false
	}
}

// NormalizeHandle strips the decoration people paste in: full URLs, leading @,
// query strings and trailing slashes. Applicants supply a handle, a URL, or a
// URL in the handle field, and all three should work.
func NormalizeHandle(platformType, raw string) string {
	h := strings.TrimSpace(raw)
	if h == "" {
		return ""
	}

	// Strip the scheme if a full URL was pasted.
	if i := strings.Index(h, "://"); i >= 0 {
		h = h[i+3:]
	}

	// Drop query and fragment.
	if i := strings.IndexAny(h, "?#"); i >= 0 {
		h = h[:i]
	}
	h = strings.Trim(h, "/")

	// Drop a leading host. People paste "youtube.com/@name" as often as the full
	// URL, so keying off "://" alone would eat the handle instead of the host.
	// A first segment containing a dot is a hostname; a handle never is.
	if i := strings.IndexByte(h, '/'); i > 0 && strings.Contains(h[:i], ".") {
		h = h[i+1:]
	}

	// YouTube URLs carry a path prefix before the handle.
	for _, prefix := range []string{"channel/", "c/", "user/", "@"} {
		if strings.HasPrefix(strings.ToLower(h), prefix) {
			h = h[len(prefix):]
			break
		}
	}

	// A trailing path segment (e.g. /videos) is not part of the handle.
	if i := strings.IndexByte(h, '/'); i >= 0 {
		h = h[:i]
	}

	return strings.TrimSpace(strings.TrimPrefix(h, "@"))
}

// ContainsCode reports whether a channel description carries the verification
// code. Matching is case-insensitive and ignores surrounding text, so the
// applicant can leave the code anywhere in their bio alongside anything else.
func ContainsCode(description, code string) bool {
	if strings.TrimSpace(code) == "" {
		return false
	}
	return strings.Contains(
		strings.ToUpper(description),
		strings.ToUpper(strings.TrimSpace(code)),
	)
}
