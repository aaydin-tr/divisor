package plan

import "fmt"

const (
	// ReleaseTrigger is the benchmark workflow started by a successful release workflow run on a tag.
	ReleaseTrigger Trigger = "release"
	// ManualTrigger is the benchmark workflow started by hand for any tag or branch.
	ManualTrigger Trigger = "manual"
)

func checkTrigger(inputs *Inputs) error {
	if _, refIsReleaseTag := refReleaseTag(inputs); inputs.Trigger == ReleaseTrigger && !refIsReleaseTag {
		return fmt.Errorf("a release-triggered run measures the released tag, and %s is not a release tag", inputs.Ref)
	}
	return nil
}

func skipReason(inputs *Inputs) string {
	if ref, _ := refReleaseTag(inputs); inputs.Trigger == ReleaseTrigger && ref.prerelease {
		return fmt.Sprintf("%s is a prerelease tag, and a release-triggered run measures stable releases only. "+
			"Run the benchmark workflow by hand on %s to measure it.", inputs.Ref, inputs.Ref)
	}
	return ""
}

// divisorUnderTest measures a tag as it was released, never rebuilt from
// today's tree. Local and smoke runs always measure the checkout, changes included.
func divisorUnderTest(inputs *Inputs) Contender {
	measuresARef := inputs.Trigger == ReleaseTrigger || inputs.Trigger == ManualTrigger
	if _, refIsReleaseTag := refReleaseTag(inputs); measuresARef && refIsReleaseTag {
		return publishedDivisor(inputs.Ref)
	}
	return divisorFromCheckout(inputs)
}

func publishedDivisor(tag string) Contender {
	return divisorContender(DivisorContenderID, divisorName, tag, publishedDivisorImage(tag))
}
