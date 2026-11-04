package plan

import (
	"strconv"
	"strings"
)

// Resources are cpusets on the 4-vCPU runner: the load generator needs two
// cores and the Benchmark backends one so that neither limits a Contender.
type Resources struct {
	LoadGeneratorCPUs string `json:"load_generator_cpus"`
	ContenderCPUs     string `json:"contender_cpus"`
	ContenderMemoryMB int    `json:"contender_memory_mb"`
	BackendCPUs       string `json:"backend_cpus"`
}

var fourCoreRunnerSplit = Resources{
	LoadGeneratorCPUs: "0,1",
	ContenderCPUs:     "2",
	ContenderMemoryMB: 512,
	BackendCPUs:       "3",
}

func (r Resources) LoadGeneratorCores() int { return coresIn(r.LoadGeneratorCPUs) }

func (r Resources) ContenderCores() int { return coresIn(r.ContenderCPUs) }

// ContenderMemoryLimit is in docker's --memory syntax.
func (r Resources) ContenderMemoryLimit() string { return strconv.Itoa(r.ContenderMemoryMB) + "m" }

func coresIn(cpuset string) int {
	return len(strings.Split(cpuset, ","))
}

type BenchmarkBackend struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

// Network is where everything listens; every container shares the host
// network, and every Contender's config names these addresses.
type Network struct {
	ContenderURL      string             `json:"contender_url"`
	BenchmarkBackends []BenchmarkBackend `json:"benchmark_backends"`
}

var hostNetwork = Network{
	ContenderURL: "http://127.0.0.1:8080/",
	BenchmarkBackends: []BenchmarkBackend{
		{Name: "backend-1", Address: "127.0.0.1:9001"},
		{Name: "backend-2", Address: "127.0.0.1:9002"},
		{Name: "backend-3", Address: "127.0.0.1:9003"},
		{Name: "backend-4", Address: "127.0.0.1:9004"},
	},
}
