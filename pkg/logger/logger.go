package logger

import (
	"fmt"

	"github.com/aaydin-tr/balancer/pkg/helper"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func InitLogger() *zap.Logger {
	logFile := helper.GetLogFile()

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
			StacktraceKey:  "S",
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
		fmt.Println(err)
		return nil
	}
	zap.ReplaceGlobals(logger)

	return logger
}
