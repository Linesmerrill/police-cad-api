package models

import "testing"

// Counts are from the production survey (Aug 2026): 1,310,044 vehicles and
// 844,732 firearms held ownership only on the deprecated id.

func TestReconcileOwnerLink(t *testing.T) {
	tests := []struct {
		name            string
		next            OwnerLink
		previousOwnerID string
		linkSent        bool
		nameSent        bool
		want            OwnerLink
	}{
		{
			name:            "unlink clears the deprecated pair too (the 1.3M case)",
			next:            OwnerLink{LinkedCivilianID: "", RegisteredOwnerID: "civA", RegisteredOwner: "Jack Dean"},
			previousOwnerID: "civA",
			linkSent:        true,
			want:            OwnerLink{},
		},
		{
			name:            "unlinking an asset held only on the deprecated id still works",
			next:            OwnerLink{RegisteredOwnerID: "civA", RegisteredOwner: "Jack Dean"},
			previousOwnerID: "civA",
			linkSent:        true,
			want:            OwnerLink{},
		},
		{
			name:            "link mirrors onto the deprecated id",
			next:            OwnerLink{LinkedCivilianID: "civB"},
			previousOwnerID: "",
			linkSent:        true,
			want:            OwnerLink{LinkedCivilianID: "civB", RegisteredOwnerID: "civB"},
		},
		{
			name:            "re-linking to someone else drops the previous owner's name",
			next:            OwnerLink{LinkedCivilianID: "civB", RegisteredOwnerID: "civA", RegisteredOwner: "Jack Dean"},
			previousOwnerID: "civA",
			linkSent:        true,
			want:            OwnerLink{LinkedCivilianID: "civB", RegisteredOwnerID: "civB"},
		},
		{
			name:            "a name supplied with the link is kept",
			next:            OwnerLink{LinkedCivilianID: "civB", RegisteredOwnerID: "civA", RegisteredOwner: "Marty Mcfly"},
			previousOwnerID: "civA",
			linkSent:        true,
			nameSent:        true,
			want:            OwnerLink{LinkedCivilianID: "civB", RegisteredOwnerID: "civB", RegisteredOwner: "Marty Mcfly"},
		},
		{
			name:            "re-saving the same owner keeps the name",
			next:            OwnerLink{LinkedCivilianID: "civA", RegisteredOwnerID: "civA", RegisteredOwner: "Jack Dean"},
			previousOwnerID: "civA",
			linkSent:        true,
			want:            OwnerLink{LinkedCivilianID: "civA", RegisteredOwnerID: "civA", RegisteredOwner: "Jack Dean"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ReconcileOwnerLink(tt.next, tt.previousOwnerID, tt.linkSent, tt.nameSent)
			if got != tt.want {
				t.Errorf("ReconcileOwnerLink() =\n  %+v\nwant\n  %+v", got, tt.want)
			}
		})
	}
}

// The guard that stops an unrelated edit from wiping ownership.
func TestReconcileOwnerLinkLeavesOwnershipAloneWhenNotAsked(t *testing.T) {
	// Recolouring a vehicle that is owned via the deprecated id: the merged
	// state has an empty linkedCivilianID, but the caller never mentioned it.
	owned := OwnerLink{RegisteredOwnerID: "civA", RegisteredOwner: "Jack Dean"}

	got := ReconcileOwnerLink(owned, "civA", false, false)

	if got != owned {
		t.Errorf("an edit that does not send linkedCivilianID must not touch ownership:\n got %+v\nwant %+v", got, owned)
	}
}

func TestReconcileOwnerLinkIsIdempotent(t *testing.T) {
	in := OwnerLink{LinkedCivilianID: "civB", RegisteredOwnerID: "civA", RegisteredOwner: "Jack Dean"}

	once := ReconcileOwnerLink(in, "civA", true, false)
	twice := ReconcileOwnerLink(once, once.RegisteredOwnerID, true, false)

	if once != twice {
		t.Errorf("second pass changed the value:\n first %+v\nsecond %+v", once, twice)
	}
}
