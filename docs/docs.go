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

// swagger:route GET /api/v1/name-search name-search nameSearchID
// Get a civilian by firstname, lastname, date-of-birth and communityID.
// responses:
//   200: nameSearchResponse

// Shows a civilian by the given firstname, lastname, date-of-birth and communityID
// swagger:response nameSearchResponse
type nameSearchResponseWrapper struct {
	// in:body
	Body []models.Civilian
}

// swagger:parameters nameSearchID
type nameSearchParamsWrapper struct {
	// in:query
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name"`
	DateOfBirth string `json:"dob"`
	CommunityID string `json:"community_id"`
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
