package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gorilla/mux"
	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

func TestGetMedicalReportsHandler(t *testing.T) {
	tests := []struct {
		name               string
		civilianID        string
		activeCommunityID string
		limit             string
		page              string
		expectedStatus    int
		expectedResponse  *models.MedicalReportResponse
		mockError         error
	}{
		{
			name:               "successful request with default pagination",
			civilianID:        "test-civilian-id",
			activeCommunityID: "test-community-id",
			limit:             "",
			page:              "",
			expectedStatus:    http.StatusOK,
			expectedResponse: &models.MedicalReportResponse{
				MedicalReports: []models.MedicalReportWithEms{
					{
						ID:                primitive.NewObjectID(),
						CivilianID:        "test-civilian-id",
						ReportingEmsID:    "test-ems-id",
						ReportDate:        "2025-05-20",
						ReportTime:        "15:44",
						Hospitalized:      "yes",
						Deceased:          false,
						Details:           "Test medical report",
						ActiveCommunityID: "test-community-id",
						CreatedAt:         primitive.DateTime(0),
						UpdatedAt:         primitive.DateTime(0),
						ReportingEms: models.EmsInfo{
							Name:       "John Smith",
							Department: "Fire/EMS",
						},
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
			expectedResponse: &models.MedicalReportResponse{
				MedicalReports: []models.MedicalReportWithEms{},
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
			mockDB := mocks.NewMedicalReportDatabase(t)
			
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
				
				mockDB.On("GetMedicalReportsByCivilianID", mock.Anything, tt.civilianID, tt.activeCommunityID, expectedLimit, expectedPage).Return(tt.expectedResponse, tt.mockError)
			}

			// Create handler
			handler := MedicalReport{DB: mockDB}

			// Create request
			req := httptest.NewRequest("GET", "/medical-reports", nil)
			
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
			handler.GetMedicalReportsHandler(w, req)

			// Assert status code
			assert.Equal(t, tt.expectedStatus, w.Code)

			// If successful, check response body
			if tt.expectedStatus == http.StatusOK && tt.expectedResponse != nil {
				var response models.MedicalReportResponse
				err := json.NewDecoder(w.Body).Decode(&response)
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedResponse.Pagination, response.Pagination)
				assert.Len(t, response.MedicalReports, len(tt.expectedResponse.MedicalReports))
			}
		})
	}
}

func TestGetMedicalReportByIDHandler(t *testing.T) {
	tests := []struct {
		name            string
		id              string
		expectedStatus  int
		expectedReport  *models.MedicalReport
		mockError       error
	}{
		{
			name:   "successful request",
			id:     "507f1f77bcf86cd799439011",
			expectedStatus: http.StatusOK,
			expectedReport: &models.MedicalReport{
				ID: primitive.NewObjectID(),
				Report: models.MedicalReportDetails{
					Date:              "2025-05-20",
					Details:           "Test medical report",
					CivilianID:        "test-civilian-id",
					ReportingEmsID:    "test-ems-id",
					Hospitalized:      false,
					Deceased:          false,
					ActiveCommunityID: "test-community-id",
					UserID:            "test-user-id",
					Name:              "John Doe",
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
			expectedReport: nil,
			mockError:      nil,
		},
		{
			name:           "not found",
			id:             "507f1f77bcf86cd799439011",
			expectedStatus: http.StatusNotFound,
			expectedReport: nil,
			mockError:      assert.AnError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock database
			mockDB := mocks.NewMedicalReportDatabase(t)
			
			// Set up mock expectations
			if tt.expectedStatus == http.StatusOK {
				mockDB.On("GetMedicalReportByID", mock.Anything, tt.id).Return(tt.expectedReport, tt.mockError)
			} else if tt.expectedStatus == http.StatusNotFound {
				mockDB.On("GetMedicalReportByID", mock.Anything, tt.id).Return(nil, tt.mockError)
			}

			// Create handler
			handler := MedicalReport{DB: mockDB}

			// Create request
			req := httptest.NewRequest("GET", "/medical-reports/"+tt.id, nil)
			
			// Set URL parameters
			if tt.id != "" {
				req = mux.SetURLVars(req, map[string]string{"id": tt.id})
			}

			// Create response recorder
			w := httptest.NewRecorder()

			// Call handler
			handler.GetMedicalReportByIDHandler(w, req)

			// Assert status code
			assert.Equal(t, tt.expectedStatus, w.Code)

			// If successful, check response body
			if tt.expectedStatus == http.StatusOK && tt.expectedReport != nil {
				var response models.MedicalReport
				err := json.NewDecoder(w.Body).Decode(&response)
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedReport.ID, response.ID)
				assert.Equal(t, tt.expectedReport.Report.Details, response.Report.Details)
			}
		})
	}
}

func TestCreateMedicalReportHandler(t *testing.T) {
	tests := []struct {
		name           string
		requestBody    *models.MedicalReport
		expectedStatus int
		mockError      error
	}{
		{
			name: "successful creation",
			requestBody: &models.MedicalReport{
				Report: models.MedicalReportDetails{
					Date:              "2025-05-20",
					Details:           "New medical report",
					CivilianID:        "test-civilian-id",
					ReportingEmsID:    "test-ems-id",
					Hospitalized:      false,
					Deceased:          false,
					ActiveCommunityID: "test-community-id",
					UserID:            "test-user-id",
					Name:              "John Doe",
					DateOfBirth:       "1990-01-01",
				},
			},
			expectedStatus: http.StatusCreated,
			mockError:      nil,
		},
		{
			name: "missing civilianID",
			requestBody: &models.MedicalReport{
				Report: models.MedicalReportDetails{
					Date:              "2025-05-20",
					Details:           "New medical report",
					ReportingEmsID:    "test-ems-id",
					Hospitalized:      false,
					Deceased:          false,
					ActiveCommunityID: "test-community-id",
					UserID:            "test-user-id",
					Name:              "John Doe",
					DateOfBirth:       "1990-01-01",
				},
			},
			expectedStatus: http.StatusBadRequest,
			mockError:      nil,
		},
		{
			name: "missing activeCommunityID",
			requestBody: &models.MedicalReport{
				Report: models.MedicalReportDetails{
					Date:           "2025-05-20",
					Details:        "New medical report",
					CivilianID:     "test-civilian-id",
					ReportingEmsID: "test-ems-id",
					Hospitalized:   false,
					Deceased:       false,
					UserID:         "test-user-id",
					Name:           "John Doe",
					DateOfBirth:    "1990-01-01",
				},
			},
			expectedStatus: http.StatusBadRequest,
			mockError:      nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock database
			mockDB := mocks.NewMedicalReportDatabase(t)
			
			// Set up mock expectations
			if tt.expectedStatus == http.StatusCreated {
				mockDB.On("CreateMedicalReport", mock.Anything, mock.AnythingOfType("*models.MedicalReport")).Return(tt.mockError)
			}

			// Create handler
			handler := MedicalReport{DB: mockDB}

			// Create request body
			body, _ := json.Marshal(tt.requestBody)
			req := httptest.NewRequest("POST", "/medical-reports", bytes.NewBuffer(body))
			req.Header.Set("Content-Type", "application/json")

			// Create response recorder
			w := httptest.NewRecorder()

			// Call handler
			handler.CreateMedicalReportHandler(w, req)

			// Assert status code
			assert.Equal(t, tt.expectedStatus, w.Code)

			// If successful, check response body
			if tt.expectedStatus == http.StatusCreated {
				var response models.MedicalReport
				err := json.NewDecoder(w.Body).Decode(&response)
				assert.NoError(t, err)
				// Note: ID will be empty in test because mock doesn't execute real logic
				// In real usage, the database would generate and return the ID
				assert.Equal(t, tt.requestBody.Report.Details, response.Report.Details)
			}
		})
	}
}

func TestUpdateMedicalReportHandler(t *testing.T) {
	tests := []struct {
		name           string
		id             string
		requestBody    *models.MedicalReport
		expectedStatus int
		mockError      error
	}{
		{
			name: "successful update",
			id:   "507f1f77bcf86cd799439011",
			requestBody: &models.MedicalReport{
				Report: models.MedicalReportDetails{
					Date:              "2025-05-20",
					Details:           "Updated medical report",
					CivilianID:        "test-civilian-id",
					ReportingEmsID:    "test-ems-id",
					Hospitalized:      true,
					Deceased:          false,
					ActiveCommunityID: "test-community-id",
					UserID:            "test-user-id",
					Name:              "John Doe",
					DateOfBirth:       "1990-01-01",
				},
			},
			expectedStatus: http.StatusOK,
			mockError:      nil,
		},
		{
			name: "missing id",
			id:   "",
			requestBody: &models.MedicalReport{
				Report: models.MedicalReportDetails{
					Date:              "2025-05-20",
					Details:           "Updated medical report",
					CivilianID:        "test-civilian-id",
					ReportingEmsID:    "test-ems-id",
					Hospitalized:      true,
					Deceased:          false,
					ActiveCommunityID: "test-community-id",
					UserID:            "test-user-id",
					Name:              "John Doe",
					DateOfBirth:       "1990-01-01",
				},
			},
			expectedStatus: http.StatusBadRequest,
			mockError:      nil,
		},
		{
			name: "missing civilianID",
			id:   "507f1f77bcf86cd799439011",
			requestBody: &models.MedicalReport{
				Report: models.MedicalReportDetails{
					Date:              "2025-05-20",
					Details:           "Updated medical report",
					ReportingEmsID:    "test-ems-id",
					Hospitalized:      true,
					Deceased:          false,
					ActiveCommunityID: "test-community-id",
					UserID:            "test-user-id",
					Name:              "John Doe",
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
			mockDB := mocks.NewMedicalReportDatabase(t)
			
			// Set up mock expectations
			if tt.expectedStatus == http.StatusOK {
				mockDB.On("UpdateMedicalReport", mock.Anything, tt.id, mock.AnythingOfType("*models.MedicalReport")).Return(tt.mockError)
			}

			// Create handler
			handler := MedicalReport{DB: mockDB}

			// Create request body
			body, _ := json.Marshal(tt.requestBody)
			req := httptest.NewRequest("PUT", "/medical-reports/"+tt.id, bytes.NewBuffer(body))
			req.Header.Set("Content-Type", "application/json")
			
			// Set URL parameters
			if tt.id != "" {
				req = mux.SetURLVars(req, map[string]string{"id": tt.id})
			}

			// Create response recorder
			w := httptest.NewRecorder()

			// Call handler
			handler.UpdateMedicalReportHandler(w, req)

			// Assert status code
			assert.Equal(t, tt.expectedStatus, w.Code)

			// If successful, check response body
			if tt.expectedStatus == http.StatusOK {
				var response map[string]string
				err := json.NewDecoder(w.Body).Decode(&response)
				assert.NoError(t, err)
				assert.Equal(t, "Medical report updated successfully", response["message"])
			}
		})
	}
}

func TestDeleteMedicalReportHandler(t *testing.T) {
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
			mockDB := mocks.NewMedicalReportDatabase(t)
			
			// Set up mock expectations
			if tt.id != "" {
				mockDB.On("DeleteMedicalReport", mock.Anything, tt.id).Return(tt.mockError)
			}

			// Create handler
			handler := MedicalReport{DB: mockDB}

			// Create request
			req := httptest.NewRequest("DELETE", "/medical-reports/"+tt.id, nil)
			
			// Set URL parameters
			if tt.id != "" {
				req = mux.SetURLVars(req, map[string]string{"id": tt.id})
			}

			// Create response recorder
			w := httptest.NewRecorder()

			// Call handler
			handler.DeleteMedicalReportHandler(w, req)

			// Assert status code
			assert.Equal(t, tt.expectedStatus, w.Code)

			// If successful, check response body
			if tt.expectedStatus == http.StatusOK {
				var response map[string]string
				err := json.NewDecoder(w.Body).Decode(&response)
				assert.NoError(t, err)
				assert.Equal(t, "Medical report deleted successfully", response["message"])
			}
		})
	}
}
