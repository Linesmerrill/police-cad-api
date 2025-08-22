package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// TestEMSPersonaIntegration tests the actual HTTP endpoints
func TestEMSPersonaIntegration(t *testing.T) {
	// Skip if not running integration tests
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Create a test router
	router := mux.NewRouter()
	
	// Create a mock database for testing
	mockDB := mocks.NewEMSPersonaDatabase(t)
	
	// Set up mock expectations for the integration test
	// We'll set these up as we go through the test
	mockDB.On("CreateEMSPersona", mock.Anything, mock.Anything).Return(nil)
	mockDB.On("GetEMSPersonaByID", mock.Anything, mock.Anything).Return(&models.EMSPersona{
		ID: primitive.NewObjectID(),
		Persona: models.PersonaDetails{
			FirstName:         "John",
			LastName:          "Doe",
			Department:        "EMS",
			AssignmentArea:    "Downtown District",
			Station:           5,
			CallSign:          "Medic-5",
			ActiveCommunityID: "test-community-123",
			UserID:            "test-user-123",
			CreatedAt:         primitive.DateTime(0),
			UpdatedAt:         primitive.DateTime(0),
		},
		Version: 0,
	}, nil)
	mockDB.On("UpdateEMSPersona", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockDB.On("DeleteEMSPersona", mock.Anything, mock.Anything).Return(nil)
	
	// Create handler
	handler := EMSPersona{DB: mockDB}
	
	// Register routes
	router.HandleFunc("/ems-personas", handler.GetEMSPersonasHandler).Methods("GET")
	router.HandleFunc("/ems-personas", handler.CreateEMSPersonaHandler).Methods("POST")
	router.HandleFunc("/ems-personas/{id}", handler.GetEMSPersonaByIDHandler).Methods("GET")
	router.HandleFunc("/ems-personas/{id}", handler.UpdateEMSPersonaHandler).Methods("PUT")
	router.HandleFunc("/ems-personas/{id}", handler.DeleteEMSPersonaHandler).Methods("DELETE")

	// Test data
	testPersona := &models.EMSPersona{
		Persona: models.PersonaDetails{
			FirstName:         "John",
			LastName:          "Doe",
			Department:        "EMS",
			AssignmentArea:    "Downtown District",
			Station:           5,
			CallSign:          "Medic-5",
			ActiveCommunityID: "test-community-123",
			UserID:            "test-user-123",
		},
	}

	t.Run("Full CRUD Integration Test", func(t *testing.T) {
		var createdID string

		// 1. CREATE - POST /ems-personas
		t.Run("Create EMS Persona", func(t *testing.T) {
			body, _ := json.Marshal(testPersona)
			req := httptest.NewRequest("POST", "/ems-personas", bytes.NewBuffer(body))
			req.Header.Set("Content-Type", "application/json")
			
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			// Check response
			assert.Equal(t, http.StatusCreated, w.Code)
			
			var response models.EMSPersona
			err := json.NewDecoder(w.Body).Decode(&response)
			assert.NoError(t, err)
			assert.NotEmpty(t, response.ID)
			assert.Equal(t, testPersona.Persona.FirstName, response.Persona.FirstName)
			assert.Equal(t, testPersona.Persona.LastName, response.Persona.LastName)
			assert.Equal(t, testPersona.Persona.Department, response.Persona.Department)
			
			// Store ID for subsequent tests
			createdID = response.ID.Hex()
			t.Logf("Created EMS Persona with ID: %s", createdID)
		})

		// 2. READ - GET /ems-personas/{id}
		t.Run("Get EMS Persona by ID", func(t *testing.T) {
			if createdID == "" {
				t.Skip("Skipping read test - no ID from create")
			}

			req := httptest.NewRequest("GET", "/ems-personas/"+createdID, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			// Check response
			assert.Equal(t, http.StatusOK, w.Code)
			
			var response models.EMSPersona
			err := json.NewDecoder(w.Body).Decode(&response)
			assert.NoError(t, err)
			assert.Equal(t, createdID, response.ID.Hex())
			assert.Equal(t, testPersona.Persona.FirstName, response.Persona.FirstName)
			assert.Equal(t, testPersona.Persona.LastName, response.Persona.LastName)
			assert.Equal(t, testPersona.Persona.Department, response.Persona.Department)
			assert.Equal(t, testPersona.Persona.AssignmentArea, response.Persona.AssignmentArea)
			assert.Equal(t, testPersona.Persona.Station, response.Persona.Station)
			assert.Equal(t, testPersona.Persona.CallSign, response.Persona.CallSign)
		})

		// 3. UPDATE - PUT /ems-personas/{id}
		t.Run("Update EMS Persona", func(t *testing.T) {
			if createdID == "" {
				t.Skip("Skipping update test - no ID from create")
			}

			// Update data
			updateData := &models.EMSPersona{
				Persona: models.PersonaDetails{
					FirstName:         "Jane",
					LastName:          "Smith",
					Department:        "Fire",
					AssignmentArea:    "Uptown District",
					Station:           10,
					CallSign:          "Fire-10",
					ActiveCommunityID: "test-community-123",
					UserID:            "test-user-123",
				},
			}

			body, _ := json.Marshal(updateData)
			req := httptest.NewRequest("PUT", "/ems-personas/"+createdID, bytes.NewBuffer(body))
			req.Header.Set("Content-Type", "application/json")
			
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			// Check response
			assert.Equal(t, http.StatusOK, w.Code)
			
			var response map[string]string
			err := json.NewDecoder(w.Body).Decode(&response)
			assert.NoError(t, err)
			assert.Equal(t, "EMS persona updated successfully", response["message"])
		})

		// 4. READ AFTER UPDATE - GET /ems-personas/{id}
		t.Run("Get Updated EMS Persona", func(t *testing.T) {
			if createdID == "" {
				t.Skip("Skipping read after update test - no ID from create")
			}

			req := httptest.NewRequest("GET", "/ems-personas/"+createdID, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			// Check response
			assert.Equal(t, http.StatusOK, w.Code)
			
			var response models.EMSPersona
			err := json.NewDecoder(w.Body).Decode(&response)
			assert.NoError(t, err)
			assert.Equal(t, createdID, response.ID.Hex())
			assert.Equal(t, "Jane", response.Persona.FirstName)
			assert.Equal(t, "Smith", response.Persona.LastName)
			assert.Equal(t, "Fire", response.Persona.Department)
			assert.Equal(t, "Uptown District", response.Persona.AssignmentArea)
			assert.Equal(t, int64(10), response.Persona.Station)
			assert.Equal(t, "Fire-10", response.Persona.CallSign)
		})

		// 5. DELETE - DELETE /ems-personas/{id}
		t.Run("Delete EMS Persona", func(t *testing.T) {
			if createdID == "" {
				t.Skip("Skipping delete test - no ID from create")
			}

			req := httptest.NewRequest("DELETE", "/ems-personas/"+createdID, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			// Check response
			assert.Equal(t, http.StatusOK, w.Code)
			
			var response map[string]string
			err := json.NewDecoder(w.Body).Decode(&response)
			assert.NoError(t, err)
			assert.Equal(t, "EMS persona deleted successfully", response["message"])
		})

		// 6. VERIFY DELETION - GET /ems-personas/{id} (should return 404)
		t.Run("Verify Deletion", func(t *testing.T) {
			if createdID == "" {
				t.Skip("Skipping verification test - no ID from create")
			}

			req := httptest.NewRequest("GET", "/ems-personas/"+createdID, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			// Should return 404 since it was deleted
			assert.Equal(t, http.StatusNotFound, w.Code)
		})
	})

	// Test error cases
	t.Run("Error Cases", func(t *testing.T) {
		// Test missing required fields
		t.Run("Create with Missing Fields", func(t *testing.T) {
			invalidPersona := &models.EMSPersona{
				Persona: models.PersonaDetails{
					// Missing required fields
					Department:        "EMS",
					ActiveCommunityID: "test-community-123",
				},
			}

			body, _ := json.Marshal(invalidPersona)
			req := httptest.NewRequest("POST", "/ems-personas", bytes.NewBuffer(body))
			req.Header.Set("Content-Type", "application/json")
			
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			assert.Equal(t, http.StatusBadRequest, w.Code)
		})

		// Test invalid department
		t.Run("Create with Invalid Department", func(t *testing.T) {
			invalidPersona := &models.EMSPersona{
				Persona: models.PersonaDetails{
					FirstName:         "John",
					LastName:          "Doe",
					Department:        "InvalidDept",
					AssignmentArea:    "Downtown District",
					ActiveCommunityID: "test-community-123",
					UserID:            "test-user-123",
				},
			}

			body, _ := json.Marshal(invalidPersona)
			req := httptest.NewRequest("POST", "/ems-personas", bytes.NewBuffer(body))
			req.Header.Set("Content-Type", "application/json")
			
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			assert.Equal(t, http.StatusBadRequest, w.Code)
		})

		// Test non-existent ID
		t.Run("Get Non-existent ID", func(t *testing.T) {
			fakeID := "507f1f77bcf86cd799439011"
			req := httptest.NewRequest("GET", "/ems-personas/"+fakeID, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			assert.Equal(t, http.StatusNotFound, w.Code)
		})
	})
}
