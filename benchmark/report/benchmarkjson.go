package report

import (
	"encoding/json"
	"math"
	"time"
)

// SchemaVersion changes whenever a field of a published benchmark.json changes meaning
// or goes away; adding a field keeps it. Version 1 holds until the first release publishes one.
const SchemaVersion = 1

const (
	millisecondDecimals = 4
	megabyteDecimals    = 2
	spreadDecimals      = 4
)

type benchmarkJSON struct {
	SchemaVersion int             `json:"schema_version"`
	Run           runJSON         `json:"run"`
	Method        methodJSON      `json:"method"`
	Contenders    []contenderJSON `json:"contenders"`
	Workloads     []workloadJSON  `json:"workloads"`
}

type runJSON struct {
	Ref           string    `json:"ref"`
	Commit        string    `json:"commit"`
	Date          time.Time `json:"date"`
	Trigger       string    `json:"trigger"`
	Mode          string    `json:"mode"`
	RunnerImage   string    `json:"runner_image"`
	Architecture  string    `json:"architecture"`
	CPUModel      string    `json:"cpu_model"`
	LoadGenerator string    `json:"load_generator"`
}

type methodJSON struct {
	Rounds             int    `json:"rounds"`
	WarmUpSeconds      int    `json:"warm_up_seconds"`
	MeasurementSeconds int    `json:"measurement_seconds"`
	ContenderCores     int    `json:"contender_cores"`
	ContenderMemoryMB  int    `json:"contender_memory_mb"`
	LoadGeneratorCores int    `json:"load_generator_cores"`
	BenchmarkBackends  int    `json:"benchmark_backends"`
	Balancing          string `json:"balancing"`
	Protocol           string `json:"protocol"`
}

type contenderJSON struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Image   string `json:"image"`
	ImageID string `json:"image_id"`
}

type workloadJSON struct {
	ID                         string       `json:"id"`
	Name                       string       `json:"name"`
	Connections                int          `json:"connections"`
	RequestsPerSecond          int          `json:"requests_per_second,omitempty"`
	CorrectCoordinatedOmission bool         `json:"correct_coordinated_omission"`
	Results                    []resultJSON `json:"results"`
}

type resultJSON struct {
	Contender string      `json:"contender"`
	Status    string      `json:"status"`
	Rounds    []roundJSON `json:"rounds"`
	// Median and Spread are null for a failed row.
	Median *numbersJSON `json:"median"`
	Spread *spreadJSON  `json:"spread"`
}

type roundJSON struct {
	Round int `json:"round"`
	*numbersJSON
	Failure string `json:"failure,omitempty"`
}

type numbersJSON struct {
	RequestsPerSecond float64 `json:"requests_per_second"`
	P50Milliseconds   float64 `json:"p50_ms"`
	P99Milliseconds   float64 `json:"p99_ms"`
	P999Milliseconds  float64 `json:"p99_9_ms"`
	// PeakMemoryMB is in MiB, like contender_memory_mb.
	PeakMemoryMB *float64 `json:"peak_memory_mb,omitempty"`
}

// spreadJSON is (max - min) / median over the rounds, a fraction per number.
type spreadJSON struct {
	RequestsPerSecond float64  `json:"requests_per_second"`
	P50               float64  `json:"p50"`
	P99               float64  `json:"p99"`
	P999              float64  `json:"p99_9"`
	PeakMemory        *float64 `json:"peak_memory,omitempty"`
}

func (r *Report) BenchmarkJSON() ([]byte, error) {
	document := benchmarkJSON{
		SchemaVersion: SchemaVersion,
		Run: runJSON{
			Ref:           r.Plan.Ref,
			Commit:        r.Plan.Commit,
			Date:          r.Environment.StartedAt,
			Trigger:       string(r.Plan.Trigger),
			Mode:          string(r.Plan.Mode),
			RunnerImage:   r.Environment.RunnerImage,
			Architecture:  r.Environment.Architecture,
			CPUModel:      r.Environment.CPUModel,
			LoadGenerator: r.Environment.LoadGenerator,
		},
		Method: methodJSON{
			Rounds:             r.Plan.Schedule.Rounds,
			WarmUpSeconds:      r.Plan.Schedule.WarmUpSeconds,
			MeasurementSeconds: r.Plan.Schedule.MeasurementSeconds,
			ContenderCores:     r.Plan.Resources.ContenderCores(),
			ContenderMemoryMB:  r.Plan.Resources.ContenderMemoryMB,
			LoadGeneratorCores: r.Plan.Resources.LoadGeneratorCores(),
			BenchmarkBackends:  len(r.Plan.Network.BenchmarkBackends),
			Balancing:          "round-robin",
			Protocol:           "HTTP/1.1 without TLS",
		},
	}
	for _, contender := range r.Plan.Contenders {
		document.Contenders = append(document.Contenders, contenderJSON{
			ID:      contender.ID,
			Name:    contender.Name,
			Version: contender.Version,
			Image:   contender.Image,
			ImageID: r.Environment.ContenderImageIDs[contender.ID],
		})
	}
	for i := range r.Workloads {
		document.Workloads = append(document.Workloads, workloadAsJSON(&r.Workloads[i]))
	}

	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func workloadAsJSON(results *WorkloadResults) workloadJSON {
	workload := workloadJSON{
		ID:                         results.Workload.ID,
		Name:                       results.Workload.Name,
		Connections:                results.Workload.Connections,
		RequestsPerSecond:          results.Workload.RequestsPerSecond,
		CorrectCoordinatedOmission: results.Workload.CorrectCoordinatedOmission,
	}
	for i := range results.Rows {
		row := &results.Rows[i]
		result := resultJSON{Contender: row.Contender.ID, Status: "ok"}
		for _, round := range row.Rounds {
			if round.Failure != "" {
				result.Rounds = append(result.Rounds, roundJSON{Round: round.Round, Failure: round.Failure})
			} else {
				result.Rounds = append(result.Rounds, roundJSON{Round: round.Round, numbersJSON: numbersAsJSON(round.Numbers)})
			}
		}
		if row.Failed() {
			result.Status = "failed"
		} else {
			result.Median = numbersAsJSON(row.Median)
			result.Spread = spreadAsJSON(row.Spread)
		}
		workload.Results = append(workload.Results, result)
	}
	return workload
}

func numbersAsJSON(numbers Numbers) *numbersJSON {
	return &numbersJSON{
		RequestsPerSecond: roundedTo(numbers.RequestsPerSecond, 1),
		P50Milliseconds:   roundedTo(numbers.P50Milliseconds, millisecondDecimals),
		P99Milliseconds:   roundedTo(numbers.P99Milliseconds, millisecondDecimals),
		P999Milliseconds:  roundedTo(numbers.P999Milliseconds, millisecondDecimals),
		PeakMemoryMB:      roundedIfPresent(numbers.PeakMemoryMB, megabyteDecimals),
	}
}

func spreadAsJSON(spread Spread) *spreadJSON {
	return &spreadJSON{
		RequestsPerSecond: roundedTo(spread.RequestsPerSecond, spreadDecimals),
		P50:               roundedTo(spread.P50, spreadDecimals),
		P99:               roundedTo(spread.P99, spreadDecimals),
		P999:              roundedTo(spread.P999, spreadDecimals),
		PeakMemory:        roundedIfPresent(spread.PeakMemory, spreadDecimals),
	}
}

func roundedTo(value float64, decimals int) float64 {
	scale := math.Pow10(decimals)
	return math.Round(value*scale) / scale
}

func roundedIfPresent(value *float64, decimals int) *float64 {
	if value == nil {
		return nil
	}
	rounded := roundedTo(*value, decimals)
	return &rounded
}
