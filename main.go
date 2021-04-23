package main

import (
	"log"
	"net/http"

	"github.com/linesmerrill/police-cad-api/logging"

	"github.com/linesmerrill/police-cad-api/api"
)

func main() {
	r := api.New()
	sugar := logging.New()

	sugar.Info("police-cad-api is up and running on port 8080")
	err := http.ListenAndServe("localhost:8080", r)
	if err != nil {
		log.Fatal(err)
	}

}
