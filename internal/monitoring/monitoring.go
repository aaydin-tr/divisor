package monitoring

import (
	"encoding/json"
	"log"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/aaydin-tr/balancer/core/types"
	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/mem"
	"github.com/shirou/gopsutil/process"
	"github.com/valyala/fasthttp"
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
	ProcessPercent float64 `json:"process_percent"`
	TotalPercent   float64 `json:"total_percent"`
	TotalMB        float64 `json:"total_mb"`
}

var once sync.Once
var pid int

func HealthChecker(server *fasthttp.Server, proxiesStats []types.ProxyStat) Monitoring {
	once.Do(func() {
		pid = os.Getpid()
	})

	monitoring := Monitoring{}
	process, err := process.NewProcess(int32(pid))
	if err != nil {
		//TODO log
		return Monitoring{}
	}

	processCpuUsage, _ := process.CPUPercent()
	monitoring.Cpu.ProcessPercent = processCpuUsage

	totalCpuUsage, err := cpu.Percent(0, false)
	if err != nil {
		//TODO log
		return Monitoring{}
	}

	monitoring.Cpu.TotalPercent = totalCpuUsage[0]

	vm, err := mem.VirtualMemory()
	if err != nil {
		//TODO log
		return Monitoring{}
	}

	monitoring.Memory.TotalMB = float64(ByteToMB(vm.Total))
	per, err := process.MemoryPercent()
	if err != nil {
		//TODO log
		return Monitoring{}
	}

	monitoring.Memory.ProcessPercent = float64(per * float32(ByteToMB(vm.Total)) / 100)
	monitoring.Memory.TotalPercent = (float64(vm.Used) / float64((vm.Total))) * 100
	monitoring.TotalGoroutine = runtime.NumGoroutine()
	monitoring.OpenConnectionCount = server.GetOpenConnectionsCount()
	monitoring.Backends = proxiesStats

	return monitoring
}

func StartMonitoringServer(server *fasthttp.Server, proxies types.IBalancer) {
	monitoringServer := fasthttp.Server{
		Handler: func(ctx *fasthttp.RequestCtx) {
			path, method := string(ctx.Request.URI().Path()), string(ctx.Request.Header.Method())

			if path == "/" && method == "GET" {
				ctx.Response.Header.Set("Access-Control-Allow-Credentials", "true")
				ctx.Response.Header.Set("Access-Control-Allow-Origin", "*")
				ctx.Response.Header.Set("Content-Type", "application/json")

				m := HealthChecker(server, proxies.Stats())
				by, err := json.Marshal(m)
				if err != nil {
					//TODO log
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
	}

	if err := monitoringServer.ListenAndServe(":8001"); err != nil {
		log.Fatalf("error in fasthttp server: %s", err)
	}
}

func ByteToMB(b uint64) uint64 {
	return b / 1024 / 1024
}
