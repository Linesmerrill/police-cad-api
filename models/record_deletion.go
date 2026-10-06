package models

// CivilianRecordDeletionAllowed reports whether players may delete records on
// characters they own. Unset (nil) means allowed, so communities that predate
// the setting keep the behaviour they always had.
func (d CommunityDetails) CivilianRecordDeletionAllowed() bool {
	return d.AllowCivilianRecordDeletion == nil || *d.AllowCivilianRecordDeletion
}
