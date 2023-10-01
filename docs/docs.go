// Package docs Lines Police CAD API.
//
// Documentation of Lines Police CAD API.
//
//     Schemes: https
//     BasePath: /
//     Version: 1.0.0
//     Host: https://police-cad-api.herokuapp.com
//
//     Consumes:
//     - application/json
//
//     Produces:
//     - application/json
//
//     Security:
//     - basic
//
//    SecurityDefinitions:
//    basic:
//      type: basic
//
// swagger:meta
package docs

import (
	"github.com/linesmerrill/police-cad-api/models"
)

// swagger:route GET /health health healthEndpointID
// Lists the healthchex of the web service api.
// responses:
//   200: healthResponse

// Shows the current health of the api. true means it is alive, false means it is not.
// swagger:response healthResponse
type healthResponseWrapper struct {
	// in:body
	Body models.HealthCheckResponse
}

// swagger:route GET /api/v1/community/{community_id} community communityByID
// Gets a single community by ID.
// responses:
//   200: communityByIDResponse
//   404: errorMessageResponse

// Shows a single community by the given {community_id}
// swagger:response communityByIDResponse
type communityByIDResponseWrapper struct {
	// in:body
	Body models.Community
}

// swagger:route GET /api/v1/community/{community_id}/{owner_id} community communityByCommunityIDAndOwnerID
// Gets a single community by community ID and owner ID.
// responses:
//   200: communityByCommunityIDAndOwnerIDResponse
//   404: errorMessageResponse

// Shows a single community by the given {community_id} and by owner {owner_id}
// swagger:response communityByCommunityIDAndOwnerIDResponse
type communityByCommunityIDAndOwnerIDResponseWrapper struct {
	// in:body
	Body models.Community
}

// swagger:route GET /api/v1/communities/{owner_id} community communitiesByOwnerID
// Gets all communities by owner ID.
// responses:
//   200: communitiesByOwnerIDResponse
//   404: errorMessageResponse

// Shows all communities by the given owner {owner_id}
// swagger:response communitiesByOwnerIDResponse
type communitiesByOwnerIDResponseWrapper struct {
	// in:body
	Body []models.Community
}

// Error message response
// swagger:response errorMessageResponse
type errorMessageResponseWrapper struct {
	// in:body
	Body models.ErrorMessageResponse
}

// swagger:route GET /api/v1/user/{user_id} user userByID
// Get user by ID.
// responses:
//   200: userByIDResponse

// Shows the user by the given userID {user_id}
// swagger:response userByIDResponse
type userByIDResponseWrapper struct {
	// in:body
	Body models.User
}

// swagger:route GET /api/v1/users/{community_id} user userByCommunityID
// Get all users by community ID.
// responses:
//   200: usersByCommunityIDResponse

// Shows all the users by the given communityID {community_id}
// swagger:response usersByCommunityIDResponse
type usersByCommunityIDResponseWrapper struct {
	// in:body
	Body []models.User
}

// swagger:route GET /api/v1/civilian/{civilian_id} civilian civilianByID
// Get a civilian by civilian ID.
// responses:
//   200: civilianByIDResponse
//   404: errorMessageResponse

// Shows a civilian by the given civilianID {civilian_id}
// swagger:response civilianByIDResponse
type civilianByIDResponseWrapper struct {
	// in:body
	Body models.Civilian
}

// swagger:route GET /api/v1/civilians civilian civilians
// Get all civilians.
// responses:
//   200: civiliansResponse

// Shows all civilians
// swagger:response civiliansResponse
type civiliansResponseWrapper struct {
	// in:body
	Body []models.Civilian
}

// swagger:route GET /api/v1/civilians/user/{user_id} civilian civiliansByUserID
// Get all civilians by userID.
// responses:
//   200: civiliansResponse

// Shows all civilians by userID
// swagger:response civiliansResponse
type civiliansByUserIDResponseWrapper struct {
	// in:body
	Body []models.Civilian
}

// swagger:parameters civiliansByUserID
type civiliansByUserIDParamsWrapper struct {
	// in:query
	ActiveCommunityID string `json:"active_community_id"`
}

// swagger:route GET /api/v1/civilians/search civilian civiliansByNameSearch
// Search civilians by supplied params.
// responses:
//   200: civiliansResponse

// Shows all civilians by search params
// swagger:response civiliansResponse
type civiliansByNameSearchResponseWrapper struct {
	// in:body
	Body []models.Civilian
}

// swagger:parameters civiliansByNameSearch
type civiliansByNameSearchParamsWrapper struct {
	// in:query
	FirstName         string `json:"first_name"`
	LastName          string `json:"last_name"`
	DateOfBirth       string `json:"date_of_birth"`
	ActiveCommunityID string `json:"active_community_id"`
}

// swagger:route GET /api/v1/vehicle/{vehicle_id} vehicle vehicleByID
// Get a vehicle by ID.
// responses:
//   200: vehicleByIDResponse
//   404: errorMessageResponse

// Shows a vehicle by the given vehicle ID {vehicle_id}
// swagger:response vehicleByIDResponse
type vehicleByIDResponseWrapper struct {
	// in:body
	Body models.Vehicle
}

// swagger:route GET /api/v1/vehicles vehicle vehicles
// Get all vehicles.
// responses:
//   200: vehiclesResponse
//   404: errorMessageResponse

// Shows all vehicles
// swagger:response vehiclesResponse
type vehiclesResponseWrapper struct {
	// in:body
	Body []models.Vehicle
}

// swagger:route GET /api/v1/vehicles/user/{user_id} vehicle vehiclesByUserID
// Get all vehicles by userID.
// responses:
//   200: vehiclesResponse

// Shows all vehicles by userID
// swagger:response vehiclesResponse
type vehiclesByUserIDResponseWrapper struct {
	// in:body
	Body []models.Vehicle
}

// swagger:parameters vehiclesByUserID
type vehiclesByUserIDParamsWrapper struct {
	// in:query
	ActiveCommunityID string `json:"active_community_id"`
}

// swagger:route GET /api/v1/vehicles/registered-owner/{registered_owner_id} vehicle vehiclesByRegisteredOwnerID
// Get all vehicles by RegisteredOwnerID.
// responses:
//   200: vehiclesResponse

// Shows all vehicles by RegisteredOwnerID
// swagger:response vehiclesResponse
type vehiclesByRegisteredOwnerIDResponseWrapper struct {
	// in:body
	Body []models.Vehicle
}

// swagger:route GET /api/v1/firearm/{firearm_id} firearm firearmByID
// Get a firearm by ID.
// responses:
//   200: firearmByIDResponse
//   404: errorMessageResponse

// Shows a firearm by the given firearm ID {firearm_id}
// swagger:response firearmByIDResponse
type firearmByIDResponseWrapper struct {
	// in:body
	Body models.Firearm
}

// swagger:route GET /api/v1/firearms firearm firearms
// Get all firearms.
// responses:
//   200: firearmsResponse
//   404: errorMessageResponse

// Shows all firearms.
// swagger:response firearmsResponse
type firearmsResponseWrapper struct {
	// in:body
	Body []models.Firearm
}

// swagger:route GET /api/v1/firearms/user/{user_id} firearm firearmsByUserID
// Get all firearms by userID.
// responses:
//   200: firearmsResponse

// Shows all firearms by userID
// swagger:response firearmsResponse
type firearmsByUserIDResponseWrapper struct {
	// in:body
	Body []models.Firearm
}

// swagger:parameters firearmsByUserID
type firearmsByUserIDParamsWrapper struct {
	// in:query
	ActiveCommunityID string `json:"active_community_id"`
}

// swagger:route GET /api/v1/firearms/registered-owner/{registered_owner_id} firearm firearmsByRegisteredOwnerID
// Get all firearms by RegisteredOwnerID.
// responses:
//   200: firearmsResponse

// Shows all firearms by RegisteredOwnerID
// swagger:response firearmsResponse
type firearmsByRegisteredOwnerIDResponseWrapper struct {
	// in:body
	Body []models.Firearm
}

// swagger:route GET /api/v1/license/{license_id} license licenseByID
// Get a license by ID.
// responses:
//   200: licenseByIDResponse
//   404: errorMessageResponse

// Shows a license by the given license ID {license_id}
// swagger:response licenseByIDResponse
type licenseByIDResponseWrapper struct {
	// in:body
	Body models.License
}

// swagger:route GET /api/v1/licenses license licenses
// Get all licenses.
// responses:
//   200: licensesResponse
//   404: errorMessageResponse

// Shows all licenses.
// swagger:response licensesResponse
type licensesResponseWrapper struct {
	// in:body
	Body []models.License
}

// swagger:route GET /api/v1/licenses/user/{user_id} license licensesByUserID
// Get all licenses by userID.
// responses:
//   200: licensesResponse

// Shows all licenses by userID
// swagger:response licensesResponse
type licensesByUserIDResponseWrapper struct {
	// in:body
	Body []models.License
}

// swagger:parameters licensesByUserID
type licensesByUserIDParamsWrapper struct {
	// in:query
	ActiveCommunityID string `json:"active_community_id"`
}

// swagger:route GET /api/v1/licenses/owner/{owner_id} license licensesByOwnerID
// Get all licenses by OwnerID.
// responses:
//   200: licensesResponse

// Shows all licenses by OwnerID
// swagger:response licensesResponse
type licensesByOwnerIDResponseWrapper struct {
	// in:body
	Body []models.License
}

// swagger:route GET /api/v1/ems/{ems_id} ems emsByID
// Get a ems by ID.
// responses:
//   200: emsByIDResponse
//   404: errorMessageResponse

// Shows an ems by the given ems ID {ems_id}
// swagger:response emsByIDResponse
type emsByIDResponseWrapper struct {
	// in:body
	Body models.Ems
}

// swagger:route GET /api/v1/ems ems ems
// Get all ems.
// responses:
//   200: emsResponse
//   404: errorMessageResponse

// Shows all ems.
// swagger:response emsResponse
type emsResponseWrapper struct {
	// in:body
	Body []models.Ems
}

// swagger:route GET /api/v1/ems/user/{user_id} ems emsByUserID
// Get all ems by userID.
// responses:
//   200: emsResponse

// Shows all ems by userID
// swagger:response emsResponse
type emsByUserIDResponseWrapper struct {
	// in:body
	Body []models.Ems
}

// swagger:parameters emsByUserID
type emsByUserIDParamsWrapper struct {
	// in:query
	ActiveCommunityID string `json:"active_community_id"`
}

// swagger:route GET /api/v1/emsVehicle/{ems_vehicle_id} emsVehicle emsVehicleByID
// Get an emsVehicle by ID.
// responses:
//   200: emsVehicleByIDResponse
//   404: errorMessageResponse

// Shows an emsVehicle by the given emsVehicle ID {ems_vehicle_id}
// swagger:response emsVehicleByIDResponse
type emsVehicleByIDResponseWrapper struct {
	// in:body
	Body models.EmsVehicle
}

// swagger:route GET /api/v1/emsVehicles emsVehicle emsVehicle
// Get all emsVehicles.
// responses:
//   200: emsVehicleResponse
//   404: errorMessageResponse

// Shows all emsVehicles.
// swagger:response emsVehicleResponse
type emsVehicleResponseWrapper struct {
	// in:body
	Body []models.EmsVehicle
}

// swagger:route GET /api/v1/emsVehicles/user/{user_id} emsVehicle emsVehicleByUserID
// Get all emsVehicles by userID.
// responses:
//   200: emsVehicleResponse

// Shows all emsVehicles by userID
// swagger:response emsVehicleResponse
type emsVehiclesByUserIDResponseWrapper struct {
	// in:body
	Body []models.EmsVehicle
}

// swagger:parameters emsVehicleByUserID
type emsVehicleByUserIDParamsWrapper struct {
	// in:query
	ActiveCommunityID string `json:"active_community_id"`
}

// swagger:route GET /api/v1/call/{call_id} call callByID
// Get a call by ID.
// responses:
//   200: callByIDResponse
//   404: errorMessageResponse

// Shows a call by the given call ID {call_id}
// swagger:response callByIDResponse
type callByIDResponseWrapper struct {
	// in:body
	Body models.Call
}

// swagger:route GET /api/v1/calls call call
// Get all calls.
// responses:
//   200: callResponse
//   404: errorMessageResponse

// Shows all calls.
// swagger:response callResponse
type callResponseWrapper struct {
	// in:body
	Body []models.Call
}

// swagger:route GET /api/v1/calls/community/{community_id} call callByCommunityID
// Get all calls by communityID.
// responses:
//   200: callResponse

// Shows all calls by communityID
// swagger:response callResponse
type callsByCommunityIDResponseWrapper struct {
	// in:body
	Body []models.Call
}

// swagger:parameters callByCommunityID
type callByCommunityIDParamsWrapper struct {
	// in:query
	Status bool `json:"status"`
}

// swagger:route GET /api/v1/warrant/{warrant_id} warrant warrantByID
// Get a warrant by ID.
// responses:
//   200: warrantByIDResponse
//   404: errorMessageResponse

// Shows a warrant by the given warrant ID {warrant_id}
// swagger:response warrantByIDResponse
type warrantByIDResponseWrapper struct {
	// in:body
	Body models.Warrant
}

// swagger:route GET /api/v1/warrants warrant warrants
// Get all warrants.
// responses:
//   200: warrantsResponse
//   404: errorMessageResponse

// Shows all warrants.
// swagger:response warrantsResponse
type warrantsResponseWrapper struct {
	// in:body
	Body []models.Warrant
}

// swagger:route GET /api/v1/warrants/user/{user_id} warrant warrantsByUserID
// Get all warrants by userID.
// responses:
//   200: warrantsResponse

// Shows all warrants by userID
// swagger:response warrantsResponse
type warrantsByUserIDResponseWrapper struct {
	// in:body
	Body []models.Warrant
}

// swagger:parameters warrantsByUserID
type warrantsByUserIDParamsWrapper struct {
	// in:query
	Page string `json:"page"`
}
