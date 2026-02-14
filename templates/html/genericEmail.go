package templates

import (
	"fmt"
	"html"
	"strings"
)

// RenderGenericEmail generates branded HTML for a generic email.
// The subject is displayed in the header banner, and bodyContent is plain text
// that gets HTML-escaped and has newlines converted to <br> tags.
func RenderGenericEmail(subject, bodyContent string) string {
	// HTML-escape the body to prevent injection, then convert newlines to <br>
	escaped := html.EscapeString(bodyContent)
	htmlBody := strings.ReplaceAll(escaped, "\n", "<br>")

	// HTML-escape the subject for safe display in the header
	safeSubject := html.EscapeString(subject)

	return fmt.Sprintf(`<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Strict//EN" "http://www.w3.org/TR/xhtml1/DTD/xhtml1-strict.dtd">
<html xmlns="http://www.w3.org/1999/xhtml">
<head>
  <meta http-equiv="Content-Type" content="text/html; charset=utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1, minimum-scale=1, maximum-scale=1">
  <title>%s</title>
  <style type="text/css">
    body { font-family: 'Segoe UI', Tahoma, Geneva, Verdana, sans-serif; margin: 0; padding: 0; background-color: #0a0a0f; }
    .container { max-width: 600px; margin: 0 auto; background-color: #12121f; }
    .header { background: linear-gradient(135deg, #667eea 0%%, #764ba2 100%%); padding: 40px 30px; text-align: center; }
    .header h1 { color: #fff; margin: 0; font-size: 24px; font-weight: 700; }
    .content { padding: 40px 30px; color: #e5e7eb; line-height: 1.6; font-size: 15px; }
    .footer { padding: 30px; text-align: center; color: #6b7280; font-size: 12px; border-top: 1px solid rgba(255,255,255,0.1); }
    .footer a { color: #667eea; text-decoration: none; }
  </style>
</head>
<body>
  <div class="container">
    <div class="header">
      <h1>%s</h1>
    </div>
    <div class="content">
      %s
    </div>
    <div class="footer">
      <p>&copy; Lines Police CAD | <a href="https://www.linespolice-cad.com">linespolice-cad.com</a></p>
      <p><a href="https://www.linespolice-cad.com/contact-us">Contact Support</a></p>
    </div>
  </div>
</body>
</html>`, safeSubject, safeSubject, htmlBody)
}
