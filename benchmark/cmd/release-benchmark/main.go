// Command release-benchmark runs a Release benchmark: Plan decides, Run measures with Docker and oha,
// Report writes the section and benchmark.json. Run it from the benchmark module.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/aaydin-tr/divisor/benchmark/equivalentsetup"
	"github.com/aaydin-tr/divisor/benchmark/plan"
	"github.com/aaydin-tr/divisor/benchmark/recording"
	"github.com/aaydin-tr/divisor/benchmark/report"
	"github.com/aaydin-tr/divisor/benchmark/run"
)

const (
	sectionFileName       = "section.md"
	benchmarkJSONFileName = "benchmark.json"
	benchmarkDirectory    = "."

	writtenFilePermissions = 0o600
)

const commandUsage = `Usage:
  release-benchmark [--checkout ..] [--results results] [--smoke]             run on the checkout, then report
  release-benchmark --trigger release|manual --ref REF [--summary FILE] ...   what the benchmark workflow runs
  release-benchmark report [--results results]                               report a saved recording again
  release-benchmark splice --notes FILE --section FILE --out FILE             put a section into release notes

A failed row exits non-zero after the report is written; so does a smoke run with a Contender
that is not in the Equivalent setup. --publish-verdict receives publish=true only when
publishing was requested and no row failed.

Flags:
`

type commandLine struct {
	trigger           plan.Trigger
	smoke             bool
	ref               string
	checkoutDirectory string
	resultsDirectory  string
	summaryFile       string

	publish            bool
	publishVerdictFile string
}

func main() {
	log.SetFlags(0)
	if err := runCommand(os.Args[1:], os.Stderr); err != nil {
		log.Fatal(err)
	}
}

func runCommand(arguments []string, logOutput io.Writer) error {
	if len(arguments) > 0 && arguments[0] == "splice" {
		return spliceReleaseNotes(arguments[1:])
	}
	reportOnly := len(arguments) > 0 && arguments[0] == "report"
	if reportOnly {
		arguments = arguments[1:]
	}

	flags := flag.NewFlagSet("release-benchmark", flag.ExitOnError)
	flags.Usage = func() {
		fmt.Fprint(flags.Output(), commandUsage)
		flags.PrintDefaults()
	}
	trigger := flags.String("trigger", string(plan.LocalTrigger), "how the run was started: local, release or manual")
	ref := flags.String("ref", "", "the tag or branch to measure, which the checkout is on (release and manual runs)")
	checkoutDirectory := flags.String("checkout", "..", "the divisor checkout to benchmark")
	resultsDirectory := flags.String("results", "results", "where the raw results, "+benchmarkJSONFileName+" and "+sectionFileName+" are written")
	summaryFile := flags.String("summary", "", "a Markdown file the run summary is appended to, such as $GITHUB_STEP_SUMMARY")
	smoke := flags.Bool("smoke", false, "run the short smoke schedule on the checkout and fail when a Contender is not in the Equivalent setup")
	publish := flags.Bool("publish", false, "a manual run's publish input: put the section on the ref's release page")
	publishVerdictFile := flags.String("publish-verdict", "", "a file publish=true or publish=false is appended to, such as $GITHUB_OUTPUT")
	_ = flags.Parse(arguments)

	if reportOnly {
		return reportSavedRecording(*resultsDirectory)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	command := commandLine{
		trigger:           plan.Trigger(*trigger),
		smoke:             *smoke,
		ref:               *ref,
		checkoutDirectory: *checkoutDirectory,
		resultsDirectory:  *resultsDirectory,
		summaryFile:       *summaryFile,

		publish:            *publish,
		publishVerdictFile: *publishVerdictFile,
	}
	summary, verdict, err := runReleaseBenchmark(ctx, &command, logOutput)
	if err != nil {
		summary = appendMarkdownBlock(summary, failedRunSummary(err))
		verdict.Publish = false
	}
	if command.summaryFile != "" {
		err = errors.Join(err, appendToFile(command.summaryFile, summary))
	}
	if command.publishVerdictFile != "" {
		err = errors.Join(err, appendToFile(command.publishVerdictFile, publishVerdictOutput(verdict)))
	}
	return err
}

// runReleaseBenchmark returns what the run summary shows, also next to an
// error, and whether the section goes on the release page.
func runReleaseBenchmark(ctx context.Context, command *commandLine, logOutput io.Writer) (string, report.PublishVerdict, error) {
	summary, verdict := "", report.PublishVerdict{}
	inputs, err := runInputs(ctx, command, logOutput)
	if err != nil {
		return summary, verdict, err
	}
	decided, err := plan.Decide(&inputs)
	if err != nil {
		return summary, verdict, err
	}
	if decided.Skipped() {
		fmt.Fprintf(logOutput, "skipped: %s\n", decided.SkipReason)
		verdict = report.SkippedRunPublishVerdict(&decided)
		return appendMarkdownBlock(skippedRunSummary(&decided), verdict.SummaryLine()), verdict, nil
	}
	if err := clearResultsDirectory(command.resultsDirectory); err != nil {
		return summary, verdict, err
	}

	recorded, err := run.Execute(ctx, &decided, run.Options{
		CheckoutDirectory:  command.checkoutDirectory,
		BenchmarkDirectory: benchmarkDirectory,
		ResultsDirectory:   command.resultsDirectory,
		Log:                logOutput,
	})
	if err != nil {
		return summary, verdict, err
	}
	if err := recording.Save(command.resultsDirectory, recorded); err != nil {
		return summary, verdict, err
	}
	built, err := writeReport(command.resultsDirectory, recorded)
	if err != nil {
		return summary, verdict, err
	}
	verdict = built.PublishVerdict()
	summary = appendMarkdownBlock(built.Markdown(), verdict.SummaryLine())
	if err := verifyEquivalentSetup(recorded); err != nil {
		return summary, verdict, err
	}
	return summary, verdict, failedRowsError(built)
}

func runInputs(ctx context.Context, command *commandLine, logOutput io.Writer) (plan.Inputs, error) {
	if command.smoke && command.trigger != plan.LocalTrigger {
		return plan.Inputs{}, fmt.Errorf("--smoke runs on the checkout, so it takes no --trigger %s", command.trigger)
	}
	var inputs plan.Inputs
	var err error
	switch command.trigger {
	case plan.LocalTrigger:
		inputs, err = localRunInputs(ctx, command.checkoutDirectory)
	case plan.ReleaseTrigger, plan.ManualTrigger:
		inputs, err = workflowRunInputs(ctx, command, logOutput)
	default:
		return plan.Inputs{}, fmt.Errorf("--trigger must be local, release or manual, got %q", command.trigger)
	}
	if err != nil {
		return plan.Inputs{}, err
	}
	if command.smoke {
		inputs.Trigger = plan.SmokeTrigger
	}
	inputs.PublishTicked = command.publish
	tags, err := gitOutput(ctx, command.checkoutDirectory, "tag", "--list")
	if err != nil {
		return plan.Inputs{}, err
	}
	inputs.Tags = strings.Fields(tags)
	inputs.BenchmarkCommit, err = gitOutput(ctx, benchmarkDirectory, "rev-parse", "HEAD")
	return inputs, err
}

func reportSavedRecording(resultsDirectory string) error {
	recorded, err := recording.Load(resultsDirectory)
	if err != nil {
		return err
	}
	if _, err := writeReport(resultsDirectory, &recorded); err != nil {
		return err
	}
	return verifyEquivalentSetup(&recorded)
}

func writeReport(resultsDirectory string, recorded *recording.Recording) (*report.Report, error) {
	built, err := report.Build(recorded)
	if err != nil {
		return nil, err
	}
	benchmarkJSON, err := built.BenchmarkJSON()
	if err != nil {
		return nil, err
	}
	section := built.Markdown()
	if err := recording.WriteResultsFile(resultsDirectory, benchmarkJSONFileName, benchmarkJSON); err != nil {
		return nil, err
	}
	if err := recording.WriteResultsFile(resultsDirectory, sectionFileName, []byte(section)); err != nil {
		return nil, err
	}
	fmt.Print(section)
	return built, nil
}

// verifyEquivalentSetup fails a recording whose Plan checks the Equivalent
// setup when any Contender is out of it, naming the Contender and the check.
func verifyEquivalentSetup(recorded *recording.Recording) error {
	if recorded.Plan.Schedule.EquivalentSetupCheck == nil {
		return nil
	}
	failures, err := equivalentsetup.Verify(recorded)
	if err != nil {
		return err
	}
	if len(failures) == 0 {
		log.Printf("Every Contender is in the Equivalent setup.")
		return nil
	}
	for _, failure := range failures {
		log.Printf("Equivalent setup check failed: %s", failure)
	}
	return fmt.Errorf("%d Equivalent setup checks failed", len(failures))
}

// clearResultsDirectory removes only what a run writes, so a mistyped
// --results never deletes anything else.
func clearResultsDirectory(resultsDirectory string) error {
	for _, written := range []string{recording.RawDirectory, recording.FileName, benchmarkJSONFileName, sectionFileName} {
		if err := os.RemoveAll(filepath.Join(resultsDirectory, written)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

func localRunInputs(ctx context.Context, checkoutDirectory string) (plan.Inputs, error) {
	commit, err := gitOutput(ctx, checkoutDirectory, "rev-parse", "HEAD")
	if err != nil {
		return plan.Inputs{}, err
	}
	ref, err := gitOutput(ctx, checkoutDirectory, "symbolic-ref", "--short", "--quiet", "HEAD")
	if err != nil {
		ref, _ = gitOutput(ctx, checkoutDirectory, "describe", "--tags", "--exact-match")
	}
	hasUncommittedChanges, err := checkoutHasUncommittedChanges(ctx, checkoutDirectory)
	if err != nil {
		return plan.Inputs{}, err
	}
	return plan.Inputs{
		Trigger:                       plan.LocalTrigger,
		Ref:                           ref,
		Commit:                        commit,
		CheckoutHasUncommittedChanges: hasUncommittedChanges,
	}, nil
}

func checkoutHasUncommittedChanges(ctx context.Context, checkoutDirectory string) (bool, error) {
	status, err := gitOutput(ctx, checkoutDirectory, "status", "--porcelain")
	return status != "", err
}

func gitOutput(ctx context.Context, directory string, arguments ...string) (string, error) {
	output, err := exec.CommandContext(ctx, "git", append([]string{"-C", directory}, arguments...)...).Output() //nolint:gosec
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(arguments, " "), err)
	}
	return strings.TrimSpace(string(output)), nil
}

func appendToFile(path, content string) error {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, writtenFilePermissions)
	if err != nil {
		return err
	}
	_, err = file.WriteString(content)
	return errors.Join(err, file.Close())
}
