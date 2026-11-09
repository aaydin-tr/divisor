package monitoring

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func noSystemStats() (systemStats, error) { return systemStats{}, nil }

func TestStartRefusesAnAddressAnotherListenerHolds(t *testing.T) {
	addr := freeAddr(t)
	first, err := start(fixedConnections(0), &countingBalancer{}, addr, noSystemStats)
	require.NoError(t, err)
	defer first.Shutdown(context.Background()) //nolint:errcheck

	second, err := start(fixedConnections(0), &countingBalancer{}, addr, noSystemStats)
	if err == nil {
		second.Shutdown(context.Background()) //nolint:errcheck
	}
	require.Error(t, err, "a second Monitoring server must not share the port")
}

func TestStartBindsAnIPv6Address(t *testing.T) {
	free, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("no IPv6 loopback on this machine: %s", err)
	}
	addr := free.Addr().String()
	require.NoError(t, free.Close())

	monitoringServer, err := start(fixedConnections(0), &countingBalancer{}, addr, noSystemStats)
	require.NoError(t, err)
	defer monitoringServer.Shutdown(context.Background()) //nolint:errcheck

	res, err := http.Get(fmt.Sprintf("http://%s/healthz", addr))
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)
}
