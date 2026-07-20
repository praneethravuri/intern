package logger

import (
	"go.uber.org/zap"
)

func InitLogger() (*zap.SugaredLogger, func()) {
	zapLogger, _ := zap.NewDevelopment()

	sugar := zapLogger.Sugar()

	cleanup := func() {
		_ = zapLogger.Sync()
	}

	return sugar, cleanup
}
