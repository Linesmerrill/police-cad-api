package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/linesmerrill/police-cad-api/models"
	"github.com/stripe/stripe-go/v82"

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
	dept := Community{DB: databases.NewCommunityDatabase(a.dbHelper), UDB: databases.NewUserDatabase(a.dbHelper)}
	c := Community{DB: databases.NewCommunityDatabase(a.dbHelper), UDB: databases.NewUserDatabase(a.dbHelper), ADB: databases.NewArchivedCommunityDatabase(a.dbHelper), IDB: databases.NewInviteCodeDatabase(a.dbHelper), UPDB: databases.NewUserPreferencesDatabase(a.dbHelper), CDB: databases.NewCivilianDatabase(a.dbHelper)}
	civ := Civilian{DB: databases.NewCivilianDatabase(a.dbHelper)}
	v := Vehicle{DB: databases.NewVehicleDatabase(a.dbHelper)}
	f := Firearm{DB: databases.NewFirearmDatabase(a.dbHelper)}
	ic := InviteCode{DB: databases.NewInviteCodeDatabase(a.dbHelper)}
	l := License{DB: databases.NewLicenseDatabase(a.dbHelper)}
	e := Ems{DB: databases.NewEmsDatabase(a.dbHelper)}

	pv := PendingVerification{PVDB: databases.NewPendingVerificationDatabase(a.dbHelper), UDB: databases.NewUserDatabase(a.dbHelper)}
	w := Warrant{DB: databases.NewWarrantDatabase(a.dbHelper)}
	call := Call{DB: databases.NewCallDatabase(a.dbHelper)}
	bolo := Bolo{DB: databases.NewBoloDatabase(a.dbHelper)}
	arrestReport := ArrestReport{DB: databases.NewArrestReportDatabase(a.dbHelper)}
	s := Spotlight{DB: databases.NewSpotlightDatabase(a.dbHelper)}
	search := Search{UserDB: databases.NewUserDatabase(a.dbHelper), CommDB: databases.NewCommunityDatabase(a.dbHelper)}
	report := Report{RDB: databases.NewReportDatabase(a.dbHelper)}
	cloudinaryHandler := CloudinaryHandler{}
	userPrefs := UserPreferences{DB: databases.NewUserPreferencesDatabase(a.dbHelper)}
	announcement := Announcement{
		ADB: databases.NewAnnouncementDatabase(a.dbHelper),
		UDB: databases.NewUserDatabase(a.dbHelper),
		CDB: databases.NewCommunityDatabase(a.dbHelper),
	}
	adminHandler := Admin{
		ADB:  databases.NewAdminDatabase(a.dbHelper),
		RDB:  databases.NewAdminResetDatabase(a.dbHelper),
		UDB:  databases.NewUserDatabase(a.dbHelper),
		CDB:  databases.NewCommunityDatabase(a.dbHelper),
		AADB: databases.NewAdminActivityDatabase(a.dbHelper),
	}

	medicalReportHandler := MedicalReport{DB: databases.NewMedicalReportDatabase(a.dbHelper)}
	medicationHandler := Medication{DB: databases.NewMedicationDatabase(a.dbHelper)}
	emsPersonaHandler := EMSPersona{DB: databases.NewEMSPersonaDatabase(a.dbHelper)}
	emsVehicleHandler := EMSVehicle{DB: databases.NewEMSVehicleDatabase(a.dbHelper)}

	// Component and Template handlers
	componentDB := databases.NewComponentDatabase(a.dbHelper)
	templateDB := databases.NewTemplateDatabase(a.dbHelper)
	
	componentHandler := NewComponent(componentDB)
	templateHandler := NewTemplate(templateDB, componentDB)
	templateMigrationHandler := NewTemplateMigration(templateDB, databases.NewCommunityDatabase(a.dbHelper))
	departmentTemplateHandler := NewDepartmentTemplate(databases.NewCommunityDatabase(a.dbHelper), templateDB)

	// healthchex
	r.HandleFunc("/health", healthCheckHandler)

	apiCreate := r.PathPrefix("/api/v1").Subrouter()
	apiV2 := r.PathPrefix("/api/v2").Subrouter()
	ws := r.PathPrefix("/ws").Subrouter()

	apiCreate.Handle("/auth/token", api.Middleware(http.HandlerFunc(m.CreateToken))).Methods("POST")
	apiCreate.Handle("/auth/logout", api.Middleware(http.HandlerFunc(api.RevokeToken))).Methods("DELETE")
	apiCreate.Handle("/admin/login", http.HandlerFunc(adminHandler.AdminLoginHandler)).Methods("POST")
	apiCreate.Handle("/admin/logout", http.HandlerFunc(adminHandler.AdminLogoutHandler)).Methods("POST")
	apiCreate.Handle("/admin/forgot-password", http.HandlerFunc(adminHandler.AdminForgotPasswordHandler)).Methods("POST")
	apiCreate.Handle("/admin/reset-password", http.HandlerFunc(adminHandler.AdminResetPasswordHandler)).Methods("POST")

	// Admin console routes (moved to appear before general user routes)
	apiCreate.Handle("/admin/search/users", http.HandlerFunc(adminHandler.AdminUserSearchHandler)).Methods("POST")
	apiCreate.Handle("/admin/search/communities", http.HandlerFunc(adminHandler.AdminCommunitySearchHandler)).Methods("POST")
	apiCreate.Handle("/admin/users/{id}", http.HandlerFunc(adminHandler.AdminUserDetailsHandler)).Methods("GET")
	apiCreate.Handle("/admin/communities/{id}", http.HandlerFunc(adminHandler.AdminCommunityDetailsHandler)).Methods("GET")
	apiCreate.Handle("/admin/users/{id}/reset-password", http.HandlerFunc(adminHandler.AdminUserResetPasswordHandler)).Methods("POST")
	apiCreate.Handle("/admin/users/{id}/reactivate", http.HandlerFunc(adminHandler.AdminUserReactivateHandler)).Methods("POST")
	// Note: initiate-reset route removed - frontend will use existing /forgot-password route directly

	// Admin user management routes
	apiCreate.Handle("/admin/users", http.HandlerFunc(adminHandler.CreateAdminUserHandler)).Methods("POST")
	apiCreate.Handle("/admin/send-reset-email", http.HandlerFunc(adminHandler.SendAdminResetEmailHandler)).Methods("POST")

	// Admin management routes
	apiCreate.Handle("/admin/search/admins", http.HandlerFunc(adminHandler.AdminSearchAdminsHandler)).Methods("POST")
	apiCreate.Handle("/admin/admins", http.HandlerFunc(adminHandler.AdminGetAllAdminsHandler)).Methods("POST")
	apiCreate.Handle("/admin/admins/{id}", http.HandlerFunc(adminHandler.AdminGetAdminDetailsHandler)).Methods("GET")
	apiCreate.Handle("/admin/admins/{id}/activity", http.HandlerFunc(adminHandler.AdminGetActivityHandler)).Methods("POST")
	apiCreate.Handle("/admin/admins/{id}/roles", http.HandlerFunc(adminHandler.AdminChangeRolesHandler)).Methods("PUT")
	apiCreate.Handle("/admin/admins/{id}", http.HandlerFunc(adminHandler.AdminDeleteAdminHandler)).Methods("DELETE")

	// Admin activity tracking routes
	apiCreate.Handle("/admin/activity/log", http.HandlerFunc(adminHandler.AdminActivityLogHandler)).Methods("POST")

	apiCreate.Handle("/verify/send-verification-code", http.HandlerFunc(pv.CreatePendingVerificationHandler)).Methods("POST")
	apiCreate.Handle("/verify/verify-code", http.HandlerFunc(pv.VerifyCodeHandler)).Methods("POST")
	apiCreate.Handle("/verify/resend-verification-code", http.HandlerFunc(pv.ResendVerificationCodeHandler)).Methods("POST")

	apiCreate.Handle("/community", api.Middleware(http.HandlerFunc(c.CreateCommunityHandler))).Methods("POST")
	apiCreate.Handle("/community/subscribe", api.Middleware(http.HandlerFunc(c.SubscribeCommunityHandler))).Methods("POST")
	apiCreate.Handle("/community/cancel-subscription", api.Middleware(http.HandlerFunc(c.CancelCommunitySubscriptionHandler))).Methods("POST")
	apiCreate.Handle("/community/join", api.Middleware(http.HandlerFunc(c.JoinCommunityHandler))).Methods("POST")
	apiCreate.Handle("/community/invite/{invite_code}", api.Middleware(http.HandlerFunc(c.GetInviteCodeHandler))).Methods("GET")
	apiCreate.Handle("/community/{community_id}", api.Middleware(http.HandlerFunc(c.CommunityHandler))).Methods("GET")
	apiCreate.Handle("/community/{community_id}", api.Middleware(http.HandlerFunc(c.UpdateCommunityFieldHandler))).Methods("PATCH")
	apiCreate.Handle("/community/{community_id}", api.Middleware(http.HandlerFunc(c.DeleteCommunityByIDHandler))).Methods("DELETE")
	apiCreate.Handle("/community/{user_id}/subscriptions", api.Middleware(http.HandlerFunc(c.GetCommunityUserSubscriptions))).Methods("GET")
	apiCreate.Handle("/community/{communityId}/members", api.Middleware(http.HandlerFunc(c.CommunityMembersHandler))).Methods("GET")
	apiV2.Handle("/community/{communityId}/members", api.Middleware(http.HandlerFunc(c.FetchCommunityMembersHandlerV2))).Methods("GET")
	apiCreate.Handle("/community/{communityId}/roles", api.Middleware(http.HandlerFunc(c.GetRolesByCommunityIDHandler))).Methods("GET")
	apiCreate.Handle("/community/{communityId}/roles", api.Middleware(http.HandlerFunc(c.AddRoleToCommunityHandler))).Methods("POST")
	apiCreate.Handle("/community/{communityId}/fines", api.Middleware(http.HandlerFunc(c.SetCommunityFinesHandler))).Methods("PUT")
	apiCreate.Handle("/community/{community_id}/archive", api.Middleware(http.HandlerFunc(c.ArchiveCommunityHandler))).Methods("POST")
	apiCreate.Handle("/community/{communityId}/roles/{roleId}/members", api.Middleware(http.HandlerFunc(c.FetchCommunityMembersByRoleIDHandler))).Methods("GET")
	apiV2.Handle("/community/{communityId}/roles/{roleId}/members", api.Middleware(http.HandlerFunc(c.FetchCommunityMembersByRoleIDHandlerV2))).Methods("GET")
	apiV2.Handle("/community/{communityId}/roles/{roleId}/members/exclude", api.Middleware(http.HandlerFunc(c.FetchCommunityMembersExcludeRoleHandlerV2))).Methods("GET")
	apiV2.Handle("/community/{communityId}/transfer-ownership", api.Middleware(http.HandlerFunc(c.TransferCommunityOwnershipHandler))).Methods("POST")
	apiCreate.Handle("/community/{communityId}/roles/{roleId}/members", api.Middleware(http.HandlerFunc(c.UpdateRoleMembersHandler))).Methods("PUT")
	apiCreate.Handle("/community/{communityId}/roles/{roleId}/name", api.Middleware(http.HandlerFunc(c.UpdateRoleNameHandler))).Methods("PUT")
	apiCreate.Handle("/community/{communityId}/roles/{roleId}/permissions", api.Middleware(http.HandlerFunc(c.UpdateRolePermissionsHandler))).Methods("PUT")
	apiCreate.Handle("/community/{communityId}/roles/{roleId}/members/{memberId}", api.Middleware(http.HandlerFunc(c.DeleteRoleMemberHandler))).Methods("DELETE")
	apiCreate.Handle("/community/{communityId}/roles/{roleId}", api.Middleware(http.HandlerFunc(c.DeleteRoleByIDHandler))).Methods("DELETE")
	apiCreate.Handle("/community/{communityId}/banned-users", api.Middleware(http.HandlerFunc(c.GetBannedUsersHandler))).Methods("GET")
	apiV2.Handle("/community/{communityId}/banned-users", api.Middleware(http.HandlerFunc(c.FetchBannedUsersHandlerV2))).Methods("GET")
	apiCreate.Handle("/community/{communityId}/online-users", api.Middleware(http.HandlerFunc(c.GetOnlineUsersHandler))).Methods("GET")
	apiCreate.Handle("/community/{communityId}/add-invite-code", api.Middleware(http.HandlerFunc(c.AddInviteCodeHandler))).Methods("POST")
	apiV2.Handle("/community/{communityId}/invite-codes", api.Middleware(http.HandlerFunc(c.GetCommunityInviteCodesHandlerV2))).Methods("GET")
	apiV2.Handle("/community/{communityId}/civilians", api.Middleware(http.HandlerFunc(c.GetCommunityCiviliansHandlerV2))).Methods("GET")
	apiCreate.Handle("/invite-code/{inviteCodeId}", api.Middleware(http.HandlerFunc(c.DeleteInviteCodeHandler))).Methods("DELETE")
	apiV2.Handle("/community/{communityId}/your-departments", api.Middleware(http.HandlerFunc(c.GetPaginatedAllDepartmentsHandler))).Methods("GET")
	apiCreate.Handle("/community/{communityId}/departments", api.Middleware(http.HandlerFunc(c.FetchAllCommunityDepartmentsHandler))).Methods("GET")
	apiV2.Handle("/community/{communityId}/departments", api.Middleware(http.HandlerFunc(c.GetPaginatedDepartmentsHandler))).Methods("GET")
	apiV2.Handle("/community/{communityId}/all-departments", api.Middleware(http.HandlerFunc(c.GetPaginatedAllDepartmentsHandler))).Methods("GET")
	apiCreate.Handle("/community/{communityId}/departments", api.Middleware(http.HandlerFunc(c.CreateCommunityDepartmentHandler))).Methods("POST")
	apiCreate.Handle("/community/{communityId}/departments/{departmentId}", api.Middleware(http.HandlerFunc(c.FetchDepartmentByIDHandler))).Methods("GET")
	apiCreate.Handle("/community/{communityId}/departments/{departmentId}", api.Middleware(http.HandlerFunc(c.DeleteCommunityDepartmentByIDHandler))).Methods("DELETE")
	apiCreate.Handle("/community/{communityId}/departments/{departmentId}", api.Middleware(http.HandlerFunc(c.UpdateDepartmentDetailsHandler))).Methods("PATCH")
	apiCreate.Handle("/community/{communityId}/departments/{departmentId}/components", api.Middleware(http.HandlerFunc(c.UpdateDepartmentComponentsHandler))).Methods("PUT")
	apiCreate.Handle("/community/{communityId}/user/{userId}/departments", api.Middleware(http.HandlerFunc(c.FetchUserDepartmentsHandler))).Methods("GET")
	apiCreate.Handle("/community/{communityId}/departments/{departmentId}/members", api.Middleware(http.HandlerFunc(c.GetDepartmentMembersHandler))).Methods("GET")
	apiCreate.Handle("/community/{communityId}/departments/{departmentId}/members", api.Middleware(http.HandlerFunc(c.UpdateDepartmentMembersHandler))).Methods("POST")
	apiCreate.Handle("/community/{communityId}/departments/{departmentId}/remove-user", api.Middleware(http.HandlerFunc(c.RemoveUserFromDepartmentHandler))).Methods("POST")
	apiCreate.Handle("/community/{communityId}/departments/{departmentId}/update-image", api.Middleware(http.HandlerFunc(c.UpdateDepartmentImageLinkHandler))).Methods("PATCH")
	apiCreate.Handle("/community/{communityId}/departments/{departmentId}/join-requests", api.Middleware(http.HandlerFunc(c.UpdateDepartmentJoinRequestHandler))).Methods("PUT")
	apiCreate.Handle("/community/{communityId}/tenCodes/{codeId}", api.Middleware(http.HandlerFunc(c.UpdateTenCodeHandler))).Methods("PUT")
	apiCreate.Handle("/community/{communityId}/tenCodes/{codeId}", api.Middleware(http.HandlerFunc(c.DeleteTenCodeHandler))).Methods("DELETE")
	apiCreate.Handle("/community/{communityId}/tenCodes/active", api.Middleware(http.HandlerFunc(c.GetActiveTenCodeHandler))).Methods("GET")
	apiCreate.Handle("/community/{communityId}/tenCodes", api.Middleware(http.HandlerFunc(c.AddTenCodeHandler))).Methods("POST")
	apiCreate.Handle("/community/{communityId}/members/{userId}/tenCode", api.Middleware(http.HandlerFunc(c.SetMemberTenCodeHandler))).Methods("PUT")
	apiCreate.Handle("/community/{communityId}/events", api.Middleware(http.HandlerFunc(c.GetEventsByCommunityIDHandler))).Methods("GET")
	apiCreate.Handle("/community/{communityId}/events", api.Middleware(http.HandlerFunc(c.AddEventToCommunityHandler))).Methods("POST")
	apiCreate.Handle("/community/{communityId}/events/{eventId}", api.Middleware(http.HandlerFunc(c.GetEventByIDHandler))).Methods("GET")
	apiCreate.Handle("/community/{communityId}/events/{eventId}", api.Middleware(http.HandlerFunc(c.UpdateEventByIDHandler))).Methods("PUT")
	apiCreate.Handle("/community/{communityId}/events/{eventId}", api.Middleware(http.HandlerFunc(c.DeleteEventByIDHandler))).Methods("DELETE")

	// Announcement routes
	apiCreate.Handle("/community/{communityId}/announcements", http.HandlerFunc(announcement.GetAnnouncementsHandler)).Methods("GET")
	apiCreate.Handle("/community/{communityId}/announcements", http.HandlerFunc(announcement.CreateAnnouncementHandler)).Methods("POST")
	apiCreate.Handle("/announcement/{announcementId}", http.HandlerFunc(announcement.GetAnnouncementHandler)).Methods("GET")
	apiCreate.Handle("/announcement/{announcementId}", http.HandlerFunc(announcement.UpdateAnnouncementHandler)).Methods("PUT")
	apiCreate.Handle("/announcement/{announcementId}", http.HandlerFunc(announcement.DeleteAnnouncementHandler)).Methods("DELETE")
	apiCreate.Handle("/announcement/{announcementId}/reactions", http.HandlerFunc(announcement.AddReactionHandler)).Methods("POST")
	apiCreate.Handle("/announcement/{announcementId}/reactions", http.HandlerFunc(announcement.RemoveReactionHandler)).Methods("DELETE")
	apiCreate.Handle("/announcement/{announcementId}/comments", http.HandlerFunc(announcement.AddCommentHandler)).Methods("POST")
	apiCreate.Handle("/announcement/{announcementId}/comments/{commentId}", http.HandlerFunc(announcement.UpdateCommentHandler)).Methods("PUT")
	apiCreate.Handle("/announcement/{announcementId}/comments/{commentId}", http.HandlerFunc(announcement.DeleteCommentHandler)).Methods("DELETE")

	apiCreate.Handle("/community/{community_id}/{owner_id}", api.Middleware(http.HandlerFunc(c.CommunityByCommunityAndOwnerIDHandler))).Methods("GET")
	apiCreate.Handle("/communities/elite", api.Middleware(http.HandlerFunc(c.GetEliteCommunitiesHandler))).Methods("GET")
	apiV2.Handle("/communities/elite", api.Middleware(http.HandlerFunc(c.FetchEliteCommunitiesHandler))).Methods("GET")
	apiCreate.Handle("/communities/{owner_id}", api.Middleware(http.HandlerFunc(c.CommunitiesByOwnerIDHandler))).Methods("GET")
	apiCreate.Handle("/communities/tag/{tag}", api.Middleware(http.HandlerFunc(c.FetchCommunitiesByTagHandler))).Methods("GET")
	apiV2.Handle("/communities/tag/{tag}", api.Middleware(http.HandlerFunc(c.FetchCommunitiesByTagHandlerV2))).Methods("GET")

	apiV2.Handle("/departments-screen-data", api.Middleware(http.HandlerFunc(dept.GetDepartmentsScreenDataHandler))).Methods("GET")

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
	apiCreate.Handle("/user/cancel-subscription", api.Middleware(http.HandlerFunc(u.CancelSubscriptionHandler))).Methods("POST")
	apiCreate.Handle("/user/unsubscribe", api.Middleware(http.HandlerFunc(u.UnsubscribeUserHandler))).Methods("POST")
	apiCreate.Handle("/user/{userId}/add-friend", api.Middleware(http.HandlerFunc(u.AddFriendHandler))).Methods("POST")
	apiCreate.Handle("/user/{user_id}/subscription", api.Middleware(http.HandlerFunc(u.UpdateUserSubscriptionHandler))).Methods("PUT")
	apiCreate.Handle("/user/{user_id}/update-status", api.Middleware(http.HandlerFunc(u.UpdateFriendStatusHandler))).Methods("PUT")
	apiCreate.Handle("/user/{userId}/communities", api.Middleware(http.HandlerFunc(u.GetUserCommunitiesHandler))).Methods("GET")
	apiV2.Handle("/user/{userId}/communities", api.Middleware(http.HandlerFunc(u.FetchUserCommunitiesHandler))).Methods("GET")
	apiCreate.Handle("/user/{userId}/communities", api.Middleware(http.HandlerFunc(u.AddCommunityToUserHandler))).Methods("PUT")
	apiCreate.Handle("/user/{userId}/random-communities", api.Middleware(http.HandlerFunc(u.GetRandomCommunitiesHandler))).Methods("GET")
	apiCreate.Handle("/user/{userId}/prioritized-communities", api.Middleware(http.HandlerFunc(u.GetPrioritizedCommunitiesHandler))).Methods("GET")
	apiV2.Handle("/user/{userId}/prioritized-communities", api.Middleware(http.HandlerFunc(u.FetchPrioritizedCommunitiesHandler))).Methods("GET")
	apiCreate.Handle("/user/{userId}/remove-community", api.Middleware(http.HandlerFunc(u.RemoveCommunityFromUserHandler))).Methods("DELETE")
	apiCreate.Handle("/user/{userId}/ban-community", api.Middleware(http.HandlerFunc(u.BanUserFromCommunityHandler))).Methods("POST")
	apiCreate.Handle("/user/{userId}/unban-community", api.Middleware(http.HandlerFunc(u.UnbanUserFromCommunityHandler))).Methods("POST")
	apiCreate.Handle("/users/{userId}/notes", api.Middleware(http.HandlerFunc(u.AddUserNoteHandler))).Methods("POST")
	apiCreate.Handle("/users/{userId}/notes/{noteId}", api.Middleware(http.HandlerFunc(u.UpdateUserNoteHandler))).Methods("PUT")
	apiCreate.Handle("/users/{userId}/notes/{noteId}", api.Middleware(http.HandlerFunc(u.DeleteUserNoteHandler))).Methods("DELETE")
	apiCreate.Handle("/user/{userId}/pending-community-request", api.Middleware(http.HandlerFunc(u.PendingCommunityRequestHandler))).Methods("POST")
	apiCreate.Handle("/user/{userId}/pending-department-request", api.Middleware(http.HandlerFunc(u.AddUserToPendingDepartmentHandler))).Methods("POST")
	apiCreate.Handle("/user/{user_id}/deactivate", api.Middleware(http.HandlerFunc(u.DeactivateUserHandler))).Methods("DELETE")
	apiCreate.Handle("/user/{user_id}/notifications/{notification_id}/read", api.Middleware(http.HandlerFunc(u.MarkNotificationAsReadHandler))).Methods("PUT")
	apiCreate.Handle("/user/{user_id}/notifications/{notification_id}", api.Middleware(http.HandlerFunc(u.DeleteNotificationHandler))).Methods("DELETE")
	apiCreate.Handle("/user/{user_id}", api.Middleware(http.HandlerFunc(u.UpdateUserByIDHandler))).Methods("PUT")
	apiCreate.Handle("/user/{user_id}", api.Middleware(http.HandlerFunc(u.UserHandler))).Methods("GET")
	apiCreate.Handle("/users/discover-people", api.Middleware(http.HandlerFunc(u.UsersDiscoverPeopleHandler))).Methods("GET")
	apiCreate.Handle("/users/last-accessed-community", api.Middleware(http.HandlerFunc(u.UsersLastAccessedCommunityHandler))).Methods("GET")
	apiCreate.Handle("/users/friends", api.Middleware(http.HandlerFunc(u.UserFriendsHandler))).Methods("GET")
	apiCreate.Handle("/users/notifications", api.Middleware(http.HandlerFunc(u.AddNotificationHandler))).Methods("POST")
	apiV2.Handle("/users/{user_id}/notifications", api.Middleware(http.HandlerFunc(u.GetUserNotificationsHandlerV2))).Methods("GET")
	apiCreate.Handle("/users/{user_id}/notifications", api.Middleware(http.HandlerFunc(u.GetUserNotificationsHandler))).Methods("GET")
	apiCreate.Handle("/users/{userId}/friends", api.Middleware(http.HandlerFunc(u.fetchUserFriendsByID))).Methods("GET")
	apiCreate.Handle("/users/{friend_id}/friends-and-mutual-friends", api.Middleware(http.HandlerFunc(u.fetchFriendsAndMutualFriendsCount))).Methods("GET")
	apiCreate.Handle("/users/{active_community_id}", api.Middleware(http.HandlerFunc(u.UsersFindAllHandler))).Methods("GET")
	apiCreate.Handle("/users", api.Middleware(http.HandlerFunc(u.FetchUsersByIdsHandler))).Methods("POST")
	// All routes for user must go above this line

	// User Preferences routes
	apiCreate.Handle("/user-preferences/{user_id}", api.Middleware(http.HandlerFunc(userPrefs.GetUserPreferencesHandler))).Methods("GET")
	apiCreate.Handle("/user-preferences", api.Middleware(http.HandlerFunc(userPrefs.CreateUserPreferencesHandler))).Methods("POST")
	apiCreate.Handle("/user-preferences/{user_id}", api.Middleware(http.HandlerFunc(userPrefs.UpdateUserPreferencesHandler))).Methods("PUT")
	apiCreate.Handle("/user-preferences/{user_id}", api.Middleware(http.HandlerFunc(userPrefs.DeleteUserPreferencesHandler))).Methods("DELETE")
	apiCreate.Handle("/user-preferences/{user_id}/community/{community_id}/department-order", api.Middleware(http.HandlerFunc(userPrefs.GetDepartmentOrderHandler))).Methods("GET")
	apiCreate.Handle("/user-preferences/{user_id}/community/{community_id}/department-order", api.Middleware(http.HandlerFunc(userPrefs.UpdateDepartmentOrderHandler))).Methods("PUT")

	// Civilian approval routes (must come before parameterized routes)
	apiCreate.Handle("/civilian/approval", api.Middleware(http.HandlerFunc(civ.CivilianApprovalHandler))).Methods("POST")
	apiCreate.Handle("/civilian/admin-approval", api.Middleware(http.HandlerFunc(civ.AdminCivilianApprovalHandler))).Methods("POST")
	apiCreate.Handle("/civilian/pending-approvals", api.Middleware(http.HandlerFunc(civ.PendingApprovalsHandler))).Methods("GET")
	
	// Civilian parameterized routes
	apiCreate.Handle("/civilian/{civilian_id}", api.Middleware(http.HandlerFunc(civ.CivilianByIDHandler))).Methods("GET")
	apiCreate.Handle("/civilian/{civilian_id}", api.Middleware(http.HandlerFunc(civ.UpdateCivilianHandler))).Methods("PUT")
	apiCreate.Handle("/civilian/{civilian_id}", api.Middleware(http.HandlerFunc(civ.DeleteCivilianHandler))).Methods("DELETE")
	apiCreate.Handle("/civilian/{civilian_id}/criminal-history", api.Middleware(http.HandlerFunc(civ.AddCriminalHistoryHandler))).Methods("POST")
	apiCreate.Handle("/civilian/{civilian_id}/criminal-history/{citation_id}", api.Middleware(http.HandlerFunc(civ.UpdateCriminalHistoryHandler))).Methods("PUT")
	apiCreate.Handle("/civilian/{civilian_id}/criminal-history/{citation_id}", api.Middleware(http.HandlerFunc(civ.DeleteCriminalHistoryHandler))).Methods("DELETE")
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
	apiCreate.Handle("/license/{license_id}", api.Middleware(http.HandlerFunc(l.UpdateLicenseByIDHandler))).Methods("PUT")
	apiCreate.Handle("/license/{license_id}", api.Middleware(http.HandlerFunc(l.DeleteLicenseByIDHandler))).Methods("DELETE")
	apiCreate.Handle("/license", api.Middleware(http.HandlerFunc(l.CreateLicenseHandler))).Methods("POST")
	apiCreate.Handle("/licenses/civilian/{civilian_id}", api.Middleware(http.HandlerFunc(l.LicensesByCivilianIDHandler))).Methods("GET")

	apiCreate.Handle("/warrant/{warrant_id}", api.Middleware(http.HandlerFunc(w.WarrantByIDHandler))).Methods("GET")
	apiCreate.Handle("/warrants", api.Middleware(http.HandlerFunc(w.WarrantHandler))).Methods("GET")
	apiCreate.Handle("/warrants/user/{user_id}", api.Middleware(http.HandlerFunc(w.WarrantsByUserIDHandler))).Methods("GET")

	apiCreate.Handle("/ems/{ems_id}", api.Middleware(http.HandlerFunc(e.EmsByIDHandler))).Methods("GET")
	apiCreate.Handle("/ems", api.Middleware(http.HandlerFunc(e.EmsHandler))).Methods("GET")
	apiCreate.Handle("/ems/user/{user_id}", api.Middleware(http.HandlerFunc(e.EmsByUserIDHandler))).Methods("GET")
	// Old EMS vehicle routes (keeping for backward compatibility)
	// apiCreate.Handle("/emsVehicle/{ems_vehicle_id}", api.Middleware(http.HandlerFunc(ev.EmsVehicleByIDHandler))).Methods("GET")
	// apiCreate.Handle("/emsVehicles", api.Middleware(http.HandlerFunc(ev.EmsVehicleHandler))).Methods("GET")
	// apiCreate.Handle("/emsVehicles/user/{user_id}", api.Middleware(http.HandlerFunc(ev.EmsVehiclesByUserIDHandler))).Methods("GET")

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

	apiCreate.Handle("/arrest-report/{arrest_report_id}", api.Middleware(http.HandlerFunc(arrestReport.GetArrestReportByIDHandler))).Methods("GET")
	apiCreate.Handle("/arrest-report/{arrest_report_id}", api.Middleware(http.HandlerFunc(arrestReport.UpdateArrestReportHandler))).Methods("PUT")
	apiCreate.Handle("/arrest-report/{arrest_report_id}", api.Middleware(http.HandlerFunc(arrestReport.DeleteArrestReportHandler))).Methods("DELETE")
	apiCreate.Handle("/arrest-report", api.Middleware(http.HandlerFunc(arrestReport.CreateArrestReportHandler))).Methods("POST")
	apiCreate.Handle("/arrest-report/arrestee/{arrestee_id}", api.Middleware(http.HandlerFunc(arrestReport.GetArrestReportsByArresteeIDHandler))).Methods("GET")

	// Medical Reports routes
	apiCreate.Handle("/medical-reports", api.Middleware(http.HandlerFunc(medicalReportHandler.GetMedicalReportsHandler))).Methods("GET")
	apiCreate.Handle("/medical-reports", api.Middleware(http.HandlerFunc(medicalReportHandler.CreateMedicalReportHandler))).Methods("POST")
	apiCreate.Handle("/medical-reports/{id}", api.Middleware(http.HandlerFunc(medicalReportHandler.GetMedicalReportByIDHandler))).Methods("GET")
	apiCreate.Handle("/medical-reports/{id}", api.Middleware(http.HandlerFunc(medicalReportHandler.UpdateMedicalReportHandler))).Methods("PUT")
	apiCreate.Handle("/medical-reports/{id}", api.Middleware(http.HandlerFunc(medicalReportHandler.DeleteMedicalReportHandler))).Methods("DELETE")

	// Medication routes
	apiCreate.Handle("/medications", api.Middleware(http.HandlerFunc(medicationHandler.GetMedicationsHandler))).Methods("GET")
	apiCreate.Handle("/medications", api.Middleware(http.HandlerFunc(medicationHandler.CreateMedicationHandler))).Methods("POST")
	apiCreate.Handle("/medications/{id}", api.Middleware(http.HandlerFunc(medicationHandler.GetMedicationByIDHandler))).Methods("GET")
	apiCreate.Handle("/medications/{id}", api.Middleware(http.HandlerFunc(medicationHandler.UpdateMedicationHandler))).Methods("PUT")
	apiCreate.Handle("/medications/{id}", api.Middleware(http.HandlerFunc(medicationHandler.DeleteMedicationHandler))).Methods("DELETE")

	// EMS Persona routes
	apiCreate.Handle("/ems-personas", api.Middleware(http.HandlerFunc(emsPersonaHandler.GetEMSPersonasHandler))).Methods("GET")
	apiCreate.Handle("/ems-personas", api.Middleware(http.HandlerFunc(emsPersonaHandler.CreateEMSPersonaHandler))).Methods("POST")
	apiCreate.Handle("/ems-personas/{id}", api.Middleware(http.HandlerFunc(emsPersonaHandler.GetEMSPersonaByIDHandler))).Methods("GET")
	apiCreate.Handle("/ems-personas/{id}", api.Middleware(http.HandlerFunc(emsPersonaHandler.UpdateEMSPersonaHandler))).Methods("PUT")
	apiCreate.Handle("/ems-personas/{id}", api.Middleware(http.HandlerFunc(emsPersonaHandler.DeleteEMSPersonaHandler))).Methods("DELETE")

	// EMS Vehicle routes
	apiCreate.Handle("/ems-vehicles", api.Middleware(http.HandlerFunc(emsVehicleHandler.GetEMSVehiclesHandler))).Methods("GET")
	apiCreate.Handle("/ems-vehicles", api.Middleware(http.HandlerFunc(emsVehicleHandler.CreateEMSVehicleHandler))).Methods("POST")
	apiCreate.Handle("/ems-vehicles/{id}", api.Middleware(http.HandlerFunc(emsVehicleHandler.GetEMSVehicleByIDHandler))).Methods("GET")
	apiCreate.Handle("/ems-vehicles/{id}", api.Middleware(http.HandlerFunc(emsVehicleHandler.UpdateEMSVehicleHandler))).Methods("PUT")
	apiCreate.Handle("/ems-vehicles/{id}", api.Middleware(http.HandlerFunc(emsVehicleHandler.DeleteEMSVehicleHandler))).Methods("DELETE")

	// Component Management routes
	apiCreate.Handle("/components", api.Middleware(http.HandlerFunc(componentHandler.CreateComponentHandler))).Methods("POST")
	apiCreate.Handle("/components", api.Middleware(http.HandlerFunc(componentHandler.GetComponentsHandler))).Methods("GET")
	apiCreate.Handle("/components/{componentId}", api.Middleware(http.HandlerFunc(componentHandler.GetComponentHandler))).Methods("GET")
	apiCreate.Handle("/components/{componentId}", api.Middleware(http.HandlerFunc(componentHandler.UpdateComponentHandler))).Methods("PUT")
	apiCreate.Handle("/components/{componentId}", api.Middleware(http.HandlerFunc(componentHandler.DeleteComponentHandler))).Methods("DELETE")
	apiCreate.Handle("/components/category/{category}", api.Middleware(http.HandlerFunc(componentHandler.GetComponentsByCategoryHandler))).Methods("GET")
	apiCreate.Handle("/components/initialize-defaults", api.Middleware(http.HandlerFunc(componentHandler.InitializeDefaultComponentsHandler))).Methods("POST")

	// Template Management routes
	apiCreate.Handle("/templates", api.Middleware(http.HandlerFunc(templateHandler.CreateTemplateHandler))).Methods("POST")
	apiCreate.Handle("/templates", api.Middleware(http.HandlerFunc(templateHandler.GetTemplatesHandler))).Methods("GET")
	apiCreate.Handle("/templates/{templateId}", api.Middleware(http.HandlerFunc(templateHandler.GetTemplateHandler))).Methods("GET")
	apiCreate.Handle("/templates/{templateId}", api.Middleware(http.HandlerFunc(templateHandler.UpdateTemplateHandler))).Methods("PUT")
	apiCreate.Handle("/templates/{templateId}", api.Middleware(http.HandlerFunc(templateHandler.DeleteTemplateHandler))).Methods("DELETE")
	apiCreate.Handle("/templates/defaults", api.Middleware(http.HandlerFunc(templateHandler.GetDefaultTemplatesHandler))).Methods("GET")
	apiCreate.Handle("/templates/category/{category}", api.Middleware(http.HandlerFunc(templateHandler.GetTemplatesByCategoryHandler))).Methods("GET")
	apiCreate.Handle("/templates/initialize-defaults", api.Middleware(http.HandlerFunc(templateHandler.InitializeDefaultTemplatesHandler))).Methods("POST")

	// Template Migration routes
	apiCreate.Handle("/templates/migrate/community/{communityId}", api.Middleware(http.HandlerFunc(templateMigrationHandler.MigrateCommunityTemplatesHandler))).Methods("POST")
	apiCreate.Handle("/templates/migrate/all", api.Middleware(http.HandlerFunc(templateMigrationHandler.MigrateAllCommunitiesTemplatesHandler))).Methods("POST")
	apiCreate.Handle("/templates/migrate/status", api.Middleware(http.HandlerFunc(templateMigrationHandler.GetMigrationStatusHandler))).Methods("GET")

	// Department Template routes
	apiCreate.Handle("/community/{communityId}/departments/with-template", api.Middleware(http.HandlerFunc(departmentTemplateHandler.CreateDepartmentWithTemplateHandler))).Methods("POST")
	apiCreate.Handle("/community/{communityId}/departments/{departmentId}/template", api.Middleware(http.HandlerFunc(departmentTemplateHandler.UpdateDepartmentTemplateHandler))).Methods("PUT")
	apiCreate.Handle("/community/{communityId}/departments/{departmentId}/template", api.Middleware(http.HandlerFunc(departmentTemplateHandler.GetDepartmentTemplateHandler))).Methods("GET")

	apiCreate.Handle("/spotlight", api.Middleware(http.HandlerFunc(s.SpotlightHandler))).Methods("GET")
	apiCreate.Handle("/spotlight", api.Middleware(http.HandlerFunc(s.SpotlightCreateHandler))).Methods("POST")

	apiCreate.Handle("/invite/{invite_code}", api.Middleware(http.HandlerFunc(ic.InviteCodeByCodeHandler))).Methods("GET")

	apiCreate.Handle("/report", api.Middleware(http.HandlerFunc(report.CreateReportHandler))).Methods("POST")

	apiCreate.Handle("/search/communities", api.Middleware(http.HandlerFunc(search.SearchCommunityHandler))).Methods("GET")
	apiCreate.Handle("/search", api.Middleware(http.HandlerFunc(search.SearchHandler))).Methods("GET")

	apiCreate.Handle("/generate-signature", api.Middleware(http.HandlerFunc(cloudinaryHandler.GenerateSignature))).Methods("POST")

	apiCreate.Handle("/success", http.HandlerFunc(u.handleSuccessRedirect)).Methods("GET")
	apiCreate.Handle("/cancel", http.HandlerFunc(u.handleCancelRedirect)).Methods("GET")
	apiCreate.Handle("/webhook-subscription-deleted", http.HandlerFunc(u.HandleRevenueCatWebhook)).Methods("POST")
	apiCreate.Handle("/webhook/stripe", http.HandlerFunc(u.HandleStripeWebhook)).Methods("POST")

	// Websocket routes
	ws.Handle("/notifications", api.Middleware(http.HandlerFunc(HandleNotificationsWebSocket))).Methods("GET")

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

	// seed head admin (optional)
	if err := databases.EnsureHeadAdmin(a.dbHelper); err != nil {
		zap.S().With(err).Error("failed to ensure head admin")
	}

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

// CorsMiddleware is a middleware that adds CORS headers to the response
func CorsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if os.Getenv("ENV") == "local" {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		} else if os.Getenv("ENV") == "development" {
			w.Header().Set("Access-Control-Allow-Origin", "https://police-cad-dev.herokuapp.com")
		} else {
			w.Header().Set("Access-Control-Allow-Origin", "https://www.linespolice-cad.com")
		}

		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Allow-Credentials", "true")

		// Handle preflight requests
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
