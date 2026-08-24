package handlers

import "testing"

func TestNormalizeDiscordInvitePatch(t *testing.T) {
	tests := []struct {
		name    string
		raw     interface{}
		want    string
		wantErr bool
	}{
		{name: "canonical link passes through", raw: "https://discord.gg/abc123", want: "https://discord.gg/abc123"},
		{name: "missing scheme is canonicalised", raw: "discord.gg/abc123", want: "https://discord.gg/abc123"},
		{name: "discord.com/invite is canonicalised", raw: "https://discord.com/invite/abc123", want: "https://discord.gg/abc123"},
		{name: "surrounding whitespace is trimmed", raw: "  discord.gg/abc123  ", want: "https://discord.gg/abc123"},
		{name: "empty clears the link", raw: "", want: ""},
		{name: "whitespace-only clears the link", raw: "   ", want: ""},
		{name: "nil clears the link", raw: nil, want: ""},

		// Refused rather than dropped: a link that quietly vanishes leaves the
		// owner believing new members are being pointed at their server.
		{name: "a channel deep link is refused", raw: "https://discord.com/channels/1/2", wantErr: true},
		{name: "an unrelated url is refused", raw: "https://example.com/join", wantErr: true},
		{name: "free text is refused", raw: "join our discord!", wantErr: true},
		{name: "a non-string is refused", raw: 42, wantErr: true},
		{name: "a list is refused", raw: []interface{}{"discord.gg/abc"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeDiscordInvitePatch(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizeOnboardingStepsPatch(t *testing.T) {
	t.Run("trims and drops blanks", func(t *testing.T) {
		got, err := normalizeOnboardingStepsPatch([]interface{}{"  Join our Discord ", "", "Read #rules"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 2 || got[0] != "Join our Discord" || got[1] != "Read #rules" {
			t.Errorf("got %#v", got)
		}
	})

	// Clearing must write an empty array, not null, so readers never have to
	// distinguish "no steps" from "field absent".
	t.Run("clearing yields an empty slice not nil", func(t *testing.T) {
		for _, raw := range []interface{}{nil, []interface{}{}, []interface{}{"", "  "}} {
			got, err := normalizeOnboardingStepsPatch(raw)
			if err != nil {
				t.Fatalf("unexpected error for %#v: %v", raw, err)
			}
			if got == nil {
				t.Errorf("got nil for %#v, want an empty slice", raw)
			}
			if len(got) != 0 {
				t.Errorf("got %#v, want empty", got)
			}
		}
	})

	t.Run("too many steps is refused", func(t *testing.T) {
		many := make([]interface{}, 0, 9)
		for i := 0; i < 9; i++ {
			many = append(many, "step")
		}
		if _, err := normalizeOnboardingStepsPatch(many); err == nil {
			t.Error("expected an error for an over-long list")
		}
	})

	t.Run("non-list is refused", func(t *testing.T) {
		if _, err := normalizeOnboardingStepsPatch("Join our Discord"); err == nil {
			t.Error("expected an error for a bare string")
		}
	})

	t.Run("list of non-strings is refused", func(t *testing.T) {
		if _, err := normalizeOnboardingStepsPatch([]interface{}{"ok", 7}); err == nil {
			t.Error("expected an error for a numeric entry")
		}
	})
}
