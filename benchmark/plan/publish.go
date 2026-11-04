package plan

import (
	"errors"
	"fmt"
	"strings"
)

// PublishTagMessageLine in an annotated tag message asks a release-triggered
// run to publish (`make tag VERSION=... BENCHMARK=publish` writes it).
const PublishTagMessageLine = "benchmark: publish"

// PublishRequest is whether the maintainer asked for the section on the
// release page, and why or why not. Report publishes only when no row failed too.
type PublishRequest struct {
	Requested bool   `json:"requested"`
	Reason    string `json:"reason"`
}

// publishRequestFor errors when publish is ticked where nothing can be
// published, so the run stops before measuring anything.
func publishRequestFor(inputs *Inputs) (PublishRequest, error) {
	ref, refIsReleaseTag := refReleaseTag(inputs)
	if inputs.PublishTicked {
		if err := checkPublishTicked(inputs, refIsReleaseTag); err != nil {
			return PublishRequest{}, fmt.Errorf("publish is ticked, but %w", err)
		}
	}
	switch inputs.Trigger {
	case ReleaseTrigger:
		return releaseTriggeredPublishRequest(inputs, ref), nil
	case ManualTrigger:
		if !refIsReleaseTag {
			return notRequested(inputs.Ref + " is not a release tag, and only a release page takes the numbers"), nil
		}
		if inputs.PublishTicked {
			return PublishRequest{Requested: true, Reason: "publish was ticked for this manual run"}, nil
		}
		return notRequested("publish was not ticked for this manual run"), nil
	}
	return notRequested(fmt.Sprintf("a %s run never publishes", inputs.Trigger)), nil
}

func checkPublishTicked(inputs *Inputs, refIsReleaseTag bool) error {
	switch {
	case inputs.Trigger == ReleaseTrigger:
		return errors.New("a release-triggered run publishes only when its tag message has the line `" + PublishTagMessageLine + "`")
	case inputs.Trigger != ManualTrigger:
		return fmt.Errorf("a %s run never publishes", inputs.Trigger)
	case !refIsReleaseTag:
		return fmt.Errorf("%s is not a release tag: only a release page takes the numbers. Run again without publish", inputs.Ref)
	case !inputs.RefHasGitHubRelease:
		return fmt.Errorf("%s has no GitHub release to publish to", inputs.Ref)
	}
	return nil
}

func releaseTriggeredPublishRequest(inputs *Inputs, ref releaseTag) PublishRequest {
	switch {
	case ref.prerelease:
		return notRequested(inputs.Ref + " is a prerelease tag, which a release-triggered run skips")
	case inputs.TagMessage == "":
		return notRequested(inputs.Ref + " has no tag message, so no line `" + PublishTagMessageLine + "`")
	case tagMessageHasPublishLine(inputs.TagMessage):
		return PublishRequest{Requested: true, Reason: "the " + inputs.Ref + " tag message has the line `" + PublishTagMessageLine + "`"}
	}
	return notRequested("the " + inputs.Ref + " tag message has no line `" + PublishTagMessageLine + "`")
}

func tagMessageHasPublishLine(tagMessage string) bool {
	for line := range strings.SplitSeq(tagMessage, "\n") {
		if strings.TrimSuffix(line, "\r") == PublishTagMessageLine {
			return true
		}
	}
	return false
}

func notRequested(reason string) PublishRequest {
	return PublishRequest{Reason: reason}
}
