package main

import (
	"log"
	"net/http"

	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/api/handlers"
	"github.com/linesmerrill/police-cad-api/config"

	_ "github.com/linesmerrill/police-cad-api/docs" // This line is necessary for go-swagger to find the docs
)

func main() {
	a := handlers.App{}
	a.Config = *config.New()

	err := a.Initialize() //initialize database and router
	if err != nil {
		zap.S().With(err).Error("error calling initialize")
		return
	}

	zap.S().Infow("police-cad-api is up and running", "url", a.Config.BaseURL, "port", a.Config.Port)
	log.Fatal(http.ListenAndServe(":"+a.Config.Port, a.Router))
}
