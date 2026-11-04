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
	checkoutCommit, err := gitOutput(ctx, command.checkoutDirectory, "rev-parse", "HEAD")
	if err != nil {
		return plan.Inputs{}, err
	}
	if checkoutCommit != refCommit {
		return plan.Inputs{}, fmt.Errorf("the checkout %s is on %s, not on %s (%s)", command.checkoutDirectory, checkoutCommit, command.ref, refCommit)
	}
	hasUncommittedChanges, err := checkoutHasUncommittedChanges(ctx, command.checkoutDirectory)
	if err != nil {
		return plan.Inputs{}, err
	}

	inputs := plan.Inputs{
		Trigger:                       command.trigger,
		Ref:                           command.ref,
		Commit:                        refCommit,
		CheckoutHasUncommittedChanges: hasUncommittedChanges,
	}
	if !refIsTag {
		return inputs, nil
	}
	if inputs.TagMessage, err = annotatedTagMessage(ctx, command.checkoutDirectory, command.ref, logOutput); err != nil {
		return plan.Inputs{}, err
	}
	if command.publish && command.trigger == plan.ManualTrigger {
		if inputs.RefHasGitHubRelease, err = githubReleaseExists(ctx, command.ref); err != nil {
			return plan.Inputs{}, err
		}
	}
	return inputs, nil
}

func resolveRef(ctx context.Context, checkoutDirectory, ref string) (refIsTag bool, refCommit string, err error) {
	if refCommit, err = gitOutput(ctx, checkoutDirectory, "rev-parse", "--verify", "--quiet", "refs/tags/"+ref+"^{commit}"); err == nil {
		return true, refCommit, nil
	}
	for _, branch := range []string{"refs/heads/" + ref, "refs/remotes/origin/" + ref} {
		if refCommit, err = gitOutput(ctx, checkoutDirectory, "rev-parse", "--verify", "--quiet", branch+"^{commit}"); err == nil {
			return false, refCommit, nil
		}
	}
	return false, "", fmt.Errorf("%s is neither a tag nor a branch in %s", ref, checkoutDirectory)
}

// annotatedTagMessage is empty for a lightweight tag, whose %(contents) would
// be its commit's message.
func annotatedTagMessage(ctx context.Context, checkoutDirectory, tag string, logOutput io.Writer) (string, error) {
	tagRef := "refs/tags/" + tag
	objectType, err := gitOutput(ctx, checkoutDirectory, "for-each-ref", "--format=%(objecttype)", tagRef)
	if err != nil {
		return "", err
	}
	if objectType != "tag" {
		fmt.Fprintf(logOutput, "%s is a lightweight tag: it has no message\n", tag)
		return "", nil
	}
	message, err := gitOutput(ctx, checkoutDirectory, "for-each-ref", "--format=%(contents)", tagRef)
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
