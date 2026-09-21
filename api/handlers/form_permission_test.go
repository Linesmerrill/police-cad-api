package handlers_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/helpers"
	"github.com/linesmerrill/police-cad-api/models"
)

// "the forms admin permission doesnt exist either" — reported by a community
// owner who went looking for it in the roles editor. There was no such
// permission: forms were owner-or-administrator only, and administrator grants
// everything else in the community too.

const (
	formPermOwnerID  = "507f1f77bcf86cd799439001"
	formPermMemberID = "507f1f77bcf86cd799439002"
	formPermOutsider = "507f1f77bcf86cd799439003"
)

func communityWithRole(t *testing.T, permissionName string, enabled bool, members ...string) *models.Community {
	t.Helper()
	return &models.Community{
		ID: primitive.NewObjectID(),
		Details: models.CommunityDetails{
			OwnerID: formPermOwnerID,
			Roles: []models.Role{
				{
					ID:      primitive.NewObjectID(),
					Name:    "Forms Team",
					Members: members,
					Permissions: []models.Permission{
						{Name: permissionName, Enabled: enabled},
					},
				},
			},
		},
	}
}

func TestManageFormsPermissionExists(t *testing.T) {
	assert.Equal(t, "manage forms", models.PermissionManageForms)
}

func TestFormsAdmin_ManageFormsGrantsIt(t *testing.T) {
	community := communityWithRole(t, models.PermissionManageForms, true, formPermMemberID)
	assert.True(t, helpers.IsCommunityAdmin(community, formPermMemberID))
}

func TestFormsAdmin_DisabledPermissionDoesNot(t *testing.T) {
	community := communityWithRole(t, models.PermissionManageForms, false, formPermMemberID)
	assert.False(t, helpers.IsCommunityAdmin(community, formPermMemberID))
}

func TestFormsAdmin_SomeoneOutsideTheRoleDoesNot(t *testing.T) {
	community := communityWithRole(t, models.PermissionManageForms, true, formPermMemberID)
	assert.False(t, helpers.IsCommunityAdmin(community, formPermOutsider))
}

func TestFormsAdmin_OwnerAndAdministratorStillPass(t *testing.T) {
	community := communityWithRole(t, "administrator", true, formPermMemberID)
	assert.True(t, helpers.IsCommunityAdmin(community, formPermOwnerID), "the owner")
	assert.True(t, helpers.IsCommunityAdmin(community, formPermMemberID), "an administrator")
}

// Another permission does not open the forms builder.
func TestFormsAdmin_AnUnrelatedPermissionDoesNot(t *testing.T) {
	community := communityWithRole(t, "manage bans", true, formPermMemberID)
	assert.False(t, helpers.IsCommunityAdmin(community, formPermMemberID))
}
