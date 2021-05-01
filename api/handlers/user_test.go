package handlers_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/linesmerrill/police-cad-api/api/handlers"
	"github.com/linesmerrill/police-cad-api/models"
)

var a handlers.App

type userDBMock struct {
	isError  bool
	objectID string
}

func executeRequest(req *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	a.Router.ServeHTTP(rr, req)
	return rr
}

func checkResponseCode(t *testing.T, expected, actual int) {
	if expected != actual {
		t.Errorf("Expected response code %d. Got %d\n", expected, actual)
	}
}

func (u userDBMock) FindOne(context.Context, interface{}) (*models.User, error) {
	if u.isError {
		return nil, errors.New("some error")
	}

	userInner := models.UserInner{
		Email:    "test@test.com",
		Username: "testUser",
	}
	return &models.User{ID: u.objectID, UserInner: userInner}, nil
}

func TestApp_UserHandlerInvalidToken(t *testing.T) {
	a.Router = a.New()
	req, _ := http.NewRequest("GET", "/api/v1/user/1234", nil)
	req.Header.Add("Authorization", "Bearer asdfasdf")
	response := executeRequest(req)

	checkResponseCode(t, http.StatusInternalServerError, response.Code)

	var m map[string]string
	json.Unmarshal(response.Body.Bytes(), &m)
	if m["error"] != "failed to parse token, token contains an invalid number of segments" {
		t.Errorf("Expected the 'error' key of the reponse to be set to 'failed to parse token, token contains an invalid number of segments'. Got '%s'", m["error"])
	}
}
