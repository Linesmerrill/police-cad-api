package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/mongo"
)

var a App

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

var MockDB *mongo.Database

func TestUnknownRoute(t *testing.T) {
	a.Router = a.New()
	req, _ := http.NewRequest("GET", "/asdf", nil)
	response := executeRequest(req)

	checkResponseCode(t, http.StatusNotFound, response.Code)

}

func TestHealthCheckRoute(t *testing.T) {
	a.Router = a.New()
	req, _ := http.NewRequest("GET", "/health", nil)
	response := executeRequest(req)

	checkResponseCode(t, http.StatusOK, response.Code)

	if !strings.Contains(response.Body.String(), "alive") {
		t.Errorf("Expected 'alive' in the reponse. Got '%s'", response.Body.String())
	}
}

func TestApp_CommunityHandlerInvalidRoute(t *testing.T) {
	a.Router = a.New()
	req, _ := http.NewRequest("GET", "api/v1/community", nil)
	response := executeRequest(req)

	checkResponseCode(t, http.StatusMovedPermanently, response.Code)
}

func TestApp_CommunityHandlerUnauthorized(t *testing.T) {
	a.Router = a.New()
	req, _ := http.NewRequest("GET", "/api/v1/community/1234", nil)
	response := executeRequest(req)

	checkResponseCode(t, http.StatusUnauthorized, response.Code)
}

func TestApp_CommunityHandlerInvalidToken(t *testing.T) {
	a.Router = a.New()
	req, _ := http.NewRequest("GET", "/api/v1/community/1234", nil)
	req.Header.Add("Authorization", "Bearer asdfasdf")
	response := executeRequest(req)

	checkResponseCode(t, http.StatusInternalServerError, response.Code)

	var m map[string]string
	json.Unmarshal(response.Body.Bytes(), &m)
	if m["error"] != "failed to parse token, token contains an invalid number of segments" {
		t.Errorf("Expected the 'error' key of the reponse to be set to 'failed to parse token, token contains an invalid number of segments'. Got '%s'", m["error"])
	}
}

//func TestApp_CommunityHandlerValidToken(t *testing.T) {
//	os.Setenv("SECRET_KEY", "test")
//	a.DB = MockDB
//	a.Router = a.New()
//	req, _ := http.NewRequest("GET", "/api/v1/community/1234", nil)
//	req.Header.Add("Authorization", "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.5mhBHqs5_DTLdINd9p5m7ZJ6XD0Xc55kIaCRY5r6HRA")
//	response := executeRequest(req)
//
//	checkResponseCode(t, http.StatusOK, response.Code)
//
//	assert.Contains(t, response.Body.String(), "this stuff")
//}
