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
// us. Those sends call SetTrackingSettings(noTrackingSettings()) on the
// message so they go out untracked.
//
// The settings are applied to the message rather than folded into a shared
// constructor on purpose. mail.NewSingleEmail is the sink for the
// go/email-injection alerts this package already carries, and routing every
// send through one constructor would move those alerts into this file and
// report pre-existing debt as new. Each send keeps its own call, so the
// analysis of them is unchanged by this fix.

// noTrackingSettings is click tracking off, in HTML and in plain text, and open
// tracking off. The sendgrid-go setters take addresses of bools, so a false
// here is sent as false rather than dropped by omitempty; a dropped field would
// mean SendGrid applies the account default, which is tracking on.
func noTrackingSettings() *mail.TrackingSettings {
	settings := mail.NewTrackingSettings()
	settings.SetClickTracking(mail.NewClickTrackingSetting().SetEnable(false).SetEnableText(false))
	settings.SetOpenTracking(mail.NewOpenTrackingSetting().SetEnable(false))
	return settings
}
