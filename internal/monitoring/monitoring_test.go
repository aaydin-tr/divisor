package monitoring

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aaydin-tr/divisor/core/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

type countingBalancer struct {
	statsCalls atomic.Int64
}

func (b *countingBalancer) Serve() func(ctx *fasthttp.RequestCtx) {
	return func(*fasthttp.RequestCtx) {}
}
func (b *countingBalancer) Shutdown() error { return nil }
func (b *countingBalancer) Stats() []types.ProxyStat {
	b.statsCalls.Add(1)
	return []types.ProxyStat{{Addr: "backend-a:80", TotalReqCount: 7, IsHostAlive: true}}
}

type fixedConnections int32

func (c fixedConnections) OpenConnectionsCount() int32 { return int32(c) }

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	return ln.Addr().String()
}

func TestSnapshotKeepsLastGoodSystemStatsOnError(t *testing.T) {
	var readShouldFail atomic.Bool
	good := systemStats{Cpu: CPUStats{ProcessPercent: 12.5, TotalPercent: 40}, Memory: MemStats{ProcessPercent: 3, TotalPercent: 55, ProcessMB: 128}}
	read := func() (systemStats, error) {
		if readShouldFail.Load() {
			return systemStats{}, errors.New("gopsutil hiccup")
		}
		return good, nil
	}
	balancer := &countingBalancer{}
	collector := newStatsCollector(fixedConnections(3), balancer, read)

	first := collector.Snapshot()
	assert.Equal(t, good.Cpu, first.Cpu)
	assert.Equal(t, good.Memory, first.Memory)

	readShouldFail.Store(true)
	second := collector.Snapshot()
	assert.Equal(t, good.Cpu, second.Cpu, "a failed read keeps the last good CPU values")
	assert.Equal(t, good.Memory, second.Memory, "a failed read keeps the last good memory values")
	assert.Len(t, second.Backends, 1, "Backend rows never depend on the system stats read")
	assert.Equal(t, int32(3), second.OpenConnectionCount)
	assert.Positive(t, second.TotalGoroutine)
}

func TestSnapshotBeforeAnySuccessfulReadReportsZeroSystemStats(t *testing.T) {
	read := func() (systemStats, error) { return systemStats{}, errors.New("never works") }
	collector := newStatsCollector(fixedConnections(0), &countingBalancer{}, read)

	snapshot := collector.Snapshot()
	assert.Equal(t, CPUStats{}, snapshot.Cpu)
	assert.Len(t, snapshot.Backends, 1)
}

func TestStartServesStatsAndShutdownStopsPolling(t *testing.T) {
	balancer := &countingBalancer{}
	addr := freeAddr(t)
	read := func() (systemStats, error) { return systemStats{Cpu: CPUStats{TotalPercent: 1}}, nil }

	s, err := start(fixedConnections(2), balancer, addr, read)
	require.NoError(t, err)

	res, err := http.Get(fmt.Sprintf("http://%s/stats", addr))
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)
	var body Monitoring
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	assert.Equal(t, "backend-a:80", body.Backends[0].Addr)
	assert.Equal(t, int32(2), body.OpenConnectionCount)
	assert.Equal(t, float64(1), body.Cpu.TotalPercent)

	metrics, err := http.Get(fmt.Sprintf("http://%s/metrics", addr))
	require.NoError(t, err)
	metrics.Body.Close()
	assert.Equal(t, http.StatusOK, metrics.StatusCode)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, s.Shutdown(ctx))
	require.NoError(t, s.Shutdown(ctx), "Shutdown is idempotent")

	callsAfterShutdown := balancer.statsCalls.Load()
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, callsAfterShutdown, balancer.statsCalls.Load(), "the poller must not touch the balancer after Shutdown")

	_, err = http.Get(fmt.Sprintf("http://%s/stats", addr))
	assert.Error(t, err, "the monitoring listener is closed after Shutdown")
}

func TestStartReportsBindFailure(t *testing.T) {
	s, err := start(fixedConnections(0), &countingBalancer{}, "256.0.0.1:1", func() (systemStats, error) { return systemStats{}, nil })
	require.Error(t, err)
	assert.Nil(t, s)
}
