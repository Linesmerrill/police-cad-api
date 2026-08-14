package platforms

import "testing"

// Applicants paste whatever is in their address bar. The handle field gets full
// URLs, @names, bare names, and URLs with tracking junk on the end — all of
// which must resolve to the same channel, or verification fails for a channel
// the person genuinely owns.
func TestNormalizeHandle(t *testing.T) {
	cases := []struct {
		name     string
		platform string
		in       string
		want     string
	}{
		{"bare handle", "youtube", "cryptic", "cryptic"},
		{"at-prefixed handle", "youtube", "@cryptic", "cryptic"},
		{"full handle url", "youtube", "https://www.youtube.com/@cryptic", "cryptic"},
		{"handle url without scheme", "youtube", "youtube.com/@cryptic", "cryptic"},
		{"legacy channel url", "youtube", "https://www.youtube.com/channel/UCabcdefghijklmnopqrstu", "UCabcdefghijklmnopqrstu"},
		{"legacy user url", "youtube", "https://www.youtube.com/user/oldschool", "oldschool"},
		{"legacy c url", "youtube", "https://www.youtube.com/c/somename", "somename"},
		{"url with trailing path", "youtube", "https://www.youtube.com/@cryptic/videos", "cryptic"},
		{"url with query string", "youtube", "https://www.youtube.com/@cryptic?sub_confirmation=1", "cryptic"},
		{"url with fragment", "youtube", "https://www.youtube.com/@cryptic#about", "cryptic"},
		{"trailing slash", "youtube", "https://www.youtube.com/@cryptic/", "cryptic"},
		{"surrounding whitespace", "youtube", "  @cryptic  ", "cryptic"},
		{"twitch url", "twitch", "https://twitch.tv/hhiclofi", "hhiclofi"},
		{"twitch url with path", "twitch", "https://www.twitch.tv/hhiclofi/videos", "hhiclofi"},
		{"tiktok at handle", "tiktok", "https://tiktok.com/@someone", "someone"},
		{"empty", "youtube", "", ""},
		{"whitespace only", "youtube", "   ", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeHandle(tc.platform, tc.in); got != tc.want {
				t.Errorf("NormalizeHandle(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// The code has to be findable in a real bio, which is full of other text,
// emoji and inconsistent casing. It must NOT match on an empty code, or every
// channel on earth would verify.
func TestContainsCode(t *testing.T) {
	cases := []struct {
		name string
		desc string
		code string
		want bool
	}{
		{"exact", "LPC-VERIFY-7F3K2A", "LPC-VERIFY-7F3K2A", true},
		{"embedded in a real bio", "GTA RP streamer 🎮 biz: me@x.com\nLPC-VERIFY-7F3K2A\nsub goal 5k", "LPC-VERIFY-7F3K2A", true},
		{"different case in bio", "lpc-verify-7f3k2a", "LPC-VERIFY-7F3K2A", true},
		{"different case in code", "LPC-VERIFY-7F3K2A", "lpc-verify-7f3k2a", true},
		{"code padded with spaces", "LPC-VERIFY-7F3K2A", "  LPC-VERIFY-7F3K2A  ", true},
		{"absent", "just a normal bio", "LPC-VERIFY-7F3K2A", false},
		{"a different code", "LPC-VERIFY-AAAAAA", "LPC-VERIFY-7F3K2A", false},
		{"empty code never matches", "anything at all", "", false},
		{"whitespace code never matches", "anything at all", "   ", false},
		{"empty description", "", "LPC-VERIFY-7F3K2A", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ContainsCode(tc.desc, tc.code); got != tc.want {
				t.Errorf("ContainsCode(%q, %q) = %v, want %v", tc.desc, tc.code, got, tc.want)
			}
		})
	}
}

// TikTok has no usable public API. It must report that honestly rather than
// pretending to verify, so callers route it to an admin.
func TestForReturnsManualOnlyWhereNoAPIExists(t *testing.T) {
	if _, err := For("youtube"); err != nil {
		t.Errorf("youtube should be automatable, got %v", err)
	}
	if _, err := For("twitch"); err != nil {
		t.Errorf("twitch should be automatable, got %v", err)
	}
	for _, p := range []string{"tiktok", "other", "", "myspace"} {
		if _, err := For(p); err != ErrManualOnly {
			t.Errorf("For(%q) = %v, want ErrManualOnly", p, err)
		}
	}
}

func TestForIsCaseInsensitive(t *testing.T) {
	if _, err := For("YouTube"); err != nil {
		t.Errorf("platform matching should be case-insensitive, got %v", err)
	}
	if _, err := For("  TWITCH "); err != nil {
		t.Errorf("platform matching should tolerate case and spacing, got %v", err)
	}
}
