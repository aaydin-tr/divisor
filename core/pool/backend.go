package pool

import (
	"sync/atomic"

	"github.com/aaydin-tr/divisor/internal/proxy"
)

// Backend is one `backends` config entry as the Pool and the Balancers see
// it: its identity is Index, its position in the config, so the same address
// listed twice is two Backends.
type Backend struct {
	Proxy    proxy.IProxyClient
	Addr     string
	ProbeURL string
	Index    int
	Weight   uint
	isAlive  atomic.Bool
}

func (b *Backend) IsAlive() bool {
	return b.isAlive.Load()
}
