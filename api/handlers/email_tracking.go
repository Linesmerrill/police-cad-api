package handlers

import (
	"github.com/sendgrid/sendgrid-go/helpers/mail"
)

// SendGrid tracking settings for the emails that carry a token or an account
// link.
//
// SendGrid click tracking rewrites every link in an outgoing email to
// http://url<n>.linespolice-cad.com/ls/click?..., the link-branding host. That
// host answers on http and gives no response on https, so a tracked link opens
// a browser "this site doesn't support a secure connection" warning, and the
// token in the link travels through a third-party redirect on its way back to
// us. Account emails are built with newAccountEmail so they go out untracked.

// noTrackingSettings is click tracking off, in HTML and in plain text, and open
// tracking off. The sendgrid-go setters take addresses of bools, so a false
// here is sent as false rather than dropped by omitempty.
func noTrackingSettings() *mail.TrackingSettings {
	settings := mail.NewTrackingSettings()
	settings.SetClickTracking(mail.NewClickTrackingSetting().SetEnable(false).SetEnableText(false))
	settings.SetOpenTracking(mail.NewOpenTrackingSetting().SetEnable(false))
	return settings
}

// newAccountEmail builds a single transactional email with tracking off.
//
// Every send in this package that carries a verification code, a reset token or
// a link into someone's account goes through here instead of calling
// mail.NewSingleEmail directly, and TestAccountEmailsAreBuiltUntracked fails if
// one stops doing so.
func newAccountEmail(from *mail.Email, subject string, to *mail.Email, plainTextContent, htmlContent string) *mail.SGMailV3 {
	msg := mail.NewSingleEmail(from, subject, to, plainTextContent, htmlContent)
	msg.SetTrackingSettings(noTrackingSettings())
	return msg
}
