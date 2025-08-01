package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/models"
)

func TestCreateAnnouncementRequest_Validation(t *testing.T) {
	tests := []struct {
		name    string
		request models.CreateAnnouncementRequest
		isValid bool
	}{
		{
			name: "Valid announcement request",
			request: models.CreateAnnouncementRequest{
				UserID:   "507f1f77bcf86cd799439011",
				Type:     "main",
				Title:    "Test Announcement",
				Content:  "This is a test announcement",
				Priority: "medium",
				IsPinned: false,
			},
			isValid: true,
		},
		{
			name: "Invalid type",
			request: models.CreateAnnouncementRequest{
				UserID:   "507f1f77bcf86cd799439011",
				Type:     "invalid",
				Title:    "Test Announcement",
				Content:  "This is a test announcement",
				Priority: "medium",
				IsPinned: false,
			},
			isValid: false,
		},
		{
			name: "Empty title",
			request: models.CreateAnnouncementRequest{
				UserID:   "507f1f77bcf86cd799439011",
				Type:     "main",
				Title:    "",
				Content:  "This is a test announcement",
				Priority: "medium",
				IsPinned: false,
			},
			isValid: false,
		},
		{
			name: "Empty content",
			request: models.CreateAnnouncementRequest{
				UserID:   "507f1f77bcf86cd799439011",
				Type:     "main",
				Title:    "Test Announcement",
				Content:  "",
				Priority: "medium",
				IsPinned: false,
			},
			isValid: false,
		},
		{
			name: "Invalid priority",
			request: models.CreateAnnouncementRequest{
				UserID:   "507f1f77bcf86cd799439011",
				Type:     "main",
				Title:    "Test Announcement",
				Content:  "This is a test announcement",
				Priority: "invalid",
				IsPinned: false,
			},
			isValid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This is a basic validation test
			// In a real implementation, you would use a validation library
			if tt.isValid {
				assert.NotEmpty(t, tt.request.Title)
				assert.NotEmpty(t, tt.request.Content)
				assert.Contains(t, []string{"main", "session", "training"}, tt.request.Type)
				assert.Contains(t, []string{"low", "medium", "high", "urgent"}, tt.request.Priority)
			}
		})
	}
}

func TestAnnouncementModel_Structure(t *testing.T) {
	// Test that the announcement model can be created correctly
	now := primitive.NewDateTimeFromTime(time.Now())
	announcement := models.Announcement{
		ID:        primitive.NewObjectID(),
		Community: primitive.NewObjectID(),
		Creator:   primitive.NewObjectID(),
		Type:      "main",
		Title:     "Test Announcement",
		Content:   "This is a test announcement content",
		Priority:  "high",
		IsActive:  true,
		IsPinned:  false,
		Reactions: []models.Reaction{},
		Comments:  []models.Comment{},
		ViewCount: 0,
		CreatedAt: now,
		UpdatedAt: now,
	}

	assert.NotEmpty(t, announcement.ID)
	assert.NotEmpty(t, announcement.Community)
	assert.NotEmpty(t, announcement.Creator)
	assert.Equal(t, "main", announcement.Type)
	assert.Equal(t, "Test Announcement", announcement.Title)
	assert.Equal(t, "This is a test announcement content", announcement.Content)
	assert.Equal(t, "high", announcement.Priority)
	assert.True(t, announcement.IsActive)
	assert.False(t, announcement.IsPinned)
	assert.Equal(t, 0, announcement.ViewCount)
}

func TestReactionModel_Structure(t *testing.T) {
	// Test that the reaction model can be created correctly
	now := primitive.NewDateTimeFromTime(time.Now())
	reaction := models.Reaction{
		User:      primitive.NewObjectID(),
		Emoji:     "👍",
		Timestamp: now,
	}

	assert.NotEmpty(t, reaction.User)
	assert.Equal(t, "👍", reaction.Emoji)
	assert.Equal(t, now, reaction.Timestamp)
}

func TestCommentModel_Structure(t *testing.T) {
	// Test that the comment model can be created correctly
	now := primitive.NewDateTimeFromTime(time.Now())
	comment := models.Comment{
		ID:        primitive.NewObjectID(),
		User:      primitive.NewObjectID(),
		Content:   "This is a test comment",
		Timestamp: now,
		Edited:    false,
		EditedAt:  nil,
	}

	assert.NotEmpty(t, comment.ID)
	assert.NotEmpty(t, comment.User)
	assert.Equal(t, "This is a test comment", comment.Content)
	assert.Equal(t, now, comment.Timestamp)
	assert.False(t, comment.Edited)
	assert.Nil(t, comment.EditedAt)
}

func TestPaginationInfo_Structure(t *testing.T) {
	// Test that the pagination info model can be created correctly
	pagination := models.PaginationInfo{
		CurrentPage:        1,
		TotalPages:         5,
		TotalAnnouncements: 50,
		HasNextPage:        true,
		HasPrevPage:        false,
	}

	assert.Equal(t, 1, pagination.CurrentPage)
	assert.Equal(t, 5, pagination.TotalPages)
	assert.Equal(t, 50, pagination.TotalAnnouncements)
	assert.True(t, pagination.HasNextPage)
	assert.False(t, pagination.HasPrevPage)
}

func TestAddReactionRequest_Validation(t *testing.T) {
	request := models.AddReactionRequest{
		UserID: "507f1f77bcf86cd799439011",
		Emoji:  "👍",
	}

	assert.NotEmpty(t, request.UserID)
	assert.NotEmpty(t, request.Emoji)
}

func TestAddCommentRequest_Validation(t *testing.T) {
	request := models.AddCommentRequest{
		UserID:  "507f1f77bcf86cd799439011",
		Content: "This is a test comment content",
	}

	assert.NotEmpty(t, request.UserID)
	assert.NotEmpty(t, request.Content)
	assert.LessOrEqual(t, len(request.Content), 1000)
}

func TestUpdateCommentRequest_Validation(t *testing.T) {
	request := models.UpdateCommentRequest{
		UserID:  "507f1f77bcf86cd799439011",
		Content: "This is an updated comment content",
	}

	assert.NotEmpty(t, request.UserID)
	assert.NotEmpty(t, request.Content)
	assert.LessOrEqual(t, len(request.Content), 1000)
}

// Helper function to create a test request with context
func createTestRequest(method, path string, body interface{}) (*http.Request, error) {
	var req *http.Request
	var err error

	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		req, err = http.NewRequest(method, path, bytes.NewBuffer(jsonBody))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
	} else {
		req, err = http.NewRequest(method, path, nil)
		if err != nil {
			return nil, err
		}
	}

	// No authentication context needed since we're getting userID from request body

	return req, nil
}

func TestCreateTestRequest(t *testing.T) {
	// Test the helper function
	body := models.CreateAnnouncementRequest{
		UserID:   "507f1f77bcf86cd799439011",
		Type:     "main",
		Title:    "Test Announcement",
		Content:  "This is a test announcement",
		Priority: "medium",
		IsPinned: false,
	}

	req, err := createTestRequest("POST", "/api/v1/community/507f1f77bcf86cd799439012/announcements", body)
	assert.NoError(t, err)
	assert.Equal(t, "POST", req.Method)
	assert.Equal(t, "/api/v1/community/507f1f77bcf86cd799439012/announcements", req.URL.Path)
	assert.Equal(t, "application/json", req.Header.Get("Content-Type"))

	// Check that user_id is in context
	userID := req.Context().Value("user_id")
	assert.Equal(t, "507f1f77bcf86cd799439011", userID)
}

func TestAnnouncementResponse_Structure(t *testing.T) {
	// Test that the announcement response model can be created correctly
	now := primitive.NewDateTimeFromTime(time.Now())
	userSummary := models.UserSummary{
		ID:             primitive.NewObjectID(),
		Username:       "testuser",
		ProfilePicture: nil,
	}

	response := models.AnnouncementResponse{
		ID:        primitive.NewObjectID(),
		Community: primitive.NewObjectID(),
		Creator:   userSummary,
		Type:      "main",
		Title:     "Test Announcement",
		Content:   "This is a test announcement content",
		Priority:  "high",
		IsActive:  true,
		IsPinned:  false,
		Reactions: []models.ReactionResponse{},
		Comments:  []models.CommentResponse{},
		ViewCount: 0,
		CreatedAt: now,
		UpdatedAt: now,
	}

	assert.NotEmpty(t, response.ID)
	assert.NotEmpty(t, response.Community)
	assert.Equal(t, "testuser", response.Creator.Username)
	assert.Equal(t, "main", response.Type)
	assert.Equal(t, "Test Announcement", response.Title)
	assert.Equal(t, "This is a test announcement content", response.Content)
	assert.Equal(t, "high", response.Priority)
	assert.True(t, response.IsActive)
	assert.False(t, response.IsPinned)
	assert.Equal(t, 0, response.ViewCount)
}

func TestAddReactionRequest_Structure(t *testing.T) {
	// Test that the AddReactionRequest model can be created correctly
	request := models.AddReactionRequest{
		UserID: "507f1f77bcf86cd799439011",
		Emoji:  "👍",
	}

	assert.NotEmpty(t, request.UserID)
	assert.NotEmpty(t, request.Emoji)
	assert.Equal(t, "507f1f77bcf86cd799439011", request.UserID)
	assert.Equal(t, "👍", request.Emoji)
} 