package models

import (
	"testing"
)

func item(key string, checked bool) ReviewChecklistItem {
	return ReviewChecklistItem{Key: key, Checked: checked}
}

// Approval is gated on this. It was previously enforced only in the browser,
// so an approval posted straight to the API landed with nothing ticked behind
// it -- an approval nobody could account for, on the one record whose entire
// purpose is accountability.
func TestUncheckedReviewChecklistKeys(t *testing.T) {
	t.Run("a complete checklist has nothing outstanding", func(t *testing.T) {
		var all []ReviewChecklistItem
		for _, k := range ReviewChecklistKeys {
			all = append(all, item(k, true))
		}
		if got := UncheckedReviewChecklistKeys(all); len(got) != 0 {
			t.Errorf("got %v, want none outstanding", got)
		}
	})

	t.Run("an empty checklist means every item is outstanding", func(t *testing.T) {
		got := UncheckedReviewChecklistKeys(nil)
		if len(got) != len(ReviewChecklistKeys) {
			t.Fatalf("got %v, want all %d keys", got, len(ReviewChecklistKeys))
		}
	})

	t.Run("never written and explicitly false are the same answer", func(t *testing.T) {
		// An item saved as false is not a confirmation, and neither is one that
		// was never saved. Treating the first as "present" would let a reviewer
		// tick a box, untick it, and still approve.
		absent := UncheckedReviewChecklistKeys([]ReviewChecklistItem{})
		explicit := UncheckedReviewChecklistKeys([]ReviewChecklistItem{
			item(ReviewCheckAutomated, false),
			item(ReviewCheckRPContent, false),
			item(ReviewCheckGenuine, false),
		})
		if len(absent) != len(explicit) {
			t.Errorf("absent %v vs explicit false %v: should agree", absent, explicit)
		}
	})

	t.Run("reports exactly what is still outstanding", func(t *testing.T) {
		got := UncheckedReviewChecklistKeys([]ReviewChecklistItem{
			item(ReviewCheckAutomated, true),
			item(ReviewCheckRPContent, false),
		})
		if len(got) != 2 {
			t.Fatalf("got %v, want the two that are not ticked", got)
		}
		want := map[string]bool{ReviewCheckRPContent: true, ReviewCheckGenuine: true}
		for _, k := range got {
			if !want[k] {
				t.Errorf("unexpected key %q", k)
			}
		}
	})

	t.Run("keys we do not recognise cannot satisfy the gate", func(t *testing.T) {
		// Otherwise anything that can write a checklist item could invent a key
		// and approve past the requirement.
		got := UncheckedReviewChecklistKeys([]ReviewChecklistItem{
			item("something_made_up", true),
			item("", true),
		})
		if len(got) != len(ReviewChecklistKeys) {
			t.Errorf("got %v, want all %d still outstanding", got, len(ReviewChecklistKeys))
		}
	})

	t.Run("duplicates of one key do not cover the others", func(t *testing.T) {
		got := UncheckedReviewChecklistKeys([]ReviewChecklistItem{
			item(ReviewCheckAutomated, true),
			item(ReviewCheckAutomated, true),
			item(ReviewCheckAutomated, true),
		})
		if len(got) != 2 {
			t.Errorf("got %v, want the other two still outstanding", got)
		}
	})

	t.Run("returns keys in the order a reviewer works through them", func(t *testing.T) {
		got := UncheckedReviewChecklistKeys(nil)
		for i, k := range ReviewChecklistKeys {
			if got[i] != k {
				t.Errorf("position %d = %q, want %q", i, got[i], k)
			}
		}
	})
}
