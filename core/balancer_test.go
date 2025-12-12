package balancer

import (
	"testing"

	ip_hash "github.com/aaydin-tr/divisor/core/ip-hash"
	"github.com/aaydin-tr/divisor/core/random"
	round_robin "github.com/aaydin-tr/divisor/core/round-robin"
	w_round_robin "github.com/aaydin-tr/divisor/core/w-round-robin"
	"github.com/aaydin-tr/divisor/mocks"
	"github.com/stretchr/testify/assert"
)

func TestNewBalancerIpHash(t *testing.T) {
	ipHashConfig := mocks.TestCases[0]
	ipHashConfig.Config.Type = "ip-hash"

	balancer := NewBalancer(&ipHashConfig.Config, nil, ipHashConfig.ProxyFunc)

	assert.IsType(t, &ip_hash.IPHash{}, balancer)
}

func TestNewBalancerRandom(t *testing.T) {
	randomConfig := mocks.TestCases[0]
	randomConfig.Config.Type = "random"

	balancer := NewBalancer(&randomConfig.Config, nil, randomConfig.ProxyFunc)

	assert.IsType(t, &random.Random{}, balancer)
}

func TestNewBalancerRoundRobin(t *testing.T) {
	roundRobinConfig := mocks.TestCases[0]
	roundRobinConfig.Config.Type = "round-robin"

	balancer := NewBalancer(&roundRobinConfig.Config, nil, roundRobinConfig.ProxyFunc)

	assert.IsType(t, &round_robin.RoundRobin{}, balancer)
}

func TestNewBalancerWRoundRobin(t *testing.T) {
	wRoundRobinConfig := mocks.TestCases[0]
	wRoundRobinConfig.Config.Type = "w-round-robin"

	balancer := NewBalancer(&wRoundRobinConfig.Config, nil, wRoundRobinConfig.ProxyFunc)

	assert.IsType(t, &w_round_robin.WRoundRobin{}, balancer)
}
