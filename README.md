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
3. Attach the jwt header as an `Authorization: Bearer` token. See [How to attach an Auth Bearer Token](#how-to-attach-an-auth-bearer-token) for more details.
4. MongoDB installed. See [download and install MongoDB](https://docs.mongodb.com/manual/installation/) to get started.

## Pre-requisites

1. MongoDB installed and running on your local machine.
```bash
brew install mongodb-community@5.0
```

2. Start MongoDB service.
```bash
brew services start mongodb-community@5.0
```

## Run


### Full Setup (All Features)
1. Copy the comprehensive environment file:
```bash
cp env.example .env
```

2. Configure all services (Stripe, SendGrid, Cloudinary) in `.env`

3. Run the application:
```bash
make run
```

### Environment Variables
- **Original**: Use `.env.example` for the original basic configuration

## Generate Mocks before committing code

```
make mocks
```

## Rest Request Examples:

Import our Postman collection here: [Postman Collection](https://github.com/Linesmerrill/police-cad-api/blob/master/docs/postman/police-cad-api.postman_collection.json)

## Docs

To build the docs run:

```
make swagger
```

To run the docs locally:

```
make serve-swagger
```

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

## How to attach an Auth Bearer Token

Attach a header with this [JWT](#how-to-generate-a-jwt) created with the 256-bit-secret to your api requests

**Example:** 

```
Authorization:Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c
```