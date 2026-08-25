package logger

import (
	"github.com/aaydin-tr/divisor/pkg/config"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// InitDefaultLogger installs the production default (JSON at info) so
// failures before the config is parsed and validated are still reported as
// structured lines.
func InitDefaultLogger() {
	InitLogger(config.Logging{Format: config.DefaultLoggingFormat, Level: config.DefaultLoggingLevel})
}

// InitLogger replaces the process-global logger (zap.S()) with one built from
// the validated logging config. Application logs go to stderr only: stdout is
// reserved for the Access log, so each stream stays homogeneous and can be
// routed separately.
func InitLogger(logging config.Logging) {
	// PrepareConfig already rejected an unparseable level; this fallback is
	// defensive only, like the encoding fallback below.
	level, err := zapcore.ParseLevel(logging.Level)
	if err != nil {
		level = zapcore.InfoLevel
	}

	zapConfig := zap.Config{
		Level:            zap.NewAtomicLevelAt(level),
		Encoding:         logging.Format,
		EncoderConfig:    encoderConfigFor(logging.Format),
		OutputPaths:      []string{"stderr"},
		ErrorOutputPaths: []string{"stderr"},
	}

	logger, err := zapConfig.Build()
	if err != nil {
		// Only an unvalidated format can fail the build; the default encoding
		// with a stderr sink cannot.
		zapConfig.Encoding = config.DefaultLoggingFormat
		zapConfig.EncoderConfig = encoderConfigFor(config.DefaultLoggingFormat)
		logger, _ = zapConfig.Build()
	}
	zap.ReplaceGlobals(logger)
}

func encoderConfigFor(format string) zapcore.EncoderConfig {
	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        "time",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		FunctionKey:    zapcore.OmitKey,
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.StringDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}
	if format == config.LoggingFormatConsole {
		encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder
	}
	return encoderConfig
}
