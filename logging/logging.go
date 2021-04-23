package logging

import "go.uber.org/zap"

// New creates a new zap logger
func New() *zap.SugaredLogger {
	logger, _ := zap.NewProduction()
	defer logger.Sync()
	return logger.Sugar()
}
