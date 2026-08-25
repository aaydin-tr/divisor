package logger

import (
	"io"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func discardAccessLogger(b *testing.B) {
	b.Helper()
	encoderConfig := zapcore.EncoderConfig{TimeKey: "time", EncodeTime: zapcore.ISO8601TimeEncoder}
	core := zapcore.NewCore(zapcore.NewJSONEncoder(encoderConfig), zapcore.AddSync(io.Discard), zapcore.InfoLevel)
	b.Cleanup(ReplaceAccessLogger(zap.New(core)))
}

// The per-request emit cost with the Access log on (field building + JSON
// encoding, I/O discarded).
func BenchmarkLogAccess(b *testing.B) {
	discardAccessLogger(b)
	entry := fullAccessLogEntry()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		LogAccess(entry)
	}
}

func BenchmarkLogAccessParallel(b *testing.B) {
	discardAccessLogger(b)
	entry := fullAccessLogEntry()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			LogAccess(entry)
		}
	})
}
