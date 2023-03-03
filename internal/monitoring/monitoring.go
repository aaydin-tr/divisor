package monitoring

import (
	"encoding/json"
	"net"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/aaydin-tr/divisor/core/types"
	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/mem"
	"github.com/shirou/gopsutil/process"
	"github.com/valyala/fasthttp"
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

func healthChecker(server *fasthttp.Server, proxiesStats []types.ProxyStat) Monitoring {
	once.Do(func() {
		pid = os.Getpid()
	})

	monitoring := Monitoring{}
	process, err := process.NewProcess(int32(pid))
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

	monitoring.Memory.ProcessMB = float64(per * float32(ByteToMB(vm.Total)) / 100)
	monitoring.Memory.ProcessPercent = per
	monitoring.Memory.TotalPercent = vm.UsedPercent
	monitoring.TotalGoroutine = runtime.NumGoroutine()
	monitoring.OpenConnectionCount = server.GetOpenConnectionsCount()
	monitoring.Backends = proxiesStats

	return monitoring
}

func StartMonitoringServer(server *fasthttp.Server, proxies types.IBalancer, addr string) {
	monitoringServer := fasthttp.Server{
		Handler: func(ctx *fasthttp.RequestCtx) {
			path, method := string(ctx.Request.URI().Path()), string(ctx.Request.Header.Method())
			ctx.Response.Header.Set("Access-Control-Allow-Credentials", "true")
			ctx.Response.Header.Set("Access-Control-Allow-Origin", "*")

			if path == "/" && method == "GET" {
				ctx.Response.Header.Set("Content-Type", "text/html")
				ctx.Response.SetBodyString(index)
				return
			} else if path == "/stats" && method == "GET" {
				ctx.Response.Header.Set("Content-Type", "application/json")

				m := healthChecker(server, proxies.Stats())
				by, err := json.Marshal(m)
				if err != nil {
					zap.S().Errorf("Error while parsing json, err: %v", err)
					return
				}

				ctx.Response.SetBodyRaw(by)
				return
			}

			ctx.Response.SetStatusCode(fasthttp.StatusNotFound)

		},
		MaxIdleWorkerDuration: 15 * time.Second,
		TCPKeepalivePeriod:    15 * time.Second,
		TCPKeepalive:          true,
		NoDefaultServerHeader: true,
	}

	ln, err := net.Listen("tcp4", addr)
	if err != nil {
		zap.S().Errorf("Error while starting monitoring server %s", err)
		return
	}

	zap.S().Infof("Monitoring server started successfully -> http://%s", addr)
	monitoringServer.Serve(ln)
}

func ByteToMB(b uint64) uint64 {
	return b / 1024 / 1024
}
