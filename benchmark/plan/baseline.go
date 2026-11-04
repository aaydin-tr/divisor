package plan

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
)

const (
	DivisorContenderID  = divisorName
	BaselineContenderID = "divisor-baseline"

	publishedDivisorImageRepository = "ghcr.io/aaydin-tr/divisor"

	configCompatibilityPromise = "divisor's config is only compatible within one major version"
)

// BaselineRelease is the previous stable divisor release the run measures
// next to the divisor being benchmarked, or why the run has none.
type BaselineRelease struct {
	Tag              string `json:"tag,omitempty"`
	NoBaselineReason string `json:"no_baseline_reason,omitempty"`
}

func (b BaselineRelease) Exists() bool { return b.Tag != "" }

var releaseTagPattern = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$`)

type releaseTag struct {
	tag                 string
	major, minor, patch int
	prerelease          bool
}

func parseReleaseTag(tag string) (releaseTag, bool) {
	parts := releaseTagPattern.FindStringSubmatch(tag)
	if parts == nil {
		return releaseTag{}, false
	}
	major, _ := strconv.Atoi(parts[1])
	minor, _ := strconv.Atoi(parts[2])
	patch, _ := strconv.Atoi(parts[3])
	return releaseTag{tag: tag, major: major, minor: minor, patch: patch, prerelease: parts[4] != ""}, true
}

// refReleaseTag is the ref as a release tag; any other ref counts as a branch.
func refReleaseTag(inputs *Inputs) (releaseTag, bool) {
	ref, isReleaseTag := parseReleaseTag(inputs.Ref)
	return ref, isReleaseTag && slices.Contains(inputs.Tags, inputs.Ref)
}

func (r releaseTag) below(other releaseTag) bool {
	return slices.Compare([]int{r.major, r.minor, r.patch}, []int{other.major, other.minor, other.patch}) < 0
}

// baselineReleaseFor refuses a Baseline of another major version: divisor's config is only
// promised compatible within one.
func baselineReleaseFor(inputs *Inputs) BaselineRelease {
	ref, refIsReleaseTag := refReleaseTag(inputs)
	refIsStableTag := refIsReleaseTag && !ref.prerelease

	var baseline releaseTag
	found := false
	for _, tag := range inputs.Tags {
		candidate, isReleaseTag := parseReleaseTag(tag)
		if !isReleaseTag || candidate.prerelease || (refIsStableTag && !candidate.below(ref)) {
			continue
		}
		if !found || baseline.below(candidate) {
			baseline, found = candidate, true
		}
	}

	switch {
	case !found && refIsStableTag:
		return BaselineRelease{NoBaselineReason: inputs.Ref + " is the first stable release"}
	case !found:
		return BaselineRelease{NoBaselineReason: "the checkout has no stable release tag"}
	case refIsStableTag && baseline.major != ref.major:
		return BaselineRelease{NoBaselineReason: fmt.Sprintf("%s, the stable release before %s, is another major version, and %s",
			baseline.tag, inputs.Ref, configCompatibilityPromise)}
	case refIsReleaseTag && baseline.major != ref.major:
		return BaselineRelease{NoBaselineReason: fmt.Sprintf("%s, the latest stable release, is another major version than %s, and %s",
			baseline.tag, inputs.Ref, configCompatibilityPromise)}
	}
	return BaselineRelease{Tag: baseline.tag}
}

// withBaselineRelease records the run's Baseline release and puts its row
// directly under divisor.
func withBaselineRelease(decided *Plan, inputs *Inputs) {
	decided.BaselineRelease = baselineReleaseFor(inputs)
	if decided.BaselineRelease.Exists() {
		divisorRow := slices.IndexFunc(decided.Contenders, func(contender Contender) bool { return contender.ID == DivisorContenderID })
		decided.Contenders = slices.Insert(decided.Contenders, divisorRow+1, baselineContender(decided.BaselineRelease))
	}
}

func baselineContender(baselineRelease BaselineRelease) Contender {
	return divisorContender(BaselineContenderID, divisorName+" (Baseline)", baselineRelease.Tag,
		publishedDivisorImage(baselineRelease.Tag))
}

func publishedDivisorImage(tag string) string {
	return publishedDivisorImageRepository + ":" + tag
}
