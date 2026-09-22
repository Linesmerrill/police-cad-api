package templates

import (
	"strings"
	"testing"
)

func bothBodies(t *testing.T, p ContentOffenseEmailParams) (string, string) {
	t.Helper()
	h, txt := RenderContentOffenseEmail(p)
	if strings.TrimSpace(h) == "" || strings.TrimSpace(txt) == "" {
		t.Fatal("one of the bodies rendered empty")
	}
	return h, txt
}

// Outbound mail comes from an unmonitored no-reply address. A recipient who
// follows a "reply to this email" instruction thinks they have asked for help
// and then hears nothing, which is worse than telling them nothing.
func TestContentOffenseEmail_NeverInvitesAReply(t *testing.T) {
	cases := []ContentOffenseEmailParams{
		{Username: "kid", Action: contentOffenseActionWarning, OffenseNumber: 1},
		{Username: "kid", Action: contentOffenseActionSuspension, PenaltyLabel: "7 days", LiftsAt: "October 1, 2026", OffenseNumber: 2},
		{Username: "kid", Action: contentOffenseActionPermanent, OffenseNumber: 4},
		{Scope: contentOffenseScopeCommunity, CommunityName: "Vice City", Action: contentOffenseActionSuspension, LiftsAt: "October 1, 2026", OffenseNumber: 1},
	}
	banned := []string{"reply to this email", "reply directly", "hit reply", "respond to this email"}
	for _, p := range cases {
		h, txt := bothBodies(t, p)
		for _, body := range []string{strings.ToLower(h), strings.ToLower(txt)} {
			for _, phrase := range banned {
				if strings.Contains(body, phrase) {
					t.Errorf("body invites a reply (%q) for action %q", phrase, p.Action)
				}
			}
			if !strings.Contains(body, "linespolice-cad.com/contact-us") {
				t.Errorf("body has no route to the contact page for action %q", p.Action)
			}
		}
	}
}

func TestContentOffenseEmail_EscapesUserSuppliedValues(t *testing.T) {
	p := ContentOffenseEmailParams{
		Username:      `<script>alert(1)</script>`,
		Scope:         contentOffenseScopeCommunity,
		CommunityName: `Vice "City" <b>Rejects</b>`,
		IssuePhrase:   "spam or repeated unwanted messages",
		Action:        contentOffenseActionSuspension,
		LiftsAt:       "October 1, 2026",
		OffenseNumber: 1,
	}
	h, _ := bothBodies(t, p)
	if strings.Contains(h, "<script>") {
		t.Error("username was not escaped into the HTML body")
	}
	if strings.Contains(h, "<b>Rejects</b>") {
		t.Error("community name was not escaped into the HTML body")
	}
	if !strings.Contains(h, "&lt;script&gt;") {
		t.Error("expected the escaped username to still appear")
	}
}

func TestContentOffenseEmail_Subjects(t *testing.T) {
	tests := []struct {
		name string
		p    ContentOffenseEmailParams
		want string
	}{
		{"user warning", ContentOffenseEmailParams{Action: contentOffenseActionWarning}, "A warning about activity on your Lines Police CAD account"},
		{"user suspension", ContentOffenseEmailParams{Action: contentOffenseActionSuspension}, "Your Lines Police CAD account has been suspended"},
		{"user permanent", ContentOffenseEmailParams{Action: contentOffenseActionPermanent}, "Your Lines Police CAD account has been removed"},
		{"community delist", ContentOffenseEmailParams{Scope: contentOffenseScopeCommunity, Action: contentOffenseActionSuspension}, "Your community has been removed from public listings on Lines Police CAD"},
		{"community removal", ContentOffenseEmailParams{Scope: contentOffenseScopeCommunity, Action: contentOffenseActionPermanent}, "Your community has been removed from Lines Police CAD"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ContentOffenseSubject(tt.p); got != tt.want {
				t.Errorf("subject = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestContentOffenseEmail_WarningSaysNoRestrictionsAndNamesTheNextStep(t *testing.T) {
	_, txt := bothBodies(t, ContentOffenseEmailParams{
		Username: "kid", IssuePhrase: "spam or repeated unwanted messages",
		Action: contentOffenseActionWarning, OffenseNumber: 1, NextPenalty: "a 7 days suspension",
	})
	if !strings.Contains(txt, "No restrictions have been placed on your account") {
		t.Error("a warning must say plainly that nothing was restricted")
	}
	if !strings.Contains(txt, "first action") {
		t.Error("a first warning should say it is the first action")
	}
	if strings.Contains(txt, "suspended") {
		t.Error("a warning must not read as a suspension")
	}
}

func TestContentOffenseEmail_SuspensionStatesTheLiftDate(t *testing.T) {
	_, txt := bothBodies(t, ContentOffenseEmailParams{
		Username: "kid", IssuePhrase: "abusive or harassing behavior",
		Action: contentOffenseActionSuspension, PenaltyLabel: "30 days",
		LiftsAt: "October 22, 2026", OffenseNumber: 2, NextPenalty: "a 1 year suspension",
	})
	if !strings.Contains(txt, "October 22, 2026") {
		t.Error("the recipient must be told when access returns, or they will ask support")
	}
	if !strings.Contains(txt, "access returns automatically") {
		t.Error("the recipient should know they do not need to do anything to get back in")
	}
	if !strings.Contains(txt, "a 1 year suspension") {
		t.Error("the next rung should be named so the escalation is not a surprise")
	}
}

// An owner who thinks their server was shut down will tell 200 people that
// before reading the second paragraph.
func TestContentOffenseEmail_CommunityDelistSaysMembersAreUnaffected(t *testing.T) {
	_, txt := bothBodies(t, ContentOffenseEmailParams{
		Username: "owner", Scope: contentOffenseScopeCommunity, CommunityName: "Vice City Rejects",
		IssuePhrase: "spam or repeated unwanted messages",
		Action:      contentOffenseActionSuspension, LiftsAt: "October 1, 2026", OffenseNumber: 1,
	})
	if !strings.Contains(txt, "Your members are not affected") {
		t.Error("the notice must say members are unaffected")
	}
	if !strings.Contains(txt, "nothing has been deleted") {
		t.Error("the notice must say nothing was deleted")
	}
	if !strings.Contains(txt, "Vice City Rejects") {
		t.Error("the notice should name the community")
	}
	if !strings.Contains(txt, "this community") {
		t.Error("the escalation line should be about the community, not the owner's account")
	}
}

func TestContentOffenseEmail_PermanentIsFinalAndNamesNoNextStep(t *testing.T) {
	_, txt := bothBodies(t, ContentOffenseEmailParams{
		Username: "kid", IssuePhrase: "hateful conduct",
		Action: contentOffenseActionPermanent, OffenseNumber: 4,
	})
	if !strings.Contains(txt, "will not be restored") {
		t.Error("a permanent removal should say so plainly")
	}
	if !strings.Contains(txt, "final action") {
		t.Error("a permanent removal should be described as final")
	}
	if strings.Contains(txt, "further action will follow") {
		t.Error("there is no further action after a permanent removal")
	}
}

func TestContentOffenseEmail_HandlesMissingUsernameAndCommunityName(t *testing.T) {
	_, txt := bothBodies(t, ContentOffenseEmailParams{
		Action: contentOffenseActionSuspension, LiftsAt: "October 1, 2026", OffenseNumber: 1,
	})
	if !strings.HasPrefix(strings.TrimSpace(txt), "Hello,") {
		t.Errorf("expected a bare greeting when we have no name, got %q", txt[:20])
	}

	_, ctxt := bothBodies(t, ContentOffenseEmailParams{
		Scope: contentOffenseScopeCommunity, Action: contentOffenseActionSuspension, OffenseNumber: 1,
	})
	if strings.Contains(ctxt, "  has been") {
		t.Error("a missing community name left a hole in the sentence")
	}
	if !strings.Contains(ctxt, "until further notice") {
		t.Error("with no lift date the notice should still say something definite")
	}
}

// The notice describes behavior only by category. An unclassified issue must
// fall back to neutral wording rather than leaving the sentence empty.
func TestContentOffenseEmail_EmptyIssuePhraseFallsBack(t *testing.T) {
	_, txt := bothBodies(t, ContentOffenseEmailParams{
		Username: "kid", Action: contentOffenseActionWarning, OffenseNumber: 1,
	})
	if !strings.Contains(txt, "breached our community standards") {
		t.Error("expected neutral fallback wording for a missing issue phrase")
	}
	if strings.Contains(txt, "reports of  associated") {
		t.Error("an empty issue phrase left a hole in the sentence")
	}
}

func TestContentOffenseEmail_SenderNameIsTheContentResolutionTeam(t *testing.T) {
	if got := ContentOffenseSenderName(); got != "Lines Police CAD Content Resolution Team" {
		t.Errorf("sender = %q", got)
	}
	_, txt := bothBodies(t, ContentOffenseEmailParams{Username: "kid", Action: contentOffenseActionWarning, OffenseNumber: 1})
	if !strings.Contains(txt, "Content Resolution Team") {
		t.Error("the notice should be signed by the Content Resolution Team")
	}
}

// No emojis and no em dashes in copy the platform sends as itself.
func TestContentOffenseEmail_NoEmDashesOrEmoji(t *testing.T) {
	h, txt := bothBodies(t, ContentOffenseEmailParams{
		Username: "kid", Scope: contentOffenseScopeCommunity, CommunityName: "Vice City",
		IssuePhrase: "hateful conduct", Action: contentOffenseActionSuspension,
		LiftsAt: "October 1, 2026", OffenseNumber: 2, NextPenalty: "a 1 year suspension",
		TestBanner: "sent to an admin",
	})
	for _, body := range []string{h, txt} {
		if strings.ContainsAny(body, "—–") {
			t.Error("copy contains an em or en dash")
		}
		for _, r := range body {
			if r > 0x2100 && r != 0x2122 {
				t.Errorf("copy contains a non-text rune %U", r)
				break
			}
		}
	}
}
