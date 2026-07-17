package helpers

import (
	"testing"

	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/models"
)

// buildFormAccessCommunity builds a community with two departments (Police,
// Fire), each with a low and high rank, plus members and an admin role.
//
//	Police: Officer (order 2, low)  / Sergeant (order 1, high)
//	Fire:   Firefighter (order 2)   / Captain (order 1, high)
type accessFixture struct {
	community *models.Community

	ownerID string
	adminID string // in an "administrator" role
	plainID string // in a role with no admin perm

	policeDeptID, fireDeptID                     string
	policeOfficerID, policeSergeantID            string
	fireFighterID, fireCaptainID                 string
	policeOfficerUser, policeSergeantUser        string
	fireFighterUser, dualDeptUser, nonMemberUser string
}

func buildFormAccessFixture() accessFixture {
	policeDept := primitive.NewObjectID()
	fireDept := primitive.NewObjectID()
	pOfficer := primitive.NewObjectID()
	pSergeant := primitive.NewObjectID()
	fFighter := primitive.NewObjectID()
	fCaptain := primitive.NewObjectID()

	f := accessFixture{
		ownerID:            primitive.NewObjectID().Hex(),
		adminID:            primitive.NewObjectID().Hex(),
		plainID:            primitive.NewObjectID().Hex(),
		policeDeptID:       policeDept.Hex(),
		fireDeptID:         fireDept.Hex(),
		policeOfficerID:    pOfficer.Hex(),
		policeSergeantID:   pSergeant.Hex(),
		fireFighterID:      fFighter.Hex(),
		fireCaptainID:      fCaptain.Hex(),
		policeOfficerUser:  primitive.NewObjectID().Hex(),
		policeSergeantUser: primitive.NewObjectID().Hex(),
		fireFighterUser:    primitive.NewObjectID().Hex(),
		dualDeptUser:       primitive.NewObjectID().Hex(),
		nonMemberUser:      primitive.NewObjectID().Hex(),
	}

	f.community = &models.Community{
		ID: primitive.NewObjectID(),
		Details: models.CommunityDetails{
			OwnerID: f.ownerID,
			Roles: []models.Role{
				{
					ID:      primitive.NewObjectID(),
					Name:    "Head Admin",
					Members: []string{f.adminID},
					Permissions: []models.Permission{
						{Name: "administrator", Enabled: true},
					},
				},
				{
					ID:      primitive.NewObjectID(),
					Name:    "Cadet",
					Members: []string{f.plainID},
					Permissions: []models.Permission{
						{Name: "administrator", Enabled: false},
						{Name: "manage records", Enabled: true},
					},
				},
			},
			Departments: []models.Department{
				{
					ID:   policeDept,
					Name: "Police",
					Ranks: []models.Rank{
						{ID: pOfficer, Name: "Officer", DisplayOrder: 2},
						{ID: pSergeant, Name: "Sergeant", DisplayOrder: 1},
					},
					Members: []models.MemberStatus{
						{UserID: f.policeOfficerUser, RankID: pOfficer.Hex()},
						{UserID: f.policeSergeantUser, RankID: pSergeant.Hex()},
						{UserID: f.dualDeptUser, RankID: pOfficer.Hex()},
					},
				},
				{
					ID:   fireDept,
					Name: "Fire",
					Ranks: []models.Rank{
						{ID: fFighter, Name: "Firefighter", DisplayOrder: 2},
						{ID: fCaptain, Name: "Captain", DisplayOrder: 1},
					},
					Members: []models.MemberStatus{
						{UserID: f.fireFighterUser, RankID: fFighter.Hex()},
						{UserID: f.dualDeptUser, RankID: fCaptain.Hex()},
					},
				},
			},
		},
	}
	return f
}

func TestIsCommunityAdmin(t *testing.T) {
	f := buildFormAccessFixture()
	cases := []struct {
		name   string
		userID string
		want   bool
	}{
		{"owner", f.ownerID, true},
		{"administrator role", f.adminID, true},
		{"non-admin role member", f.plainID, false},
		{"plain member", f.policeOfficerUser, false},
		{"empty user", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsCommunityAdmin(f.community, c.userID); got != c.want {
				t.Fatalf("IsCommunityAdmin(%s) = %v, want %v", c.name, got, c.want)
			}
		})
	}
}

func TestUserCanSeeForm(t *testing.T) {
	f := buildFormAccessFixture()

	// Police: Sergeant-and-above may see. Fire: Captain-and-above may see.
	sergeantPlus := &models.RankRule{Mode: RankRuleModeAtOrAbove, AnchorRankIDs: []string{f.policeSergeantID}}
	captainPlus := &models.RankRule{Mode: RankRuleModeAtOrAbove, AnchorRankIDs: []string{f.fireCaptainID}}

	policeGate := models.DepartmentRankGate{DepartmentID: f.policeDeptID, VisibleToRankRule: sergeantPlus}
	fireGate := models.DepartmentRankGate{DepartmentID: f.fireDeptID, VisibleToRankRule: captainPlus}
	// A gate with no rank rule: any member of that department may see.
	policeOpenGate := models.DepartmentRankGate{DepartmentID: f.policeDeptID}

	cases := []struct {
		name   string
		gates  []models.DepartmentRankGate
		userID string
		want   bool
	}{
		{"no gates - everyone sees", nil, f.policeOfficerUser, true},
		{"admin bypasses gate", []models.DepartmentRankGate{policeGate}, f.adminID, true},
		{"owner bypasses gate", []models.DepartmentRankGate{policeGate}, f.ownerID, true},
		{"police sergeant passes police gate", []models.DepartmentRankGate{policeGate}, f.policeSergeantUser, true},
		{"police officer fails police gate", []models.DepartmentRankGate{policeGate}, f.policeOfficerUser, false},
		{"non-member fails police gate", []models.DepartmentRankGate{policeGate}, f.nonMemberUser, false},
		{"fire member not covered by police-only gate", []models.DepartmentRankGate{policeGate}, f.fireFighterUser, false},
		// Multi-department: officer in police (fails) but captain in fire (passes) => visible.
		{"dual-dept user passes via fire gate", []models.DepartmentRankGate{policeGate, fireGate}, f.dualDeptUser, true},
		{"fire firefighter fails captain gate", []models.DepartmentRankGate{fireGate}, f.fireFighterUser, false},
		{"open police gate lets any police member", []models.DepartmentRankGate{policeOpenGate}, f.policeOfficerUser, true},
		{"open police gate still excludes non-member", []models.DepartmentRankGate{policeOpenGate}, f.fireFighterUser, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := UserCanSeeForm(f.community, c.userID, c.gates); got != c.want {
				t.Fatalf("UserCanSeeForm(%s) = %v, want %v", c.name, got, c.want)
			}
		})
	}
}

func TestUserCanEditForm(t *testing.T) {
	f := buildFormAccessFixture()

	// Editable only by police Sergeant+, but visible to all police (no
	// visible rule) — a police Officer should see but not edit.
	sergeantPlus := &models.RankRule{Mode: RankRuleModeAtOrAbove, AnchorRankIDs: []string{f.policeSergeantID}}
	gate := models.DepartmentRankGate{DepartmentID: f.policeDeptID, EditableByRankRule: sergeantPlus}

	cases := []struct {
		name   string
		gates  []models.DepartmentRankGate
		userID string
		want   bool
	}{
		{"no gates - everyone edits", nil, f.policeOfficerUser, true},
		{"admin edits", []models.DepartmentRankGate{gate}, f.adminID, true},
		{"sergeant edits", []models.DepartmentRankGate{gate}, f.policeSergeantUser, true},
		{"officer cannot edit", []models.DepartmentRankGate{gate}, f.policeOfficerUser, false},
		{"non-member cannot edit", []models.DepartmentRankGate{gate}, f.nonMemberUser, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := UserCanEditForm(f.community, c.userID, c.gates); got != c.want {
				t.Fatalf("UserCanEditForm(%s) = %v, want %v", c.name, got, c.want)
			}
		})
	}
}

func TestUserIsDepartmentMember(t *testing.T) {
	f := buildFormAccessFixture()
	if !UserIsDepartmentMember(f.community, f.policeDeptID, f.policeOfficerUser) {
		t.Fatal("expected police officer to be a member of police dept")
	}
	if UserIsDepartmentMember(f.community, f.policeDeptID, f.fireFighterUser) {
		t.Fatal("firefighter should not be a police dept member")
	}
	if UserIsDepartmentMember(f.community, f.policeDeptID, "") {
		t.Fatal("empty user should never be a member")
	}
}
