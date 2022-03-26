# Police CAD API

[![Actions Status](https://github.com/linesmerrill/police-cad-api/actions/workflows/go.yml/badge.svg)](https://github.com/linesmerrill/police-cad-api/actions)
[![codecov](https://codecov.io/gh/linesmerrill/police-cad-api/branch/master/graph/badge.svg)](https://codecov.io/gh/linesmerrill/police-cad-api)

This is as a simple backend API to serve all routes associated with
the official [Lines Police CAD](https://github.com/Linesmerrill/police-cad).

## Routes

To view the routes, check our swagger [here](https://police-cad-api.herokuapp.com/)

## Generate Mocks before committing code

`$ make mocks`

## Run

1. Duplicate `.env.example` and rename the new file to `.env`. Edit to your configurations.
2. `$ make run`

## Rest Request Examples:

Import our Postman collection here: [Postman Collection](https://github.com/Linesmerrill/police-cad-api/blob/master/docs/postman/police-cad-api.postman_collection.json)

## Docs

To build the docs run:

`$ make swagger`

To run the docs locally:

`$ make serve-swagger`

To update the docs navigate to `/docs/docs.go` and follow along with the swagger annotations and comments to
document your routes and parameters