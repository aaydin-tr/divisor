package pool

import (
	"sync/atomic"

	"github.com/aaydin-tr/divisor/core/types"
	"github.com/aaydin-tr/divisor/internal/proxy"
)

// Backend is one `backends` config entry; its identity is Index (the config
// position at boot, a never-used one when a Reload adds it), so the same
// address listed twice is two Backends.
type Backend struct {
	Proxy               proxy.IProxyClient
	Addr                string
	ProbeURL            string
	ExpectedProbeStatus types.ExpectedProbeStatus
	Index               int
	Weight              uint
	isAlive             atomic.Bool
}

func (b *Backend) IsAlive() bool {
	return b.isAlive.Load()
}
