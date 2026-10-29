package monitoring

import (
	"testing"

	"github.com/aaydin-tr/divisor/core/types"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
)

var backendGauges = map[string]*prometheus.GaugeVec{
	"backend_total_request_count":   backendTotalReqCount,
	"backend_average_response_time": backendAvgResTime,
	"backend_connection_count":      backendConnsCount,
	"backend_alive":                 backendAlive,
}

func TestBackendSeriesOfARemovedBackendDisappear(t *testing.T) {
	const keptAddr, removedAddr = "reload-kept:80", "reload-removed:80"
	reported := updatePrometheusMetrics(&Monitoring{Backends: []types.ProxyStat{
		{Addr: keptAddr, IsHostAlive: true},
		{Addr: removedAddr, IsHostAlive: true},
	}}, nil)

	updatePrometheusMetrics(&Monitoring{Backends: []types.ProxyStat{{Addr: keptAddr, IsHostAlive: true}}}, reported)

	// DeleteLabelValues reports whether the series existed (and removes it).
	for name, gauge := range backendGauges {
		assert.False(t, gauge.DeleteLabelValues(removedAddr), "%s still reports a Backend a Reload removed", name)
		assert.True(t, gauge.DeleteLabelValues(keptAddr), "%s lost a Backend still configured", name)
	}
}
