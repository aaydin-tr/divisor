package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/aaydin-tr/divisor/benchmark/plan"
	"github.com/aaydin-tr/divisor/benchmark/report"
)

// workflowRunInputs reads the ref the benchmark workflow measures from its
// checkout, which must be on that ref with full history and force-fetched tags.
func workflowRunInputs(ctx context.Context, command *commandLine, logOutput io.Writer) (plan.Inputs, error) {
	if command.ref == "" {
		return plan.Inputs{}, fmt.Errorf("a %s run needs --ref", command.trigger)
	}
	refIsTag, refCommit, err := resolveRef(ctx, command.checkoutDirectory, command.ref)
	if err != nil {
		return plan.Inputs{}, err
	}
	checkoutCommit, err := git(ctx, command.checkoutDirectory, "rev-parse", "HEAD")
	if err != nil {
		return plan.Inputs{}, err
	}
	if checkoutCommit != refCommit {
		return plan.Inputs{}, fmt.Errorf("the checkout %s is on %s, not on %s (%s)", command.checkoutDirectory, checkoutCommit, command.ref, refCommit)
	}
	uncommittedChanges, err := git(ctx, command.checkoutDirectory, "status", "--porcelain")
	if err != nil {
		return plan.Inputs{}, err
	}

	inputs := plan.Inputs{
		Trigger:                       command.trigger,
		Ref:                           command.ref,
		Commit:                        refCommit,
		CheckoutHasUncommittedChanges: uncommittedChanges != "",
	}
	if refIsTag {
		inputs.TagMessage, err = annotatedTagMessage(ctx, command.checkoutDirectory, command.ref, logOutput)
	}
	if err == nil && refIsTag && command.publish && command.trigger == plan.ManualTrigger {
		inputs.RefHasGitHubRelease, err = githubReleaseExists(ctx, command.ref)
	}
	return inputs, err
}

func resolveRef(ctx context.Context, checkoutDirectory, ref string) (refIsTag bool, commit string, err error) {
	if commit, err := git(ctx, checkoutDirectory, "rev-parse", "--verify", "--quiet", "refs/tags/"+ref+"^{commit}"); err == nil {
		return true, commit, nil
	}
	for _, branch := range []string{"refs/heads/" + ref, "refs/remotes/origin/" + ref} {
		if commit, err := git(ctx, checkoutDirectory, "rev-parse", "--verify", "--quiet", branch+"^{commit}"); err == nil {
			return false, commit, nil
		}
	}
	return false, "", fmt.Errorf("%s is neither a tag nor a branch in %s", ref, checkoutDirectory)
}

// annotatedTagMessage is empty for a lightweight tag, whose %(contents) would
// be its commit's message.
func annotatedTagMessage(ctx context.Context, checkoutDirectory, tag string, logOutput io.Writer) (string, error) {
	tagRef := "refs/tags/" + tag
	objectType, err := git(ctx, checkoutDirectory, "for-each-ref", "--format=%(objecttype)", tagRef)
	if err != nil {
		return "", err
	}
	if objectType != "tag" {
		fmt.Fprintf(logOutput, "%s is a lightweight tag: it has no message\n", tag)
		return "", nil
	}
	message, err := git(ctx, checkoutDirectory, "for-each-ref", "--format=%(contents)", tagRef)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(logOutput, "%s is an annotated tag with the message:\n%s\n", tag, indented(message))
	return message, nil
}

func skippedRunSummary(decided *plan.Plan) string {
	return "## Release benchmark skipped\n\n" + decided.SkipReason + "\n"
}

func failedRunSummary(err error) string {
	return "## Release benchmark failed\n\n```\n" + err.Error() + "\n```\n"
}

// failedRowsError fails the run once its report is written: a failed row
// means a Contender's Equivalent setup is broken.
func failedRowsError(built *report.Report) error {
	failed := built.FailedRows()
	if len(failed) == 0 {
		return nil
	}
	return fmt.Errorf("failed rows (see Failed: under the tables): %s", strings.Join(failed, ", "))
}

// appendMarkdownBlock relies on every block ending in a newline: one more makes the blank line between them.
func appendMarkdownBlock(markdown, block string) string {
	if markdown == "" {
		return block
	}
	return markdown + "\n" + block
}

func indented(text string) string {
	return "    " + strings.ReplaceAll(text, "\n", "\n    ")
}
