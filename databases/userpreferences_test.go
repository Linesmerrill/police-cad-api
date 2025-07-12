gspackage databases

import (
	"testing"

	"github.com/linesmerrill/police-cad-api/models"
	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

func TestNewUserPreferencesDatabase(t *testing.T) {
	// Test that we can create a new user preferences database
	// This is a simple test that doesn't require mocks
	db := &mongoDatabase{} // This will be nil but we're just testing the constructor
	userPrefsDB := NewUserPreferencesDatabase(db)
	
	assert.NotNil(t, userPrefsDB)
	assert.IsType(t, &userPreferencesDatabase{}, userPrefsDB)
}

func TestUserPreferencesDatabase_Constructor(t *testing.T) {
	// Test that the constructor properly initializes the database
	var db DatabaseHelper = nil // We'll use nil for this test
	userPrefsDB := NewUserPreferencesDatabase(db)
	
	assert.NotNil(t, userPrefsDB)
	assert.IsType(t, &userPreferencesDatabase{}, userPrefsDB)
	
	// Test that the database field is set correctly
	upDB := userPrefsDB.(*userPreferencesDatabase)
	assert.Equal(t, db, upDB.db)
}

func TestUserPreferencesModel(t *testing.T) {
	// Test that we can create a user preferences model
	userPrefs := models.UserPreferences{
		ID:     primitive.NewObjectID(),
		UserID: "test-user",
		CommunityPreferences: map[string]models.CommunityPreference{
			"community1": {
				DepartmentOrder: []models.DepartmentOrder{
					{DepartmentID: "dept1", Order: 0},
					{DepartmentID: "dept2", Order: 1},
				},
			},
		},
	}
	
	assert.NotNil(t, userPrefs)
	assert.Equal(t, "test-user", userPrefs.UserID)
	assert.Len(t, userPrefs.CommunityPreferences, 1)
	assert.Len(t, userPrefs.CommunityPreferences["community1"].DepartmentOrder, 2)
}

func TestUserPreferencesDatabase_Interface(t *testing.T) {
	// Test that the database implements the expected interface
	var _ UserPreferencesDatabase = &userPreferencesDatabase{}
} 