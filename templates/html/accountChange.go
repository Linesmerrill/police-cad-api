package templates

import "fmt"

// RenderEmailChangeCode produces the HTML body for the verification-code email sent to a user's
// CURRENT address when they request an email change.
func RenderEmailChangeCode(code string) string {
	return RenderGenericEmail(
		"Email Change Verification",
		fmt.Sprintf("A change to the email address on your Lines Police CAD account was just requested.\n\nUse this code to confirm:\n\n%s\n\nThe code expires in 15 minutes. If you did not request this change, ignore this email — your account is unchanged. If you receive multiple requests you did not make, contact support.", code),
	)
}

// RenderPasswordChangeCode produces the HTML body for the verification-code email sent to a user's
// current address when they request a password change while logged in.
func RenderPasswordChangeCode(code string) string {
	return RenderGenericEmail(
		"Password Change Verification",
		fmt.Sprintf("A password change on your Lines Police CAD account was just requested.\n\nUse this code to confirm:\n\n%s\n\nThe code expires in 15 minutes. If you did not request this change, ignore this email — your account is unchanged. If you receive multiple requests you did not make, contact support.", code),
	)
}

// RenderEmailChangedNotice is the after-the-fact notification sent to a user's PREVIOUS email
// address once their email change completes.
func RenderEmailChangedNotice(newEmail string) string {
	return RenderGenericEmail(
		"Email Address Changed",
		fmt.Sprintf("The email address on your Lines Police CAD account was just changed to:\n\n%s\n\nIf you did not make this change, contact support immediately at https://www.linespolice-cad.com/contact-us — your account may have been compromised.", newEmail),
	)
}

// RenderPasswordChangedNotice is the after-the-fact notification sent to the account email
// once a password change completes.
func RenderPasswordChangedNotice() string {
	return RenderGenericEmail(
		"Password Changed",
		"The password on your Lines Police CAD account was just changed.\n\nIf you did not make this change, contact support immediately at https://www.linespolice-cad.com/contact-us — your account may have been compromised.",
	)
}
