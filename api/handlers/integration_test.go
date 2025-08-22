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

// TestIntegrationEndpoints tests the actual HTTP endpoints for both EMS Personas and EMS Vehicles
func TestIntegrationEndpoints(t *testing.T) {
	// Skip if not running integration tests
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	t.Run("EMS Personas Integration", func(t *testing.T) {
		testEMSPersonasIntegration(t)
	})

	t.Run("EMS Vehicles Integration", func(t *testing.T) {
		testEMSVehiclesIntegration(t)
	})
}

func testEMSPersonasIntegration(t *testing.T) {
	// Create a test router
	router := mux.NewRouter()
	
	// Create a mock database
	mockDB := mocks.NewEMSPersonaDatabase(t)
	
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

	// Test GET with pagination
	t.Run("GET /ems-personas - List with Pagination", func(t *testing.T) {
		expectedResponse := &models.EMSPersonaResponse{
			Personas: []models.EMSPersonaWithDetails{
				{
					ID:                primitive.NewObjectID(),
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
			},
			Pagination: models.Pagination{
				CurrentPage:  0,
				TotalPages:   1,
				TotalRecords: 1,
				Limit:        20,
			},
		}

		// Set up mock expectation
		mockDB.On("GetEMSPersonasByCommunityID", mock.Anything, "test-community-123", "test-user-123", int64(20), int64(0)).Return(expectedResponse, nil).Once()

		req := httptest.NewRequest("GET", "/ems-personas?active_community_id=test-community-123&user_id=test-user-123&limit=20&page=0", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		// Check response
		assert.Equal(t, http.StatusOK, w.Code)
		
		var response models.EMSPersonaResponse
		err := json.NewDecoder(w.Body).Decode(&response)
		assert.NoError(t, err)
		assert.Equal(t, expectedResponse.Pagination, response.Pagination)
		assert.Len(t, response.Personas, 1)
		assert.Equal(t, expectedResponse.Personas[0].FirstName, response.Personas[0].FirstName)
		
		t.Logf("✅ GET /ems-personas - Success: Retrieved EMS Personas with pagination")
	})

	// Test CREATE
	t.Run("POST /ems-personas - Create", func(t *testing.T) {
		// Set up mock expectation
		mockDB.On("CreateEMSPersona", mock.Anything, mock.Anything).Return(nil).Once()

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
		assert.Equal(t, testPersona.Persona.FirstName, response.Persona.FirstName)
		assert.Equal(t, testPersona.Persona.LastName, response.Persona.LastName)
		assert.Equal(t, testPersona.Persona.Department, response.Persona.Department)
		
		t.Logf("✅ POST /ems-personas - Success: Created EMS Persona")
	})

	// Test GET by ID
	t.Run("GET /ems-personas/{id} - Read", func(t *testing.T) {
		testID := "507f1f77bcf86cd799439011"
		expectedPersona := &models.EMSPersona{
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
		}

		// Set up mock expectation
		mockDB.On("GetEMSPersonaByID", mock.Anything, testID).Return(expectedPersona, nil).Once()

		req := httptest.NewRequest("GET", "/ems-personas/"+testID, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		// Check response
		assert.Equal(t, http.StatusOK, w.Code)
		
		var response models.EMSPersona
		err := json.NewDecoder(w.Body).Decode(&response)
		assert.NoError(t, err)
		assert.Equal(t, expectedPersona.Persona.FirstName, response.Persona.FirstName)
		assert.Equal(t, expectedPersona.Persona.LastName, response.Persona.LastName)
		assert.Equal(t, expectedPersona.Persona.Department, response.Persona.Department)
		
		t.Logf("✅ GET /ems-personas/{id} - Success: Retrieved EMS Persona")
	})

	// Test PUT
	t.Run("PUT /ems-personas/{id} - Update", func(t *testing.T) {
		testID := "507f1f77bcf86cd799439011"
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

		// Set up mock expectation
		mockDB.On("UpdateEMSPersona", mock.Anything, testID, updateData).Return(nil).Once()

		body, _ := json.Marshal(updateData)
		req := httptest.NewRequest("PUT", "/ems-personas/"+testID, bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		// Check response
		assert.Equal(t, http.StatusOK, w.Code)
		
		var response map[string]string
		err := json.NewDecoder(w.Body).Decode(&response)
		assert.NoError(t, err)
		assert.Equal(t, "EMS persona updated successfully", response["message"])
		
		t.Logf("✅ PUT /ems-personas/{id} - Success: Updated EMS Persona")
	})

	// Test DELETE
	t.Run("DELETE /ems-personas/{id} - Delete", func(t *testing.T) {
		testID := "507f1f77bcf86cd799439011"

		// Set up mock expectation
		mockDB.On("DeleteEMSPersona", mock.Anything, testID).Return(nil).Once()

		req := httptest.NewRequest("DELETE", "/ems-personas/"+testID, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		// Check response
		assert.Equal(t, http.StatusOK, w.Code)
		
		var response map[string]string
		err := json.NewDecoder(w.Body).Decode(&response)
		assert.NoError(t, err)
		assert.Equal(t, "EMS persona deleted successfully", response["message"])
		
		t.Logf("✅ DELETE /ems-personas/{id} - Success: Deleted EMS Persona")
	})

	// Test error cases
	t.Run("Error Cases", func(t *testing.T) {
		// Test missing required fields
		t.Run("POST /ems-personas - Missing Fields", func(t *testing.T) {
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
			t.Logf("✅ POST /ems-personas - Missing Fields: Correctly returned 400")
		})

		// Test invalid department
		t.Run("POST /ems-personas - Invalid Department", func(t *testing.T) {
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
			t.Logf("✅ POST /ems-personas - Invalid Department: Correctly returned 400")
		})
	})
}

func testEMSVehiclesIntegration(t *testing.T) {
	// Create a test router
	router := mux.NewRouter()
	
	// Create a mock database
	mockDB := mocks.NewEMSVehicleDatabase(t)
	
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

	// Test GET with pagination
	t.Run("GET /ems-vehicles - List with Pagination", func(t *testing.T) {
		expectedResponse := &models.EMSVehicleResponse{
			Vehicles: []models.EMSVehicleWithDetails{
				{
					ID:                primitive.NewObjectID(),
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
			},
			Pagination: models.Pagination{
				CurrentPage:  0,
				TotalPages:   1,
				TotalRecords: 1,
				Limit:        20,
			},
		}

		// Set up mock expectation
		mockDB.On("GetEMSVehiclesByCommunityID", mock.Anything, "test-community-123", "test-user-123", int64(20), int64(0)).Return(expectedResponse, nil).Once()

		req := httptest.NewRequest("GET", "/ems-vehicles?active_community_id=test-community-123&user_id=test-user-123&limit=20&page=0", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		// Check response
		assert.Equal(t, http.StatusOK, w.Code)
		
		var response models.EMSVehicleResponse
		err := json.NewDecoder(w.Body).Decode(&response)
		assert.NoError(t, err)
		assert.Equal(t, expectedResponse.Pagination, response.Pagination)
		assert.Len(t, response.Vehicles, 1)
		assert.Equal(t, expectedResponse.Vehicles[0].Plate, response.Vehicles[0].Plate)
		
		t.Logf("✅ GET /ems-vehicles - Success: Retrieved EMS Vehicles with pagination")
	})

	// Test CREATE
	t.Run("POST /ems-vehicles - Create", func(t *testing.T) {
		// Set up mock expectation
		mockDB.On("CreateEMSVehicle", mock.Anything, mock.Anything).Return(nil).Once()

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
		assert.Equal(t, testVehicle.Vehicle.Plate, response.Vehicle.Plate)
		assert.Equal(t, testVehicle.Vehicle.Model, response.Vehicle.Model)
		assert.Equal(t, testVehicle.Vehicle.EngineNumber, response.Vehicle.EngineNumber)
		
		t.Logf("✅ POST /ems-vehicles - Success: Created EMS Vehicle")
	})

	// Test GET by ID
	t.Run("GET /ems-vehicles/{id} - Read", func(t *testing.T) {
		testID := "507f1f77bcf86cd799439011"
		expectedVehicle := &models.EMSVehicle{
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
		}

		// Set up mock expectation
		mockDB.On("GetEMSVehicleByID", mock.Anything, testID).Return(expectedVehicle, nil).Once()

		req := httptest.NewRequest("GET", "/ems-vehicles/"+testID, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		// Check response
		assert.Equal(t, http.StatusOK, w.Code)
		
		var response models.EMSVehicle
		err := json.NewDecoder(w.Body).Decode(&response)
		assert.NoError(t, err)
		assert.Equal(t, expectedVehicle.Vehicle.Plate, response.Vehicle.Plate)
		assert.Equal(t, expectedVehicle.Vehicle.Model, response.Vehicle.Model)
		assert.Equal(t, expectedVehicle.Vehicle.EngineNumber, response.Vehicle.EngineNumber)
		
		t.Logf("✅ GET /ems-vehicles/{id} - Success: Retrieved EMS Vehicle")
	})

	// Test PUT
	t.Run("PUT /ems-vehicles/{id} - Update", func(t *testing.T) {
		testID := "507f1f77bcf86cd799439011"
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

		// Set up mock expectation
		mockDB.On("UpdateEMSVehicle", mock.Anything, testID, updateData).Return(nil).Once()

		body, _ := json.Marshal(updateData)
		req := httptest.NewRequest("PUT", "/ems-vehicles/"+testID, bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		// Check response
		assert.Equal(t, http.StatusOK, w.Code)
		
		var response map[string]string
		err := json.NewDecoder(w.Body).Decode(&response)
		assert.NoError(t, err)
		assert.Equal(t, "EMS vehicle updated successfully", response["message"])
		
		t.Logf("✅ PUT /ems-vehicles/{id} - Success: Updated EMS Vehicle")
	})

	// Test DELETE
	t.Run("DELETE /ems-vehicles/{id} - Delete", func(t *testing.T) {
		testID := "507f1f77bcf86cd799439011"

		// Set up mock expectation
		mockDB.On("DeleteEMSVehicle", mock.Anything, testID).Return(nil).Once()

		req := httptest.NewRequest("DELETE", "/ems-vehicles/"+testID, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		// Check response
		assert.Equal(t, http.StatusOK, w.Code)
		
		var response map[string]string
		err := json.NewDecoder(w.Body).Decode(&response)
		assert.NoError(t, err)
		assert.Equal(t, "EMS vehicle deleted successfully", response["message"])
		
		t.Logf("✅ DELETE /ems-vehicles/{id} - Success: Deleted EMS Vehicle")
	})

	// Test error cases
	t.Run("Error Cases", func(t *testing.T) {
		// Test missing required fields
		t.Run("POST /ems-vehicles - Missing Fields", func(t *testing.T) {
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
			t.Logf("✅ POST /ems-vehicles - Missing Fields: Correctly returned 400")
		})

		// Test invalid model
		t.Run("POST /ems-vehicles - Invalid Model", func(t *testing.T) {
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
			t.Logf("✅ POST /ems-vehicles - Invalid Model: Correctly returned 400")
		})
	})
}
