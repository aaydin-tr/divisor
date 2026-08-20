package types

import (
	"time"

	"github.com/valyala/fasthttp"
)

// HealthCheckerStopTimeout caps how long Shutdown waits for a health-check
// round already in flight, so a hung probe cannot eat the graceful-shutdown
// budget or deadlock a balancer whose checker never started.
const HealthCheckerStopTimeout = 5 * time.Second

type IsHostAlive func(string) bool

type HashFunc func([]byte) uint32

type IBalancer interface {
	Serve() func(ctx *fasthttp.RequestCtx)
	Stats() []ProxyStat
	Shutdown() error
}

type ProxyStat struct {
	Addr          string    `json:"addr"`
	TotalReqCount uint64    `json:"total_req_count"`
	AvgResTime    float64   `json:"avg_res_time"`
	LastUseTime   time.Time `json:"last_use_time"`
	ConnsCount    int       `json:"conns_count"`
	IsHostAlive   bool      `json:"is_host_alive"`
	BackendHash   uint32    `json:"backend_hash"`
}
