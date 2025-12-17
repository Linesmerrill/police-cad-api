package main

import (
	"log"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/api/handlers"
	"github.com/linesmerrill/police-cad-api/config"

	_ "github.com/linesmerrill/police-cad-api/docs" // This line is necessary for go-swagger to find the docs
)

func main() {
	a := handlers.App{}
	a.Config = *config.New()

	err := a.Initialize() // initialize database and router
	if err != nil {
		zap.S().With(err).Error("error calling initialize")
		return
	}

	// Configure HTTP server with timeouts to prevent resource exhaustion
	server := &http.Server{
		Addr:         ":" + a.Config.Port,
		Handler:      handlers.CorsMiddleware(a.Router),
		ReadTimeout:  30 * time.Second,  // Maximum time to read request
		WriteTimeout: 30 * time.Second,  // Maximum time to write response
		IdleTimeout:  120 * time.Second, // Maximum time to wait for next request
	}

	zap.S().Infow("police-cad-api is up and running", "url", a.Config.BaseURL, "port", a.Config.Port)
	log.Fatal(server.ListenAndServe())
}
