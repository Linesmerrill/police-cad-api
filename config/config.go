package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"

	"go.mongodb.org/mongo-driver/mongo"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/models"
)

// Config holds the project config values
type Config struct {
	URL          string
	DatabaseName string
	BaseURL      string
	Port         string
}

// New sets up all config related services
func New() *Config {

	// setup zap logger and replace default logger
	logger, err := setLogger(os.Getenv("LOG_LEVEL"))
	if err != nil {
		// if we get an error, we will just set the default to debug and move on
		zap.S().With(err).Warn("issue setting logger")
	}
	defer logger.Sync()
	_ = zap.ReplaceGlobals(logger)

	return &Config{
		URL:          os.Getenv("DB_URI"),
		DatabaseName: os.Getenv("DB_NAME"),
		BaseURL:      os.Getenv("BASE_URL"),
		Port:         os.Getenv("PORT"),
	}

}

// InfoStatus logs at info level and writes an HTTP error response.
// Use this for expected conditions that are not actual errors (e.g., resource not found).
func InfoStatus(message string, httpStatusCode int, w http.ResponseWriter, err error) {
	if err != nil {
		zap.S().Infow(message, "info", err)
	} else {
		zap.S().Info(message)
	}
	w.WriteHeader(httpStatusCode)
	var errorMsg string
	if err != nil {
		errorMsg = safeClientError(err)
	} else {
		errorMsg = message
	}
	b, _ := json.Marshal(models.ErrorMessageResponse{Response: models.MessageError{Message: message, Error: errorMsg}})
	w.Write(b)
	return
}

// ErrorStatus is a useful function that will log, write http headers and body for a
// give message, status code and err
func ErrorStatus(message string, httpStatusCode int, w http.ResponseWriter, err error) {
	if err != nil {
		zap.S().Errorw(message, "error", err)
	} else {
		zap.S().Error(message)
	}
	w.WriteHeader(httpStatusCode)
	var errorMsg string
	if err != nil {
		errorMsg = safeClientError(err)
	} else {
		errorMsg = message
	}
	b, _ := json.Marshal(models.ErrorMessageResponse{Response: models.MessageError{Message: message, Error: errorMsg}})
	w.Write(b)
	return
}

// safeClientError returns a client-safe representation of err. Raw MongoDB
// driver errors embed server-side internals — the database name, collection,
// index names, and document IDs — which must never reach an API client. Those
// are scrubbed down to a generic string here; the full error is still logged
// server-side by the caller (ErrorStatus/InfoStatus log via zap before calling
// this). Application errors built with fmt.Errorf carry no internals and pass
// through unchanged so callers keep their human-readable messages.
func safeClientError(err error) string {
	if err == nil {
		return ""
	}
	if mongo.IsDuplicateKeyError(err) {
		return "resource already exists"
	}
	var writeErr mongo.WriteException
	var cmdErr mongo.CommandError
	var bulkErr mongo.BulkWriteException
	if errors.As(err, &writeErr) || errors.As(err, &cmdErr) || errors.As(err, &bulkErr) {
		return "a database error occurred"
	}
	return err.Error()
}

// setLogger is a helper function to set the logger based on the environment
func setLogger(env string) (*zap.Logger, error) {
	if env == "production" {
		return zap.NewProduction()
	} else if env == "development" {
		return zap.NewDevelopment()
	} else if env == "local" {
		return zap.NewExample(), nil
	} else {
		return zap.NewExample(), fmt.Errorf("cannot find ENV var so defaulting to debug level logging")
	}
}
