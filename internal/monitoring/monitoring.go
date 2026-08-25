package monitoring

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aaydin-tr/divisor/core/types"
	"github.com/fasthttp/router"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
	gopsutilProcess "github.com/shirou/gopsutil/v4/process"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttpadaptor"
	"github.com/valyala/fasthttp/reuseport"
	"go.uber.org/zap"
)

const prometheusUpdateInterval = 5 * time.Second

type Monitoring struct {
	Backends            []types.ProxyStat `json:"backends"`
	Memory              MemStats          `json:"memory"`
	Cpu                 CPUStats          `json:"cpu"`
	TotalGoroutine      int               `json:"total_goroutine"`
	OpenConnectionCount int32             `json:"open_conn_count"`
}

type CPUStats struct {
	ProcessPercent float64 `json:"process_percent"`
	TotalPercent   float64 `json:"total_percent"`
}

type MemStats struct {
	ProcessPercent float32 `json:"process_percent"`
	TotalPercent   float64 `json:"total_percent"`
	ProcessMB      float64 `json:"process_mb"`
}

// OpenConnectionsCounter is the one thing monitoring needs from the running
// server, whichever stack serves it.
type OpenConnectionsCounter interface {
	OpenConnectionsCount() int32
}

// systemStats is the part of a snapshot read from the OS through gopsutil,
// the only part that can fail.
type systemStats struct {
	Cpu    CPUStats
	Memory MemStats
}

type systemStatsReader func() (systemStats, error)

// statsCollector assembles Monitoring snapshots. Backend rows and the
// connection count come from the process itself and are always current; the
// system stats keep their last good values when a read fails, so one gopsutil
// hiccup never publishes a zeroed snapshot.
type statsCollector struct {
	connections OpenConnectionsCounter
	balancer    types.IBalancer
	readSystem  systemStatsReader

	mu             sync.Mutex
	lastGoodSystem systemStats
}

func newStatsCollector(connections OpenConnectionsCounter, balancer types.IBalancer, readSystem systemStatsReader) *statsCollector {
	return &statsCollector{connections: connections, balancer: balancer, readSystem: readSystem}
}

func (c *statsCollector) Snapshot() Monitoring {
	snapshot := Monitoring{
		Backends:            c.balancer.Stats(),
		TotalGoroutine:      runtime.NumGoroutine(),
		OpenConnectionCount: c.connections.OpenConnectionsCount(),
	}

	system, err := c.readSystem()
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		zap.S().Errorf("System stats unavailable, reporting the last good values: %v", err)
	} else {
		c.lastGoodSystem = system
	}
	snapshot.Cpu = c.lastGoodSystem.Cpu
	snapshot.Memory = c.lastGoodSystem.Memory
	return snapshot
}

var processID = os.Getpid()

func readSystemStatsFromOS() (systemStats, error) {
	process, err := gopsutilProcess.NewProcess(int32(processID))
	if err != nil {
		return systemStats{}, fmt.Errorf("process %d: %w", processID, err)
	}

	var stats systemStats
	if stats.Cpu.ProcessPercent, err = process.CPUPercent(); err != nil {
		return systemStats{}, fmt.Errorf("process cpu percent: %w", err)
	}

	totalCpuUsage, err := cpu.Percent(0, false)
	if err != nil {
		return systemStats{}, fmt.Errorf("total cpu percent: %w", err)
	}
	if len(totalCpuUsage) == 0 {
		return systemStats{}, fmt.Errorf("total cpu percent: no cpu reported")
	}
	stats.Cpu.TotalPercent = totalCpuUsage[0]

	virtualMemory, err := mem.VirtualMemory()
	if err != nil {
		return systemStats{}, fmt.Errorf("virtual memory: %w", err)
	}
	processMemoryPercent, err := process.MemoryPercent()
	if err != nil {
		return systemStats{}, fmt.Errorf("process memory percent: %w", err)
	}

	stats.Memory.ProcessMB = float64(processMemoryPercent * float32(ByteToMB(virtualMemory.Total)) / 100) //nolint:mnd
	stats.Memory.ProcessPercent = processMemoryPercent
	stats.Memory.TotalPercent = virtualMemory.UsedPercent
	return stats, nil
}

// Server is the monitoring HTTP server (dashboard, /stats, /metrics, probe
// endpoints) plus the poller that feeds Prometheus. Start binds and serves
// it; Shutdown stops the poller and the server, so neither touches the Pool
// afterwards.
type Server struct {
	httpServer  *fasthttp.Server
	stopPolling chan struct{}
	pollingDone chan struct{}
	stopOnce    sync.Once
	// ready gates /ready only. Backend health never does: zero Alive
	// Backends is divisor working, and gating on it risks a bootstrap deadlock.
	ready atomic.Bool
}

// Start binds addr synchronously, so a bind failure is reported to the
// caller instead of being logged from a goroutine; nothing polls before the
// bind succeeds.
func Start(connections OpenConnectionsCounter, balancer types.IBalancer, addr string) (*Server, error) {
	return start(connections, balancer, addr, readSystemStatsFromOS)
}

func start(connections OpenConnectionsCounter, balancer types.IBalancer, addr string, readSystem systemStatsReader) (*Server, error) {
	listener, err := reuseport.Listen("tcp4", addr)
	if err != nil {
		return nil, fmt.Errorf("monitoring server listen on %s: %w", addr, err)
	}

	registerPrometheusMetrics()
	collector := newStatsCollector(connections, balancer, readSystem)

	s := &Server{
		stopPolling: make(chan struct{}),
		pollingDone: make(chan struct{}),
	}
	s.httpServer = newMonitoringHTTPServer(collector, &s.ready)
	s.ready.Store(true)
	go s.pollPrometheus(collector)
	go s.serve(listener, addr)
	return s, nil
}

// MarkNotReady flips /ready to 503; the entrypoint calls it the moment
// graceful shutdown begins, so orchestrators stop routing new traffic.
func (s *Server) MarkNotReady() { s.ready.Store(false) }

func newMonitoringHTTPServer(collector *statsCollector, ready *atomic.Bool) *fasthttp.Server {
	r := router.New()
	r.GET("/", func(ctx *fasthttp.RequestCtx) {
		ctx.Response.Header.Set("Content-Type", "text/html")
		ctx.Response.SetBodyString(index)
	})

	r.GET("/healthz", func(ctx *fasthttp.RequestCtx) {
		ctx.SetStatusCode(fasthttp.StatusOK)
	})

	r.GET("/ready", func(ctx *fasthttp.RequestCtx) {
		if ready.Load() {
			ctx.SetStatusCode(fasthttp.StatusOK)
		} else {
			ctx.SetStatusCode(fasthttp.StatusServiceUnavailable)
		}
	})

	r.GET("/stats", func(ctx *fasthttp.RequestCtx) {
		ctx.Response.Header.Set("Content-Type", "application/json")
		body, err := json.Marshal(collector.Snapshot())
		if err != nil {
			zap.S().Errorf("Error while parsing json, err: %v", err)
			return
		}
		ctx.Response.SetBodyRaw(body)
	})

	r.GET("/metrics", fasthttpadaptor.NewFastHTTPHandler(promhttp.Handler()))

	return &fasthttp.Server{
		Handler:               r.Handler,
		MaxIdleWorkerDuration: 15 * time.Second,
		TCPKeepalivePeriod:    15 * time.Second,
		TCPKeepalive:          true,
		NoDefaultServerHeader: true,
	}
}

func (s *Server) pollPrometheus(collector *statsCollector) {
	defer close(s.pollingDone)
	ticker := time.NewTicker(prometheusUpdateInterval)
	defer ticker.Stop()
	for {
		snapshot := collector.Snapshot()
		updatePrometheusMetrics(&snapshot)
		select {
		case <-s.stopPolling:
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) serve(listener net.Listener, addr string) {
	zap.S().Infof("Monitoring server is running on http://%s", addr)
	// fasthttp returns nil from Serve once Shutdown closes the listener.
	if err := s.httpServer.Serve(listener); err != nil {
		zap.S().Errorf("Monitoring server stopped serving: %s", err)
	}
}

// Shutdown stops the poller, waits for a poll in flight, then shuts the
// HTTP server down within ctx. Safe to call more than once.
func (s *Server) Shutdown(ctx context.Context) error {
	// Defensive: Shutdown alone must never leave /ready answering 200.
	s.MarkNotReady()
	s.stopOnce.Do(func() { close(s.stopPolling) })
	select {
	case <-s.pollingDone:
	case <-ctx.Done():
		return fmt.Errorf("monitoring poller did not stop: %w", ctx.Err())
	}
	return s.httpServer.ShutdownWithContext(ctx)
}

func ByteToMB(b uint64) uint64 {
	return b / 1024 / 1024 //nolint:mnd
}
