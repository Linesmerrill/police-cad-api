package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gorilla/mux"
	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

func TestGetMedicationsHandler(t *testing.T) {
	tests := []struct {
		name               string
		civilianID        string
		activeCommunityID string
		limit             string
		page              string
		expectedStatus    int
		expectedResponse  *models.MedicationResponse
		mockError         error
	}{
		{
			name:               "successful request with default pagination",
			civilianID:        "test-civilian-id",
			activeCommunityID: "test-community-id",
			limit:             "",
			page:              "",
			expectedStatus:    http.StatusOK,
			expectedResponse: &models.MedicationResponse{
				Medications: []models.MedicationWithDetails{
					{
						ID:                primitive.NewObjectID(),
						StartDate:         "2025-05-20",
						Name:              "Aspirin",
						Dosage:            "100mg",
						Frequency:         "daily",
						CivilianID:        "test-civilian-id",
						ActiveCommunityID: "test-community-id",
						UserID:            "test-user-id",
						FirstName:         "John",
						LastName:          "Doe",
						DateOfBirth:       "1990-01-01",
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
			},
			mockError: nil,
		},
		{
			name:               "missing civilian_id",
			civilianID:        "",
			activeCommunityID: "test-community-id",
			expectedStatus:    http.StatusBadRequest,
			expectedResponse:  nil,
			mockError:         nil,
		},
		{
			name:               "missing active_community_id",
			civilianID:        "test-civilian-id",
			activeCommunityID: "",
			expectedStatus:    http.StatusBadRequest,
			expectedResponse:  nil,
			mockError:         nil,
		},
		{
			name:               "custom pagination",
			civilianID:        "test-civilian-id",
			activeCommunityID: "test-community-id",
			limit:             "10",
			page:              "1",
			expectedStatus:    http.StatusOK,
			expectedResponse: &models.MedicationResponse{
				Medications: []models.MedicationWithDetails{},
				Pagination: models.Pagination{
					CurrentPage:  1,
					TotalPages:   0,
					TotalRecords: 0,
					Limit:        10,
				},
			},
			mockError: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
					// Create mock database
		mockDB := mocks.NewMedicationDatabase(t)
		
		// Set up mock expectations
		if tt.expectedStatus == http.StatusOK {
			// Calculate expected limit and page values
			expectedLimit := int64(20)
			expectedPage := int64(0)
			
			if tt.limit != "" {
				if limit, err := strconv.ParseInt(tt.limit, 10, 64); err == nil && limit > 0 {
					expectedLimit = limit
				}
			}
			
			if tt.page != "" {
				if page, err := strconv.ParseInt(tt.page, 10, 64); err == nil && page >= 0 {
					expectedPage = page
				}
			}
			
			mockDB.On("GetMedicationsByCivilianID", context.Background(), tt.civilianID, tt.activeCommunityID, expectedLimit, expectedPage).Return(tt.expectedResponse, tt.mockError)
		}

			// Create handler
			handler := Medication{DB: mockDB}

			// Create request
			req := httptest.NewRequest("GET", "/medications", nil)
			
			// Add query parameters
			q := req.URL.Query()
			if tt.civilianID != "" {
				q.Add("civilian_id", tt.civilianID)
			}
			if tt.activeCommunityID != "" {
				q.Add("active_community_id", tt.activeCommunityID)
			}
			if tt.limit != "" {
				q.Add("limit", tt.limit)
			}
			if tt.page != "" {
				q.Add("page", tt.page)
			}
			req.URL.RawQuery = q.Encode()

			// Create response recorder
			w := httptest.NewRecorder()

			// Call handler
			handler.GetMedicationsHandler(w, req)

			// Assert status code
			assert.Equal(t, tt.expectedStatus, w.Code)

			// If successful, check response body
			if tt.expectedStatus == http.StatusOK && tt.expectedResponse != nil {
				var response models.MedicationResponse
				err := json.NewDecoder(w.Body).Decode(&response)
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedResponse.Pagination, response.Pagination)
				assert.Len(t, response.Medications, len(tt.expectedResponse.Medications))
			}
		})
	}
}

func TestGetMedicationByIDHandler(t *testing.T) {
	tests := []struct {
		name            string
		id              string
		expectedStatus  int
		expectedMedication *models.Medication
		mockError       error
	}{
		{
			name:   "successful request",
			id:     "507f1f77bcf86cd799439011",
			expectedStatus: http.StatusOK,
			expectedMedication: &models.Medication{
				ID: primitive.NewObjectID(),
				Medication: models.MedicationDetails{
					StartDate:         "2025-05-20",
					Name:              "Aspirin",
					Dosage:            "100mg",
					Frequency:         "daily",
					CivilianID:        "test-civilian-id",
					ActiveCommunityID: "test-community-id",
					UserID:            "test-user-id",
					FirstName:         "John",
					LastName:          "Doe",
					DateOfBirth:       "1990-01-01",
					CreatedAt:         primitive.DateTime(0),
					UpdatedAt:         primitive.DateTime(0),
				},
				Version: 0,
			},
			mockError: nil,
		},
		{
			name:           "missing id",
			id:             "",
			expectedStatus: http.StatusBadRequest,
			expectedMedication: nil,
			mockError:      nil,
		},
		{
			name:           "not found",
			id:             "507f1f77bcf86cd799439011",
			expectedStatus: http.StatusNotFound,
			expectedMedication: nil,
			mockError:      assert.AnError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
					// Create mock database
		mockDB := mocks.NewMedicationDatabase(t)
		
		// Set up mock expectations
		if tt.expectedStatus == http.StatusOK {
			mockDB.On("GetMedicationByID", context.Background(), tt.id).Return(tt.expectedMedication, tt.mockError)
		} else if tt.expectedStatus == http.StatusNotFound {
			mockDB.On("GetMedicationByID", context.Background(), tt.id).Return(nil, tt.mockError)
		}

			// Create handler
			handler := Medication{DB: mockDB}

			// Create request
			req := httptest.NewRequest("GET", "/medications/"+tt.id, nil)
			
			// Set URL parameters
			if tt.id != "" {
				req = mux.SetURLVars(req, map[string]string{"id": tt.id})
			}

			// Create response recorder
			w := httptest.NewRecorder()

			// Call handler
			handler.GetMedicationByIDHandler(w, req)

			// Assert status code
			assert.Equal(t, tt.expectedStatus, w.Code)

			// If successful, check response body
			if tt.expectedStatus == http.StatusOK && tt.expectedMedication != nil {
				var response models.Medication
				err := json.NewDecoder(w.Body).Decode(&response)
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedMedication.ID, response.ID)
				assert.Equal(t, tt.expectedMedication.Medication.Name, response.Medication.Name)
			}
		})
	}
}

func TestCreateMedicationHandler(t *testing.T) {
	tests := []struct {
		name           string
		requestBody    *models.Medication
		expectedStatus int
		mockError      error
	}{
		{
			name: "successful creation",
			requestBody: &models.Medication{
				Medication: models.MedicationDetails{
					StartDate:         "2025-05-20",
					Name:              "Aspirin",
					Dosage:            "100mg",
					Frequency:         "daily",
					CivilianID:        "test-civilian-id",
					ActiveCommunityID: "test-community-id",
					UserID:            "test-user-id",
					FirstName:         "John",
					LastName:          "Doe",
					DateOfBirth:       "1990-01-01",
				},
			},
			expectedStatus: http.StatusCreated,
			mockError:      nil,
		},
		{
			name: "missing civilianID",
			requestBody: &models.Medication{
				Medication: models.MedicationDetails{
					StartDate:         "2025-05-20",
					Name:              "Aspirin",
					Dosage:            "100mg",
					Frequency:         "daily",
					ActiveCommunityID: "test-community-id",
					UserID:            "test-user-id",
					FirstName:         "John",
					LastName:          "Doe",
					DateOfBirth:       "1990-01-01",
				},
			},
			expectedStatus: http.StatusBadRequest,
			mockError:      nil,
		},
		{
			name: "missing activeCommunityID",
			requestBody: &models.Medication{
				Medication: models.MedicationDetails{
					StartDate:  "2025-05-20",
					Name:       "Aspirin",
					Dosage:     "100mg",
					Frequency:  "daily",
					CivilianID: "test-civilian-id",
					UserID:     "test-user-id",
					FirstName:  "John",
					LastName:   "Doe",
					DateOfBirth: "1990-01-01",
				},
			},
			expectedStatus: http.StatusBadRequest,
			mockError:      nil,
		},
		{
			name: "missing name",
			requestBody: &models.Medication{
				Medication: models.MedicationDetails{
					StartDate:         "2025-05-20",
					Dosage:            "100mg",
					Frequency:         "daily",
					CivilianID:        "test-civilian-id",
					ActiveCommunityID: "test-community-id",
					UserID:            "test-user-id",
					FirstName:         "John",
					LastName:          "Doe",
					DateOfBirth:       "1990-01-01",
				},
			},
			expectedStatus: http.StatusBadRequest,
			mockError:      nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
					// Create mock database
		mockDB := mocks.NewMedicationDatabase(t)
		
		// Set up mock expectations
		if tt.expectedStatus == http.StatusCreated {
			mockDB.On("CreateMedication", context.Background(), tt.requestBody).Return(tt.mockError)
		}

			// Create handler
			handler := Medication{DB: mockDB}

			// Create request body
			body, _ := json.Marshal(tt.requestBody)
			req := httptest.NewRequest("POST", "/medications", bytes.NewBuffer(body))
			req.Header.Set("Content-Type", "application/json")

			// Create response recorder
			w := httptest.NewRecorder()

			// Call handler
			handler.CreateMedicationHandler(w, req)

			// Assert status code
			assert.Equal(t, tt.expectedStatus, w.Code)

			// If successful, check response body
			if tt.expectedStatus == http.StatusCreated {
				var response models.Medication
				err := json.NewDecoder(w.Body).Decode(&response)
				assert.NoError(t, err)
				// Note: ID will be empty in test because mock doesn't execute real logic
				// In real usage, the database would generate and return the ID
				assert.Equal(t, tt.requestBody.Medication.Name, response.Medication.Name)
			}
		})
	}
}

func TestUpdateMedicationHandler(t *testing.T) {
	tests := []struct {
		name           string
		id             string
		requestBody    *models.Medication
		expectedStatus int
		mockError      error
	}{
		{
			name: "successful update",
			id:   "507f1f77bcf86cd799439011",
			requestBody: &models.Medication{
				Medication: models.MedicationDetails{
					StartDate:         "2025-05-20",
					Name:              "Updated Aspirin",
					Dosage:            "200mg",
					Frequency:         "twice daily",
					CivilianID:        "test-civilian-id",
					ActiveCommunityID: "test-community-id",
					UserID:            "test-user-id",
					FirstName:         "John",
					LastName:          "Doe",
					DateOfBirth:       "1990-01-01",
				},
			},
			expectedStatus: http.StatusOK,
			mockError:      nil,
		},
		{
			name: "missing id",
			id:   "",
			requestBody: &models.Medication{
				Medication: models.MedicationDetails{
					StartDate:         "2025-05-20",
					Name:              "Updated Aspirin",
					Dosage:            "200mg",
					Frequency:         "twice daily",
					CivilianID:        "test-civilian-id",
					ActiveCommunityID: "test-community-id",
					UserID:            "test-user-id",
					FirstName:         "John",
					LastName:          "Doe",
					DateOfBirth:       "1990-01-01",
				},
			},
			expectedStatus: http.StatusBadRequest,
			mockError:      nil,
		},
		{
			name: "missing civilianID",
			id:   "507f1f77bcf86cd799439011",
			requestBody: &models.Medication{
				Medication: models.MedicationDetails{
					StartDate:         "2025-05-20",
					Name:              "Updated Aspirin",
					Dosage:            "200mg",
					Frequency:         "twice daily",
					ActiveCommunityID: "test-community-id",
					UserID:            "test-user-id",
					FirstName:         "John",
					LastName:          "Doe",
					DateOfBirth:       "1990-01-01",
				},
			},
			expectedStatus: http.StatusBadRequest,
			mockError:      nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
					// Create mock database
		mockDB := mocks.NewMedicationDatabase(t)
		
		// Set up mock expectations
		if tt.expectedStatus == http.StatusOK {
			mockDB.On("UpdateMedication", context.Background(), tt.id, tt.requestBody).Return(tt.mockError)
		}

			// Create handler
			handler := Medication{DB: mockDB}

			// Create request body
			body, _ := json.Marshal(tt.requestBody)
			req := httptest.NewRequest("PUT", "/medications/"+tt.id, bytes.NewBuffer(body))
			req.Header.Set("Content-Type", "application/json")
			
			// Set URL parameters
			if tt.id != "" {
				req = mux.SetURLVars(req, map[string]string{"id": tt.id})
			}

			// Create response recorder
			w := httptest.NewRecorder()

			// Call handler
			handler.UpdateMedicationHandler(w, req)

			// Assert status code
			assert.Equal(t, tt.expectedStatus, w.Code)

			// If successful, check response body
			if tt.expectedStatus == http.StatusOK {
				var response map[string]string
				err := json.NewDecoder(w.Body).Decode(&response)
				assert.NoError(t, err)
				assert.Equal(t, "Medication updated successfully", response["message"])
			}
		})
	}
}

func TestDeleteMedicationHandler(t *testing.T) {
	tests := []struct {
		name           string
		id             string
		expectedStatus int
		mockError      error
	}{
		{
			name:           "successful deletion",
			id:             "507f1f77bcf86cd799439011",
			expectedStatus: http.StatusOK,
			mockError:      nil,
		},
		{
			name:           "missing id",
			id:             "",
			expectedStatus: http.StatusBadRequest,
			mockError:      nil,
		},
		{
			name:           "database error",
			id:             "507f1f77bcf86cd799439011",
			expectedStatus: http.StatusInternalServerError,
			mockError:      assert.AnError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
					// Create mock database
		mockDB := mocks.NewMedicationDatabase(t)
		
		// Set up mock expectations
		if tt.id != "" {
			mockDB.On("DeleteMedication", context.Background(), tt.id).Return(tt.mockError)
		}

			// Create handler
			handler := Medication{DB: mockDB}

			// Create request
			req := httptest.NewRequest("DELETE", "/medications/"+tt.id, nil)
			
			// Set URL parameters
			if tt.id != "" {
				req = mux.SetURLVars(req, map[string]string{"id": tt.id})
			}

			// Create response recorder
			w := httptest.NewRecorder()

			// Call handler
			handler.DeleteMedicationHandler(w, req)

			// Assert status code
			assert.Equal(t, tt.expectedStatus, w.Code)

			// If successful, check response body
			if tt.expectedStatus == http.StatusOK {
				var response map[string]string
				err := json.NewDecoder(w.Body).Decode(&response)
				assert.NoError(t, err)
				assert.Equal(t, "Medication deleted successfully", response["message"])
			}
		})
	}
}
