package config

import (
	"go.mongodb.org/mongo-driver/mongo"
	"go.uber.org/zap"
)

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
