package config

import (
	"fmt"
	"net/http"
	"os"

	"go.uber.org/zap"
)

// Config holds the project config values
type Config struct {
	Url          string
	DatabaseName string
	BaseUrl      string
	Port         string
}

// New sets up all config related services
func New() *Config {

	//setup zap logger and replace default logger
	logger := zap.NewExample()
	defer logger.Sync()
	_ = zap.ReplaceGlobals(logger)

	return &Config{
		Url:          os.Getenv("DB_URI"),
		DatabaseName: os.Getenv("DB_NAME"),
		BaseUrl:      os.Getenv("BASE_URL"),
		Port:         os.Getenv("PORT"),
	}

}

// ErrorStatus is a useful function that will log, write http headers and body for a
// give message, status code and err
func ErrorStatus(message string, httpStatusCode int, w http.ResponseWriter, err error) {
	zap.S().With(err).Error(message)
	w.WriteHeader(httpStatusCode)
	w.Write([]byte(fmt.Sprintf(`{"response": "%s, %v"}`, message, err)))
	return
}
