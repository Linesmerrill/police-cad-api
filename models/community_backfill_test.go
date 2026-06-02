package models

import "testing"

func actionsContain(actions []string, want string) bool {
	for _, a := range actions {
		if a == want {
			return true
		}
	}
	return false
}

// A legacy community (owner set, but no roles array at all) must be brought up
// to the current schema: a Head Admin role for the owner plus the default
// reference data and an initialized members map.
func TestBuildCommunityBackfill_LegacyCommunity(t *testing.T) {
	c := &Community{Details: CommunityDetails{OwnerID: "owner1"}}

	set, actions := BuildCommunityBackfill(c)

	if len(actions) == 0 {
		t.Fatal("expected repairs for a legacy community, got none")
	}
	if !actionsContain(actions, "add Head Admin role") {
		t.Errorf("expected a Head Admin role to be added, actions=%v", actions)
	}
	for _, key := range []string{
		"community.roles", "community.tenCodes", "community.fines", "community.penalCodes",
		"community.inviteCodeIds", "community.banList", "community.departments", "community.events",
		"community.visibility", "community.members", "community.membersCount", "community.updatedAt",
	} {
		if _, ok := set[key]; !ok {
			t.Errorf("expected $set to include %q, set=%v", key, set)
		}
	}

	// The stamped Head Admin role must actually grant the owner admin power.
	roles, ok := set["community.roles"].([]Role)
	if !ok {
		t.Fatalf("community.roles should be []Role, got %T", set["community.roles"])
	}
	if !OwnerHasAdmin(roles, "owner1") {
		t.Error("owner should hold admin after the Head Admin role is stamped")
	}
}

// A healthy community whose owner already holds admin must be left completely
// untouched (no write on the hot read path), even if other fields look sparse.
func TestBuildCommunityBackfill_HealthyNoop(t *testing.T) {
	c := &Community{Details: CommunityDetails{
		OwnerID: "owner1",
		Roles:   []Role{BuildHeadAdminRole("owner1")},
	}}

	set, actions := BuildCommunityBackfill(c)

	if len(actions) != 0 {
		t.Errorf("expected no repairs for a healthy community, got %v", actions)
	}
	if len(set) != 0 {
		t.Errorf("expected an empty $set for a healthy community, got %v", set)
	}
}

// Re-running on the already-healed output must be a no-op (idempotency).
func TestBuildCommunityBackfill_Idempotent(t *testing.T) {
	c := &Community{Details: CommunityDetails{OwnerID: "owner1"}}
	set, _ := BuildCommunityBackfill(c)

	healed := &Community{Details: CommunityDetails{
		OwnerID: "owner1",
		Roles:   set["community.roles"].([]Role),
	}}
	if _, actions := BuildCommunityBackfill(healed); len(actions) != 0 {
		t.Errorf("second pass should be a no-op for the owner-admin gate, got %v", actions)
	}
}

// When a Head Admin role exists but the owner is not in it (and holds admin
// nowhere else), the owner is added to the existing role rather than a second
// Head Admin role being created.
func TestBuildCommunityBackfill_AddOwnerToExistingHeadAdmin(t *testing.T) {
	existing := BuildHeadAdminRole("someoneElse") // enabled admin, owner not a member
	c := &Community{Details: CommunityDetails{
		OwnerID: "owner1",
		Roles:   []Role{existing},
	}}

	set, actions := BuildCommunityBackfill(c)

	if !actionsContain(actions, "add owner to Head Admin role") {
		t.Errorf("expected the owner to be added to the existing Head Admin role, actions=%v", actions)
	}
	roles := set["community.roles"].([]Role)
	if len(roles) != 1 {
		t.Errorf("expected no duplicate Head Admin role, got %d roles", len(roles))
	}
	if !OwnerHasAdmin(roles, "owner1") {
		t.Error("owner should hold admin after being added to the existing Head Admin role")
	}
}

// An empty owner ID cannot be repaired and must be a no-op.
func TestBuildCommunityBackfill_NoOwner(t *testing.T) {
	c := &Community{Details: CommunityDetails{OwnerID: ""}}
	if _, actions := BuildCommunityBackfill(c); len(actions) != 0 {
		t.Errorf("expected no-op when ownerID is empty, got %v", actions)
	}
}

func TestIsHeadAdminRole(t *testing.T) {
	if !IsHeadAdminRole(BuildHeadAdminRole("o")) {
		t.Error("BuildHeadAdminRole output should be detected as the Head Admin role")
	}
	// Disabled admin permission must NOT count as Head Admin.
	disabled := Role{Permissions: []Permission{{Name: "administrator", Description: "Head Admin", Enabled: false}}}
	if IsHeadAdminRole(disabled) {
		t.Error("a disabled administrator permission should not be a Head Admin role")
	}
}
