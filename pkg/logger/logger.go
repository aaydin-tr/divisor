package logger

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func InitLogger(logFile string) {
	config := zap.Config{
		Level:       zap.NewAtomicLevelAt(zap.InfoLevel),
		Development: false,
		Encoding:    "console",
		EncoderConfig: zapcore.EncoderConfig{
			TimeKey:        "T",
			LevelKey:       "L",
			NameKey:        "N",
			CallerKey:      "C",
			FunctionKey:    zapcore.OmitKey,
			MessageKey:     "M",
			LineEnding:     zapcore.DefaultLineEnding,
			EncodeLevel:    zapcore.CapitalLevelEncoder,
			EncodeTime:     zapcore.ISO8601TimeEncoder,
			EncodeDuration: zapcore.StringDurationEncoder,
		},
		OutputPaths:      []string{logFile, "stdout"},
		ErrorOutputPaths: []string{logFile, "stdout"},
	}

	logger, err := config.Build()
	if err != nil {
		// The file sink could not be opened (e.g. /var/log/divisor/ owned by
		// another user); keep serving with stdout-only logging rather than
		// handing zap a nil logger.
		config.OutputPaths = []string{"stdout"}
		config.ErrorOutputPaths = []string{"stdout"}
		logger, fallbackErr := config.Build()
		if fallbackErr != nil {
			logger = zap.NewNop()
		}
		zap.ReplaceGlobals(logger)
		zap.S().Warnf("Could not open log file %s, logging to stdout only: %v", logFile, err)
		return
	}
	zap.ReplaceGlobals(logger)
}
