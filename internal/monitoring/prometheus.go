package monitoring

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

const backendAddressLabel = "address"

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
	}, []string{backendAddressLabel})
	backendAvgResTime = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "backend_average_response_time",
		Help: "Average response time for each backend",
	}, []string{backendAddressLabel})
	backendConnsCount = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "backend_connection_count",
		Help: "Number of connections for each backend",
	}, []string{backendAddressLabel})
	backendAlive = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "backend_alive",
		Help: "Whether the backend is alive or not",
	}, []string{backendAddressLabel})
)

// backendGauges are the series labeled by Backend address; each is registered
// and, once no Backend has its address, deleted.
var backendGauges = []*prometheus.GaugeVec{backendTotalReqCount, backendAvgResTime, backendConnsCount, backendAlive}

var registerPrometheusMetricsOnce sync.Once

// registerPrometheusMetrics registers once per process: MustRegister panics
// on a second registration, and Start may run more than once in tests.
func registerPrometheusMetrics() {
	registerPrometheusMetricsOnce.Do(registerAllPrometheusMetrics)
}

func registerAllPrometheusMetrics() {
	prometheus.MustRegister(processMemoryPercent)
	prometheus.MustRegister(totalMemoryPercent)
	prometheus.MustRegister(processMemoryMB)
	prometheus.MustRegister(processCPUPercent)
	prometheus.MustRegister(totalCPUPercent)
	prometheus.MustRegister(totalGoroutine)
	prometheus.MustRegister(openConnCount)
	for _, gauge := range backendGauges {
		prometheus.MustRegister(gauge)
	}
}

// Handing back the addresses reported last time drops the series of an address
// a Reload removed every Backend from.
func updatePrometheusMetrics(m *Monitoring, previouslyReportedAddresses map[string]bool) map[string]bool {
	processMemoryPercent.Set(float64(m.Memory.ProcessPercent))
	totalMemoryPercent.Set(float64(m.Memory.TotalPercent))
	processMemoryMB.Set(m.Memory.ProcessMB)
	processCPUPercent.Set(m.Cpu.ProcessPercent)
	totalCPUPercent.Set(m.Cpu.TotalPercent)
	totalGoroutine.Set(float64(m.TotalGoroutine))
	openConnCount.Set(float64(m.OpenConnectionCount))

	reportedAddresses := make(map[string]bool, len(m.Backends))
	for _, backend := range m.Backends {
		reportedAddresses[backend.Addr] = true
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

	for address := range previouslyReportedAddresses {
		if reportedAddresses[address] {
			continue
		}
		for _, gauge := range backendGauges {
			gauge.DeleteLabelValues(address)
		}
	}
	return reportedAddresses
}
