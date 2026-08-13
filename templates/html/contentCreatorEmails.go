package templates

import (
	"fmt"
	"strings"
)

// ContentCreatorEmailData holds data for content creator email templates
type ContentCreatorEmailData struct {
	DisplayName     string
	Status          string // approved, rejected
	RejectionReason string
	Feedback        string
	ApplicantName   string
	PrimaryPlatform string
	TotalFollowers  string
}

// RenderApplicationSubmittedEmail generates the HTML for the application submitted confirmation email
func RenderApplicationSubmittedEmail(displayName string) string {
	return fmt.Sprintf(`<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Strict//EN" "http://www.w3.org/TR/xhtml1/DTD/xhtml1-strict.dtd">
<html xmlns="http://www.w3.org/1999/xhtml">
<head>
  <meta http-equiv="Content-Type" content="text/html; charset=utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1, minimum-scale=1, maximum-scale=1">
  <title>Application Received - Lines Police CAD Creator Program</title>
  <style type="text/css">
    body { font-family: 'Segoe UI', Tahoma, Geneva, Verdana, sans-serif; margin: 0; padding: 0; background-color: #0a0a0f; }
    .container { max-width: 600px; margin: 0 auto; background-color: #12121f; }
    .header { background: linear-gradient(135deg, #fbbf24 0%%, #f59e0b 100%%); padding: 40px 30px; text-align: center; }
    .header h1 { color: #000; margin: 0; font-size: 24px; font-weight: 700; }
    .content { padding: 40px 30px; color: #e5e7eb; }
    .content h2 { color: #fff; margin-top: 0; }
    .highlight-box { background: rgba(251, 191, 36, 0.1); border: 1px solid rgba(251, 191, 36, 0.3); border-radius: 12px; padding: 20px; margin: 20px 0; }
    .highlight-box h3 { color: #fbbf24; margin-top: 0; font-size: 16px; }
    .timeline { margin: 30px 0; }
    .timeline-item { display: flex; margin-bottom: 15px; }
    .timeline-icon { width: 24px; height: 24px; background: #fbbf24; border-radius: 50%%; margin-right: 15px; flex-shrink: 0; }
    .timeline-text { color: #9ca3af; font-size: 14px; line-height: 24px; }
    .cta-button { display: inline-block; background: linear-gradient(135deg, #fbbf24 0%%, #f59e0b 100%%); color: #000; padding: 14px 28px; border-radius: 8px; text-decoration: none; font-weight: 700; margin-top: 20px; }
    .footer { padding: 30px; text-align: center; color: #6b7280; font-size: 12px; border-top: 1px solid rgba(255,255,255,0.1); }
    .footer a { color: #fbbf24; text-decoration: none; }
  </style>
</head>
<body>
  <div class="container">
    <div class="header">
      <h1>🎬 Application Received!</h1>
    </div>
    <div class="content">
      <h2>Hi %s,</h2>
      <p>Thank you for applying to the <strong>Lines Police CAD Content Creator Program</strong>! We're excited to review your application.</p>

      <div class="highlight-box">
        <h3>⚡ One thing we need from you</h3>
        <p style="margin-bottom: 0;">Before your application goes to our team, we confirm you own the channels you listed. Grab your <strong>verification code</strong> and add it to your channel description &mdash; it takes about a minute, and your application will not move forward until it is done.</p>
      </div>

      <a href="https://www.linespolice-cad.com/content-creators/me" class="cta-button">Get my verification code</a>

      <div class="timeline">
        <div class="timeline-item">
          <div class="timeline-icon"></div>
          <div class="timeline-text"><strong>Step 1:</strong> Application submitted ✓</div>
        </div>
        <div class="timeline-item">
          <div class="timeline-icon" style="background: #fbbf24;"></div>
          <div class="timeline-text"><strong>Step 2:</strong> <strong style="color:#fbbf24;">Your turn</strong> &mdash; add your verification code to each channel&rsquo;s description, then press Check</div>
        </div>
        <div class="timeline-item">
          <div class="timeline-icon" style="background: #6b7280;"></div>
          <div class="timeline-text"><strong>Step 3:</strong> Automatic checks &mdash; we confirm the channel exists, that the code is there, and read your follower count</div>
        </div>
        <div class="timeline-item">
          <div class="timeline-icon" style="background: #6b7280;"></div>
          <div class="timeline-text"><strong>Step 4:</strong> Team review &mdash; two of our team look at applications that passed the checks</div>
        </div>
        <div class="timeline-item">
          <div class="timeline-icon" style="background: #6b7280;"></div>
          <div class="timeline-text"><strong>Step 5:</strong> You&rsquo;ll receive an email with our decision</div>
        </div>
      </div>

      <p style="color: #9ca3af; font-size: 14px;">A few things worth knowing: you can remove the code once a channel is verified, we re-check automatically every few hours so you do not have to sit on the page, and your follower count is read from the channel itself &mdash; so do not worry if the number you entered was not exact. TikTok has no public API, so our team confirms those by eye.</p>

      <p style="margin-top: 24px; color: #9ca3af; font-size: 14px;">Any questions about the program or your application, just reach out to our support team.</p>
    </div>
    <div class="footer">
      <p>© Lines Police CAD | <a href="https://www.linespolice-cad.com">linespolice-cad.com</a></p>
      <p><a href="https://www.linespolice-cad.com/contact-us">Contact Support</a></p>
    </div>
  </div>
</body>
</html>`, displayName)
}

// RenderAdminNewApplicationEmail generates the HTML for the admin notification email
func RenderAdminNewApplicationEmail(applicantName, displayName, primaryPlatform, totalFollowers string) string {
	return fmt.Sprintf(`<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Strict//EN" "http://www.w3.org/TR/xhtml1/DTD/xhtml1-strict.dtd">
<html xmlns="http://www.w3.org/1999/xhtml">
<head>
  <meta http-equiv="Content-Type" content="text/html; charset=utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1, minimum-scale=1, maximum-scale=1">
  <title>New Creator Application - Lines Police CAD</title>
  <style type="text/css">
    body { font-family: 'Segoe UI', Tahoma, Geneva, Verdana, sans-serif; margin: 0; padding: 0; background-color: #0a0a0f; }
    .container { max-width: 600px; margin: 0 auto; background-color: #12121f; }
    .header { background: linear-gradient(135deg, #3b82f6 0%%, #1d4ed8 100%%); padding: 30px; text-align: center; }
    .header h1 { color: #fff; margin: 0; font-size: 22px; font-weight: 700; }
    .content { padding: 30px; color: #e5e7eb; }
    .info-grid { display: table; width: 100%%; margin: 20px 0; }
    .info-row { display: table-row; }
    .info-label { display: table-cell; padding: 10px 15px 10px 0; color: #9ca3af; font-size: 14px; width: 40%%; }
    .info-value { display: table-cell; padding: 10px 0; color: #fff; font-size: 14px; font-weight: 600; }
    .alert-box { background: rgba(251, 191, 36, 0.1); border: 1px solid rgba(251, 191, 36, 0.3); border-radius: 8px; padding: 15px; margin: 20px 0; }
    .alert-box p { margin: 0; color: #fbbf24; font-size: 14px; }
    .cta-button { display: inline-block; background: linear-gradient(135deg, #fbbf24 0%%, #f59e0b 100%%); color: #000; padding: 14px 28px; border-radius: 8px; text-decoration: none; font-weight: 700; margin-top: 15px; }
    .footer { padding: 20px 30px; text-align: center; color: #6b7280; font-size: 12px; border-top: 1px solid rgba(255,255,255,0.1); }
  </style>
</head>
<body>
  <div class="container">
    <div class="header">
      <h1>📝 New Creator Application</h1>
    </div>
    <div class="content">
      <p>A new application has been submitted to the Content Creator Program and requires review.</p>

      <div class="info-grid">
        <div class="info-row">
          <div class="info-label">Applicant:</div>
          <div class="info-value">%s</div>
        </div>
        <div class="info-row">
          <div class="info-label">Display Name:</div>
          <div class="info-value">%s</div>
        </div>
        <div class="info-row">
          <div class="info-label">Primary Platform:</div>
          <div class="info-value">%s</div>
        </div>
        <div class="info-row">
          <div class="info-label">Total Followers:</div>
          <div class="info-value">%s</div>
        </div>
      </div>

      <div class="alert-box">
        <p>⏱️ <strong>Reminder:</strong> Applications should be reviewed within 3-5 business days. This application requires at least 2 admin approvals.</p>
      </div>

      <a href="https://www.linespolice-cad.com/admin/console" class="cta-button">Review in Admin Console</a>
    </div>
    <div class="footer">
      <p>Lines Police CAD Admin Notification</p>
    </div>
  </div>
</body>
</html>`, applicantName, displayName, strings.Title(primaryPlatform), totalFollowers)
}

// RenderApplicationApprovedEmail generates the HTML for the approval notification email
func RenderApplicationApprovedEmail(displayName string) string {
	return fmt.Sprintf(`<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Strict//EN" "http://www.w3.org/TR/xhtml1/DTD/xhtml1-strict.dtd">
<html xmlns="http://www.w3.org/1999/xhtml">
<head>
  <meta http-equiv="Content-Type" content="text/html; charset=utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1, minimum-scale=1, maximum-scale=1">
  <title>Welcome to the Creator Program! - Lines Police CAD</title>
  <style type="text/css">
    body { font-family: 'Segoe UI', Tahoma, Geneva, Verdana, sans-serif; margin: 0; padding: 0; background-color: #0a0a0f; }
    .container { max-width: 600px; margin: 0 auto; background-color: #12121f; }
    .header { background: linear-gradient(135deg, #22c55e 0%%, #16a34a 100%%); padding: 40px 30px; text-align: center; }
    .header h1 { color: #fff; margin: 0; font-size: 26px; font-weight: 700; }
    .content { padding: 40px 30px; color: #e5e7eb; }
    .content h2 { color: #fff; margin-top: 0; }
    .success-box { background: rgba(34, 197, 94, 0.1); border: 1px solid rgba(34, 197, 94, 0.3); border-radius: 12px; padding: 25px; margin: 25px 0; text-align: center; }
    .success-box h3 { color: #22c55e; margin-top: 0; font-size: 20px; }
    .benefits { margin: 30px 0; }
    .benefit-item { background: rgba(255,255,255,0.03); border-radius: 8px; padding: 15px; margin-bottom: 10px; display: flex; align-items: center; }
    .benefit-icon { font-size: 24px; margin-right: 15px; }
    .benefit-text { color: #e5e7eb; }
    .benefit-text strong { color: #fff; }
    .cta-button { display: inline-block; background: linear-gradient(135deg, #fbbf24 0%%, #f59e0b 100%%); color: #000; padding: 14px 28px; border-radius: 8px; text-decoration: none; font-weight: 700; margin-top: 20px; }
    .note-box { background: rgba(251, 191, 36, 0.1); border: 1px solid rgba(251, 191, 36, 0.3); border-radius: 8px; padding: 15px; margin: 25px 0; }
    .note-box p { margin: 0; color: #fbbf24; font-size: 14px; }
    .footer { padding: 30px; text-align: center; color: #6b7280; font-size: 12px; border-top: 1px solid rgba(255,255,255,0.1); }
    .footer a { color: #fbbf24; text-decoration: none; }
  </style>
</head>
<body>
  <div class="container">
    <div class="header">
      <h1>🎉 Congratulations!</h1>
    </div>
    <div class="content">
      <h2>Welcome to the Creator Program, %s!</h2>
      <p>We're thrilled to inform you that your application to the <strong>Lines Police CAD Content Creator Program</strong> has been <strong>approved</strong>!</p>

      <div class="success-box">
        <h3>✓ You're officially a Lines Police CAD Creator!</h3>
        <p style="margin: 0; color: #9ca3af;">Your benefits are now active and ready to use.</p>
      </div>

      <h3 style="color: #fff; margin-bottom: 15px;">🎁 Your Benefits Include:</h3>
      <div class="benefits">
        <div class="benefit-item">
          <div class="benefit-icon">👤</div>
          <div class="benefit-text"><strong>Premium Plus, our highest tier</strong> - Automatically activated on your account</div>
        </div>
        <div class="benefit-item">
          <div class="benefit-icon">🏢</div>
          <div class="benefit-text"><strong>Premium community boost</strong> - Apply to one community you own</div>
        </div>
        <div class="benefit-item">
          <div class="benefit-icon">⭐</div>
          <div class="benefit-text"><strong>Featured Profile</strong> - Showcase your content on our creators page</div>
        </div>
      </div>

      <div class="note-box">
        <p>💡 <strong>Next Step:</strong> Visit your Creator Dashboard to claim your free Premium boost for one of your communities!</p>
      </div>

      <a href="https://www.linespolice-cad.com/content-creators/me" class="cta-button">Go to Creator Dashboard</a>

      <p style="margin-top: 30px; color: #9ca3af; font-size: 14px;">Thank you for being part of the Lines Police CAD community. We can't wait to see your content!</p>
    </div>
    <div class="footer">
      <p>© Lines Police CAD | <a href="https://www.linespolice-cad.com">linespolice-cad.com</a></p>
      <p><a href="https://www.linespolice-cad.com/contact-us">Contact Support</a></p>
    </div>
  </div>
</body>
</html>`, displayName)
}

// RenderCreatorTierUpgradeEmail generates the one-off announcement telling
// existing creators their program benefits have been raised to the top tier.
// annualValue is the combined yearly saving, e.g. "$195".
func RenderCreatorTierUpgradeEmail(displayName, annualValue string) string {
	return fmt.Sprintf(`<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Strict//EN" "http://www.w3.org/TR/xhtml1/DTD/xhtml1-strict.dtd">
<html xmlns="http://www.w3.org/1999/xhtml">
<head>
  <meta http-equiv="Content-Type" content="text/html; charset=utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1, minimum-scale=1, maximum-scale=1">
  <title>Your Creator benefits just got bigger - Lines Police CAD</title>
  <style type="text/css">
    body { font-family: 'Segoe UI', Tahoma, Geneva, Verdana, sans-serif; margin: 0; padding: 0; background-color: #0a0a0f; }
    .container { max-width: 600px; margin: 0 auto; background-color: #12121f; }
    .header { background: linear-gradient(135deg, #fbbf24 0%%, #f59e0b 100%%); padding: 40px 30px; text-align: center; }
    .header h1 { color: #000; margin: 0; font-size: 26px; font-weight: 700; }
    .content { padding: 40px 30px; color: #e5e7eb; }
    .content h2 { color: #fff; margin-top: 0; }
    .success-box { background: rgba(251, 191, 36, 0.1); border: 1px solid rgba(251, 191, 36, 0.3); border-radius: 12px; padding: 25px; margin: 25px 0; text-align: center; }
    .success-box h3 { color: #fbbf24; margin-top: 0; font-size: 20px; }
    .value { font-size: 34px; font-weight: 800; color: #fbbf24; margin: 4px 0; }
    .benefits { margin: 30px 0; }
    .benefit-item { background: rgba(255,255,255,0.03); border-radius: 8px; padding: 15px; margin-bottom: 10px; }
    .benefit-text { color: #e5e7eb; }
    .benefit-text strong { color: #fff; }
    .cta-button { display: inline-block; background: linear-gradient(135deg, #fbbf24 0%%, #f59e0b 100%%); color: #000; padding: 14px 28px; border-radius: 8px; text-decoration: none; font-weight: 700; margin-top: 20px; }
    .footer { padding: 30px; text-align: center; color: #6b7280; font-size: 12px; border-top: 1px solid rgba(255,255,255,0.1); }
    .footer a { color: #fbbf24; text-decoration: none; }
  </style>
</head>
<body>
  <div class="container">
    <div class="header">
      <h1>🚀 Your Creator benefits just got bigger</h1>
    </div>
    <div class="content">
      <h2>Great news, %s!</h2>
      <p>We're raising what the <strong>Content Creator Program</strong> gives you. You are being moved up to <strong>Premium Plus</strong>, our highest subscription tier, completely free of charge.</p>

      <div class="success-box">
        <h3>Your new yearly value</h3>
        <div class="value">%s</div>
        <p style="margin: 0; color: #9ca3af;">saved every year, on us</p>
      </div>

      <div class="benefits">
        <div class="benefit-item">
          <div class="benefit-text"><strong>Premium Plus on your account</strong> - unlimited communities, no ads, and a verified badge</div>
        </div>
        <div class="benefit-item">
          <div class="benefit-text"><strong>Premium boost for one community</strong> - promotion in search and Discover, plus a verified community badge</div>
        </div>
        <div class="benefit-item">
          <div class="benefit-text"><strong>Nothing to do</strong> - your account is already upgraded</div>
        </div>
      </div>

      <p>Why? Because you are awesome and you make great content. Creators like you are a big part of why people find Lines Police CAD in the first place, and we wanted your benefits to reflect that.</p>

      <p><strong>Keep up the awesome work. We appreciate you.</strong></p>

      <a href="https://www.linespolice-cad.com/content-creators/me" class="cta-button">View Your Benefits</a>
    </div>
    <div class="footer">
      <p>© Lines Police CAD | <a href="https://www.linespolice-cad.com">linespolice-cad.com</a></p>
      <p><a href="https://www.linespolice-cad.com/contact-us">Contact Support</a></p>
    </div>
  </div>
</body>
</html>`, displayName, annualValue)
}

// RenderCreatorRemovedEmail generates the HTML for the creator removal notification email
func RenderCreatorRemovedEmail(displayName, reason string) string {
	return fmt.Sprintf(`<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Strict//EN" "http://www.w3.org/TR/xhtml1/DTD/xhtml1-strict.dtd">
<html xmlns="http://www.w3.org/1999/xhtml">
<head>
  <meta http-equiv="Content-Type" content="text/html; charset=utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1, minimum-scale=1, maximum-scale=1">
  <title>Creator Program Removal Notice - Lines Police CAD</title>
  <style type="text/css">
    body { font-family: 'Segoe UI', Tahoma, Geneva, Verdana, sans-serif; margin: 0; padding: 0; background-color: #0a0a0f; }
    .container { max-width: 600px; margin: 0 auto; background-color: #12121f; }
    .header { background: linear-gradient(135deg, #ef4444 0%%, #dc2626 100%%); padding: 40px 30px; text-align: center; }
    .header h1 { color: #fff; margin: 0; font-size: 24px; font-weight: 700; }
    .content { padding: 40px 30px; color: #e5e7eb; }
    .content h2 { color: #fff; margin-top: 0; }
    .reason-box { background: rgba(239, 68, 68, 0.1); border: 1px solid rgba(239, 68, 68, 0.3); border-radius: 12px; padding: 20px; margin: 20px 0; }
    .reason-box h4 { color: #ef4444; margin-top: 0; margin-bottom: 10px; }
    .reason-box p { margin: 0; color: #e5e7eb; }
    .revoked-box { background: rgba(107, 114, 128, 0.1); border: 1px solid rgba(107, 114, 128, 0.3); border-radius: 12px; padding: 20px; margin: 25px 0; }
    .revoked-box h4 { color: #9ca3af; margin-top: 0; margin-bottom: 15px; }
    .revoked-item { display: flex; align-items: center; margin-bottom: 10px; color: #9ca3af; font-size: 14px; }
    .revoked-item:last-child { margin-bottom: 0; }
    .revoked-icon { margin-right: 10px; }
    .reapply-box { background: rgba(251, 191, 36, 0.1); border: 1px solid rgba(251, 191, 36, 0.3); border-radius: 8px; padding: 15px; margin: 25px 0; }
    .reapply-box p { margin: 0; color: #fbbf24; font-size: 14px; }
    .cta-button { display: inline-block; background: linear-gradient(135deg, #fbbf24 0%%, #f59e0b 100%%); color: #000; padding: 14px 28px; border-radius: 8px; text-decoration: none; font-weight: 700; margin-top: 20px; }
    .footer { padding: 30px; text-align: center; color: #6b7280; font-size: 12px; border-top: 1px solid rgba(255,255,255,0.1); }
    .footer a { color: #fbbf24; text-decoration: none; }
  </style>
</head>
<body>
  <div class="container">
    <div class="header">
      <h1>Creator Program Removal Notice</h1>
    </div>
    <div class="content">
      <h2>Hi %s,</h2>
      <p>We regret to inform you that you have been <strong>removed from the Lines Police CAD Content Creator Program</strong>.</p>

      <div class="reason-box">
        <h4>📋 Reason for Removal:</h4>
        <p>%s</p>
      </div>

      <div class="revoked-box">
        <h4>Benefits Revoked:</h4>
        <div class="revoked-item">
          <span class="revoked-icon">✕</span>
          <span>Premium Plus - No longer active on your account</span>
        </div>
        <div class="revoked-item">
          <span class="revoked-icon">✕</span>
          <span>Premium community boost - Removed from your community (if applied)</span>
        </div>
        <div class="revoked-item">
          <span class="revoked-icon">✕</span>
          <span>Featured Profile - Removed from creators directory</span>
        </div>
      </div>

      <div class="reapply-box">
        <p>🔄 If you believe this removal was made in error, or if you'd like to rejoin the program in the future, you are welcome to submit a new application.</p>
      </div>

      <a href="https://www.linespolice-cad.com/content-creators/apply" class="cta-button">Apply to Rejoin</a>

      <p style="margin-top: 30px; color: #9ca3af; font-size: 14px;">If you have any questions about this decision, please contact our support team.</p>
    </div>
    <div class="footer">
      <p>© Lines Police CAD | <a href="https://www.linespolice-cad.com">linespolice-cad.com</a></p>
      <p><a href="https://www.linespolice-cad.com/contact-us">Contact Support</a></p>
    </div>
  </div>
</body>
</html>`, displayName, reason)
}

// RenderApplicationRejectedEmail generates the HTML for the rejection notification email
func RenderApplicationRejectedEmail(displayName, rejectionReason, feedback string) string {
	feedbackSection := ""
	if feedback != "" {
		feedbackSection = fmt.Sprintf(`
      <div style="background: rgba(255,255,255,0.03); border-radius: 8px; padding: 15px; margin: 20px 0;">
        <h4 style="color: #fff; margin-top: 0; margin-bottom: 10px;">💬 Additional Feedback:</h4>
        <p style="margin: 0; color: #e5e7eb;">%s</p>
      </div>`, feedback)
	}

	return fmt.Sprintf(`<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Strict//EN" "http://www.w3.org/TR/xhtml1/DTD/xhtml1-strict.dtd">
<html xmlns="http://www.w3.org/1999/xhtml">
<head>
  <meta http-equiv="Content-Type" content="text/html; charset=utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1, minimum-scale=1, maximum-scale=1">
  <title>Application Update - Lines Police CAD Creator Program</title>
  <style type="text/css">
    body { font-family: 'Segoe UI', Tahoma, Geneva, Verdana, sans-serif; margin: 0; padding: 0; background-color: #0a0a0f; }
    .container { max-width: 600px; margin: 0 auto; background-color: #12121f; }
    .header { background: linear-gradient(135deg, #6b7280 0%%, #4b5563 100%%); padding: 40px 30px; text-align: center; }
    .header h1 { color: #fff; margin: 0; font-size: 24px; font-weight: 700; }
    .content { padding: 40px 30px; color: #e5e7eb; }
    .content h2 { color: #fff; margin-top: 0; }
    .reason-box { background: rgba(239, 68, 68, 0.1); border: 1px solid rgba(239, 68, 68, 0.3); border-radius: 12px; padding: 20px; margin: 20px 0; }
    .reason-box h4 { color: #ef4444; margin-top: 0; margin-bottom: 10px; }
    .reason-box p { margin: 0; color: #e5e7eb; }
    .encourage-box { background: rgba(251, 191, 36, 0.1); border: 1px solid rgba(251, 191, 36, 0.3); border-radius: 8px; padding: 15px; margin: 25px 0; }
    .encourage-box p { margin: 0; color: #fbbf24; font-size: 14px; }
    .cta-button { display: inline-block; background: linear-gradient(135deg, #fbbf24 0%%, #f59e0b 100%%); color: #000; padding: 14px 28px; border-radius: 8px; text-decoration: none; font-weight: 700; margin-top: 20px; }
    .footer { padding: 30px; text-align: center; color: #6b7280; font-size: 12px; border-top: 1px solid rgba(255,255,255,0.1); }
    .footer a { color: #fbbf24; text-decoration: none; }
  </style>
</head>
<body>
  <div class="container">
    <div class="header">
      <h1>Application Update</h1>
    </div>
    <div class="content">
      <h2>Hi %s,</h2>
      <p>Thank you for your interest in the <strong>Lines Police CAD Content Creator Program</strong>. After careful review by our team, we regret to inform you that your application was not approved at this time.</p>

      <div class="reason-box">
        <h4>📋 Reason:</h4>
        <p>%s</p>
      </div>
      %s
      <div class="encourage-box">
        <p>🔄 <strong>Don't give up!</strong> You're welcome to apply again in the future once you've addressed the feedback above. We'd love to have you in the program!</p>
      </div>

      <p>You can view more details about your application status:</p>
      <a href="https://www.linespolice-cad.com/content-creators/me" class="cta-button">View Application Status</a>

      <p style="margin-top: 30px; color: #9ca3af; font-size: 14px;">If you have any questions about this decision, please don't hesitate to reach out to our support team.</p>
    </div>
    <div class="footer">
      <p>© Lines Police CAD | <a href="https://www.linespolice-cad.com">linespolice-cad.com</a></p>
      <p><a href="https://www.linespolice-cad.com/contact-us">Contact Support</a></p>
    </div>
  </div>
</body>
</html>`, displayName, rejectionReason, feedbackSection)
}

// RenderLowFollowerWarningEmail generates the HTML for the low follower warning email
func RenderLowFollowerWarningEmail(displayName string, currentFollowers, threshold, gracePeriodDays int) string {
	return fmt.Sprintf(`<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Strict//EN" "http://www.w3.org/TR/xhtml1/DTD/xhtml1-strict.dtd">
<html xmlns="http://www.w3.org/1999/xhtml">
<head>
  <meta http-equiv="Content-Type" content="text/html; charset=utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1, minimum-scale=1, maximum-scale=1">
  <title>Action Required: Follower Count Below Minimum - Lines Police CAD</title>
  <style type="text/css">
    body { font-family: 'Segoe UI', Tahoma, Geneva, Verdana, sans-serif; margin: 0; padding: 0; background-color: #0a0a0f; }
    .container { max-width: 600px; margin: 0 auto; background-color: #12121f; }
    .header { background: linear-gradient(135deg, #f59e0b 0%%, #d97706 100%%); padding: 40px 30px; text-align: center; }
    .header h1 { color: #000; margin: 0; font-size: 24px; font-weight: 700; }
    .content { padding: 40px 30px; color: #e5e7eb; }
    .content h2 { color: #fff; margin-top: 0; }
    .warning-box { background: rgba(245, 158, 11, 0.1); border: 1px solid rgba(245, 158, 11, 0.3); border-radius: 12px; padding: 25px; margin: 25px 0; }
    .warning-box h3 { color: #f59e0b; margin-top: 0; font-size: 18px; }
    .stats-grid { display: table; width: 100%%; margin: 20px 0; }
    .stats-row { display: table-row; }
    .stats-label { display: table-cell; padding: 10px 15px 10px 0; color: #9ca3af; font-size: 14px; width: 50%%; }
    .stats-value { display: table-cell; padding: 10px 0; color: #fff; font-size: 14px; font-weight: 600; }
    .stats-value.warning { color: #f59e0b; }
    .action-box { background: rgba(255,255,255,0.03); border-radius: 8px; padding: 20px; margin: 25px 0; }
    .action-box h4 { color: #fff; margin-top: 0; margin-bottom: 15px; }
    .action-item { display: flex; margin-bottom: 12px; color: #e5e7eb; font-size: 14px; }
    .action-item:last-child { margin-bottom: 0; }
    .action-icon { margin-right: 10px; color: #fbbf24; }
    .cta-button { display: inline-block; background: linear-gradient(135deg, #fbbf24 0%%, #f59e0b 100%%); color: #000; padding: 14px 28px; border-radius: 8px; text-decoration: none; font-weight: 700; margin-top: 20px; }
    .footer { padding: 30px; text-align: center; color: #6b7280; font-size: 12px; border-top: 1px solid rgba(255,255,255,0.1); }
    .footer a { color: #fbbf24; text-decoration: none; }
  </style>
</head>
<body>
  <div class="container">
    <div class="header">
      <h1>⚠️ Action Required</h1>
    </div>
    <div class="content">
      <h2>Hi %s,</h2>
      <p>We noticed that your follower count has dropped below the minimum requirement for the <strong>Lines Police CAD Content Creator Program</strong>.</p>

      <div class="warning-box">
        <h3>📊 Current Status</h3>
        <div class="stats-grid">
          <div class="stats-row">
            <div class="stats-label">Your highest follower count:</div>
            <div class="stats-value warning">%d followers</div>
          </div>
          <div class="stats-row">
            <div class="stats-label">Minimum required:</div>
            <div class="stats-value">%d followers</div>
          </div>
          <div class="stats-row">
            <div class="stats-label">Time to resolve:</div>
            <div class="stats-value">%d days</div>
          </div>
        </div>
      </div>

      <div class="action-box">
        <h4>🎯 What you can do:</h4>
        <div class="action-item">
          <span class="action-icon">1.</span>
          <span>Continue creating great content to grow your audience</span>
        </div>
        <div class="action-item">
          <span class="action-icon">2.</span>
          <span>Once you're back above %d followers, visit your dashboard and sync your counts</span>
        </div>
        <div class="action-item">
          <span class="action-icon">3.</span>
          <span>Your account will automatically return to good standing</span>
        </div>
      </div>

      <p>Don't worry - we want to help you succeed! You have <strong>%d days</strong> to get your follower count back up. If your count is still below the minimum after this period, your creator account will be removed.</p>

      <a href="https://www.linespolice-cad.com/content-creators/me" class="cta-button">View My Dashboard</a>

      <p style="margin-top: 30px; color: #9ca3af; font-size: 14px;">If you have any questions or need assistance, please don't hesitate to contact our support team.</p>
    </div>
    <div class="footer">
      <p>© Lines Police CAD | <a href="https://www.linespolice-cad.com">linespolice-cad.com</a></p>
      <p><a href="https://www.linespolice-cad.com/contact-us">Contact Support</a></p>
    </div>
  </div>
</body>
</html>`, displayName, currentFollowers, threshold, gracePeriodDays, threshold, gracePeriodDays)
}

// RenderGracePeriodRecoveryEmail generates the HTML for the grace period recovery email
func RenderGracePeriodRecoveryEmail(displayName string, currentFollowers int) string {
	return fmt.Sprintf(`<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Strict//EN" "http://www.w3.org/TR/xhtml1/DTD/xhtml1-strict.dtd">
<html xmlns="http://www.w3.org/1999/xhtml">
<head>
  <meta http-equiv="Content-Type" content="text/html; charset=utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1, minimum-scale=1, maximum-scale=1">
  <title>Great News: Account Back in Good Standing! - Lines Police CAD</title>
  <style type="text/css">
    body { font-family: 'Segoe UI', Tahoma, Geneva, Verdana, sans-serif; margin: 0; padding: 0; background-color: #0a0a0f; }
    .container { max-width: 600px; margin: 0 auto; background-color: #12121f; }
    .header { background: linear-gradient(135deg, #22c55e 0%%, #16a34a 100%%); padding: 40px 30px; text-align: center; }
    .header h1 { color: #fff; margin: 0; font-size: 26px; font-weight: 700; }
    .content { padding: 40px 30px; color: #e5e7eb; }
    .content h2 { color: #fff; margin-top: 0; }
    .success-box { background: rgba(34, 197, 94, 0.1); border: 1px solid rgba(34, 197, 94, 0.3); border-radius: 12px; padding: 25px; margin: 25px 0; text-align: center; }
    .success-box h3 { color: #22c55e; margin-top: 0; font-size: 20px; }
    .stats-highlight { font-size: 36px; font-weight: 800; color: #22c55e; margin: 15px 0; }
    .benefits { margin: 30px 0; }
    .benefit-item { background: rgba(255,255,255,0.03); border-radius: 8px; padding: 15px; margin-bottom: 10px; display: flex; align-items: center; }
    .benefit-icon { font-size: 24px; margin-right: 15px; }
    .benefit-text { color: #e5e7eb; }
    .benefit-text strong { color: #fff; }
    .cta-button { display: inline-block; background: linear-gradient(135deg, #fbbf24 0%%, #f59e0b 100%%); color: #000; padding: 14px 28px; border-radius: 8px; text-decoration: none; font-weight: 700; margin-top: 20px; }
    .footer { padding: 30px; text-align: center; color: #6b7280; font-size: 12px; border-top: 1px solid rgba(255,255,255,0.1); }
    .footer a { color: #fbbf24; text-decoration: none; }
  </style>
</head>
<body>
  <div class="container">
    <div class="header">
      <h1>🎉 Congratulations!</h1>
    </div>
    <div class="content">
      <h2>Great news, %s!</h2>
      <p>Your follower count is now back above the minimum requirement. Your <strong>Lines Police CAD Content Creator</strong> account is in good standing!</p>

      <div class="success-box">
        <h3>✓ Account Restored</h3>
        <div class="stats-highlight">%d followers</div>
        <p style="margin: 0; color: #9ca3af;">Your current follower count</p>
      </div>

      <h3 style="color: #fff; margin-bottom: 15px;">🎁 Your Benefits Remain Active:</h3>
      <div class="benefits">
        <div class="benefit-item">
          <div class="benefit-icon">👤</div>
          <div class="benefit-text"><strong>Premium Plus</strong> - Still active on your account</div>
        </div>
        <div class="benefit-item">
          <div class="benefit-icon">🏢</div>
          <div class="benefit-text"><strong>Premium community boost</strong> - Still available for your community</div>
        </div>
        <div class="benefit-item">
          <div class="benefit-icon">⭐</div>
          <div class="benefit-text"><strong>Featured Profile</strong> - Visible in our creators directory</div>
        </div>
      </div>

      <p>Keep up the amazing work! We're thrilled to have you as part of the Lines Police CAD creator community.</p>

      <a href="https://www.linespolice-cad.com/content-creators/me" class="cta-button">View My Dashboard</a>

      <p style="margin-top: 30px; color: #9ca3af; font-size: 14px;">Thank you for being part of our community. Keep creating awesome content!</p>
    </div>
    <div class="footer">
      <p>© Lines Police CAD | <a href="https://www.linespolice-cad.com">linespolice-cad.com</a></p>
      <p><a href="https://www.linespolice-cad.com/contact-us">Contact Support</a></p>
    </div>
  </div>
</body>
</html>`, displayName, currentFollowers)
}

// RenderGracePeriodReminderEmail generates the HTML for the final reminder email (1 day before removal)
func RenderGracePeriodReminderEmail(displayName string, currentFollowers, threshold int) string {
	return fmt.Sprintf(`<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Strict//EN" "http://www.w3.org/TR/xhtml1/DTD/xhtml1-strict.dtd">
<html xmlns="http://www.w3.org/1999/xhtml">
<head>
  <meta http-equiv="Content-Type" content="text/html; charset=utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1, minimum-scale=1, maximum-scale=1">
  <title>Final Reminder: Account Removal Tomorrow - Lines Police CAD</title>
  <style type="text/css">
    body { font-family: 'Segoe UI', Tahoma, Geneva, Verdana, sans-serif; margin: 0; padding: 0; background-color: #0a0a0f; }
    .container { max-width: 600px; margin: 0 auto; background-color: #12121f; }
    .header { background: linear-gradient(135deg, #ef4444 0%%, #dc2626 100%%); padding: 40px 30px; text-align: center; }
    .header h1 { color: #fff; margin: 0; font-size: 24px; font-weight: 700; }
    .content { padding: 40px 30px; color: #e5e7eb; }
    .content h2 { color: #fff; margin-top: 0; }
    .urgent-box { background: rgba(239, 68, 68, 0.1); border: 1px solid rgba(239, 68, 68, 0.3); border-radius: 12px; padding: 25px; margin: 25px 0; }
    .urgent-box h3 { color: #ef4444; margin-top: 0; font-size: 18px; }
    .countdown { font-size: 48px; font-weight: 800; color: #ef4444; text-align: center; margin: 20px 0; }
    .countdown-label { text-align: center; color: #9ca3af; font-size: 14px; margin-bottom: 20px; }
    .stats-grid { display: table; width: 100%%; margin: 20px 0; }
    .stats-row { display: table-row; }
    .stats-label { display: table-cell; padding: 10px 15px 10px 0; color: #9ca3af; font-size: 14px; width: 50%%; }
    .stats-value { display: table-cell; padding: 10px 0; color: #fff; font-size: 14px; font-weight: 600; }
    .stats-value.danger { color: #ef4444; }
    .action-box { background: rgba(251, 191, 36, 0.1); border: 1px solid rgba(251, 191, 36, 0.3); border-radius: 8px; padding: 20px; margin: 25px 0; }
    .action-box h4 { color: #fbbf24; margin-top: 0; }
    .cta-button { display: inline-block; background: linear-gradient(135deg, #fbbf24 0%%, #f59e0b 100%%); color: #000; padding: 14px 28px; border-radius: 8px; text-decoration: none; font-weight: 700; margin-top: 20px; }
    .footer { padding: 30px; text-align: center; color: #6b7280; font-size: 12px; border-top: 1px solid rgba(255,255,255,0.1); }
    .footer a { color: #fbbf24; text-decoration: none; }
  </style>
</head>
<body>
  <div class="container">
    <div class="header">
      <h1>⏰ Final Reminder</h1>
    </div>
    <div class="content">
      <h2>Hi %s,</h2>
      <p>This is your <strong>final reminder</strong> that your creator account will be removed tomorrow due to low follower count.</p>

      <div class="urgent-box">
        <h3>⚠️ Account Removal in:</h3>
        <div class="countdown">24 Hours</div>
        <div class="countdown-label">Time remaining to resolve</div>
        <div class="stats-grid">
          <div class="stats-row">
            <div class="stats-label">Your current followers:</div>
            <div class="stats-value danger">%d followers</div>
          </div>
          <div class="stats-row">
            <div class="stats-label">Minimum required:</div>
            <div class="stats-value">%d followers</div>
          </div>
        </div>
      </div>

      <div class="action-box">
        <h4>🚀 Last chance to save your account:</h4>
        <p style="margin: 0;">If you've recently gained followers and are now above %d, visit your dashboard immediately to sync your updated counts. Your account will automatically return to good standing.</p>
      </div>

      <a href="https://www.linespolice-cad.com/content-creators/me" class="cta-button">Sync My Followers Now</a>

      <p style="margin-top: 30px; color: #9ca3af; font-size: 14px;">If your account is removed, you'll lose access to all creator benefits. However, you're always welcome to reapply in the future.</p>
    </div>
    <div class="footer">
      <p>© Lines Police CAD | <a href="https://www.linespolice-cad.com">linespolice-cad.com</a></p>
      <p><a href="https://www.linespolice-cad.com/contact-us">Contact Support</a></p>
    </div>
  </div>
</body>
</html>`, displayName, currentFollowers, threshold, threshold)
}

// RenderChecksFailedEmail tells an applicant that the automatic checks found a
// problem, and exactly which one.
//
// Before this existed, a failed application notified nobody: admins are only
// emailed when checks pass and applicants only when rejected, so an application
// that failed screening and was never rejected simply sat there.
//
// ownershipProven changes the ending: someone who has proved a channel is
// theirs is a real applicant who fell short of a requirement, and a human is
// also looking. Someone who has not is told only how to fix it.
func RenderChecksFailedEmail(displayName string, reasons []string, ownershipProven bool) string {
	items := ""
	for _, r := range reasons {
		items += `<li style="margin-bottom:10px;">` + r + `</li>`
	}
	if items == "" {
		items = `<li>One of our automatic checks did not pass.</li>`
	}

	closing := `Your application stays open. Fix the above and we will pick it up automatically &mdash; there is no need to reapply.`
	if ownershipProven {
		closing = `Your application stays open, and because you have already verified your channel a member of our team will take a look too. Fix the above and we will pick it up automatically &mdash; there is no need to reapply.`
	}

	return fmt.Sprintf(`<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Strict//EN" "http://www.w3.org/TR/xhtml1/DTD/xhtml1-strict.dtd">
<html xmlns="http://www.w3.org/1999/xhtml">
<head>
  <meta http-equiv="Content-Type" content="text/html; charset=utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1, minimum-scale=1, maximum-scale=1">
  <title>Your application needs a change - Lines Police CAD</title>
  <style type="text/css">
    body { font-family: 'Segoe UI', Tahoma, Geneva, Verdana, sans-serif; margin: 0; padding: 0; background-color: #0a0a0f; }
    .container { max-width: 600px; margin: 0 auto; background-color: #12121f; }
    .header { background: linear-gradient(135deg, #f59e0b 0%%, #d97706 100%%); padding: 36px 30px; text-align: center; }
    .header h1 { color: #fff; margin: 0; font-size: 24px; font-weight: 700; }
    .content { padding: 36px 30px; color: #e5e7eb; }
    .content h2 { color: #fff; margin-top: 0; }
    .reasons { background: rgba(251,191,36,0.08); border: 1px solid rgba(251,191,36,0.3); border-radius: 12px; padding: 20px 20px 20px 38px; margin: 22px 0; }
    .reasons li { color: #fde68a; line-height: 1.6; }
    .cta-button { display: inline-block; background: linear-gradient(135deg, #fbbf24 0%%, #f59e0b 100%%); color: #000; padding: 14px 28px; border-radius: 8px; text-decoration: none; font-weight: 700; margin-top: 18px; }
    .footer { padding: 28px 30px; text-align: center; color: #6b7280; font-size: 12px; border-top: 1px solid rgba(255,255,255,0.1); }
    .footer a { color: #fbbf24; text-decoration: none; }
  </style>
</head>
<body>
  <div class="container">
    <div class="header">
      <h1>Your application needs a change</h1>
    </div>
    <div class="content">
      <h2>Hi %s,</h2>
      <p>We ran the automatic checks on your Content Creator Program application, and something needs your attention:</p>

      <ul class="reasons">%s</ul>

      <p>%s</p>

      <a href="https://www.linespolice-cad.com/content-creators/me" class="cta-button">Open my application</a>

      <p style="margin-top: 28px; color: #9ca3af; font-size: 14px;">If you think this is wrong, reply to this email or contact support and a person will look at it.</p>
    </div>
    <div class="footer">
      <p>&copy; Lines Police CAD | <a href="https://www.linespolice-cad.com">linespolice-cad.com</a></p>
      <p><a href="https://www.linespolice-cad.com/contact-us">Contact Support</a></p>
    </div>
  </div>
</body>
</html>`, displayName, items, closing)
}

// RenderRequirementNotMetEmail tells an applicant their application did not meet
// a published program requirement.
//
// Separate from RenderApplicationRejectedEmail on purpose: that one opens with
// "after careful review by our team", which would be a lie here. Nobody
// reviewed this — a number was measured against a published minimum. Saying so
// plainly is kinder than implying a person weighed them up and said no, and it
// makes the way back obvious: change the number, apply again.
//
// requirement is the rule in the applicant's words ("At least 500 followers on
// one channel"). measured is what we actually read. steps are the numbered
// instructions for reapplying.
func RenderRequirementNotMetEmail(displayName, requirement, measured string, steps []string) string {
	stepItems := ""
	for _, s := range steps {
		stepItems += `<li style="margin-bottom:8px;">` + s + `</li>`
	}

	return fmt.Sprintf(`<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Strict//EN" "http://www.w3.org/TR/xhtml1/DTD/xhtml1-strict.dtd">
<html xmlns="http://www.w3.org/1999/xhtml">
<head>
  <meta http-equiv="Content-Type" content="text/html; charset=utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1, minimum-scale=1, maximum-scale=1">
  <title>About your Creator Program application - Lines Police CAD</title>
  <style type="text/css">
    body { font-family: 'Segoe UI', Tahoma, Geneva, Verdana, sans-serif; margin: 0; padding: 0; background-color: #0a0a0f; }
    .container { max-width: 600px; margin: 0 auto; background-color: #12121f; }
    .header { background: linear-gradient(135deg, #6b7280 0%%, #4b5563 100%%); padding: 36px 30px; text-align: center; }
    .header h1 { color: #fff; margin: 0; font-size: 24px; font-weight: 700; }
    .content { padding: 36px 30px; color: #e5e7eb; line-height: 1.6; }
    .content h2 { color: #fff; margin-top: 0; }
    .req-box { background: rgba(255,255,255,0.03); border: 1px solid rgba(255,255,255,0.1); border-radius: 12px; padding: 20px; margin: 22px 0; }
    .req-label { color: #9ca3af; font-size: 12px; text-transform: uppercase; letter-spacing: 0.06em; margin: 0 0 6px; }
    .req-value { color: #fff; font-weight: 700; margin: 0 0 16px; }
    .req-value-last { color: #fff; font-weight: 700; margin: 0; }
    .steps { background: rgba(34,197,94,0.07); border: 1px solid rgba(34,197,94,0.28); border-radius: 12px; padding: 20px 20px 20px 38px; margin: 22px 0; }
    .steps li { color: #bbf7d0; }
    .cta-button { display: inline-block; background: linear-gradient(135deg, #fbbf24 0%%, #f59e0b 100%%); color: #000; padding: 14px 28px; border-radius: 8px; text-decoration: none; font-weight: 700; margin-top: 8px; }
    .footer { padding: 28px 30px; text-align: center; color: #6b7280; font-size: 12px; border-top: 1px solid rgba(255,255,255,0.1); }
    .footer a { color: #fbbf24; text-decoration: none; }
  </style>
</head>
<body>
  <div class="container">
    <div class="header">
      <h1>About your application</h1>
    </div>
    <div class="content">
      <h2>Hi %s,</h2>
      <p>Thank you for applying to the <strong>Lines Police CAD Content Creator Program</strong>. We are not able to accept this application, because it does not meet one of the program requirements.</p>

      <div class="req-box">
        <p class="req-label">The requirement</p>
        <p class="req-value">%s</p>
        <p class="req-label">What we found</p>
        <p class="req-value-last">%s</p>
      </div>

      <p>This one is measured automatically from your channel, so there is nothing you need to appeal &mdash; and nothing stopping you from applying again the moment it changes.</p>

      <p style="margin-bottom:0;"><strong style="color:#fff;">When you are ready to reapply:</strong></p>
      <ol class="steps">%s</ol>

      <a href="https://www.linespolice-cad.com/content-creators/apply" class="cta-button">Apply again</a>

      <p style="margin-top: 28px; color: #9ca3af; font-size: 14px;">If you think we measured the wrong channel, reply to this email and a person will take a look.</p>
    </div>
    <div class="footer">
      <p>&copy; Lines Police CAD | <a href="https://www.linespolice-cad.com">linespolice-cad.com</a></p>
      <p><a href="https://www.linespolice-cad.com/contact-us">Contact Support</a></p>
    </div>
  </div>
</body>
</html>`, displayName, requirement, measured, stepItems)
}
