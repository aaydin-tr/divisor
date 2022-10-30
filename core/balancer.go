package balancer

import (
	round_robin "github.com/aaydin-tr/balancer/core/round-robin"
	w_round_robin "github.com/aaydin-tr/balancer/core/w-round-robin"

	"github.com/aaydin-tr/balancer/core/types"
	"github.com/aaydin-tr/balancer/pkg/config"
)

var balancers = map[string]func(config *config.Config) types.IBalancer{
	"round-robin":   round_robin.NewRoundRobin,
	"w-round-robin": w_round_robin.NewWRoundRobin,
}

func NewBalancer(config *config.Config) types.IBalancer {
	return balancers[config.Type](config)
}
