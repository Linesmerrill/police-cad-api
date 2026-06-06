package templates

import (
	"fmt"
	"html"
	"strings"
)

// RpPromoOffenseEvidenceLine is a single promotion shown in the evidence table
// of an offense email.
type RpPromoOffenseEvidenceLine struct {
	CommunityName string
	ServerName    string
	InviteURL     string
	PostedAt      string // human-readable, already formatted by the caller
}

// RpPromoOffenseEmailParams is the content for an RP promotion offense email.
type RpPromoOffenseEmailParams struct {
	Username      string
	OffenseNumber int
	PenaltyLabel  string // e.g. "7-day", "30-day", "1-year", "permanent"
	LiftsAt       string // human-readable date the restriction lifts, or "" if permanent
	Reason        string // admin-supplied reason / summary of what happened
	Evidence      []RpPromoOffenseEvidenceLine
	ToSURL        string // link to the Discord Community Promotion section of the ToS
	AppealInfo    string // how to appeal (e.g. open a ticket in the Discord assistance channel)
}

// rpPromoOffenseLadder describes the full escalation ladder so the email can
// transparently show the recipient where they are and what comes next.
var rpPromoOffenseLadder = []string{
	"1st offense — 7-day restriction",
	"2nd offense — 30-day restriction",
	"3rd offense — 1-year restriction",
	"4th offense — permanent restriction (appealable)",
}

// RenderRpPromoOffenseEmail returns the HTML and plain-text bodies for an RP
// promotion offense notification. The HTML mirrors the branded style of
// RenderGenericEmail. All user-controlled values are HTML-escaped.
func RenderRpPromoOffenseEmail(p RpPromoOffenseEmailParams) (htmlBody, textBody string) {
	return renderRpPromoOffenseHTML(p), renderRpPromoOffenseText(p)
}

func rpPromoPenaltySentence(p RpPromoOffenseEmailParams) string {
	if p.LiftsAt == "" || strings.EqualFold(p.PenaltyLabel, "permanent") {
		return "Your ability to post server promotions has been permanently restricted."
	}
	return fmt.Sprintf("Your ability to post server promotions has been restricted for %s. The restriction lifts on %s.", p.PenaltyLabel, p.LiftsAt)
}

func renderRpPromoOffenseHTML(p RpPromoOffenseEmailParams) string {
	subject := "Action taken on your Lines Police CAD server promotions"

	greeting := "Hello,"
	if strings.TrimSpace(p.Username) != "" {
		greeting = "Hello " + html.EscapeString(p.Username) + ","
	}

	var rows strings.Builder
	for _, e := range p.Evidence {
		invite := html.EscapeString(e.InviteURL)
		rows.WriteString(fmt.Sprintf(`<tr>
      <td style="padding:8px 12px;border-bottom:1px solid rgba(255,255,255,0.08);">%s</td>
      <td style="padding:8px 12px;border-bottom:1px solid rgba(255,255,255,0.08);">%s</td>
      <td style="padding:8px 12px;border-bottom:1px solid rgba(255,255,255,0.08);"><a href="%s" style="color:#38bdf8;text-decoration:none;">%s</a></td>
      <td style="padding:8px 12px;border-bottom:1px solid rgba(255,255,255,0.08);">%s</td>
    </tr>`,
			html.EscapeString(e.CommunityName),
			html.EscapeString(e.ServerName),
			invite, invite,
			html.EscapeString(e.PostedAt)))
	}

	var ladder strings.Builder
	for i, step := range rpPromoOffenseLadder {
		style := "color:#9ca3af;"
		if i+1 == p.OffenseNumber {
			style = "color:#fbbf24;font-weight:700;" // highlight the current step
		}
		ladder.WriteString(fmt.Sprintf(`<li style="%s margin-bottom:4px;">%s</li>`, style, html.EscapeString(step)))
	}

	reasonBlock := ""
	if strings.TrimSpace(p.Reason) != "" {
		reasonBlock = fmt.Sprintf(`<p style="margin:0 0 16px;"><strong>What happened:</strong><br>%s</p>`,
			strings.ReplaceAll(html.EscapeString(p.Reason), "\n", "<br>"))
	}

	appeal := html.EscapeString(p.AppealInfo)
	tosURL := html.EscapeString(p.ToSURL)

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
    table { width: 100%%; border-collapse: collapse; font-size: 13px; margin: 8px 0 20px; }
    th { text-align: left; padding: 8px 12px; color: #9ca3af; border-bottom: 1px solid rgba(255,255,255,0.18); font-weight: 600; }
    ul { padding-left: 20px; margin: 8px 0 20px; }
    .footer { padding: 28px 30px; text-align: center; color: #6b7280; font-size: 12px; border-top: 1px solid rgba(255,255,255,0.1); }
    .footer a { color: #667eea; text-decoration: none; }
    a { color: #38bdf8; }
  </style>
</head>
<body>
  <div class="container">
    <div class="header"><h1>%s</h1></div>
    <div class="content">
      <p style="margin:0 0 16px;">%s</p>
      <div class="penalty"><strong>Offense #%d.</strong> %s</div>
      %s
      <p style="margin:0 0 8px;"><strong>Promotions involved:</strong></p>
      <table>
        <thead><tr><th>Community</th><th>Server name</th><th>Invite</th><th>Posted</th></tr></thead>
        <tbody>%s</tbody>
      </table>
      <p style="margin:0 0 8px;">This violates our server-promotion terms, which allow only one promotion per community per posting window. Creating additional communities to post again is treated as evading that limit. See the <a href="%s">Discord Community Promotion</a> section of our Terms of Service.</p>
      <p style="margin:16px 0 8px;"><strong>How restrictions escalate:</strong></p>
      <ul>%s</ul>
      <p style="margin:0 0 8px;"><strong>Appeals:</strong> %s</p>
    </div>
    <div class="footer">
      <p>&copy; Lines Police CAD | <a href="https://www.linespolice-cad.com">linespolice-cad.com</a></p>
      <p><a href="https://www.linespolice-cad.com/contact-us">Contact Support</a></p>
    </div>
  </div>
</body>
</html>`, subject, subject, greeting, p.OffenseNumber, rpPromoPenaltySentence(p), reasonBlock, rows.String(), tosURL, ladder.String(), appeal)
}

func renderRpPromoOffenseText(p RpPromoOffenseEmailParams) string {
	var b strings.Builder
	if strings.TrimSpace(p.Username) != "" {
		b.WriteString("Hello " + p.Username + ",\n\n")
	} else {
		b.WriteString("Hello,\n\n")
	}
	b.WriteString(fmt.Sprintf("Offense #%d. %s\n\n", p.OffenseNumber, rpPromoPenaltySentence(p)))
	if strings.TrimSpace(p.Reason) != "" {
		b.WriteString("What happened:\n" + p.Reason + "\n\n")
	}
	b.WriteString("Promotions involved:\n")
	for _, e := range p.Evidence {
		b.WriteString(fmt.Sprintf("  - %s | %s | %s | %s\n", e.CommunityName, e.ServerName, e.InviteURL, e.PostedAt))
	}
	b.WriteString("\nThis violates our server-promotion terms, which allow only one promotion per community per posting window. Creating additional communities to post again is treated as evading that limit.\n")
	b.WriteString("Terms of Service: " + p.ToSURL + "\n\n")
	b.WriteString("How restrictions escalate:\n")
	for _, step := range rpPromoOffenseLadder {
		b.WriteString("  - " + step + "\n")
	}
	b.WriteString("\nAppeals: " + p.AppealInfo + "\n")
	return b.String()
}
