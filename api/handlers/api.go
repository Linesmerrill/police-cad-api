package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/linesmerrill/police-cad-api/models"
	"github.com/stripe/stripe-go/v76"

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
	// setup go-guardian for middleware
	m := api.MiddlewareDB{DB: databases.NewUserDatabase(a.dbHelper)}
	m.SetupGoGuardian()

	r := mux.NewRouter()

	u := User{DB: databases.NewUserDatabase(a.dbHelper), CDB: databases.NewCommunityDatabase(a.dbHelper)}
	c := Community{DB: databases.NewCommunityDatabase(a.dbHelper), UDB: databases.NewUserDatabase(a.dbHelper)}
	civ := Civilian{DB: databases.NewCivilianDatabase(a.dbHelper)}
	v := Vehicle{DB: databases.NewVehicleDatabase(a.dbHelper)}
	f := Firearm{DB: databases.NewFirearmDatabase(a.dbHelper)}
	l := License{DB: databases.NewLicenseDatabase(a.dbHelper)}
	e := Ems{DB: databases.NewEmsDatabase(a.dbHelper)}
	ev := EmsVehicle{DB: databases.NewEmsVehicleDatabase(a.dbHelper)}
	w := Warrant{DB: databases.NewWarrantDatabase(a.dbHelper)}
	call := Call{DB: databases.NewCallDatabase(a.dbHelper)}
	bolo := Bolo{DB: databases.NewBoloDatabase(a.dbHelper)}
	s := Spotlight{DB: databases.NewSpotlightDatabase(a.dbHelper)}
	search := Search{UserDB: databases.NewUserDatabase(a.dbHelper), CommDB: databases.NewCommunityDatabase(a.dbHelper)}
	report := Report{RDB: databases.NewReportDatabase(a.dbHelper)}
	cloudinaryHandler := CloudinaryHandler{}

	// healthchex
	r.HandleFunc("/health", healthCheckHandler)

	apiCreate := r.PathPrefix("/api/v1").Subrouter()

	apiCreate.Handle("/auth/token", api.Middleware(http.HandlerFunc(m.CreateToken))).Methods("POST")
	apiCreate.Handle("/auth/logout", api.Middleware(http.HandlerFunc(api.RevokeToken))).Methods("DELETE")

	apiCreate.Handle("/community", api.Middleware(http.HandlerFunc(c.CreateCommunityHandler))).Methods("POST")
	apiCreate.Handle("/community/{community_id}", api.Middleware(http.HandlerFunc(c.CommunityHandler))).Methods("GET")
	apiCreate.Handle("/community/{community_id}", api.Middleware(http.HandlerFunc(c.UpdateCommunityFieldHandler))).Methods("PATCH")
	apiCreate.Handle("/community/{community_id}", api.Middleware(http.HandlerFunc(c.DeleteCommunityByIDHandler))).Methods("DELETE")
	apiCreate.Handle("/community/{communityId}/members", api.Middleware(http.HandlerFunc(c.CommunityMembersHandler))).Methods("GET")
	apiCreate.Handle("/community/{communityId}/roles", api.Middleware(http.HandlerFunc(c.GetRolesByCommunityIDHandler))).Methods("GET")
	apiCreate.Handle("/community/{communityId}/roles", api.Middleware(http.HandlerFunc(c.AddRoleToCommunityHandler))).Methods("POST")
	apiCreate.Handle("/community/{communityId}/fines", api.Middleware(http.HandlerFunc(c.SetCommunityFinesHandler))).Methods("PUT")
	apiCreate.Handle("/community/{communityId}/roles/{roleId}/members", api.Middleware(http.HandlerFunc(c.FetchCommunityMembersByRoleIDHandler))).Methods("GET")
	apiCreate.Handle("/community/{communityId}/roles/{roleId}/members", api.Middleware(http.HandlerFunc(c.UpdateRoleMembersHandler))).Methods("PUT")
	apiCreate.Handle("/community/{communityId}/roles/{roleId}/name", api.Middleware(http.HandlerFunc(c.UpdateRoleNameHandler))).Methods("PUT")
	apiCreate.Handle("/community/{communityId}/roles/{roleId}/permissions", api.Middleware(http.HandlerFunc(c.UpdateRolePermissionsHandler))).Methods("PUT")
	apiCreate.Handle("/community/{communityId}/roles/{roleId}/members/{memberId}", api.Middleware(http.HandlerFunc(c.DeleteRoleMemberHandler))).Methods("DELETE")
	apiCreate.Handle("/community/{communityId}/roles/{roleId}", api.Middleware(http.HandlerFunc(c.DeleteRoleByIDHandler))).Methods("DELETE")
	apiCreate.Handle("/community/{communityId}/banned-users", api.Middleware(http.HandlerFunc(c.GetBannedUsersHandler))).Methods("GET")
	apiCreate.Handle("/community/{communityId}/add-invite-code", api.Middleware(http.HandlerFunc(c.AddInviteCodeHandler))).Methods("POST")
	apiCreate.Handle("/community/{communityId}/departments", api.Middleware(http.HandlerFunc(c.FetchAllCommunityDepartmentsHandler))).Methods("GET")
	apiCreate.Handle("/community/{communityId}/departments", api.Middleware(http.HandlerFunc(c.CreateCommunityDepartmentHandler))).Methods("POST")
	apiCreate.Handle("/community/{communityId}/departments/{departmentId}", api.Middleware(http.HandlerFunc(c.FetchDepartmentByIDHandler))).Methods("GET")
	apiCreate.Handle("/community/{communityId}/departments/{departmentId}", api.Middleware(http.HandlerFunc(c.DeleteCommunityDepartmentByIDHandler))).Methods("DELETE")
	apiCreate.Handle("/community/{communityId}/departments/{departmentId}", api.Middleware(http.HandlerFunc(c.UpdateDepartmentDetailsHandler))).Methods("PATCH")
	apiCreate.Handle("/community/{communityId}/user/{userId}/departments", api.Middleware(http.HandlerFunc(c.FetchUserDepartmentsHandler))).Methods("GET")
	apiCreate.Handle("/community/{communityId}/departments/{departmentId}/members", api.Middleware(http.HandlerFunc(c.UpdateDepartmentMembersHandler))).Methods("POST")
	apiCreate.Handle("/community/{communityId}/departments/{departmentId}/remove-user", api.Middleware(http.HandlerFunc(c.RemoveUserFromDepartmentHandler))).Methods("POST")
	apiCreate.Handle("/community/{communityId}/departments/{departmentId}/update-image", api.Middleware(http.HandlerFunc(c.UpdateDepartmentImageLinkHandler))).Methods("PATCH")
	apiCreate.Handle("/community/{communityId}/departments/{departmentId}/join-requests", api.Middleware(http.HandlerFunc(c.UpdateDepartmentJoinRequestHandler))).Methods("PUT")
	apiCreate.Handle("/community/{communityId}/tenCodes/{codeId}", api.Middleware(http.HandlerFunc(c.UpdateTenCodeHandler))).Methods("PUT")
	apiCreate.Handle("/community/{communityId}/tenCodes/{codeId}", api.Middleware(http.HandlerFunc(c.DeleteTenCodeHandler))).Methods("DELETE")
	apiCreate.Handle("/community/{communityId}/tenCodes", api.Middleware(http.HandlerFunc(c.AddTenCodeHandler))).Methods("POST")
	apiCreate.Handle("/community/{communityId}/members/{userId}/tenCode", api.Middleware(http.HandlerFunc(c.SetMemberTenCodeHandler))).Methods("PUT")
	apiCreate.Handle("/community/{communityId}/events", api.Middleware(http.HandlerFunc(c.GetEventsByCommunityIDHandler))).Methods("GET")
	apiCreate.Handle("/community/{communityId}/events", api.Middleware(http.HandlerFunc(c.AddEventToCommunityHandler))).Methods("POST")
	apiCreate.Handle("/community/{communityId}/events/{eventId}", api.Middleware(http.HandlerFunc(c.GetEventByIDHandler))).Methods("GET")
	apiCreate.Handle("/community/{communityId}/events/{eventId}", api.Middleware(http.HandlerFunc(c.UpdateEventByIDHandler))).Methods("PUT")
	apiCreate.Handle("/community/{communityId}/events/{eventId}", api.Middleware(http.HandlerFunc(c.DeleteEventByIDHandler))).Methods("DELETE")
	apiCreate.Handle("/community/{community_id}/{owner_id}", api.Middleware(http.HandlerFunc(c.CommunityByCommunityAndOwnerIDHandler))).Methods("GET")
	apiCreate.Handle("/communities/{owner_id}", api.Middleware(http.HandlerFunc(c.CommunitiesByOwnerIDHandler))).Methods("GET")

	apiCreate.Handle("/user/create-user", http.HandlerFunc(u.UserCreateHandler)).Methods("POST")
	apiCreate.Handle("/user/check-user", http.HandlerFunc(u.UserCheckEmailHandler)).Methods("POST")
	apiCreate.Handle("/user/online-status", api.Middleware(http.HandlerFunc(u.SetOnlineStatusHandler))).Methods("PUT")
	apiCreate.Handle("/user/block", api.Middleware(http.HandlerFunc(u.BlockUserHandler))).Methods("POST")
	apiCreate.Handle("/user/unblock", api.Middleware(http.HandlerFunc(u.UnblockUserHandler))).Methods("POST")
	apiCreate.Handle("/user/unfriend", api.Middleware(http.HandlerFunc(u.UnfriendUserHandler))).Methods("POST")
	apiCreate.Handle("/user/last-accessed-community", api.Middleware(http.HandlerFunc(u.UpdateLastAccessedCommunityHandler))).Methods("PUT")
	apiCreate.Handle("/user/subscribe", api.Middleware(http.HandlerFunc(u.SubscribeUserHandler))).Methods("POST")
	apiCreate.Handle("/user/create-checkout-session", api.Middleware(http.HandlerFunc(u.CreateCheckoutSessionHandler))).Methods("POST")
	apiCreate.Handle("/user/verify-subscription", api.Middleware(http.HandlerFunc(u.VerifySubscriptionHandler))).Methods("POST")
	apiCreate.Handle("/user/unsubscribe", api.Middleware(http.HandlerFunc(u.UnsubscribeUserHandler))).Methods("POST")
	apiCreate.Handle("/user/{userId}/add-friend", api.Middleware(http.HandlerFunc(u.AddFriendHandler))).Methods("POST")
	apiCreate.Handle("/user/{user_id}/update-status", api.Middleware(http.HandlerFunc(u.UpdateFriendStatusHandler))).Methods("PUT")
	apiCreate.Handle("/user/{userId}/communities", api.Middleware(http.HandlerFunc(u.GetUserCommunitiesHandler))).Methods("GET")
	apiCreate.Handle("/user/{userId}/communities", api.Middleware(http.HandlerFunc(u.AddCommunityToUserHandler))).Methods("PUT")
	apiCreate.Handle("/user/{userId}/random-communities", api.Middleware(http.HandlerFunc(u.GetRandomCommunitiesHandler))).Methods("GET")
	apiCreate.Handle("/user/{userId}/remove-community", api.Middleware(http.HandlerFunc(u.RemoveCommunityFromUserHandler))).Methods("DELETE")
	apiCreate.Handle("/user/{userId}/ban-community", api.Middleware(http.HandlerFunc(u.BanUserFromCommunityHandler))).Methods("POST")
	apiCreate.Handle("/user/{userId}/unban-community", api.Middleware(http.HandlerFunc(u.UnbanUserFromCommunityHandler))).Methods("POST")
	apiCreate.Handle("/users/{userId}/notes", api.Middleware(http.HandlerFunc(u.AddUserNoteHandler))).Methods("POST")
	apiCreate.Handle("/users/{userId}/notes/{noteId}", api.Middleware(http.HandlerFunc(u.UpdateUserNoteHandler))).Methods("PUT")
	apiCreate.Handle("/users/{userId}/notes/{noteId}", api.Middleware(http.HandlerFunc(u.DeleteUserNoteHandler))).Methods("DELETE")
	apiCreate.Handle("/user/{userId}/pending-community-request", api.Middleware(http.HandlerFunc(u.PendingCommunityRequestHandler))).Methods("POST")
	apiCreate.Handle("/user/{userId}/pending-department-request", api.Middleware(http.HandlerFunc(u.AddUserToPendingDepartmentHandler))).Methods("POST")
	apiCreate.Handle("/user/{user_id}/notifications/{notification_id}/read", api.Middleware(http.HandlerFunc(u.MarkNotificationAsReadHandler))).Methods("PUT")
	apiCreate.Handle("/user/{user_id}/notifications/{notification_id}", api.Middleware(http.HandlerFunc(u.DeleteNotificationHandler))).Methods("DELETE")
	apiCreate.Handle("/user/{user_id}", api.Middleware(http.HandlerFunc(u.UpdateUserByIDHandler))).Methods("PUT")
	apiCreate.Handle("/user/{user_id}", api.Middleware(http.HandlerFunc(u.UserHandler))).Methods("GET")

	apiCreate.Handle("/users/discover-people", api.Middleware(http.HandlerFunc(u.UsersDiscoverPeopleHandler))).Methods("GET")
	apiCreate.Handle("/users/last-accessed-community", api.Middleware(http.HandlerFunc(u.UsersLastAccessedCommunityHandler))).Methods("GET")
	apiCreate.Handle("/users/friends", api.Middleware(http.HandlerFunc(u.UserFriendsHandler))).Methods("GET")
	apiCreate.Handle("/users/notifications", api.Middleware(http.HandlerFunc(u.AddNotificationHandler))).Methods("POST")
	apiCreate.Handle("/users/{user_id}/notifications", api.Middleware(http.HandlerFunc(u.GetUserNotificationsHandler))).Methods("GET")
	apiCreate.Handle("/users/{userId}/friends", api.Middleware(http.HandlerFunc(u.fetchUserFriendsByID))).Methods("GET")
	apiCreate.Handle("/users/{friend_id}/friends-and-mutual-friends", api.Middleware(http.HandlerFunc(u.fetchFriendsAndMutualFriendsCount))).Methods("GET")
	apiCreate.Handle("/users/{active_community_id}", api.Middleware(http.HandlerFunc(u.UsersFindAllHandler))).Methods("GET")
	apiCreate.Handle("/users", api.Middleware(http.HandlerFunc(u.FetchUsersByIdsHandler))).Methods("POST")
	// All routes for user must go above this line

	apiCreate.Handle("/civilian/{civilian_id}", api.Middleware(http.HandlerFunc(civ.CivilianByIDHandler))).Methods("GET")
	apiCreate.Handle("/civilian/{civilian_id}", api.Middleware(http.HandlerFunc(civ.UpdateCivilianHandler))).Methods("PUT")
	apiCreate.Handle("/civilian/{civilian_id}", api.Middleware(http.HandlerFunc(civ.DeleteCivilianHandler))).Methods("DELETE")
	apiCreate.Handle("/civilian/{civilian_id}/criminal-history", api.Middleware(http.HandlerFunc(civ.AddCriminalHistoryHandler))).Methods("POST")
	apiCreate.Handle("/civilian/{civilian_id}/criminal-history/{citation_id}", api.Middleware(http.HandlerFunc(civ.UpdateCriminalHistoryHandler))).Methods("PUT")
	apiCreate.Handle("/civilian", api.Middleware(http.HandlerFunc(civ.CreateCivilianHandler))).Methods("POST")
	apiCreate.Handle("/civilians", api.Middleware(http.HandlerFunc(civ.CivilianHandler))).Methods("GET")
	apiCreate.Handle("/civilians/user/{user_id}", api.Middleware(http.HandlerFunc(civ.CiviliansByUserIDHandler))).Methods("GET")
	apiCreate.Handle("/civilians/search", api.Middleware(http.HandlerFunc(civ.CiviliansByNameSearchHandler))).Methods("GET")

	apiCreate.Handle("/vehicle/{vehicle_id}", api.Middleware(http.HandlerFunc(v.VehicleByIDHandler))).Methods("GET")
	apiCreate.Handle("/vehicle/{vehicle_id}", api.Middleware(http.HandlerFunc(v.UpdateVehicleHandler))).Methods("PUT")
	apiCreate.Handle("/vehicle/{vehicle_id}", api.Middleware(http.HandlerFunc(v.DeleteVehicleHandler))).Methods("DELETE")
	apiCreate.Handle("/vehicle", api.Middleware(http.HandlerFunc(v.CreateVehicleHandler))).Methods("POST")
	apiCreate.Handle("/vehicles", api.Middleware(http.HandlerFunc(v.VehicleHandler))).Methods("GET")
	apiCreate.Handle("/vehicles/user/{user_id}", api.Middleware(http.HandlerFunc(v.VehiclesByUserIDHandler))).Methods("GET")
	apiCreate.Handle("/vehicles/registered-owner/{registered_owner_id}", api.Middleware(http.HandlerFunc(v.VehiclesByRegisteredOwnerIDHandler))).Methods("GET")
	apiCreate.Handle("/vehicles/search", api.Middleware(http.HandlerFunc(v.VehicleSearchHandler))).Methods("GET")

	apiCreate.Handle("/firearm/{firearm_id}", api.Middleware(http.HandlerFunc(f.FirearmByIDHandler))).Methods("GET")
	apiCreate.Handle("/firearm/{firearm_id}", api.Middleware(http.HandlerFunc(f.UpdateFirearmHandler))).Methods("PUT")
	apiCreate.Handle("/firearm/{firearm_id}", api.Middleware(http.HandlerFunc(f.DeleteFirearmHandler))).Methods("DELETE")
	apiCreate.Handle("/firearm", api.Middleware(http.HandlerFunc(f.CreateFirearmHandler))).Methods("POST")
	apiCreate.Handle("/firearms", api.Middleware(http.HandlerFunc(f.FirearmHandler))).Methods("GET")
	apiCreate.Handle("/firearms/user/{user_id}", api.Middleware(http.HandlerFunc(f.FirearmsByUserIDHandler))).Methods("GET")
	apiCreate.Handle("/firearms/registered-owner/{registered_owner_id}", api.Middleware(http.HandlerFunc(f.FirearmsByRegisteredOwnerIDHandler))).Methods("GET")
	apiCreate.Handle("/firearms/search", api.Middleware(http.HandlerFunc(f.FirearmsSearchHandler))).Methods("GET")

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
	apiCreate.Handle("/call/{call_id}", api.Middleware(http.HandlerFunc(call.UpdateCallByIDHandler))).Methods("PUT")
	apiCreate.Handle("/call/{call_id}", api.Middleware(http.HandlerFunc(call.DeleteCallByIDHandler))).Methods("DELETE")
	apiCreate.Handle("/call/{call_id}/note/{note_id}", api.Middleware(http.HandlerFunc(call.EditCallNoteByIDHandler))).Methods("PUT")
	apiCreate.Handle("/call/{call_id}/note/{note_id}", api.Middleware(http.HandlerFunc(call.DeleteCallNoteByIDHandler))).Methods("DELETE")
	apiCreate.Handle("/call/{call_id}/note", api.Middleware(http.HandlerFunc(call.AddCallNoteHandler))).Methods("POST")
	apiCreate.Handle("/calls", api.Middleware(http.HandlerFunc(call.CallHandler))).Methods("GET")
	apiCreate.Handle("/calls", api.Middleware(http.HandlerFunc(call.CreateCallHandler))).Methods("POST")
	apiCreate.Handle("/calls/community/{community_id}", api.Middleware(http.HandlerFunc(call.CallsByCommunityIDHandler))).Methods("GET")

	apiCreate.Handle("/bolo/{bolo_id}", api.Middleware(http.HandlerFunc(bolo.GetBoloByIDHandler))).Methods("GET")
	apiCreate.Handle("/bolo/{bolo_id}", api.Middleware(http.HandlerFunc(bolo.UpdateBoloHandler))).Methods("PUT")
	apiCreate.Handle("/bolo/{bolo_id}", api.Middleware(http.HandlerFunc(bolo.DeleteBoloHandler))).Methods("DELETE")
	apiCreate.Handle("/bolo", api.Middleware(http.HandlerFunc(bolo.CreateBoloHandler))).Methods("POST")
	apiCreate.Handle("/bolos", api.Middleware(http.HandlerFunc(bolo.FetchDepartmentBolosHandler))).Methods("GET")

	apiCreate.Handle("/spotlight", api.Middleware(http.HandlerFunc(s.SpotlightHandler))).Methods("GET")
	apiCreate.Handle("/spotlight", api.Middleware(http.HandlerFunc(s.SpotlightCreateHandler))).Methods("POST")

	apiCreate.Handle("/report", api.Middleware(http.HandlerFunc(report.CreateReportHandler))).Methods("POST")

	apiCreate.Handle("/search", api.Middleware(http.HandlerFunc(search.SearchHandler))).Methods("GET")

	apiCreate.Handle("/generate-signature", api.Middleware(http.HandlerFunc(cloudinaryHandler.GenerateSignature))).Methods("POST")

	apiCreate.Handle("/success", http.HandlerFunc(u.handleSuccessRedirect)).Methods("GET")
	apiCreate.Handle("/cancel", http.HandlerFunc(u.handleCancelRedirect)).Methods("GET")

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

	// initialize stripe
	stripeKey := os.Getenv("STRIPE_SECRET_KEY")
	if stripeKey == "" {
		return fmt.Errorf("stripe secret key is not set")
	}
	stripe.Key = stripeKey

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
