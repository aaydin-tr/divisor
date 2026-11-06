package report

import (
	"slices"
	"strings"
	"testing"

	"github.com/aaydin-tr/divisor/benchmark/plan"
)

func TestWorkloadTablesRankTheContenders(t *testing.T) {
	tests := []struct {
		name      string
		workload  plan.Workload
		rows      []Row
		wantOrder []string
	}{
		{"Throughput: highest req/s first", plan.Throughput, []Row{
			rowWith("divisor", Numbers{RequestsPerSecond: 25000}),
			rowWith("Caddy", Numbers{RequestsPerSecond: 7000}),
			rowWith("nginx", Numbers{RequestsPerSecond: 31000}),
			rowWith("Traefik", Numbers{RequestsPerSecond: 8000}),
		}, []string{"nginx", "divisor", "Traefik", "Caddy"}},
		{"Fixed-rate latency: lowest p50 first", plan.FixedRateLatency, []Row{
			rowWith("divisor", Numbers{P50Milliseconds: 0.35}),
			rowWith("Caddy", Numbers{P50Milliseconds: 0.99}),
			rowWith("nginx", Numbers{P50Milliseconds: 0.30}),
			rowWith("Traefik", Numbers{P50Milliseconds: 0.77}),
		}, []string{"nginx", "divisor", "Traefik", "Caddy"}},
		{"a tie keeps the Plan's order", plan.Throughput, []Row{
			rowWith("divisor", Numbers{RequestsPerSecond: 20000}),
			rowWith("Caddy", Numbers{RequestsPerSecond: 20000}),
			rowWith("nginx", Numbers{RequestsPerSecond: 30000}),
		}, []string{"nginx", "divisor", "Caddy"}},
		{"failed rows come last, in the Plan's order", plan.Throughput, []Row{
			failedRow("divisor"),
			rowWith("Caddy", Numbers{RequestsPerSecond: 7000}),
			failedRow("nginx"),
			rowWith("Traefik", Numbers{RequestsPerSecond: 8000}),
		}, []string{"Traefik", "Caddy", "divisor", "nginx"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			table := workloadTable(&WorkloadResults{Workload: test.workload, Rows: test.rows})

			if got := contenderColumn(table); !slices.Equal(got, test.wantOrder) {
				t.Errorf("rows %v, want %v\n%s", got, test.wantOrder, table)
			}
		})
	}
}

func rowWith(contenderName string, median Numbers) Row {
	return Row{Contender: plan.Contender{Name: contenderName}, Rounds: []RoundResult{{Round: 1, Numbers: median}}, Median: median}
}

// contenderColumn reads the Contender cell of each table row, skipping the heading and alignment lines.
func contenderColumn(table string) []string {
	var contenders []string
	for _, line := range strings.Split(table, "\n") {
		cells := strings.Split(line, " | ")
		if !strings.HasPrefix(line, "| ") || len(cells) < 2 {
			continue
		}
		contender := strings.TrimPrefix(cells[0], "| ")
		if contender == "Contender" || contender == "---" {
			continue
		}
		contenders = append(contenders, contender)
	}
	return contenders
}
