package logger

import (
	"os"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// The Access log is a machine stream: always JSON on stdout, regardless of
// logging.format (.scratch/logging/spec.md).
var accessLogger atomic.Pointer[zap.Logger]

func init() {
	accessLogger.Store(zap.NewNop())
}

const accessLogTimeLayout = "2006-01-02T15:04:05.000Z07:00"

// AccessLogEntry is one answered request. A zero Backend means no Backend was
// involved (the zero-Alive 503 path): backend and request_seq are omitted.
type AccessLogEntry struct {
	ClientIP        string
	Method          string
	Path            string
	Status          int
	Backend         string
	Duration        time.Duration
	BytesOut        int
	RequestSequence uint64
	ShortCircuit    bool
}

// InitAccessLogger installs the Access log's stdout core, or a no-op core
// when the config leaves it off, so the request path pays only
// AccessLogEnabled's check.
func InitAccessLogger(enabled bool) {
	if !enabled {
		accessLogger.Store(zap.NewNop())
		return
	}

	encoderConfig := zapcore.EncoderConfig{
		TimeKey:    "time",
		LevelKey:   zapcore.OmitKey,
		NameKey:    zapcore.OmitKey,
		CallerKey:  zapcore.OmitKey,
		MessageKey: zapcore.OmitKey,
		LineEnding: zapcore.DefaultLineEnding,
		EncodeTime: func(t time.Time, encoder zapcore.PrimitiveArrayEncoder) {
			encoder.AppendString(t.UTC().Format(accessLogTimeLayout))
		},
	}
	core := zapcore.NewCore(zapcore.NewJSONEncoder(encoderConfig), zapcore.Lock(os.Stdout), zapcore.InfoLevel)
	accessLogger.Store(zap.New(core))
}

// ReplaceAccessLogger swaps in a caller-built logger (tests observe the
// Access log through zap cores) and returns a restore func.
func ReplaceAccessLogger(logger *zap.Logger) func() {
	previous := accessLogger.Swap(logger)
	return func() { accessLogger.Store(previous) }
}

func AccessLogEnabled() bool {
	return accessLogger.Load().Core().Enabled(zapcore.InfoLevel)
}

func LogAccess(entry AccessLogEntry) {
	fields := make([]zap.Field, 0, 9)
	fields = append(fields,
		zap.String("client_ip", entry.ClientIP),
		zap.String("method", entry.Method),
		zap.String("path", entry.Path),
		zap.Int("status", entry.Status),
	)
	if entry.Backend != "" {
		fields = append(fields,
			zap.String("backend", entry.Backend),
			zap.Uint64("request_seq", entry.RequestSequence),
		)
	}
	fields = append(fields,
		zap.Float64("duration_ms", float64(entry.Duration)/float64(time.Millisecond)),
		zap.Int("bytes_out", entry.BytesOut),
	)
	if entry.ShortCircuit {
		fields = append(fields, zap.Bool("short_circuit", true))
	}
	accessLogger.Load().Info("", fields...)
}
