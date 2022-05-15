# Police CAD API

[![Actions Status](https://github.com/linesmerrill/police-cad-api/actions/workflows/go.yml/badge.svg)](https://github.com/linesmerrill/police-cad-api/actions)
[![codecov](https://codecov.io/gh/linesmerrill/police-cad-api/branch/master/graph/badge.svg)](https://codecov.io/gh/linesmerrill/police-cad-api)

This is as a simple backend API to serve all routes associated with
the official [Lines Police CAD](https://github.com/Linesmerrill/police-cad).

## Routes

To view the routes, check our swagger [here](https://police-cad-api.herokuapp.com/)

## Requirements

1. Go 1.17+ installed. See [download and install Go](https://go.dev/doc/install) to get started.
2. Generate jwt token to communicate with front-end web server. See [How to Generate a JWT](#how-to-generate-a-jwt) for more details.
## Run

1. Duplicate `.env.example` and rename the new file to `.env`. Edit to your configurations.
2. `$ make run`

## Generate Mocks before committing code

`$ make mocks`

## Rest Request Examples:

Import our Postman collection here: [Postman Collection](https://github.com/Linesmerrill/police-cad-api/blob/master/docs/postman/police-cad-api.postman_collection.json)

## Docs

To build the docs run:

`$ make swagger`

To run the docs locally:

`$ make serve-swagger`

To update the docs navigate to `/docs/docs.go` and follow along with the swagger annotations and comments to
document your routes and parameters.

## How to Generate a JWT

I recommend using [https://jwt.io/](https://jwt.io/) to easily generate jwt's for your applications.

**Header:** (you can leave this the same)

```
{
  "alg": "HS256",
  "typ": "JWT"
}
```

**Payload:** (you can modify this to be an empty object)

```
{}
```

**Verify Signature:** (generate a 256 random alphanumeric key and paste in here)

We recommend visiting [https://passwordsgenerator.net/](https://passwordsgenerator.net/) and selecting the following:

1. Password Length: `256`
2. Include Numbers: ✅
3. Include Lowercase Characters: ✅ 
4. Include Uppercase Characters: ✅
5. Generate On Your Device: ✅
6. Click Generate and copy the password back into [https://jwt.io/](https://jwt.io/)

This 256-bit-secret will also be used to decrypt/encrypt the jwt in `police-cad-api` environment variables

**Encoded:** (The encoded JWT you can use to now communicate between the backend and frontend)

Copy and paste this into your environment variables in `police-cad` application.