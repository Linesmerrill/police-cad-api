package handlers

import (
	"io"
	"net/http"

	"github.com/linesmerrill/police-cad-api/config"
	collections "github.com/linesmerrill/police-cad-api/mongodb"

	"go.uber.org/zap"

	"github.com/gorilla/mux"

	"github.com/linesmerrill/police-cad-api/api"
)

// App stores the router and db connection so it can be reused
type App struct {
	Router   *mux.Router
	DB       collections.CollectionHelper
	Config   config.Config
	dbHelper collections.DatabaseHelper
}

// New creates a new mux router and all the routes
func (a *App) New() *mux.Router {
	r := mux.NewRouter()
	//healthchex
	r.HandleFunc("/health", healthCheckHandler)

	apiCreate := r.PathPrefix("/api/v1").Subrouter()

	apiCreate.Handle("/community/{community_id}", api.Middleware(http.HandlerFunc(a.CommunityHandler))).Methods("GET")
	apiCreate.Handle("/community/{community_id}/{owner_id}", api.Middleware(http.HandlerFunc(a.CommunityByOwnerHandler))).Methods("GET")

	return r
}

// Initialize is invoked by main to connect with the database and create a router
func (a *App) Initialize() {

	client, err := collections.NewClient(&a.Config)
	if err != nil {
		//if we fail to create a new database client, then kill the pod
		zap.S().With(err).Fatal("failed to create new client")
	}

	a.dbHelper = collections.NewDatabase(&a.Config, client)
	err = client.Connect()
	if err != nil {
		//if we fail to connect to the database, then kill the pod
		zap.S().With(err).Fatal("failed to connect to database")
	}
	zap.S().Info("police-cad-api has connected to the database")

	//initialize api router
	a.initializeRoutes()

}

func (a *App) initializeRoutes() {
	a.Router = a.New()
}

func healthCheckHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, `{"alive": true}`)
}
