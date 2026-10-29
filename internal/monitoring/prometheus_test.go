package monitoring

import (
	"testing"

	"github.com/aaydin-tr/divisor/core/types"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
)

func TestBackendSeriesOfARemovedBackendDisappear(t *testing.T) {
	const keptAddress, removedAddress = "reload-kept:80", "reload-removed:80"
	reported := updatePrometheusMetrics(&Monitoring{Backends: []types.ProxyStat{
		{Addr: keptAddress, IsHostAlive: true},
		{Addr: removedAddress, IsHostAlive: true},
	}}, nil)

	updatePrometheusMetrics(&Monitoring{Backends: []types.ProxyStat{{Addr: keptAddress, IsHostAlive: true}}}, reported)

	// DeleteLabelValues reports whether the series existed (and removes it).
	for _, gauge := range backendGauges {
		name := gaugeName(gauge)
		assert.False(t, gauge.DeleteLabelValues(removedAddress), "%s still reports a Backend a Reload removed", name)
		assert.True(t, gauge.DeleteLabelValues(keptAddress), "%s lost a Backend still configured", name)
	}
}

func gaugeName(gauge *prometheus.GaugeVec) string {
	descriptions := make(chan *prometheus.Desc, 1)
	gauge.Describe(descriptions)
	return (<-descriptions).String()
}
