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

func TestGetEMSPersonasHandler(t *testing.T) {
	tests := []struct {
		name               string
		activeCommunityID string
		limit             string
		page              string
		expectedStatus    int
		expectedResponse  *models.EMSPersonaResponse
		mockError         error
	}{
		{
			name:               "successful request with default pagination",
			activeCommunityID: "test-community-id",
			limit:             "",
			page:              "",
			expectedStatus:    http.StatusOK,
			expectedResponse: &models.EMSPersonaResponse{
				Personas: []models.EMSPersonaWithDetails{
					{
						ID:                primitive.NewObjectID(),
						FirstName:         "John",
						LastName:          "Doe",
						Department:        "EMS",
						AssignmentArea:    "Downtown District",
						Station:           5,
						CallSign:          "Medic-5",
						ActiveCommunityID: "test-community-id",
						UserID:            "test-user-id",
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
			name:               "missing active_community_id",
			activeCommunityID: "",
			expectedStatus:    http.StatusBadRequest,
			expectedResponse:  nil,
			mockError:         nil,
		},
		{
			name:               "custom pagination",
			activeCommunityID: "test-community-id",
			limit:             "10",
			page:              "1",
			expectedStatus:    http.StatusOK,
			expectedResponse: &models.EMSPersonaResponse{
				Personas: []models.EMSPersonaWithDetails{},
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
			mockDB := mocks.NewEMSPersonaDatabase(t)
			
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
				
				mockDB.On("GetEMSPersonasByCommunityID", context.Background(), tt.activeCommunityID, expectedLimit, expectedPage).Return(tt.expectedResponse, tt.mockError)
			}

			// Create handler
			handler := EMSPersona{DB: mockDB}

			// Create request
			req := httptest.NewRequest("GET", "/ems-personas", nil)
			
			// Add query parameters
			q := req.URL.Query()
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
			handler.GetEMSPersonasHandler(w, req)

			// Assert status code
			assert.Equal(t, tt.expectedStatus, w.Code)

			// If successful, check response body
			if tt.expectedStatus == http.StatusOK && tt.expectedResponse != nil {
				var response models.EMSPersonaResponse
				err := json.NewDecoder(w.Body).Decode(&response)
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedResponse.Pagination, response.Pagination)
				assert.Len(t, response.Personas, len(tt.expectedResponse.Personas))
			}
		})
	}
}

func TestGetEMSPersonaByIDHandler(t *testing.T) {
	tests := []struct {
		name            string
		id              string
		expectedStatus  int
		expectedPersona *models.EMSPersona
		mockError       error
	}{
		{
			name:   "successful request",
			id:     "507f1f77bcf86cd799439011",
			expectedStatus: http.StatusOK,
			expectedPersona: &models.EMSPersona{
				ID: primitive.NewObjectID(),
				Persona: models.PersonaDetails{
					FirstName:         "John",
					LastName:          "Doe",
					Department:        "EMS",
					AssignmentArea:    "Downtown District",
					Station:           5,
					CallSign:          "Medic-5",
					ActiveCommunityID: "test-community-id",
					UserID:            "test-user-id",
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
			expectedPersona: nil,
			mockError:      nil,
		},
		{
			name:           "not found",
			id:             "507f1f77bcf86cd799439011",
			expectedStatus: http.StatusNotFound,
			expectedPersona: nil,
			mockError:      assert.AnError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock database
			mockDB := mocks.NewEMSPersonaDatabase(t)
			
			// Set up mock expectations
			if tt.expectedStatus == http.StatusOK {
				mockDB.On("GetEMSPersonaByID", context.Background(), tt.id).Return(tt.expectedPersona, tt.mockError)
			} else if tt.expectedStatus == http.StatusNotFound {
				mockDB.On("GetEMSPersonaByID", context.Background(), tt.id).Return(nil, tt.mockError)
			}

			// Create handler
			handler := EMSPersona{DB: mockDB}

			// Create request
			req := httptest.NewRequest("GET", "/ems-personas/"+tt.id, nil)
			
			// Set URL parameters
			if tt.id != "" {
				req = mux.SetURLVars(req, map[string]string{"id": tt.id})
			}

			// Create response recorder
			w := httptest.NewRecorder()

			// Call handler
			handler.GetEMSPersonaByIDHandler(w, req)

			// Assert status code
			assert.Equal(t, tt.expectedStatus, w.Code)

			// If successful, check response body
			if tt.expectedStatus == http.StatusOK && tt.expectedPersona != nil {
				var response models.EMSPersona
				err := json.NewDecoder(w.Body).Decode(&response)
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedPersona.ID, response.ID)
				assert.Equal(t, tt.expectedPersona.Persona.FirstName, response.Persona.FirstName)
			}
		})
	}
}

func TestCreateEMSPersonaHandler(t *testing.T) {
	tests := []struct {
		name           string
		requestBody    *models.EMSPersona
		expectedStatus int
		mockError      error
	}{
		{
			name: "successful creation",
			requestBody: &models.EMSPersona{
				Persona: models.PersonaDetails{
					FirstName:         "John",
					LastName:          "Doe",
					Department:        "EMS",
					AssignmentArea:    "Downtown District",
					Station:           5,
					CallSign:          "Medic-5",
					ActiveCommunityID: "test-community-id",
					UserID:            "test-user-id",
				},
			},
			expectedStatus: http.StatusCreated,
			mockError:      nil,
		},
		{
			name: "missing firstName",
			requestBody: &models.EMSPersona{
				Persona: models.PersonaDetails{
					LastName:          "Doe",
					Department:        "EMS",
					AssignmentArea:    "Downtown District",
					ActiveCommunityID: "test-community-id",
					UserID:            "test-user-id",
				},
			},
			expectedStatus: http.StatusBadRequest,
			mockError:      nil,
		},
		{
			name: "missing lastName",
			requestBody: &models.EMSPersona{
				Persona: models.PersonaDetails{
					FirstName:         "John",
					Department:        "EMS",
					AssignmentArea:    "Downtown District",
					ActiveCommunityID: "test-community-id",
					UserID:            "test-user-id",
				},
			},
			expectedStatus: http.StatusBadRequest,
			mockError:      nil,
		},
		{
			name: "missing department",
			requestBody: &models.EMSPersona{
				Persona: models.PersonaDetails{
					FirstName:         "John",
					LastName:          "Doe",
					AssignmentArea:    "Downtown District",
					ActiveCommunityID: "test-community-id",
					UserID:            "test-user-id",
				},
			},
			expectedStatus: http.StatusBadRequest,
			mockError:      nil,
		},
		{
			name: "invalid department",
			requestBody: &models.EMSPersona{
				Persona: models.PersonaDetails{
					FirstName:         "John",
					LastName:          "Doe",
					Department:        "Invalid",
					AssignmentArea:    "Downtown District",
					ActiveCommunityID: "test-community-id",
					UserID:            "test-user-id",
				},
			},
			expectedStatus: http.StatusBadRequest,
			mockError:      nil,
		},
		{
			name: "missing assignmentArea",
			requestBody: &models.EMSPersona{
				Persona: models.PersonaDetails{
					FirstName:         "John",
					LastName:          "Doe",
					Department:        "EMS",
					ActiveCommunityID: "test-community-id",
					UserID:            "test-user-id",
				},
			},
			expectedStatus: http.StatusBadRequest,
			mockError:      nil,
		},
		{
			name: "missing activeCommunityID",
			requestBody: &models.EMSPersona{
				Persona: models.PersonaDetails{
					FirstName:      "John",
					LastName:       "Doe",
					Department:     "EMS",
					AssignmentArea: "Downtown District",
					UserID:         "test-user-id",
				},
			},
			expectedStatus: http.StatusBadRequest,
			mockError:      nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock database
			mockDB := mocks.NewEMSPersonaDatabase(t)
			
			// Set up mock expectations
			if tt.expectedStatus == http.StatusCreated {
				mockDB.On("CreateEMSPersona", context.Background(), tt.requestBody).Return(tt.mockError)
			}

			// Create handler
			handler := EMSPersona{DB: mockDB}

			// Create request body
			body, _ := json.Marshal(tt.requestBody)
			req := httptest.NewRequest("POST", "/ems-personas", bytes.NewBuffer(body))
			req.Header.Set("Content-Type", "application/json")

			// Create response recorder
			w := httptest.NewRecorder()

			// Call handler
			handler.CreateEMSPersonaHandler(w, req)

			// Assert status code
			assert.Equal(t, tt.expectedStatus, w.Code)

			// If successful, check response body
			if tt.expectedStatus == http.StatusCreated {
				var response models.EMSPersona
				err := json.NewDecoder(w.Body).Decode(&response)
				assert.NoError(t, err)
				assert.Equal(t, tt.requestBody.Persona.FirstName, response.Persona.FirstName)
			}
		})
	}
}

func TestUpdateEMSPersonaHandler(t *testing.T) {
	tests := []struct {
		name           string
		id             string
		requestBody    *models.EMSPersona
		expectedStatus int
		mockError      error
	}{
		{
			name: "successful update",
			id:   "507f1f77bcf86cd799439011",
			requestBody: &models.EMSPersona{
				Persona: models.PersonaDetails{
					FirstName:         "John",
					LastName:          "Smith",
					Department:        "Fire",
					AssignmentArea:    "Uptown District",
					Station:           10,
					CallSign:          "Fire-10",
					ActiveCommunityID: "test-community-id",
					UserID:            "test-user-id",
				},
			},
			expectedStatus: http.StatusOK,
			mockError:      nil,
		},
		{
			name: "missing id",
			id:   "",
			requestBody: &models.EMSPersona{
				Persona: models.PersonaDetails{
					FirstName:         "John",
					LastName:          "Smith",
					Department:        "Fire",
					AssignmentArea:    "Uptown District",
					ActiveCommunityID: "test-community-id",
					UserID:            "test-user-id",
				},
			},
			expectedStatus: http.StatusBadRequest,
			mockError:      nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock database
			mockDB := mocks.NewEMSPersonaDatabase(t)
			
			// Set up mock expectations
			if tt.expectedStatus == http.StatusOK {
				mockDB.On("UpdateEMSPersona", context.Background(), tt.id, tt.requestBody).Return(tt.mockError)
			}

			// Create handler
			handler := EMSPersona{DB: mockDB}

			// Create request body
			body, _ := json.Marshal(tt.requestBody)
			req := httptest.NewRequest("PUT", "/ems-personas/"+tt.id, bytes.NewBuffer(body))
			req.Header.Set("Content-Type", "application/json")
			
			// Set URL parameters
			if tt.id != "" {
				req = mux.SetURLVars(req, map[string]string{"id": tt.id})
			}

			// Create response recorder
			w := httptest.NewRecorder()

			// Call handler
			handler.UpdateEMSPersonaHandler(w, req)

			// Assert status code
			assert.Equal(t, tt.expectedStatus, w.Code)

			// If successful, check response body
			if tt.expectedStatus == http.StatusOK {
				var response map[string]string
				err := json.NewDecoder(w.Body).Decode(&response)
				assert.NoError(t, err)
				assert.Equal(t, "EMS persona updated successfully", response["message"])
			}
		})
	}
}

func TestDeleteEMSPersonaHandler(t *testing.T) {
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
			mockDB := mocks.NewEMSPersonaDatabase(t)
			
			// Set up mock expectations
			if tt.id != "" {
				mockDB.On("DeleteEMSPersona", context.Background(), tt.id).Return(tt.mockError)
			}

			// Create handler
			handler := EMSPersona{DB: mockDB}

			// Create request
			req := httptest.NewRequest("DELETE", "/ems-personas/"+tt.id, nil)
			
			// Set URL parameters
			if tt.id != "" {
				req = mux.SetURLVars(req, map[string]string{"id": tt.id})
			}

			// Create response recorder
			w := httptest.NewRecorder()

			// Call handler
			handler.DeleteEMSPersonaHandler(w, req)

			// Assert status code
			assert.Equal(t, tt.expectedStatus, w.Code)

			// If successful, check response body
			if tt.expectedStatus == http.StatusOK {
				var response map[string]string
				err := json.NewDecoder(w.Body).Decode(&response)
				assert.NoError(t, err)
				assert.Equal(t, "EMS persona deleted successfully", response["message"])
			}
		})
	}
}
