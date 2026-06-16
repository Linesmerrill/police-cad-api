package models

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestUserMarshalJSONStripsSecrets ensures the password hash and the
// reset/verification tokens are never present in a marshaled user — this is
// the account-takeover fix (leaking resetPasswordToken let an attacker reset
// any account).
func TestUserMarshalJSONStripsSecrets(t *testing.T) {
	u := User{
		ID: "507f1f77bcf86cd799439011",
		Details: UserDetails{
			Email:                  "victim@example.com",
			Username:               "victim",
			Password:               "$2a$10$supersecrethashvalue",
			ResetPasswordToken:     "reset-token-abc123",
			ResetPasswordExpires:   "2026-01-01T00:00:00Z",
			EmailVerificationToken: "verify-token-xyz789",
		},
	}

	b, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := string(b)

	for _, secret := range []string{
		"$2a$10$supersecrethashvalue",
		"reset-token-abc123",
		"verify-token-xyz789",
	} {
		if strings.Contains(out, secret) {
			t.Errorf("marshaled user leaked secret %q\n%s", secret, out)
		}
	}

	// Non-secret fields must still be present.
	if !strings.Contains(out, "victim@example.com") || !strings.Contains(out, "victim") {
		t.Errorf("expected non-secret fields to remain in output: %s", out)
	}
}

// TestUserDetailsUnmarshalStillReadsPassword guards the signup path: decoding a
// request body into UserDetails must still populate the password (MarshalJSON
// only affects output).
func TestUserDetailsUnmarshalStillReadsPassword(t *testing.T) {
	body := `{"email":"new@example.com","username":"new","password":"plaintext-pw"}`
	var d UserDetails
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if d.Password != "plaintext-pw" {
		t.Errorf("password not decoded from input, got %q", d.Password)
	}
}
