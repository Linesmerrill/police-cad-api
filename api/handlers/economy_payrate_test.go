package handlers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/models"
)

// The bug these cover: departmentPayRate used to consult the rank list only when
// the member carried a non-empty rankId, so a department could have a full ladder
// of configured hourly rates that never paid anyone. Members only acquire a rankId
// by going through the promotion flow, and members of a public department have no
// members[] entry to carry one at all, so "no rankId" is the common case, not the
// edge case.

func rankFixture(name string, order int, pay int, isDefault bool) models.Rank {
	return models.Rank{
		ID:             primitive.NewObjectID(),
		Name:           name,
		DisplayOrder:   order,
		IsDefault:      isDefault,
		PayRatePerHour: models.Cents(pay),
	}
}

func TestDepartmentPayRate(t *testing.T) {
	colonel := rankFixture("Colonel", 0, 10000, false)
	trooper := rankFixture("Trooper", 1, 3500, false)
	applicant := rankFixture("Applicant", 2, 2500, true)
	unpaid := rankFixture("Reserve", 3, 0, false)

	dept := &models.Department{
		ID:             primitive.NewObjectID(),
		BasePayPerHour: models.Cents(1500),
		Ranks:          []models.Rank{colonel, trooper, applicant, unpaid},
	}

	t.Run("assigned rank is paid its own rate", func(t *testing.T) {
		rate, rankID := departmentPayRate(dept, colonel.ID.Hex())
		assert.Equal(t, int64(10000), rate)
		assert.Equal(t, colonel.ID.Hex(), rankID)
	})

	t.Run("assigned rank with no rate falls back to department base", func(t *testing.T) {
		rate, rankID := departmentPayRate(dept, unpaid.ID.Hex())
		assert.Equal(t, int64(1500), rate)
		// Still reports the rank they hold — only the rate falls back.
		assert.Equal(t, unpaid.ID.Hex(), rankID)
	})

	t.Run("unranked member is paid the default rank, not department base", func(t *testing.T) {
		rate, rankID := departmentPayRate(dept, "")
		assert.Equal(t, int64(2500), rate, "should be the Applicant (default) rate, not base pay")
		assert.Equal(t, applicant.ID.Hex(), rankID)
	})

	t.Run("rankId pointing at a deleted rank resolves to the default", func(t *testing.T) {
		rate, rankID := departmentPayRate(dept, primitive.NewObjectID().Hex())
		assert.Equal(t, int64(2500), rate)
		assert.Equal(t, applicant.ID.Hex(), rankID)
	})

	t.Run("no default rank leaves an unranked member on department base", func(t *testing.T) {
		noDefault := &models.Department{
			ID:             primitive.NewObjectID(),
			BasePayPerHour: models.Cents(1500),
			Ranks:          []models.Rank{colonel, trooper},
		}
		rate, rankID := departmentPayRate(noDefault, "")
		assert.Equal(t, int64(1500), rate)
		assert.Equal(t, "", rankID)
	})

	t.Run("default rank with no rate falls back to department base", func(t *testing.T) {
		freeDefault := rankFixture("Applicant", 2, 0, true)
		d := &models.Department{
			ID:             primitive.NewObjectID(),
			BasePayPerHour: models.Cents(1500),
			Ranks:          []models.Rank{colonel, freeDefault},
		}
		rate, rankID := departmentPayRate(d, "")
		assert.Equal(t, int64(1500), rate)
		assert.Equal(t, freeDefault.ID.Hex(), rankID)
	})

	t.Run("department with no ranks pays base", func(t *testing.T) {
		d := &models.Department{ID: primitive.NewObjectID(), BasePayPerHour: models.Cents(1500)}
		rate, rankID := departmentPayRate(d, "")
		assert.Equal(t, int64(1500), rate)
		assert.Equal(t, "", rankID)
	})

	t.Run("nil department pays nothing", func(t *testing.T) {
		rate, rankID := departmentPayRate(nil, colonel.ID.Hex())
		assert.Equal(t, int64(0), rate)
		assert.Equal(t, "", rankID)
	})
}

func TestResolveMemberRank(t *testing.T) {
	sergeant := rankFixture("Sergeant", 0, 5500, false)
	applicant := rankFixture("Applicant", 1, 2500, true)
	dept := &models.Department{
		ID:    primitive.NewObjectID(),
		Ranks: []models.Rank{sergeant, applicant},
	}

	assert.Equal(t, sergeant.ID, resolveMemberRank(dept, sergeant.ID.Hex()).ID)
	assert.Equal(t, applicant.ID, resolveMemberRank(dept, "").ID, "unranked resolves to the default rank")
	assert.Nil(t, resolveMemberRank(nil, ""))
	assert.Nil(t, resolveMemberRank(&models.Department{}, ""), "no ranks configured resolves to nothing")
}

// resolveDepartmentEconomy still has to find the department and apply the
// session defaults; only pay moved out of it.
func TestResolveDepartmentEconomyDefaults(t *testing.T) {
	deptID := primitive.NewObjectID()
	community := &models.Community{
		Details: models.CommunityDetails{
			Departments: []models.Department{{ID: deptID}},
		},
	}

	dept, payoutMode, maxSession, afkGrace, ok := resolveDepartmentEconomy(community, deptID.Hex())
	assert.True(t, ok)
	assert.Equal(t, deptID, dept.ID)
	assert.Equal(t, "on_heartbeat", payoutMode)
	assert.Equal(t, 120, maxSession)
	assert.Equal(t, 60, afkGrace)

	_, _, _, _, ok = resolveDepartmentEconomy(community, primitive.NewObjectID().Hex())
	assert.False(t, ok, "unknown department is not resolvable")

	_, _, _, _, ok = resolveDepartmentEconomy(nil, deptID.Hex())
	assert.False(t, ok)
}
