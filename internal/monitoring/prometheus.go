package monitoring

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	processMemoryPercent = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "process_memory_percent",
		Help: "Process memory usage percent",
	})
	totalMemoryPercent = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "total_memory_percent",
		Help: "Total memory usage percent",
	})
	processMemoryMB = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "process_memory_mb",
		Help: "Process memory usage in MB",
	})
	processCPUPercent = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "process_cpu_percent",
		Help: "Process CPU usage percent",
	})
	totalCPUPercent = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "total_cpu_percent",
		Help: "Total CPU usage percent",
	})
	totalGoroutine = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "total_goroutine",
		Help: "Total number of goroutines",
	})
	openConnCount = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "open_conn_count",
		Help: "Open connection count",
	})

	backendTotalReqCount = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "backend_total_request_count",
		Help: "Total request count for each backend",
	}, []string{"address"})
	backendAvgResTime = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "backend_average_response_time",
		Help: "Average response time for each backend",
	}, []string{"address"})
	backendConnsCount = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "backend_connection_count",
		Help: "Number of connections for each backend",
	}, []string{"address"})
	backendAlive = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "backend_alive",
		Help: "Whether the backend is alive or not",
	}, []string{"address"})
)

func init_prometheus() {
	prometheus.MustRegister(processMemoryPercent)
	prometheus.MustRegister(totalMemoryPercent)
	prometheus.MustRegister(processMemoryMB)
	prometheus.MustRegister(processCPUPercent)
	prometheus.MustRegister(totalCPUPercent)
	prometheus.MustRegister(totalGoroutine)
	prometheus.MustRegister(openConnCount)
	prometheus.MustRegister(backendTotalReqCount)
	prometheus.MustRegister(backendAvgResTime)
	prometheus.MustRegister(backendConnsCount)
	prometheus.MustRegister(backendAlive)
}

func updatePrometheusMetrics(m Monitoring) {
	processMemoryPercent.Set(float64(m.Memory.ProcessPercent))
	totalMemoryPercent.Set(float64(m.Memory.TotalPercent))
	processMemoryMB.Set(m.Memory.ProcessMB)
	processCPUPercent.Set(m.Cpu.ProcessPercent)
	totalCPUPercent.Set(m.Cpu.TotalPercent)
	totalGoroutine.Set(float64(m.TotalGoroutine))
	openConnCount.Set(float64(m.OpenConnectionCount))

	for _, backend := range m.Backends {
		backendTotalReqCount.WithLabelValues(backend.Addr).Set(float64(backend.TotalReqCount))
		backendAvgResTime.WithLabelValues(backend.Addr).Set(backend.AvgResTime)
		backendConnsCount.WithLabelValues(backend.Addr).Set(float64(backend.ConnsCount))
		backendAlive.WithLabelValues(backend.Addr).Set(func() float64 {
			if backend.IsHostAlive {
				return 1
			}
			return 0
		}())
	}
}
