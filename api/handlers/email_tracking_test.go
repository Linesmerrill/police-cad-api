package handlers

// SendGrid click tracking rewrote every link in our emails to
// http://url<n>.linespolice-cad.com/ls/click?..., a host with no TLS, so a
// reset link opened a browser security warning and the token went through a
// third-party redirect. These tests assert the tracking fields in the body
// that actually goes to api.sendgrid.com, and that the sends which carry a
// token or an account link are still built the untracked way.

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/sendgrid/sendgrid-go/helpers/mail"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// trackingSettingsOf pulls the tracking_settings object out of the request body
// sendgrid-go would POST, so the assertions are about the wire format rather
// than about Go struct fields.
func trackingSettingsOf(t *testing.T, msg *mail.SGMailV3) map[string]interface{} {
	t.Helper()
	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(mail.GetRequestBody(msg), &body))
	settings, ok := body["tracking_settings"].(map[string]interface{})
	require.True(t, ok, "tracking_settings missing from the payload: %s", mail.GetRequestBody(msg))
	return settings
}

func assertTrackingOff(t *testing.T, msg *mail.SGMailV3) {
	t.Helper()
	settings := trackingSettingsOf(t, msg)
	assert.Equal(t, map[string]interface{}{
		"click_tracking": map[string]interface{}{"enable": false, "enable_text": false},
		"open_tracking":  map[string]interface{}{"enable": false},
	}, settings)
}

func TestNoTrackingSettings(t *testing.T) {
	settings := noTrackingSettings()

	require.NotNil(t, settings.ClickTracking)
	require.NotNil(t, settings.ClickTracking.Enable)
	require.NotNil(t, settings.ClickTracking.EnableText)
	require.NotNil(t, settings.OpenTracking)
	require.NotNil(t, settings.OpenTracking.Enable)

	assert.False(t, *settings.ClickTracking.Enable)
	assert.False(t, *settings.ClickTracking.EnableText, "plain text parts are rewritten too")
	assert.False(t, *settings.OpenTracking.Enable)
}

// A false is only off if it survives omitempty, which it does because
// sendgrid-go holds these as *bool. A dropped field means SendGrid falls back
// to the account default, which is tracking on.
func TestNewAccountEmailPayloadSaysTrackingIsOff(t *testing.T) {
	msg := newAccountEmail(
		mail.NewEmail("Lines Police CAD", "no-reply@linespolice-cad.com"),
		"Password Reset Request",
		mail.NewEmail("", "member@example.test"),
		"Reset your password: https://www.linespolice-cad.com/reset/abc",
		`<a href="https://www.linespolice-cad.com/reset/abc">Reset your password</a>`,
	)

	t.Logf("POST /v3/mail/send body:\n%s", indentJSON(t, mail.GetRequestBody(msg)))

	assertTrackingOff(t, msg)
}

// The control: mail.NewSingleEmail, which is what these sends used to call,
// sends no tracking fields at all, so SendGrid applies the account default and
// rewrites the links. Without this the assertion above could pass against a
// payload shape that never reached SendGrid's tracking settings.
func TestPlainSingleEmailCarriesNoTrackingSettings(t *testing.T) {
	msg := mail.NewSingleEmail(
		mail.NewEmail("Lines Police CAD", "no-reply@linespolice-cad.com"),
		"Password Reset Request",
		mail.NewEmail("", "member@example.test"),
		"text",
		"<p>html</p>",
	)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(mail.GetRequestBody(msg), &body))
	assert.NotContains(t, body, "tracking_settings")
}

// The console reset send has a seam, so this one asserts the real message the
// handler hands to SendGrid rather than a reconstruction of it.
func TestSendUserResetEmailSendsTrackingOff(t *testing.T) {
	t.Setenv("SENDGRID_API_KEY", "test-key")

	var sent *mail.SGMailV3
	original := sendGridSendFn
	sendGridSendFn = func(msg *mail.SGMailV3) (int, string, error) {
		sent = msg
		return 202, "", nil
	}
	t.Cleanup(func() { sendGridSendFn = original })

	require.NoError(t, sendUserResetEmail("member@example.test", "https://www.linespolice-cad.com/reset/abc"))
	require.NotNil(t, sent)

	t.Logf("POST /v3/mail/send body:\n%s", indentJSON(t, mail.GetRequestBody(sent)))
	assertTrackingOff(t, sent)

	// And the link in the email is still ours, untouched.
	body := string(mail.GetRequestBody(sent))
	assert.Contains(t, body, "https://www.linespolice-cad.com/reset/abc")
	assert.NotContains(t, body, "linespolice-cad.com/ls/click")
}

// untrackedSends are the sends that carry a verification code, a reset token or
// a link into someone's account. Each must build its message with
// newAccountEmail; a mail.NewSingleEmail call inside one of these functions is
// an email going out with tracking on.
var untrackedSends = map[string][]string{
	"accountverification.go": {"sendSensitiveEmailAsync"},
	"admin.go":               {"sendResetEmail", "sendAdminResetEmail"},
	"admin_password_help.go": {"sendUserResetEmail"},
	"pendingverification.go": {"CreatePendingVerificationHandler", "sendVerificationEmail"},
}

func TestAccountEmailsAreBuiltUntracked(t *testing.T) {
	for file, functions := range untrackedSends {
		source, err := os.ReadFile(file)
		require.NoError(t, err, "in-scope file %s has moved; update untrackedSends", file)

		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, file, source, 0)
		require.NoError(t, err)

		for _, name := range functions {
			decl := findFunc(parsed, name)
			require.NotNil(t, decl, "%s: %s has gone; update untrackedSends", file, name)

			body := string(source[fset.Position(decl.Pos()).Offset:fset.Position(decl.End()).Offset])
			assert.Contains(t, body, "newAccountEmail(",
				"%s: %s must build its message with newAccountEmail", file, name)
			assert.NotContains(t, body, "mail.NewSingleEmail(",
				"%s: %s sends an account email with SendGrid tracking left on", file, name)
		}
	}
}

func findFunc(file *ast.File, name string) *ast.FuncDecl {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == name {
			return fn
		}
	}
	return nil
}

func indentJSON(t *testing.T, raw []byte) string {
	t.Helper()
	var out strings.Builder
	var pretty map[string]interface{}
	if err := json.Unmarshal(raw, &pretty); err != nil {
		return string(raw)
	}
	encoder := json.NewEncoder(&out)
	encoder.SetIndent("", "  ")
	require.NoError(t, encoder.Encode(pretty))
	return out.String()
}
