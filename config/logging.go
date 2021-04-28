package config

import (
	"fmt"
	"net/http"

	"go.mongodb.org/mongo-driver/mongo"
	"go.uber.org/zap"
)

// Config holds the project config values
type Config struct {
	MongoClient mongo.Client
}

// New sets up all config related services
func New() {

	//setup zap logger and replace default logger
	logger := zap.NewExample()
	defer logger.Sync()
	_ = zap.ReplaceGlobals(logger)
}

// ErrorStatus is a useful function that will log, write http headers and body for a
// give message, status code and err
func ErrorStatus(message string, httpStatusCode int, w http.ResponseWriter, err error) {
	zap.S().With(err).Error(message)
	w.WriteHeader(httpStatusCode)
	w.Write([]byte(fmt.Sprintf(`{"response": "%s, %v"}`, message, err)))
	return
}
