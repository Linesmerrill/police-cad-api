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

// TestEMSVehicleIntegration tests the actual HTTP endpoints
func TestEMSVehicleIntegration(t *testing.T) {
	// Skip if not running integration tests
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Create a test router
	router := mux.NewRouter()
	
	// Create a mock database for testing
	mockDB := mocks.NewEMSVehicleDatabase(t)
	
	// Set up mock expectations for the integration test
	mockDB.On("CreateEMSVehicle", mock.Anything, mock.Anything).Return(nil)
	mockDB.On("GetEMSVehicleByID", mock.Anything, mock.Anything).Return(&models.EMSVehicle{
		ID: primitive.NewObjectID(),
		Vehicle: models.EMSVehicleDetails{
			Plate:             "AMB123",
			Model:             "Ambulance",
			EngineNumber:      "ENG-001",
			Color:             "White",
			RegisteredOwner:   "City Hospital",
			ActiveCommunityID: "test-community-123",
			UserID:            "test-user-123",
			CreatedAt:         primitive.DateTime(0),
			UpdatedAt:         primitive.DateTime(0),
		},
		Version: 0,
	}, nil)
	mockDB.On("UpdateEMSVehicle", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockDB.On("DeleteEMSVehicle", mock.Anything, mock.Anything).Return(nil)
	
	// Create handler
	handler := EMSVehicle{DB: mockDB}
	
	// Register routes
	router.HandleFunc("/ems-vehicles", handler.GetEMSVehiclesHandler).Methods("GET")
	router.HandleFunc("/ems-vehicles", handler.CreateEMSVehicleHandler).Methods("POST")
	router.HandleFunc("/ems-vehicles/{id}", handler.GetEMSVehicleByIDHandler).Methods("GET")
	router.HandleFunc("/ems-vehicles/{id}", handler.UpdateEMSVehicleHandler).Methods("PUT")
	router.HandleFunc("/ems-vehicles/{id}", handler.DeleteEMSVehicleHandler).Methods("DELETE")

	// Test data
	testVehicle := &models.EMSVehicle{
		Vehicle: models.EMSVehicleDetails{
			Plate:             "AMB123",
			Model:             "Ambulance",
			EngineNumber:      "ENG-001",
			Color:             "White",
			RegisteredOwner:   "City Hospital",
			ActiveCommunityID: "test-community-123",
			UserID:            "test-user-123",
		},
	}

	t.Run("Full CRUD Integration Test", func(t *testing.T) {
		var createdID string

		// 1. CREATE - POST /ems-vehicles
		t.Run("Create EMS Vehicle", func(t *testing.T) {
			body, _ := json.Marshal(testVehicle)
			req := httptest.NewRequest("POST", "/ems-vehicles", bytes.NewBuffer(body))
			req.Header.Set("Content-Type", "application/json")
			
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			// Check response
			assert.Equal(t, http.StatusCreated, w.Code)
			
			var response models.EMSVehicle
			err := json.NewDecoder(w.Body).Decode(&response)
			assert.NoError(t, err)
			assert.NotEmpty(t, response.ID)
			assert.Equal(t, testVehicle.Vehicle.Plate, response.Vehicle.Plate)
			assert.Equal(t, testVehicle.Vehicle.Model, response.Vehicle.Model)
			assert.Equal(t, testVehicle.Vehicle.EngineNumber, response.Vehicle.EngineNumber)
			
			// Store ID for subsequent tests
			createdID = response.ID.Hex()
			t.Logf("Created EMS Vehicle with ID: %s", createdID)
		})

		// 2. READ - GET /ems-vehicles/{id}
		t.Run("Get EMS Vehicle by ID", func(t *testing.T) {
			if createdID == "" {
				t.Skip("Skipping read test - no ID from create")
			}

			req := httptest.NewRequest("GET", "/ems-vehicles/"+createdID, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			// Check response
			assert.Equal(t, http.StatusOK, w.Code)
			
			var response models.EMSVehicle
			err := json.NewDecoder(w.Body).Decode(&response)
			assert.NoError(t, err)
			assert.Equal(t, createdID, response.ID.Hex())
			assert.Equal(t, testVehicle.Vehicle.Plate, response.Vehicle.Plate)
			assert.Equal(t, testVehicle.Vehicle.Model, response.Vehicle.Model)
			assert.Equal(t, testVehicle.Vehicle.EngineNumber, response.Vehicle.EngineNumber)
			assert.Equal(t, testVehicle.Vehicle.Color, response.Vehicle.Color)
			assert.Equal(t, testVehicle.Vehicle.RegisteredOwner, response.Vehicle.RegisteredOwner)
		})

		// 3. UPDATE - PUT /ems-vehicles/{id}
		t.Run("Update EMS Vehicle", func(t *testing.T) {
			if createdID == "" {
				t.Skip("Skipping update test - no ID from create")
			}

			// Update data
			updateData := &models.EMSVehicle{
				Vehicle: models.EMSVehicleDetails{
					Plate:             "FIRE456",
					Model:             "FireTruck",
					EngineNumber:      "ENG-002",
					Color:             "Red",
					RegisteredOwner:   "Fire Department",
					ActiveCommunityID: "test-community-123",
					UserID:            "test-user-123",
				},
			}

			body, _ := json.Marshal(updateData)
			req := httptest.NewRequest("PUT", "/ems-vehicles/"+createdID, bytes.NewBuffer(body))
			req.Header.Set("Content-Type", "application/json")
			
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			// Check response
			assert.Equal(t, http.StatusOK, w.Code)
			
			var response map[string]string
			err := json.NewDecoder(w.Body).Decode(&response)
			assert.NoError(t, err)
			assert.Equal(t, "EMS vehicle updated successfully", response["message"])
		})

		// 4. READ AFTER UPDATE - GET /ems-vehicles/{id}
		t.Run("Get Updated EMS Vehicle", func(t *testing.T) {
			if createdID == "" {
				t.Skip("Skipping read after update test - no ID from create")
			}

			req := httptest.NewRequest("GET", "/ems-vehicles/"+createdID, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			// Check response
			assert.Equal(t, http.StatusOK, w.Code)
			
			var response models.EMSVehicle
			err := json.NewDecoder(w.Body).Decode(&response)
			assert.NoError(t, err)
			assert.Equal(t, createdID, response.ID.Hex())
			assert.Equal(t, "FIRE456", response.Vehicle.Plate)
			assert.Equal(t, "FireTruck", response.Vehicle.Model)
			assert.Equal(t, "ENG-002", response.Vehicle.EngineNumber)
			assert.Equal(t, "Red", response.Vehicle.Color)
			assert.Equal(t, "Fire Department", response.Vehicle.RegisteredOwner)
		})

		// 5. DELETE - DELETE /ems-vehicles/{id}
		t.Run("Delete EMS Vehicle", func(t *testing.T) {
			if createdID == "" {
				t.Skip("Skipping delete test - no ID from create")
			}

			req := httptest.NewRequest("DELETE", "/ems-vehicles/"+createdID, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			// Check response
			assert.Equal(t, http.StatusOK, w.Code)
			
			var response map[string]string
			err := json.NewDecoder(w.Body).Decode(&response)
			assert.NoError(t, err)
			assert.Equal(t, "EMS vehicle deleted successfully", response["message"])
		})

		// 6. VERIFY DELETION - GET /ems-vehicles/{id} (should return 404)
		t.Run("Verify Deletion", func(t *testing.T) {
			if createdID == "" {
				t.Skip("Skipping verification test - no ID from create")
			}

			req := httptest.NewRequest("GET", "/ems-vehicles/"+createdID, nil)
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
			invalidVehicle := &models.EMSVehicle{
				Vehicle: models.EMSVehicleDetails{
					// Missing required fields
					Model:             "Ambulance",
					ActiveCommunityID: "test-community-123",
				},
			}

			body, _ := json.Marshal(invalidVehicle)
			req := httptest.NewRequest("POST", "/ems-vehicles", bytes.NewBuffer(body))
			req.Header.Set("Content-Type", "application/json")
			
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			assert.Equal(t, http.StatusBadRequest, w.Code)
		})

		// Test invalid model
		t.Run("Create with Invalid Model", func(t *testing.T) {
			invalidVehicle := &models.EMSVehicle{
				Vehicle: models.EMSVehicleDetails{
					Plate:             "AMB123",
					Model:             "InvalidModel",
					EngineNumber:      "ENG-001",
					ActiveCommunityID: "test-community-123",
					UserID:            "test-user-123",
				},
			}

			body, _ := json.Marshal(invalidVehicle)
			req := httptest.NewRequest("POST", "/ems-vehicles", bytes.NewBuffer(body))
			req.Header.Set("Content-Type", "application/json")
			
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			assert.Equal(t, http.StatusBadRequest, w.Code)
		})

		// Test plate too long
		t.Run("Create with Plate Too Long", func(t *testing.T) {
			invalidVehicle := &models.EMSVehicle{
				Vehicle: models.EMSVehicleDetails{
					Plate:             "AMB123456789",
					Model:             "Ambulance",
					EngineNumber:      "ENG-001",
					ActiveCommunityID: "test-community-123",
					UserID:            "test-user-123",
				},
			}

			body, _ := json.Marshal(invalidVehicle)
			req := httptest.NewRequest("POST", "/ems-vehicles", bytes.NewBuffer(body))
			req.Header.Set("Content-Type", "application/json")
			
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			assert.Equal(t, http.StatusBadRequest, w.Code)
		})

		// Test non-existent ID
		t.Run("Get Non-existent ID", func(t *testing.T) {
			fakeID := "507f1f77bcf86cd799439011"
			req := httptest.NewRequest("GET", "/ems-vehicles/"+fakeID, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			assert.Equal(t, http.StatusNotFound, w.Code)
		})
	})
}
