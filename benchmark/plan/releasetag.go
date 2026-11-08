package plan

import (
	"regexp"
	"slices"
)

const publishedDivisorImageRepository = "ghcr.io/aaydin-tr/divisor"

var releaseTagPattern = regexp.MustCompile(`^v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$`)

type releaseTag struct {
	prerelease bool
}

func parseReleaseTag(tag string) (releaseTag, bool) {
	parts := releaseTagPattern.FindStringSubmatch(tag)
	if parts == nil {
		return releaseTag{}, false
	}
	prereleaseSuffix := parts[1]
	return releaseTag{prerelease: prereleaseSuffix != ""}, true
}

// refReleaseTag is the ref as a release tag; any other ref counts as a branch.
func refReleaseTag(inputs *Inputs) (releaseTag, bool) {
	ref, isReleaseTag := parseReleaseTag(inputs.Ref)
	return ref, isReleaseTag && slices.Contains(inputs.Tags, inputs.Ref)
}

func publishedDivisorImage(tag string) string {
	return publishedDivisorImageRepository + ":" + tag
}
