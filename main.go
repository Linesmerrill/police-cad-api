package main

import (
	"log"
	"net/http"

	"github.com/linesmerrill/police-cad-api/api"

	"github.com/linesmerrill/police-cad-api/config"

	"github.com/linesmerrill/police-cad-api/logging"
)

func main() {
	logger := logging.New()

	p := config.Processor{Logger: logger}
	r := api.New(p)

	logger.Info("police-cad-api is up and running on port 8081")
	err := http.ListenAndServe("localhost:8081", r)
	if err != nil {
		log.Fatal(err)
	}

}
