package handlers

import (
	"crypto/subtle"
	"net/http"
	"os"
	"strings"

	"go.uber.org/zap"
)

// apiGatewayKeyEnv is the name of the Heroku config var that holds the shared
// secret. When it is empty the gateway is disabled (fail-open) so deploying the
// code changes nothing until the secret is actually configured.
const apiGatewayKeyEnv = "API_GATEWAY_KEY"

// apiGatewayHeader is the header clients send carrying the shared secret.
const apiGatewayHeader = "X-API-Key"

// allowedGatewayOrigins are the web origins that are permitted to call the API
// directly from the browser without the shared secret. Browser traffic from our
// own website always carries one of these as the Origin/Referer, so we don't
// have to expose the secret in client-side JavaScript.
var allowedGatewayOrigins = []string{
	"https://www.linespolice-cad.com",
	"https://linespolice-cad.com",
	"https://police-cad-dev.herokuapp.com",
	"http://localhost:8080",
	"http://localhost:3000",
	"http://127.0.0.1:8080",
}

// blockedClientMarkers are substrings (lower-cased) that identify generic HTTP
// clients / scripting tools used by "random" direct callers. A request whose
// User-Agent matches one of these is rejected unless it presents the shared
// secret or comes from one of our own origins. This is intentionally a
// best-effort, easily-spoofable check — it is the "quick and dirty" part 1
// lockdown to stop casual/automated direct access. The real per-client auth is
// part 2.
var blockedClientMarkers = []string{
	"curl/",
	"wget/",
	"python-requests",
	"python-urllib",
	"go-http-client",
	"java/",
	"libwww-perl",
	"ruby",
	"httpie",
	"postman",
	"insomnia",
	"scrapy",
	"node-fetch",
	"axios/",
	"got (",
	"aiohttp",
}

// ApiKeyGateway is the outermost middleware. It blocks direct API access from
// random users while letting our own website and mobile app through.
//
// A request is allowed when ANY of the following is true:
//   - the gateway is disabled (API_GATEWAY_KEY unset) — fail-open;
//   - it is a CORS preflight (OPTIONS) or a health check;
//   - it presents the correct shared secret in the X-API-Key header
//     (server-to-server calls from the website backend);
//   - it originates from one of our own web origins (browser traffic from the
//     website — Origin/Referer based, so the secret never has to ship to the
//     browser);
//   - it does NOT look like a browser or a generic scripting tool. This is the
//     "exempt the mobile app" path: the published mobile app sends no secret and
//     can't be updated instantly, so anything that isn't clearly a browser or a
//     known CLI/script tool is allowed through (default-allow) to guarantee the
//     app keeps working until the part-2 fix.
//
// Everything else (browsers on other origins, curl/python/postman/etc. without
// the secret) gets a 403.
func ApiKeyGateway(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := os.Getenv(apiGatewayKeyEnv)

		// Fail-open: until the secret is configured in Heroku, do nothing.
		if key == "" {
			next.ServeHTTP(w, r)
			return
		}

		// Always allow CORS preflight and health checks.
		if r.Method == http.MethodOptions || isHealthPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		// 1. Shared secret (website backend, and any future first-party client).
		if provided := r.Header.Get(apiGatewayHeader); provided != "" {
			if subtle.ConstantTimeCompare([]byte(provided), []byte(key)) == 1 {
				next.ServeHTTP(w, r)
				return
			}
			// A wrong key is a hard reject — don't fall through to the lenient
			// browser/mobile heuristics.
			zap.S().Warnw("api gateway: invalid X-API-Key", "path", r.URL.Path, "ua", r.Header.Get("User-Agent"))
			gatewayForbidden(w)
			return
		}

		// 2. Browser traffic from our own website (no secret needed).
		if originAllowed(r) {
			next.ServeHTTP(w, r)
			return
		}

		// 3. Reject anything that looks like a browser or a generic scripting
		//    tool. Mobile-app traffic (and other unknown non-browser clients)
		//    falls through and is allowed — see the doc comment above.
		if looksLikeBrowserOrTool(r.Header.Get("User-Agent")) {
			zap.S().Warnw("api gateway: blocked direct access",
				"path", r.URL.Path,
				"ua", r.Header.Get("User-Agent"),
				"origin", r.Header.Get("Origin"))
			gatewayForbidden(w)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func isHealthPath(path string) bool {
	return path == "/health" || strings.HasPrefix(path, "/health/")
}

// originAllowed reports whether the request comes from one of our own web
// origins, checking both the Origin header (set on cross-origin browser fetches)
// and the Referer (fallback for navigations / older browsers).
func originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin != "" {
		for _, o := range allowedGatewayOrigins {
			if origin == o {
				return true
			}
		}
		return false
	}
	if referer := r.Header.Get("Referer"); referer != "" {
		for _, o := range allowedGatewayOrigins {
			if strings.HasPrefix(referer, o+"/") || referer == o {
				return true
			}
		}
	}
	return false
}

// looksLikeBrowserOrTool returns true for browsers (any UA containing "Mozilla")
// and for the common scripting/HTTP-client tools listed in blockedClientMarkers.
// The mobile app's User-Agent (e.g. CFNetwork/Darwin on iOS, okhttp on Android)
// is deliberately NOT treated as a browser or tool, so it falls through and is
// allowed. We can't distinguish the app from a generic okhttp/CFNetwork client
// without an app change, so we err on the side of keeping the app working.
func looksLikeBrowserOrTool(ua string) bool {
	if ua == "" {
		// Empty UA is unusual for both browsers and our clients; block it since
		// random scripts frequently send no UA.
		return true
	}
	lua := strings.ToLower(ua)

	// Browsers always send a Mozilla-prefixed UA.
	if strings.Contains(lua, "mozilla") {
		return true
	}

	for _, marker := range blockedClientMarkers {
		if strings.Contains(lua, marker) {
			return true
		}
	}
	return false
}

func gatewayForbidden(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte(`{"error":"forbidden","message":"Direct API access is not allowed. This API is restricted to the Lines Police CAD website and mobile app."}`))
}
