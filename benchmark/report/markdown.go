package report

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/aaydin-tr/divisor/benchmark/plan"
)

const methodDocumentationURL = "https://github.com/aaydin-tr/divisor/blob/%s/benchmark/README.md"

const notAvailable = "n/a"

type column struct {
	heading string
	value   func(Numbers) string
	// notAvailableNote goes under the table when a row's value is notAvailable.
	notAvailableNote string
}

var (
	requestsPerSecondColumn = column{heading: "req/s", value: func(numbers Numbers) string { return withThousandsSeparators(numbers.RequestsPerSecond) }}
	p50Column               = column{heading: "p50", value: func(numbers Numbers) string { return inMilliseconds(numbers.P50Milliseconds) }}
	p99Column               = column{heading: "p99", value: func(numbers Numbers) string { return inMilliseconds(numbers.P99Milliseconds) }}
	p999Column              = column{heading: "p99.9", value: func(numbers Numbers) string { return inMilliseconds(numbers.P999Milliseconds) }}
	peakMemoryColumn        = column{
		heading: "peak memory",
		value: func(numbers Numbers) string {
			if numbers.PeakMemoryMB == nil {
				return notAvailable
			}
			return inMegabytes(*numbers.PeakMemoryMB)
		},
		notAvailableNote: "Peak memory n/a: the run could not read the proxy container's `memory.peak` file, which needs cgroup v2 and Linux 5.19 or later.",
	}
)

type workloadTableLayout struct {
	columns []column
	// ranksAbove orders the table's rows: the better Contender comes first.
	ranksAbove func(contender, other Numbers) bool
}

var workloadTableLayouts = map[string]workloadTableLayout{
	plan.Throughput.ID: {
		columns:    []column{requestsPerSecondColumn, p50Column, p99Column, peakMemoryColumn},
		ranksAbove: func(contender, other Numbers) bool { return contender.RequestsPerSecond > other.RequestsPerSecond },
	},
	plan.FixedRateLatency.ID: {
		columns:    []column{p50Column, p99Column, p999Column},
		ranksAbove: func(contender, other Numbers) bool { return contender.P50Milliseconds < other.P50Milliseconds },
	},
}

// Markdown is the section the release page carries: blocks in reading order,
// separated by a blank line.
func (r *Report) Markdown() string {
	blocks := []string{"## Performance", r.environmentLine()}
	for i := range r.Workloads {
		blocks = append(blocks, workloadTable(&r.Workloads[i]))
	}
	return strings.Join(blocks, "\n\n") + "\n"
}

func (r *Report) environmentLine() string {
	links := fmt.Sprintf("[How this is measured]("+methodDocumentationURL+")", r.methodRef())
	if r.Environment.WorkflowRunURL != "" {
		links += fmt.Sprintf(" · [Workflow run](%s)", r.Environment.WorkflowRunURL)
	}
	return strings.Join([]string{
		fmt.Sprintf("Runner: %s, %s, %s.", r.Environment.RunnerImage, r.Environment.Architecture, r.Environment.CPUModel),
		fmt.Sprintf("Each proxy gets %s and %d MB and balances round-robin over %d backends, HTTP/1.1 without TLS.",
			cores(r.Plan.Resources.ContenderCores()), r.Plan.Resources.ContenderMemoryMB, len(r.Plan.Network.BenchmarkBackends)),
		r.roundsSentence(),
		links,
	}, " ")
}

func (r *Report) roundsSentence() string {
	if r.Plan.Mode == plan.SmokeMode {
		return fmt.Sprintf("These numbers come from one short smoke round (%ds), not a measurement.", r.Plan.Schedule.MeasurementSeconds)
	}
	return fmt.Sprintf("Each number is the median of %d rounds.", r.Plan.Schedule.Rounds)
}

// methodRef is where the method and Contender configs that ran are read: the
// benchmark module's commit, since a measured tag may predate them.
func (r *Report) methodRef() string {
	for _, ref := range []string{r.Plan.BenchmarkCommit, r.Plan.Ref} {
		if ref != "" {
			return ref
		}
	}
	return r.Plan.Commit
}

func workloadTable(results *WorkloadResults) string {
	layout := workloadTableLayouts[results.Workload.ID]
	columns := layout.columns
	lines := []string{
		"### " + results.Workload.Name,
		"",
		workloadDescription(results.Workload),
		"",
	}

	headings := []string{"Contender", "Version"}
	alignments := []string{"---", "---"}
	for _, column := range columns {
		headings = append(headings, column.heading)
		alignments = append(alignments, "---:")
	}
	lines = append(lines, tableLine(headings), tableLine(alignments))

	var notes, failures []string
	for _, row := range rowsInRankOrder(results.Rows, layout.ranksAbove) {
		cells := []string{row.Contender.Name, row.Contender.Version}
		for _, column := range columns {
			if row.Failed() {
				cells = append(cells, "failed")
				continue
			}
			cell := column.value(row.Median)
			if cell == notAvailable && !slices.Contains(notes, column.notAvailableNote) {
				notes = append(notes, column.notAvailableNote)
			}
			cells = append(cells, cell)
		}
		lines = append(lines, tableLine(cells))
		for _, failure := range row.Failures() {
			failures = append(failures, fmt.Sprintf("- %s, %s", row.Contender.Name, failure))
		}
	}

	for _, note := range notes {
		lines = append(lines, "", note)
	}
	if len(failures) > 0 {
		lines = append(lines, "", "Failed:", "")
		lines = append(lines, failures...)
	}
	return strings.Join(lines, "\n")
}

// rowsInRankOrder puts failed rows last; rows that tie, and failed rows, keep the Plan's Contender order.
func rowsInRankOrder(rows []Row, ranksAbove func(contender, other Numbers) bool) []*Row {
	ranked := make([]*Row, len(rows))
	for i := range rows {
		ranked[i] = &rows[i]
	}
	slices.SortStableFunc(ranked, func(row, other *Row) int {
		switch {
		case row.Failed() != other.Failed():
			if row.Failed() {
				return 1
			}
			return -1
		case row.Failed():
			return 0
		case ranksAbove(row.Median, other.Median):
			return -1
		case ranksAbove(other.Median, row.Median):
			return 1
		}
		return 0
	})
	return ranked
}

func workloadDescription(workload plan.Workload) string {
	if workload.RequestsPerSecond == 0 {
		return fmt.Sprintf("%d keep-alive connections, each sending its next request as soon as the last one is answered.", workload.Connections)
	}
	description := fmt.Sprintf("%s requests per second over %d keep-alive connections", withThousandsSeparators(float64(workload.RequestsPerSecond)), workload.Connections)
	if workload.CorrectCoordinatedOmission {
		description += ", latency corrected for coordinated omission"
	}
	return description + "."
}

func tableLine(cells []string) string {
	return "| " + strings.Join(cells, " | ") + " |"
}

func cores(count int) string {
	if count == 1 {
		return "1 core"
	}
	return strconv.Itoa(count) + " cores"
}

func inMilliseconds(milliseconds float64) string {
	return strconv.FormatFloat(milliseconds, 'f', 2, 64) + " ms"
}

func inMegabytes(megabytes float64) string {
	return strconv.FormatFloat(megabytes, 'f', 1, 64) + " MB"
}

func withThousandsSeparators(value float64) string {
	digits := strconv.FormatInt(int64(math.Round(value)), 10)
	var grouped strings.Builder
	for i, digit := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			grouped.WriteByte(',')
		}
		grouped.WriteRune(digit)
	}
	return grouped.String()
}
