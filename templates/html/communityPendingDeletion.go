package templates

import (
	"fmt"
	"time"
)

// RenderCommunityPendingDeletionReminderEmail builds the HTML for the
// 24-hour-before-hard-delete reminder. Owner cannot self-restore; the email
// directs them to contact support if they need the community back.
func RenderCommunityPendingDeletionReminderEmail(displayName, communityName string, scheduledDeletionAt time.Time) string {
	if displayName == "" {
		displayName = "there"
	}
	scheduled := "shortly"
	if !scheduledDeletionAt.IsZero() {
		scheduled = scheduledDeletionAt.Format("Mon, Jan 2 2006 15:04 UTC")
	}

	return fmt.Sprintf(`<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Strict//EN" "http://www.w3.org/TR/xhtml1/DTD/xhtml1-strict.dtd">
<html xmlns="http://www.w3.org/1999/xhtml">
<head>
  <meta http-equiv="Content-Type" content="text/html; charset=utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1, minimum-scale=1, maximum-scale=1">
  <title>Community Deletion Reminder - Lines Police CAD</title>
  <style type="text/css">
    body { font-family: 'Segoe UI', Tahoma, Geneva, Verdana, sans-serif; margin: 0; padding: 0; background-color: #0a0a0f; }
    .container { max-width: 600px; margin: 0 auto; background-color: #12121f; }
    .header { background: linear-gradient(135deg, #ef4444 0%%, #b91c1c 100%%); padding: 40px 30px; text-align: center; }
    .header h1 { color: #fff; margin: 0; font-size: 22px; font-weight: 700; }
    .content { padding: 40px 30px; color: #e5e7eb; }
    .content h2 { color: #fff; margin-top: 0; }
    .highlight-box { background: rgba(239, 68, 68, 0.1); border: 1px solid rgba(239, 68, 68, 0.3); border-radius: 12px; padding: 20px; margin: 20px 0; }
    .highlight-box strong { color: #fca5a5; }
    .footer { padding: 30px; text-align: center; color: #6b7280; font-size: 12px; border-top: 1px solid rgba(255,255,255,0.1); }
    .footer a { color: #38bdf8; text-decoration: none; }
  </style>
</head>
<body>
  <div class="container">
    <div class="header">
      <h1>Last chance: %s deletes in 24 hours</h1>
    </div>
    <div class="content">
      <h2>Hi %s,</h2>
      <p>This is a heads-up that your community <strong>%s</strong> is scheduled for permanent deletion on <strong>%s</strong>.</p>

      <div class="highlight-box">
        <p style="margin: 0;">Once deleted, all civilians, departments, vehicles, calls, court cases, BOLOs, and other community data will be <strong>permanently removed</strong>. This cannot be undone after the deadline.</p>
      </div>

      <p>If you scheduled this deletion intentionally, no action is needed.</p>
      <p>If this was a mistake or you've changed your mind, please reply to this email or contact support before the deadline so we can restore the community.</p>
    </div>
    <div class="footer">
      <p>Lines Police CAD &middot; <a href="https://linespolice-cad.com">linespolice-cad.com</a></p>
    </div>
  </div>
</body>
</html>`, communityName, displayName, communityName, scheduled)
}
