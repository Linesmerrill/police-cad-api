package handlers

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/linesmerrill/police-cad-api/models"

	"github.com/gorilla/mux"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
)

// App stores the router and db connection, so it can be reused
type App struct {
	Router   *mux.Router
	DB       databases.CollectionHelper
	Config   config.Config
	dbHelper databases.DatabaseHelper
}

// New creates a new mux router and all the routes
func (a *App) New() *mux.Router {
	r := mux.NewRouter()

	u := User{DB: databases.NewUserDatabase(a.dbHelper)}
	c := Community{DB: databases.NewCommunityDatabase(a.dbHelper)}
	civ := Civilian{DB: databases.NewCivilianDatabase(a.dbHelper)}
	v := Vehicle{DB: databases.NewVehicleDatabase(a.dbHelper)}
	f := Firearm{DB: databases.NewFirearmDatabase(a.dbHelper)}
	l := License{DB: databases.NewLicenseDatabase(a.dbHelper)}
	e := Ems{DB: databases.NewEmsDatabase(a.dbHelper)}
	ev := EmsVehicle{DB: databases.NewEmsVehicleDatabase(a.dbHelper)}
	w := Warrant{DB: databases.NewWarrantDatabase(a.dbHelper)}
	call := Call{DB: databases.NewCallDatabase(a.dbHelper)}

	// healthchex
	r.HandleFunc("/health", healthCheckHandler)

	apiCreate := r.PathPrefix("/api/v1").Subrouter()

	apiCreate.Handle("/community/{community_id}", api.Middleware(http.HandlerFunc(c.CommunityHandler))).Methods("GET")
	apiCreate.Handle("/community/{community_id}/{owner_id}", api.Middleware(http.HandlerFunc(c.CommunityByCommunityAndOwnerIDHandler))).Methods("GET")
	apiCreate.Handle("/communities/{owner_id}", api.Middleware(http.HandlerFunc(c.CommunitiesByOwnerIDHandler))).Methods("GET")
	apiCreate.Handle("/user/{user_id}", api.Middleware(http.HandlerFunc(u.UserHandler))).Methods("GET")
	apiCreate.Handle("/users/{active_community_id}", api.Middleware(http.HandlerFunc(u.UsersFindAllHandler))).Methods("GET")
	apiCreate.Handle("/civilian/{civilian_id}", api.Middleware(http.HandlerFunc(civ.CivilianByIDHandler))).Methods("GET")
	apiCreate.Handle("/civilians", api.Middleware(http.HandlerFunc(civ.CivilianHandler))).Methods("GET")
	apiCreate.Handle("/civilians/user/{user_id}", api.Middleware(http.HandlerFunc(civ.CiviliansByUserIDHandler))).Methods("GET")
	apiCreate.Handle("/civilians/search", api.Middleware(http.HandlerFunc(civ.CiviliansByNameSearchHandler))).Methods("GET")
	apiCreate.Handle("/vehicle/{vehicle_id}", api.Middleware(http.HandlerFunc(v.VehicleByIDHandler))).Methods("GET")
	apiCreate.Handle("/vehicles", api.Middleware(http.HandlerFunc(v.VehicleHandler))).Methods("GET")
	apiCreate.Handle("/vehicles/user/{user_id}", api.Middleware(http.HandlerFunc(v.VehiclesByUserIDHandler))).Methods("GET")
	apiCreate.Handle("/vehicles/registered-owner/{registered_owner_id}", api.Middleware(http.HandlerFunc(v.VehiclesByRegisteredOwnerIDHandler))).Methods("GET")
	apiCreate.Handle("/vehicles/search", api.Middleware(http.HandlerFunc(v.VehiclesByPlateSearchHandler))).Methods("GET")
	apiCreate.Handle("/firearm/{firearm_id}", api.Middleware(http.HandlerFunc(f.FirearmByIDHandler))).Methods("GET")
	apiCreate.Handle("/firearms", api.Middleware(http.HandlerFunc(f.FirearmHandler))).Methods("GET")
	apiCreate.Handle("/firearms/user/{user_id}", api.Middleware(http.HandlerFunc(f.FirearmsByUserIDHandler))).Methods("GET")
	apiCreate.Handle("/firearms/registered-owner/{registered_owner_id}", api.Middleware(http.HandlerFunc(f.FirearmsByRegisteredOwnerIDHandler))).Methods("GET")
	apiCreate.Handle("/license/{license_id}", api.Middleware(http.HandlerFunc(l.LicenseByIDHandler))).Methods("GET")
	apiCreate.Handle("/licenses", api.Middleware(http.HandlerFunc(l.LicenseHandler))).Methods("GET")
	apiCreate.Handle("/licenses/user/{user_id}", api.Middleware(http.HandlerFunc(l.LicensesByUserIDHandler))).Methods("GET")
	apiCreate.Handle("/licenses/owner/{owner_id}", api.Middleware(http.HandlerFunc(l.LicensesByOwnerIDHandler))).Methods("GET")

	apiCreate.Handle("/warrant/{warrant_id}", api.Middleware(http.HandlerFunc(w.WarrantByIDHandler))).Methods("GET")
	apiCreate.Handle("/warrants", api.Middleware(http.HandlerFunc(w.WarrantHandler))).Methods("GET")
	apiCreate.Handle("/warrants/user/{user_id}", api.Middleware(http.HandlerFunc(w.WarrantsByUserIDHandler))).Methods("GET")

	apiCreate.Handle("/ems/{ems_id}", api.Middleware(http.HandlerFunc(e.EmsByIDHandler))).Methods("GET")
	apiCreate.Handle("/ems", api.Middleware(http.HandlerFunc(e.EmsHandler))).Methods("GET")
	apiCreate.Handle("/ems/user/{user_id}", api.Middleware(http.HandlerFunc(e.EmsByUserIDHandler))).Methods("GET")
	apiCreate.Handle("/emsVehicle/{ems_vehicle_id}", api.Middleware(http.HandlerFunc(ev.EmsVehicleByIDHandler))).Methods("GET")
	apiCreate.Handle("/emsVehicles", api.Middleware(http.HandlerFunc(ev.EmsVehicleHandler))).Methods("GET")
	apiCreate.Handle("/emsVehicles/user/{user_id}", api.Middleware(http.HandlerFunc(ev.EmsVehiclesByUserIDHandler))).Methods("GET")
	apiCreate.Handle("/call/{call_id}", api.Middleware(http.HandlerFunc(call.CallByIDHandler))).Methods("GET")
	apiCreate.Handle("/calls", api.Middleware(http.HandlerFunc(call.CallHandler))).Methods("GET")
	apiCreate.Handle("/calls/community/{community_id}", api.Middleware(http.HandlerFunc(call.CallsByCommunityIDHandler))).Methods("GET")

	// swagger docs hosted at "/"
	r.PathPrefix("/").Handler(http.StripPrefix("/", http.FileServer(http.Dir("./docs/"))))
	return r
}

// Initialize is invoked by main to connect with the database and create a router
func (a *App) Initialize() error {

	client, err := databases.NewClient(&a.Config)
	if err != nil {
		// if we fail to create a new database client, then kill the pod
		zap.S().With(err).Error("failed to create new client")
		return err
	}

	a.dbHelper = databases.NewDatabase(&a.Config, client)
	err = client.Connect()
	if err != nil {
		// if we fail to connect to the database, then kill the pod
		zap.S().With(err).Error("failed to connect to database")
		return err
	}
	zap.S().Info("police-cad-api has connected to the database")

	// initialize api router
	a.initializeRoutes()
	return nil

}

func (a *App) initializeRoutes() {
	a.Router = a.New()
}

func healthCheckHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	b, _ := json.Marshal(models.HealthCheckResponse{
		Alive: true,
	})
	_, _ = io.WriteString(w, string(b))
}
