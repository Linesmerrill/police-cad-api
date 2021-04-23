package api

import (
	"io"
	"net/http"

	"github.com/gorilla/mux"

	"github.com/linesmerrill/police-cad-api/api/handlers"
	"github.com/linesmerrill/police-cad-api/config"
)

type api struct {
	logging config.Processor
}

// New creates a new mux router and all the routes
func New(p config.Processor) *mux.Router {
	myAPI := api{logging: p}
	r := mux.NewRouter()
	//healthchex
	r.HandleFunc("/health", healthCheckHandler)

	api := r.PathPrefix("/api/v1").Subrouter()
	api.Handle("/communities", myAPI.middleware(http.HandlerFunc(handlers.CommunitiesHandler))).Methods("GET")

	return r
}

func healthCheckHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, `{"alive": true}`)
}
