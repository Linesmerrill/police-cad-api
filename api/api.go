package api

import (
	"io"
	"net/http"

	"github.com/gorilla/mux"
)

// New creates a new mux router and all the routes
func New() *mux.Router {
	r := mux.NewRouter()
	r.HandleFunc("/health", healthCheckHandler)

	return r
}

func healthCheckHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, `{"alive": true}`)
}
