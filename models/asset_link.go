package models

// Asset ownership storage.
//
// A vehicle or firearm records its owning civilian twice:
//
//	linkedCivilianID   canonical
//	registeredOwnerID  deprecated, and still how ~93% of links are stored
//	registeredOwner    deprecated owner NAME, kept beside registeredOwnerID
//
// The list handlers already match either id (see
// VehiclesByRegisteredOwnerIDHandler), so reads are fine. Writes were not:
// every client unlinks by sending {"linkedCivilianID": ""}, which left
// registeredOwnerID untouched, so the asset kept matching the $or and came
// straight back on the next load. Measured Aug 2026: 1,310,044 vehicles and
// 844,732 firearms were linked ONLY through the deprecated id and could not be
// unlinked from any surface.
//
// Reconciling here rather than in each client fixes the mobile app and the
// website's department dashboard at once, with no client release.

// OwnerLink is the ownership triple a vehicle or firearm carries.
type OwnerLink struct {
	LinkedCivilianID  string
	RegisteredOwnerID string
	RegisteredOwner   string
}

// ReconcileOwnerLink brings the deprecated pair in step with the canonical
// linkedCivilianID.
//
// next is the ownership state after the caller's update has been merged in.
// previousOwnerID is what registeredOwnerID held beforehand. linkSent and
// nameSent report whether the caller explicitly included linkedCivilianID and
// registeredOwner in this request.
//
// linkSent matters: without it, any unrelated edit — recolouring a vehicle,
// say — would arrive with an empty linkedCivilianID and silently unlink an
// asset held on the deprecated id. Ownership only changes when the caller
// actually asked to change it.
func ReconcileOwnerLink(next OwnerLink, previousOwnerID string, linkSent, nameSent bool) OwnerLink {
	if !linkSent {
		return next
	}

	// Unlink clears all three. Leaving the owner name behind would keep
	// showing the previous person on a CAD lookup of an unowned asset.
	if next.LinkedCivilianID == "" {
		return OwnerLink{}
	}

	// Link, or re-link to someone else. The deprecated id has to follow, or
	// the asset keeps matching its old owner and shows up on two civilians.
	next.RegisteredOwnerID = next.LinkedCivilianID

	// The name belongs to whoever previousOwnerID was. If ownership moved and
	// the caller did not supply a fresh name, drop it — blank is recoverable,
	// the wrong person's name on a lookup is not.
	if !nameSent && previousOwnerID != "" && previousOwnerID != next.LinkedCivilianID {
		next.RegisteredOwner = ""
	}

	return next
}
