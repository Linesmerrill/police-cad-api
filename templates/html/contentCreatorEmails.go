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
        <h3>📋 What happens next?</h3>
        <p style="margin-bottom: 0;">Our team will carefully review your application. This process typically takes <strong>5-7 business days</strong>. Applications require approval from at least two team members to ensure fair evaluation.</p>
      </div>

      <div class="timeline">
        <div class="timeline-item">
          <div class="timeline-icon"></div>
          <div class="timeline-text"><strong>Step 1:</strong> Application submitted ✓</div>
        </div>
        <div class="timeline-item">
          <div class="timeline-icon" style="background: #6b7280;"></div>
          <div class="timeline-text"><strong>Step 2:</strong> First review by our team</div>
        </div>
        <div class="timeline-item">
          <div class="timeline-icon" style="background: #6b7280;"></div>
          <div class="timeline-text"><strong>Step 3:</strong> Second review & final decision</div>
        </div>
        <div class="timeline-item">
          <div class="timeline-icon" style="background: #6b7280;"></div>
          <div class="timeline-text"><strong>Step 4:</strong> You'll receive an email with our decision</div>
        </div>
      </div>

      <p>In the meantime, you can check your application status anytime:</p>
      <a href="https://www.linespolice-cad.com/content-creators/me" class="cta-button">View Application Status</a>

      <p style="margin-top: 30px; color: #9ca3af; font-size: 14px;">If you have any questions about the program or your application, please don't hesitate to reach out to our support team.</p>
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
        <p>⏱️ <strong>Reminder:</strong> Applications should be reviewed within 5-7 business days. This application requires at least 2 admin approvals.</p>
      </div>

      <a href="https://www.linespolice-cad.com/lpc-admin" class="cta-button">Review in Admin Console</a>
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
          <div class="benefit-text"><strong>Personal Base Plan</strong> - Automatically activated on your account</div>
        </div>
        <div class="benefit-item">
          <div class="benefit-icon">🏢</div>
          <div class="benefit-text"><strong>Community Base Plan</strong> - Apply to one community you own</div>
        </div>
        <div class="benefit-item">
          <div class="benefit-icon">⭐</div>
          <div class="benefit-text"><strong>Featured Profile</strong> - Showcase your content on our creators page</div>
        </div>
      </div>

      <div class="note-box">
        <p>💡 <strong>Next Step:</strong> Visit your Creator Dashboard to claim your free Community Base Plan for one of your communities!</p>
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
