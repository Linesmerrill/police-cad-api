package templates

import (
	"fmt"
	"html"
	"strings"
)

// ContentOffenseEmailParams is the content for a moderation notice sent after a
// user report was upheld.
//
// There is deliberately no field for the report text or the reporter. A notice
// describes the behavior only by category: in a player base this size,
// repeating the detail identifies whoever filed the report, who is frequently a
// child who reported a bully.
type ContentOffenseEmailParams struct {
	Username string
	// Scope is "user" or "community". A community notice tells the owner their
	// server was delisted and that their members are unaffected.
	Scope         string
	CommunityName string

	// IssuePhrase is the categorical wording, e.g. "abusive or harassing
	// behavior". Produced by models.IssuePhrase, never the raw issue string.
	IssuePhrase string

	// Action is "warning", "suspension" or "permanent".
	Action string
	// PenaltyLabel is the duration in words, e.g. "7 days". Empty for a warning
	// or a permanent action.
	PenaltyLabel string
	// LiftsAt is the formatted date the penalty ends, empty when there is none.
	LiftsAt string

	OffenseNumber int
	// NextPenalty is the rung that follows, e.g. "a 30 days suspension". Empty
	// when this is already the final rung.
	NextPenalty string

	TestBanner string
}

// Content offense actions, mirroring models.PenaltyAction*.
const (
	contentOffenseActionWarning    = "warning"
	contentOffenseActionSuspension = "suspension"
	contentOffenseActionPermanent  = "permanent"
)

const contentOffenseScopeCommunity = "community"

// contentOffenseSender is the display name on the From address. The address
// itself stays no-reply@linespolice-cad.com.
const contentOffenseSender = "Lines Police CAD Content Resolution Team"

// ContentOffenseSenderName returns the From display name for these notices.
func ContentOffenseSenderName() string { return contentOffenseSender }

// ContentOffenseSubject returns the subject line for a notice.
func ContentOffenseSubject(p ContentOffenseEmailParams) string {
	if p.Scope == contentOffenseScopeCommunity {
		if p.Action == contentOffenseActionPermanent {
			return "Your community has been removed from Lines Police CAD"
		}
		return "Your community has been removed from public listings on Lines Police CAD"
	}
	switch p.Action {
	case contentOffenseActionWarning:
		return "A warning about activity on your Lines Police CAD account"
	case contentOffenseActionPermanent:
		return "Your Lines Police CAD account has been removed"
	default:
		return "Your Lines Police CAD account has been suspended"
	}
}

// contentOffenseActionBlock is the "what we have done" paragraph.
func contentOffenseActionBlock(p ContentOffenseEmailParams) string {
	if p.Scope == contentOffenseScopeCommunity {
		name := strings.TrimSpace(p.CommunityName)
		if name == "" {
			name = "Your community"
		}
		switch p.Action {
		case contentOffenseActionPermanent:
			return fmt.Sprintf("%s has been permanently removed from Lines Police CAD.", name)
		default:
			until := "until further notice"
			if p.LiftsAt != "" {
				until = "until " + p.LiftsAt
			}
			return fmt.Sprintf("%s has been removed from public listings %s. It will not appear in community search, browse, or featured listings during that time. Your members are not affected. Everyone already in the community can sign in and use it exactly as before, and nothing has been deleted.", name, until)
		}
	}

	switch p.Action {
	case contentOffenseActionWarning:
		return "No restrictions have been placed on your account at this time."
	case contentOffenseActionPermanent:
		return "Your account has been permanently removed and will not be restored."
	default:
		until := "until further notice"
		if p.LiftsAt != "" {
			until = "until " + p.LiftsAt
		}
		return fmt.Sprintf("Your account has been suspended %s. You will not be able to sign in until then, and access returns automatically.", until)
	}
}

// contentOffenseStrikeBlock is the "what happens next" paragraph.
func contentOffenseStrikeBlock(p ContentOffenseEmailParams) string {
	subject := "your account"
	if p.Scope == contentOffenseScopeCommunity {
		subject = "this community"
	}

	if p.Action == contentOffenseActionPermanent {
		return "This was the final action taken on " + subject + "."
	}
	if p.OffenseNumber <= 1 {
		return "This is the first action taken on " + subject + ". If this behavior continues, further action will follow, up to and including permanent removal."
	}
	if p.NextPenalty == "" {
		return fmt.Sprintf("This is action %d on %s. A further breach will result in permanent removal.", p.OffenseNumber, subject)
	}
	return fmt.Sprintf("This is action %d on %s. A further breach will result in %s.", p.OffenseNumber, subject, p.NextPenalty)
}

func contentOffenseIssueSentence(p ContentOffenseEmailParams) string {
	phrase := strings.TrimSpace(p.IssuePhrase)
	if phrase == "" {
		phrase = "activity that breached our community standards"
	}
	if p.Scope == contentOffenseScopeCommunity {
		name := strings.TrimSpace(p.CommunityName)
		if name == "" {
			name = "your community"
		}
		return fmt.Sprintf("We received reports of %s associated with %s. We reviewed them and found the activity breached our Terms of Service.", phrase, name)
	}
	return fmt.Sprintf("We received reports of %s associated with this account. We reviewed them and found the activity breached our Terms of Service.", phrase)
}

const contentOffenseIntro = "We carry out periodic reviews to make sure Lines Police CAD continues to meet the safety standards we set for our service."

// contentOffenseAppeal routes to the contact page. Outbound mail comes from an
// unmonitored no-reply address, so a notice must never invite a reply.
const contentOffenseAppeal = `If you believe this was a mistake, you can reach us through our contact page at <a href="https://www.linespolice-cad.com/contact-us">linespolice-cad.com/contact-us</a> and a member of our team will take another look.`

const contentOffenseAppealText = "If you believe this was a mistake, you can reach us through our contact page at https://www.linespolice-cad.com/contact-us and a member of our team will take another look."

// RenderContentOffenseEmail returns the HTML and plain-text bodies for a
// moderation notice. Every caller-supplied value is HTML-escaped.
func RenderContentOffenseEmail(p ContentOffenseEmailParams) (htmlBody, textBody string) {
	return renderContentOffenseHTML(p), renderContentOffenseText(p)
}

func contentOffenseGreeting(p ContentOffenseEmailParams) string {
	if strings.TrimSpace(p.Username) == "" {
		return "Hello,"
	}
	return "Hello " + p.Username + ","
}

func renderContentOffenseHTML(p ContentOffenseEmailParams) string {
	subject := ContentOffenseSubject(p)

	testBanner := ""
	if strings.TrimSpace(p.TestBanner) != "" {
		testBanner = fmt.Sprintf(`<div style="background:rgba(56,189,248,0.12); border:1px solid rgba(56,189,248,0.4); border-radius:8px; padding:12px 14px; margin:0 0 16px; color:#7dd3fc;"><strong>TEST EMAIL</strong> - %s</div>`,
			html.EscapeString(p.TestBanner))
	}

	whatYouCanDo := "Review your account against our Terms of Service."
	if p.Scope == contentOffenseScopeCommunity {
		whatYouCanDo = "Review your community against our Terms of Service and make sure your staff know them."
	}

	return fmt.Sprintf(`<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Strict//EN" "http://www.w3.org/TR/xhtml1/DTD/xhtml1-strict.dtd">
<html xmlns="http://www.w3.org/1999/xhtml">
<head>
  <meta http-equiv="Content-Type" content="text/html; charset=utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1, minimum-scale=1, maximum-scale=1">
  <title>%s</title>
  <style type="text/css">
    body { font-family: 'Segoe UI', Tahoma, Geneva, Verdana, sans-serif; margin: 0; padding: 0; background-color: #0a0a0f; }
    .container { max-width: 640px; margin: 0 auto; background-color: #12121f; }
    .header { background: linear-gradient(135deg, #667eea 0%%, #764ba2 100%%); padding: 36px 30px; text-align: center; }
    .header h1 { color: #fff; margin: 0; font-size: 22px; font-weight: 700; }
    .content { padding: 36px 30px; color: #e5e7eb; line-height: 1.6; font-size: 15px; }
    .penalty { background: rgba(248,113,113,0.12); border: 1px solid rgba(248,113,113,0.35); border-radius: 8px; padding: 16px; margin: 0 0 20px; color: #fecaca; }
    .label { color: #9ca3af; font-size: 13px; text-transform: uppercase; letter-spacing: 0.04em; margin: 0 0 6px; }
    .footer { padding: 28px 30px; text-align: center; color: #6b7280; font-size: 12px; border-top: 1px solid rgba(255,255,255,0.1); }
    .footer a { color: #667eea; text-decoration: none; }
    a { color: #38bdf8; }
  </style>
</head>
<body>
  <div class="container">
    <div class="header"><h1>%s</h1></div>
    <div class="content">
      %s
      <p style="margin:0 0 16px;">%s</p>
      <p style="margin:0 0 16px;">%s</p>
      <p style="margin:0 0 16px;">%s</p>
      <p class="label">What we have done</p>
      <div class="penalty">%s</div>
      <p class="label">What happens next</p>
      <p style="margin:0 0 20px;">%s</p>
      <p class="label">What you can do</p>
      <p style="margin:0 0 8px;">%s</p>
      <p style="margin:0 0 24px;">%s</p>
      <p style="margin:0;">Content Resolution Team<br>Lines Police CAD</p>
    </div>
    <div class="footer">
      <p>&copy; Lines Police CAD | <a href="https://www.linespolice-cad.com">linespolice-cad.com</a></p>
      <p><a href="https://www.linespolice-cad.com/contact-us">Contact Support</a></p>
    </div>
  </div>
</body>
</html>`,
		html.EscapeString(subject),
		html.EscapeString(subject),
		testBanner,
		html.EscapeString(contentOffenseGreeting(p)),
		html.EscapeString(contentOffenseIntro),
		html.EscapeString(contentOffenseIssueSentence(p)),
		html.EscapeString(contentOffenseActionBlock(p)),
		html.EscapeString(contentOffenseStrikeBlock(p)),
		html.EscapeString(whatYouCanDo),
		contentOffenseAppeal,
	)
}

func renderContentOffenseText(p ContentOffenseEmailParams) string {
	var b strings.Builder

	if strings.TrimSpace(p.TestBanner) != "" {
		b.WriteString("TEST EMAIL - " + p.TestBanner + "\n\n")
	}

	b.WriteString(contentOffenseGreeting(p) + "\n\n")
	b.WriteString(contentOffenseIntro + "\n\n")
	b.WriteString(contentOffenseIssueSentence(p) + "\n\n")
	b.WriteString("What we have done\n")
	b.WriteString(contentOffenseActionBlock(p) + "\n\n")
	b.WriteString("What happens next\n")
	b.WriteString(contentOffenseStrikeBlock(p) + "\n\n")
	b.WriteString("What you can do\n")
	if p.Scope == contentOffenseScopeCommunity {
		b.WriteString("Review your community against our Terms of Service and make sure your staff know them.\n")
	} else {
		b.WriteString("Review your account against our Terms of Service.\n")
	}
	b.WriteString(contentOffenseAppealText + "\n\n")
	b.WriteString("Content Resolution Team\nLines Police CAD\n\n")
	b.WriteString("(c) Lines Police CAD | https://www.linespolice-cad.com\n")

	return b.String()
}
