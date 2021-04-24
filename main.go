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
	zap.S().Infof("police-cad-api is up and running on port %v", port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf("%v:%v", baseURL, port), a.Router))
}
