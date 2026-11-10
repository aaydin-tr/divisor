package monitoring

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var dashboardResourceReference = regexp.MustCompile(`(?:src|href)="([^"]*)"|url\(([^)]*)\)|@import`)

func getFromMonitoringServer(t *testing.T, addr, path string) (*http.Response, string) {
	t.Helper()
	res, err := http.Get(fmt.Sprintf("http://%s%s", addr, path))
	require.NoError(t, err)
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	return res, string(body)
}

func TestDashboardLoadsEverythingFromTheMonitoringServer(t *testing.T) {
	addr := freeAddr(t)
	s, err := start(fixedConnections(0), &countingBalancer{}, addr, func() (systemStats, error) { return systemStats{}, nil })
	require.NoError(t, err)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.Shutdown(ctx) //nolint:errcheck
	}()

	dashboard, page := getFromMonitoringServer(t, addr, "/")
	require.Equal(t, http.StatusOK, dashboard.StatusCode)

	for _, reference := range dashboardResourceReference.FindAllStringSubmatch(page, -1) {
		require.NotEqual(t, "@import", reference[0], "the dashboard imports a stylesheet")
		resourcePath := reference[1] + reference[2]
		require.Regexp(t, `^/[^/]`, resourcePath, "the dashboard loads %q, not a path on the Monitoring server", resourcePath)
		resource, _ := getFromMonitoringServer(t, addr, resourcePath)
		assert.Equal(t, http.StatusOK, resource.StatusCode, resourcePath)
	}

	chartScript := regexp.MustCompile(`<script src="([^"]*)"`).FindStringSubmatch(page)
	require.NotNil(t, chartScript, "the dashboard draws its charts with a script")
	script, body := getFromMonitoringServer(t, addr, chartScript[1])
	assert.Contains(t, script.Header.Get("Content-Type"), "javascript")
	assert.Contains(t, body, "Chart.js v4.1.2")
}
