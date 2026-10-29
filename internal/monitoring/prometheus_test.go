package monitoring

import (
	"testing"

	"github.com/aaydin-tr/divisor/core/types"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
)

func TestBackendsSharingAnAddressReportOneSeriesEach(t *testing.T) {
	const sharedAddress = "shared-address:80"
	updatePrometheusMetrics(&Monitoring{Backends: []types.ProxyStat{
		{Addr: sharedAddress, BackendHash: 0, IsHostAlive: true},
		{Addr: sharedAddress, BackendHash: 1, IsHostAlive: false},
	}}, nil)

	for _, gauge := range backendGauges {
		name := gaugeName(gauge)
		assert.True(t, gauge.DeleteLabelValues(sharedAddress, "0"), "%s has no series for Index 0", name)
		assert.True(t, gauge.DeleteLabelValues(sharedAddress, "1"), "%s has no series for Index 1", name)
	}
}

func TestBackendSeriesOfARemovedBackendDisappear(t *testing.T) {
	const keptAddress, removedAddress = "reload-kept:80", "reload-removed:80"
	reported := updatePrometheusMetrics(&Monitoring{Backends: []types.ProxyStat{
		{Addr: keptAddress, BackendHash: 0, IsHostAlive: true},
		{Addr: removedAddress, BackendHash: 1, IsHostAlive: true},
		{Addr: keptAddress, BackendHash: 2, IsHostAlive: true},
	}}, nil)

	updatePrometheusMetrics(&Monitoring{Backends: []types.ProxyStat{{Addr: keptAddress, BackendHash: 0, IsHostAlive: true}}}, reported)

	// DeleteLabelValues reports whether the series existed (and removes it).
	for _, gauge := range backendGauges {
		name := gaugeName(gauge)
		assert.False(t, gauge.DeleteLabelValues(removedAddress, "1"), "%s still reports a Backend a Reload removed", name)
		assert.False(t, gauge.DeleteLabelValues(keptAddress, "2"), "%s still reports a removed Backend sharing a kept one's address", name)
		assert.True(t, gauge.DeleteLabelValues(keptAddress, "0"), "%s lost a Backend still configured", name)
	}
}

func gaugeName(gauge *prometheus.GaugeVec) string {
	descriptions := make(chan *prometheus.Desc, 1)
	gauge.Describe(descriptions)
	return (<-descriptions).String()
}
