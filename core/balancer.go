package balancer

import (
	ip_hash "github.com/aaydin-tr/divisor/core/ip-hash"
	random "github.com/aaydin-tr/divisor/core/random"
	round_robin "github.com/aaydin-tr/divisor/core/round-robin"
	"github.com/aaydin-tr/divisor/core/types"
	w_round_robin "github.com/aaydin-tr/divisor/core/w-round-robin"
	"github.com/aaydin-tr/divisor/internal/proxy"

	"github.com/aaydin-tr/divisor/pkg/config"
)

var balancers = map[string]func(config *config.Config, proxyFunc proxy.ProxyFunc) types.IBalancer{
	"round-robin":   round_robin.NewRoundRobin,
	"w-round-robin": w_round_robin.NewWRoundRobin,
	"ip-hash":       ip_hash.NewIPHash,
	"random":        random.NewRandom,
}

func NewBalancer(config *config.Config, proxyFunc proxy.ProxyFunc) types.IBalancer {
	return balancers[config.Type](config, proxyFunc)
}
