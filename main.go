package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/api/handlers"
	"github.com/linesmerrill/police-cad-api/config"
)

func main() {
	a := handlers.App{}
	config.New()

	a.Initialize(os.Getenv("DB_URI"), os.Getenv("DB_NAME")) //initialize database and router

	port := os.Getenv("PORT")
	baseURL := os.Getenv("BASE_URL")
	zap.S().Infow("police-cad-api is up and running",
		"port", port,
		"url", baseURL,
	)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%v", port), a.Router))
}
