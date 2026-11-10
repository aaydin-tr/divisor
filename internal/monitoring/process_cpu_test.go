package monitoring

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func burnOneCPU(duration time.Duration) {
	deadline := time.Now().Add(duration)
	for time.Now().Before(deadline) {
	}
}

func TestProcessCPUPercentReportsTheLatestWindowNotTheLifetimeAverage(t *testing.T) {
	collector := newStatsCollector(fixedConnections(0), &countingBalancer{}, (&osSystemStatsReader{}).ReadSystemStats)
	collector.Snapshot()

	burnOneCPU(minimumProcessCPUWindow + 200*time.Millisecond)
	afterBusyWindow := collector.Snapshot().Cpu.ProcessPercent

	time.Sleep(minimumProcessCPUWindow + 200*time.Millisecond)
	afterIdleWindow := collector.Snapshot().Cpu.ProcessPercent

	assert.Greater(t, afterBusyWindow, 50.0, "a window spent burning one CPU reads as most of one CPU")
	assert.Less(t, afterIdleWindow, 25.0, "an idle window reads near zero, whatever the process burned before it")
}

func TestProcessCPUPercentFirstReadingReportsTheCPUUsedSinceTheProcessStarted(t *testing.T) {
	burnOneCPU(300 * time.Millisecond)
	collector := newStatsCollector(fixedConnections(0), &countingBalancer{}, (&osSystemStatsReader{}).ReadSystemStats)

	assert.Greater(t, collector.Snapshot().Cpu.ProcessPercent, 0.0, "the process has burned CPU since it started")
}

func TestProcessCPUPercentReadSoonAfterAnotherRepeatsIt(t *testing.T) {
	collector := newStatsCollector(fixedConnections(0), &countingBalancer{}, (&osSystemStatsReader{}).ReadSystemStats)
	collector.Snapshot()
	time.Sleep(minimumProcessCPUWindow + 200*time.Millisecond)
	idleReading := collector.Snapshot().Cpu.ProcessPercent

	burnOneCPU(100 * time.Millisecond)
	readingSoonAfter := collector.Snapshot().Cpu.ProcessPercent

	assert.Equal(t, idleReading, readingSoonAfter, "a window shorter than the minimum is not measured on its own")
}
