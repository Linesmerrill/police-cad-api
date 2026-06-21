package main

import (
	"log"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/api/handlers"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"

	_ "github.com/linesmerrill/police-cad-api/docs" // This line is necessary for go-swagger to find the docs
)

func main() {
	// Initialize metrics collection (10k traces, 1 hour window)
	api.InitMetrics(10000, 1*time.Hour)
	// Register DB query recorder to avoid import cycles
	databases.SetDBQueryRecorder(api.RecordDBQueryFromContext)
	zap.S().Info("Metrics collection initialized")

	a := handlers.App{}
	a.Config = *config.New()

	err := a.Initialize() // initialize database and router
	if err != nil {
		zap.S().With(err).Error("error calling initialize")
		return
	}

	// Start the content creator scheduler for background jobs
	if a.Scheduler != nil {
		a.Scheduler.Start()
		defer a.Scheduler.Stop()
	}

	// Middleware chain (outermost first): gateway → CORS → write-auth → metrics.
	// The gateway rejects random direct callers; write-auth rejects
	// unauthenticated mutations. Both are fail-open until their env flags are set.
	handler := api.MetricsMiddleware(a.Router)
	handler = api.RequireWriteAuth(handler)
	handler = handlers.CorsMiddleware(handler)
	handler = handlers.ApiKeyGateway(handler)

	// Configure HTTP server with timeouts to prevent resource exhaustion
	server := &http.Server{
		Addr:         ":" + a.Config.Port,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,  // Maximum time to read request
		WriteTimeout: 30 * time.Second,  // Maximum time to write response
		IdleTimeout:  120 * time.Second, // Maximum time to wait for next request
	}

	zap.S().Infow("police-cad-api is up and running", "url", a.Config.BaseURL, "port", a.Config.Port)
	log.Fatal(server.ListenAndServe())
}
