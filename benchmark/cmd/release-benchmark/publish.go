package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/aaydin-tr/divisor/benchmark/releasenotes"
	"github.com/aaydin-tr/divisor/benchmark/report"
)

// spliceReleaseNotes is the publish job's step between reading the release
// notes with gh and writing them back.
func spliceReleaseNotes(arguments []string) error {
	flags := flag.NewFlagSet("release-benchmark splice", flag.ExitOnError)
	notesFile := flags.String("notes", "", "the release's current notes")
	sectionFile := flags.String("section", "", "the section to splice in, "+sectionFileName+" of a run")
	splicedFile := flags.String("out", "", "where the spliced notes are written")
	_ = flags.Parse(arguments)
	if *notesFile == "" || *sectionFile == "" || *splicedFile == "" {
		return errors.New("splice needs --notes, --section and --out")
	}

	notes, err := os.ReadFile(*notesFile)
	if err != nil {
		return err
	}
	section, err := os.ReadFile(*sectionFile)
	if err != nil {
		return err
	}
	spliced, err := releasenotes.Splice(string(notes), string(section))
	if err != nil {
		return err
	}
	return os.WriteFile(*splicedFile, []byte(spliced), writtenFilePermissions) //nolint:gosec
}

// githubReleaseExists asks gh, so the benchmark workflow's run step needs GH_TOKEN and GH_REPO.
func githubReleaseExists(ctx context.Context, tag string) (bool, error) {
	var stderr bytes.Buffer
	lookup := exec.CommandContext(ctx, "gh", "release", "view", tag, "--json", "tagName")
	lookup.Stderr = &stderr
	err := lookup.Run()
	switch {
	case err == nil:
		return true, nil
	case strings.Contains(stderr.String(), "release not found"):
		return false, nil
	}
	return false, fmt.Errorf("looking up the GitHub release of %s: %w: %s", tag, err, strings.TrimSpace(stderr.String()))
}

func publishVerdictOutput(verdict report.PublishVerdict) string {
	return fmt.Sprintf("publish=%t\n", verdict.Publish)
}
