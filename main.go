package main

import (
	"log"
	"net/http"

	"github.com/linesmerrill/police-cad-api/api/handlers"

	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/config"
)

func main() {
	a := handlers.App{}
	a.Config = *config.New()

	a.Initialize() //initialize database and router

	zap.S().Infow("police-cad-api is up and running", "url", a.Config.BaseUrl, "port", a.Config.Port)
	log.Fatal(http.ListenAndServe(":"+a.Config.Port, a.Router))
}
