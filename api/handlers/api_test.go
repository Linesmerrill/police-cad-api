package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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

//func TestApp_CommunityHandler(t *testing.T) {
//	a.Router = a.New()
//	req, _ := http.NewRequest("GET", "/health", nil)
//	response := executeRequest(req)
//
//	checkResponseCode(t, http.StatusNotFound, response.Code)
//
//	var m map[string]string
//	json.Unmarshal(response.Body.Bytes(), &m)
//	if m["error"] != "Product not found" {
//		t.Errorf("Expected the 'error' key of the reponse to be set to 'Product not found'. Got '%s'", m["error"])
//	}
//}
