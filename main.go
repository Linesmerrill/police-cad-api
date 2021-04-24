package main

import (
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

	zap.S().Info("police-cad-api is up and running on port 8081")
	log.Fatal(http.ListenAndServe("localhost:8081", a.Router))
}
