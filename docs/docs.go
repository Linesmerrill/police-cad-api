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
	"github.com/linesmerrill/police-cad-api/api/handlers"
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
	Body handlers.HealthCheckResponse
}

// swagger:route GET /api/v1/community community communityByID
// Gets a single community by ID.
// responses:
//   200: communityByIDResponse

// Shows a single community by the given {ID}
// swagger:response communityByIDResponse
type communityByIDResponseWrapper struct {
	// in:body
	Body models.Community
}
