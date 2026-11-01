package models

import (
	"os"
	"strings"
)

// DeveloperAPIBaseURLEnv overrides the public base URL the docs and the console
// tell developers to call.
const DeveloperAPIBaseURLEnv = "DEVELOPER_API_PUBLIC_BASE_URL"

// defaultDeveloperAPIBaseURL is the public hostname for the developer API.
//
// This had no DNS record when the documentation was written, so every example
// in the quickstart failed to resolve; Hark has since added the Heroku custom
// domain and a proxied Cloudflare CNAME for it. The value lives here, once,
// precisely so that a move costs one line rather than a hunt through five
// documents — which is how it came to be wrong in the first place.
//
// If it ever has to change again, set DEVELOPER_API_PUBLIC_BASE_URL rather
// than editing this, and nothing else needs touching.
const defaultDeveloperAPIBaseURL = "https://api.linespolice-cad.com"

// DeveloperAPIBaseURL is the origin developers should call, with no trailing
// slash. One source for the docs, the OpenAPI servers list and the console.
func DeveloperAPIBaseURL() string {
	if v := strings.TrimSpace(os.Getenv(DeveloperAPIBaseURLEnv)); v != "" {
		return strings.TrimRight(v, "/")
	}
	return defaultDeveloperAPIBaseURL
}

// DeveloperAPIURL is the full prefix for the developer surface, e.g.
// https://host/api/v1/dev
func DeveloperAPIURL() string {
	return DeveloperAPIBaseURL() + "/api/v1/dev"
}

// DeveloperAPIAuthHeader is the one header the middleware reads, named here so
// the documentation and the code cannot drift.
//
// The console used to tell developers to send "X-Developer-Key", which this API
// has never accepted.
const DeveloperAPIAuthHeader = "Authorization"

// DeveloperAPIAuthScheme is how the key is presented in that header.
const DeveloperAPIAuthScheme = "Bearer"
