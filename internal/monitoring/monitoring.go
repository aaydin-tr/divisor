package monitoring

import (
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"sync"
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

var once sync.Once
var pid int

func getServerStats(server any, proxiesStats []types.ProxyStat) Monitoring {
	once.Do(func() {
		pid = os.Getpid()
	})

	monitoring := Monitoring{}
	process, err := gopsutilProcess.NewProcess(int32(pid))
	if err != nil {
		zap.S().Errorf("Error while getting process, err: %v", err)
		return Monitoring{}
	}

	processCpuUsage, _ := process.CPUPercent()
	monitoring.Cpu.ProcessPercent = processCpuUsage

	totalCpuUsage, err := cpu.Percent(0, false)
	if err != nil {
		zap.S().Errorf("Error while getting total cpu usage, err: %v", err)
		return Monitoring{}
	}

	monitoring.Cpu.TotalPercent = totalCpuUsage[0]
	vm, err := mem.VirtualMemory()
	if err != nil {
		zap.S().Errorf("Error while getting virtual memory stat, err: %v", err)
		return Monitoring{}
	}

	per, err := process.MemoryPercent()
	if err != nil {
		zap.S().Errorf("Error while getting process memory percent, err: %v", err)
		return Monitoring{}
	}

	monitoring.Memory.ProcessMB = float64(per * float32(ByteToMB(vm.Total)) / 100) //nolint:mnd
	monitoring.Memory.ProcessPercent = per
	monitoring.Memory.TotalPercent = vm.UsedPercent
	monitoring.TotalGoroutine = runtime.NumGoroutine()

	switch s := server.(type) {
	case *fasthttp.Server:
		monitoring.OpenConnectionCount = s.GetOpenConnectionsCount()
	case *http.Server:
		// net/http doesn't expose connection count directly, set to 0
		// TODO: Implement net/http connection count
		monitoring.OpenConnectionCount = 0
	}

	monitoring.Backends = proxiesStats

	return monitoring
}

func StartMonitoringServer(server any, proxies types.IBalancer, addr string) {
	const sleepDuration = 5 * time.Second
	r := router.New()
	init_prometheus()
	go func() {
		for {
			stats := getServerStats(server, proxies.Stats())
			updatePrometheusMetrics(&stats)
			time.Sleep(sleepDuration)
		}
	}()

	r.GET("/", func(ctx *fasthttp.RequestCtx) {
		ctx.Response.Header.Set("Content-Type", "text/html")
		ctx.Response.SetBodyString(index)
	})

	r.GET("/stats", func(ctx *fasthttp.RequestCtx) {
		ctx.Response.Header.Set("Content-Type", "application/json")
		m := getServerStats(server, proxies.Stats())
		by, err := json.Marshal(m)
		if err != nil {
			zap.S().Errorf("Error while parsing json, err: %v", err)
			return
		}

		ctx.Response.SetBodyRaw(by)
	})

	r.GET("/metrics", fasthttpadaptor.NewFastHTTPHandler(promhttp.Handler()))

	monitoringServer := fasthttp.Server{
		Handler:               r.Handler,
		MaxIdleWorkerDuration: 15 * time.Second,
		TCPKeepalivePeriod:    15 * time.Second,
		TCPKeepalive:          true,
		NoDefaultServerHeader: true,
	}

	ln, err := reuseport.Listen("tcp4", addr)
	if err != nil {
		zap.S().Errorf("Error while starting monitoring server %s", err)
		return
	}

	zap.S().Infof("Monitoring server is running on http://%s", addr)
	if err := monitoringServer.Serve(ln); err != nil {
		zap.S().Errorf("Error while starting monitoring server %s", err)
		return
	}
}

func ByteToMB(b uint64) uint64 {
	return b / 1024 / 1024 //nolint:mnd
}
