package plan

import (
	"fmt"
	"strings"
	"testing"
)

const commit = "aa36fa6c0ffee0000000000000000000000000ee"

func decideLocalRun(t *testing.T, inputs Inputs) Plan {
	t.Helper()
	inputs.Trigger = LocalTrigger
	if inputs.Commit == "" {
		inputs.Commit = commit
	}
	decided, err := Decide(&inputs)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	return decided
}

func TestLocalRunMeasuresTheCheckoutFirst(t *testing.T) {
	decided := decideLocalRun(t, Inputs{Ref: "main"})

	if decided.Mode != FullMode {
		t.Errorf("mode = %q, want %q", decided.Mode, FullMode)
	}
	divisor := decided.Contenders[0]
	if divisor.Name != "divisor" || divisor.Definition != "divisor" {
		t.Errorf("first Contender = %+v, want divisor on the divisor definition", divisor)
	}
	if !divisor.ImageFromCheckout || divisor.Image != CheckoutDivisorImage {
		t.Errorf("Contender image = %q (from checkout: %v), want %q built from the checkout", divisor.Image, divisor.ImageFromCheckout, CheckoutDivisorImage)
	}
	if divisor.BuildArgs["DIVISOR_IMAGE"] != CheckoutDivisorImage {
		t.Errorf("build args = %v, want DIVISOR_IMAGE=%s", divisor.BuildArgs, CheckoutDivisorImage)
	}
}

func TestRowsAreDivisorThenHAProxyNginxAndTraefik(t *testing.T) {
	var rows []string
	for _, contender := range decideLocalRun(t, Inputs{Ref: "main"}).Contenders {
		rows = append(rows, contender.Name)
	}

	if got, want := strings.Join(rows, ", "), "divisor, HAProxy, nginx, Traefik"; got != want {
		t.Errorf("rows = %s, want %s", got, want)
	}
}

func TestUpstreamContendersRunTheirPinnedOfficialImageAtItsVersion(t *testing.T) {
	officialImageRepositories := map[string]string{"haproxy": "haproxy", "nginx": "nginx", "traefik": "traefik"}

	for _, contender := range decideLocalRun(t, Inputs{Ref: "main"}).Contenders[1:] {
		repository, version, _ := strings.Cut(contender.Image, ":")
		if repository != officialImageRepositories[contender.ID] || contender.Definition != contender.ID {
			t.Errorf("%s: image %q on definition %q, want the official %s image on contenders/%s",
				contender.Name, contender.Image, contender.Definition, officialImageRepositories[contender.ID], contender.ID)
		}
		if contender.Version == "" || contender.Version != version {
			t.Errorf("%s: version %q, want the pinned image's version %q", contender.Name, contender.Version, version)
		}
		if contender.ImageFromCheckout || len(contender.BuildArgs) != 0 {
			t.Errorf("%s: built from the checkout %v with build args %v, want neither", contender.Name, contender.ImageFromCheckout, contender.BuildArgs)
		}
	}
}

func TestCheckoutVersionNamesTheRefAndCommit(t *testing.T) {
	tests := []struct {
		name   string
		inputs Inputs
		want   string
	}{
		{"branch", Inputs{Ref: "main"}, "main@aa36fa6"},
		{"detached checkout", Inputs{}, "aa36fa6"},
		{"uncommitted changes", Inputs{Ref: "feature/x", CheckoutHasUncommittedChanges: true}, "feature/x@aa36fa6-dirty"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := decideLocalRun(t, test.inputs).Contenders[0].Version; got != test.want {
				t.Errorf("version = %q, want %q", got, test.want)
			}
		})
	}
}

func TestWorkloadsAreThroughputThenFixedRateLatency(t *testing.T) {
	var described []string
	for _, workload := range decideLocalRun(t, Inputs{}).Workloads {
		described = append(described, fmt.Sprintf("%s: %d connections, %d req/s, coordinated-omission correction %v",
			workload.Name, workload.Connections, workload.RequestsPerSecond, workload.CorrectCoordinatedOmission))
	}

	want := []string{
		"Throughput: 64 connections, 0 req/s, coordinated-omission correction false",
		"Fixed-rate latency: 64 connections, 5000 req/s, coordinated-omission correction true",
	}
	if strings.Join(described, "\n") != strings.Join(want, "\n") {
		t.Errorf("Workloads:\n%s\nwant:\n%s", strings.Join(described, "\n"), strings.Join(want, "\n"))
	}
}

func TestFullScheduleIsThreeRoundsOfEveryContenderThroughBothWorkloads(t *testing.T) {
	decided := decideLocalRun(t, Inputs{})

	want := `round 1: divisor: throughput (5s warm-up, 30s measured), fixed-rate-latency (5s warm-up, 30s measured)
round 1: haproxy: throughput (5s warm-up, 30s measured), fixed-rate-latency (5s warm-up, 30s measured)
round 1: nginx: throughput (5s warm-up, 30s measured), fixed-rate-latency (5s warm-up, 30s measured)
round 1: traefik: throughput (5s warm-up, 30s measured), fixed-rate-latency (5s warm-up, 30s measured)
round 2: divisor: throughput (5s warm-up, 30s measured), fixed-rate-latency (5s warm-up, 30s measured)
round 2: haproxy: throughput (5s warm-up, 30s measured), fixed-rate-latency (5s warm-up, 30s measured)
round 2: nginx: throughput (5s warm-up, 30s measured), fixed-rate-latency (5s warm-up, 30s measured)
round 2: traefik: throughput (5s warm-up, 30s measured), fixed-rate-latency (5s warm-up, 30s measured)
round 3: divisor: throughput (5s warm-up, 30s measured), fixed-rate-latency (5s warm-up, 30s measured)
round 3: haproxy: throughput (5s warm-up, 30s measured), fixed-rate-latency (5s warm-up, 30s measured)
round 3: nginx: throughput (5s warm-up, 30s measured), fixed-rate-latency (5s warm-up, 30s measured)
round 3: traefik: throughput (5s warm-up, 30s measured), fixed-rate-latency (5s warm-up, 30s measured)`
	if got := describeSchedule(decided); got != want {
		t.Errorf("schedule:\n%s\nwant:\n%s", got, want)
	}
	if decided.Schedule.ReadinessTimeout() <= 0 {
		t.Error("a Contender that never becomes ready must fail within a bounded time")
	}
}

func TestResourcesSplitTheFourCoreRunner(t *testing.T) {
	resources := decideLocalRun(t, Inputs{}).Resources

	got := fmt.Sprintf("load generator on CPUs %s, Contender on CPU %s with %d MB, Benchmark backends on CPU %s",
		resources.LoadGeneratorCPUs, resources.ContenderCPUs, resources.ContenderMemoryMB, resources.BackendCPUs)
	want := "load generator on CPUs 0,1, Contender on CPU 2 with 512 MB, Benchmark backends on CPU 3"
	if got != want {
		t.Errorf("resources: %s\nwant: %s", got, want)
	}
	if resources.LoadGeneratorCores() != 2 || resources.ContenderCores() != 1 {
		t.Errorf("cores: load generator %d, Contender %d, want 2 and 1", resources.LoadGeneratorCores(), resources.ContenderCores())
	}
}

func describeSchedule(decided Plan) string {
	var rounds []string
	for _, turn := range decided.Turns() {
		var workloads []string
		for _, workload := range turn.Workloads {
			workloads = append(workloads, fmt.Sprintf("%s (%v warm-up, %v measured)",
				workload.ID, decided.Schedule.WarmUp(), decided.Schedule.Measurement()))
		}
		rounds = append(rounds, fmt.Sprintf("round %d: %s: %s", turn.Round, turn.Contender.ID, strings.Join(workloads, ", ")))
	}
	return strings.Join(rounds, "\n")
}

func TestSmokeScheduleIsOneShortRoundWithoutWarmUpThatChecksTheEquivalentSetup(t *testing.T) {
	decided, err := Decide(&Inputs{Trigger: SmokeTrigger, Commit: commit})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}

	if decided.Mode != SmokeMode {
		t.Errorf("mode = %q, want %q", decided.Mode, SmokeMode)
	}
	want := `round 1: divisor: throughput (0s warm-up, 3s measured), fixed-rate-latency (0s warm-up, 3s measured)
round 1: haproxy: throughput (0s warm-up, 3s measured), fixed-rate-latency (0s warm-up, 3s measured)
round 1: nginx: throughput (0s warm-up, 3s measured), fixed-rate-latency (0s warm-up, 3s measured)
round 1: traefik: throughput (0s warm-up, 3s measured), fixed-rate-latency (0s warm-up, 3s measured)`
	if got := describeSchedule(decided); got != want {
		t.Errorf("schedule:\n%s\nwant:\n%s", got, want)
	}
	check := decided.Schedule.EquivalentSetupCheck
	if check == nil {
		t.Fatal("smoke mode must check the Equivalent setup")
	}
	if check.Requests < 100*len(decided.Network.BenchmarkBackends) || check.Connections < 2 {
		t.Errorf("Equivalent setup check = %+v, want many requests over a few keep-alive connections", *check)
	}
}

func TestFullScheduleLeavesTheEquivalentSetupCheckToSmokeMode(t *testing.T) {
	if check := decideLocalRun(t, Inputs{}).Schedule.EquivalentSetupCheck; check != nil {
		t.Errorf("full mode checks the Equivalent setup with %+v, want no check", *check)
	}
}
