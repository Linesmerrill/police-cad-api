package models

import "testing"

// Every kind a client can send has to be resolvable, or a reporter is offered
// something that then fails. api/handlers has the matching test that each kind
// here has a resolver.
func TestReportableKinds_AreWellFormed(t *testing.T) {
	kinds := ReportableKinds()
	if len(kinds) == 0 {
		t.Fatal("no reportable content")
	}
	for name, kind := range kinds {
		if name != kind.Kind {
			t.Errorf("%s is keyed as %s", kind.Kind, name)
		}
		if kind.Label == "" {
			t.Errorf("%s has no label for the console", name)
		}
		if len(kind.Fields) == 0 {
			t.Errorf("%s has no reportable fields", name)
		}
		seen := map[string]bool{}
		for _, f := range kind.Fields {
			if f.Name == "" || f.Label == "" {
				t.Errorf("%s has a field with no name or label", name)
			}
			if seen[f.Name] {
				t.Errorf("%s repeats the field %s", name, f.Name)
			}
			seen[f.Name] = true
		}
	}
}

// Content only a community's members can see must be marked, or the visibility
// check has nothing to act on and anyone could report a community they have
// never been in.
func TestReportableKinds_MembersOnlyContentIsMarked(t *testing.T) {
	for _, kind := range []string{TargetAnnouncement, TargetAnnouncementComment, TargetCommunityEvent} {
		k, _ := LookupReportableKind(kind)
		if !k.MembersOnly {
			t.Errorf("%s is members-only content but is not marked", kind)
		}
	}
	for _, kind := range []string{TargetCommunity, TargetUserProfile, TargetFeatureRequest} {
		k, _ := LookupReportableKind(kind)
		if k.MembersOnly {
			t.Errorf("%s is public but is marked members-only", kind)
		}
	}
}

func TestValidateFields(t *testing.T) {
	community, _ := LookupReportableKind(TargetCommunity)

	if err := community.ValidateFields([]string{"name", "description"}); err != nil {
		t.Errorf("real fields rejected: %v", err)
	}
	// No fields means the content as a whole: a reporter who cannot say which
	// part is wrong still has a real complaint.
	if err := community.ValidateFields(nil); err != nil {
		t.Errorf("reporting the whole thing rejected: %v", err)
	}
	for _, field := range []string{"ownerID", "subscription", "", "banList"} {
		if err := community.ValidateFields([]string{field}); err == nil {
			t.Errorf("%q was accepted as a reportable field", field)
		}
	}
}

// The console must know not to print an image field's value, and never to open
// one on a child-safety report.
func TestHasImageField(t *testing.T) {
	profile, _ := LookupReportableKind(TargetUserProfile)
	if !profile.HasImageField([]string{"profilePicture"}) {
		t.Error("a profile picture is an image")
	}
	if profile.HasImageField([]string{"username"}) {
		t.Error("a username is not an image")
	}
	if profile.HasImageField(nil) {
		t.Error("no fields named means no image named")
	}
}

func TestLookupReportableKind_IsForgivingAboutCase(t *testing.T) {
	if _, ok := LookupReportableKind("  Community "); !ok {
		t.Error("case and spacing should not matter")
	}
	if _, ok := LookupReportableKind("arrest_report"); ok {
		t.Error("a kind nobody registered is not reportable")
	}
}
