package balancer

import (
	"time"

	round_robin "github.com/aaydin-tr/balancer/core/round-robin"

	"github.com/aaydin-tr/balancer/core/types"
	"github.com/aaydin-tr/balancer/pkg/config"
)

var balancers = map[string]func(config *config.Config, healtCheckerFunc types.HealtCheckerType, healtCheckerTime time.Duration) types.IBalancer{
	"round-robin": round_robin.NewRoundRobin,
	/*"w-round-robin": w_round_robin.NewWRoundRobin,
	"ip-hash":       ip_hash.NewIPHash,
	"random":        random.NewRandom,*/
}

func NewBalancer(config *config.Config, healtCheckerFunc types.HealtCheckerType, healtCheckerTime time.Duration) types.IBalancer {
	return balancers[config.Type](config, healtCheckerFunc, healtCheckerTime)
}
