package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The workflow passes the run summary file; these runs end before Docker is needed.

func TestReleaseTriggeredRunOnAPrereleaseTagSaysItWasSkipped(t *testing.T) {
	checkout := checkoutOnAnnotatedTag(t, "v1.1.0-rc.1", "v1.1.0-rc.1\n\nbenchmark: publish")
	summaryFile := filepath.Join(t.TempDir(), "summary.md")
	var logOutput bytes.Buffer

	publishVerdictFile := filepath.Join(t.TempDir(), "github-output")
	err := runCommand([]string{"--trigger", "release", "--ref", "v1.1.0-rc.1", "--checkout", checkout,
		"--results", t.TempDir(), "--summary", summaryFile, "--publish-verdict", publishVerdictFile}, &logOutput)
	if err != nil {
		t.Fatalf("a skipped run is not a failure: %v", err)
	}
	if verdict := readFile(t, publishVerdictFile); verdict != "publish=false\n" {
		t.Errorf("publish verdict = %q, want publish=false", verdict)
	}

	summary := readFile(t, summaryFile)
	want := "## Release benchmark skipped\n\nv1.1.0-rc.1 is a prerelease tag, and a release-triggered run measures stable releases only. " +
		"Run the benchmark workflow by hand on v1.1.0-rc.1 to measure it.\n\n" +
		"**Release page:** not published, because v1.1.0-rc.1 is a prerelease tag, which a release-triggered run skips.\n"
	if summary != want {
		t.Errorf("summary:\n%s\nwant:\n%s", summary, want)
	}
	if !strings.Contains(logOutput.String(), "benchmark: publish") {
		t.Errorf("log does not show the annotated tag's message:\n%s", logOutput.String())
	}
}

func TestLightweightTagHasNoMessageInTheLog(t *testing.T) {
	checkout := checkoutOnAnnotatedTag(t, "v1.1.0-rc.1", "v1.1.0-rc.1")
	gitIn(t, checkout, "tag", "v1.1.0-rc.2")
	var logOutput bytes.Buffer

	if err := runCommand([]string{"--trigger", "release", "--ref", "v1.1.0-rc.2", "--checkout", checkout,
		"--results", t.TempDir()}, &logOutput); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(logOutput.String(), "v1.1.0-rc.2 is a lightweight tag") {
		t.Errorf("log:\n%s\nwant it to say the tag is lightweight", logOutput.String())
	}
}

func TestFailedRunPutsTheReasonInTheSummary(t *testing.T) {
	checkout := checkoutOnAnnotatedTag(t, "v1.1.0", "v1.1.0")
	summaryFile := filepath.Join(t.TempDir(), "summary.md")

	err := runCommand([]string{"--trigger", "manual", "--ref", "no-such-ref", "--checkout", checkout,
		"--results", t.TempDir(), "--summary", summaryFile}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("a ref that is neither a tag nor a branch must fail the run")
	}

	summary := readFile(t, summaryFile)
	if !strings.HasPrefix(summary, "## Release benchmark failed\n\n") || !strings.Contains(summary, "no-such-ref is neither a tag nor a branch") {
		t.Errorf("summary:\n%s\nwant the failure and its reason", summary)
	}
}

func TestCheckoutMustBeOnTheMeasuredRef(t *testing.T) {
	checkout := checkoutOnAnnotatedTag(t, "v1.1.0", "v1.1.0")
	gitIn(t, checkout, "commit", "--allow-empty", "--quiet", "--message", "after the release")

	err := runCommand([]string{"--trigger", "release", "--ref", "v1.1.0", "--checkout", checkout,
		"--results", t.TempDir()}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "not on v1.1.0") {
		t.Errorf("error = %v, want one saying the checkout is not on v1.1.0", err)
	}
}

func TestManualRunPublishingABranchFailsBeforeAnythingRuns(t *testing.T) {
	checkout := checkoutOnAnnotatedTag(t, "v1.1.0", "v1.1.0")
	gitIn(t, checkout, "checkout", "--quiet", "-b", "feature/faster-pick")
	summaryFile := filepath.Join(t.TempDir(), "summary.md")
	results := t.TempDir()

	err := runCommand([]string{"--trigger", "manual", "--ref", "feature/faster-pick", "--publish", "--checkout", checkout,
		"--results", results, "--summary", summaryFile}, &bytes.Buffer{})

	want := "publish is ticked, but feature/faster-pick is not a release tag: only a release page takes the numbers. Run again without publish"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want %q", err, want)
	}
	if summary := readFile(t, summaryFile); !strings.HasPrefix(summary, "## Release benchmark failed\n\n") || !strings.Contains(summary, want) {
		t.Errorf("summary:\n%s\nwant the failure and its reason", summary)
	}
	if written, _ := os.ReadDir(results); len(written) != 0 {
		t.Errorf("results directory has %d entries, want nothing measured", len(written))
	}
}

func TestSpliceTheSectionIntoReleaseNotes(t *testing.T) {
	directory := t.TempDir()
	notesFile, sectionFile, splicedFile := filepath.Join(directory, "notes.md"), filepath.Join(directory, "section.md"), filepath.Join(directory, "spliced.md")
	writeFile(t, notesFile, "## Changelog\n* 1a2b3c4 fix: x\n\n---\nReleased by [GoReleaser](https://github.com/goreleaser/goreleaser).\n")
	writeFile(t, sectionFile, "## Performance\n\nnumbers\n")

	if err := runCommand([]string{"splice", "--notes", notesFile, "--section", sectionFile, "--out", splicedFile}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	want := "## Changelog\n* 1a2b3c4 fix: x\n\n<!-- release-benchmark:begin -->\n\n## Performance\n\nnumbers\n\n<!-- release-benchmark:end -->\n\n" +
		"---\nReleased by [GoReleaser](https://github.com/goreleaser/goreleaser).\n"
	if spliced := readFile(t, splicedFile); spliced != want {
		t.Errorf("spliced notes:\n%s\nwant:\n%s", spliced, want)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func checkoutOnAnnotatedTag(t *testing.T, tag, message string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	checkout := t.TempDir()
	gitIn(t, checkout, "init", "--quiet")
	gitIn(t, checkout, "commit", "--allow-empty", "--quiet", "--message", "release")
	gitIn(t, checkout, "tag", "--annotate", "--message", message, tag)
	return checkout
}

func gitIn(t *testing.T, directory string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory, "-c", "user.name=test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false"}, arguments...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(arguments, " "), err, output)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
