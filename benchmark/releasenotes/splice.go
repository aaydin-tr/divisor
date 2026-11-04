// Package releasenotes puts the Release benchmark section into a release's
// notes (Splice). It is pure: the publish job reads and writes the notes with gh.
package releasenotes

import (
	"fmt"
	"strings"
)

// BeginMarker and EndMarker hold the section; GoReleaser's release footer
// (.goreleaser.yaml) writes the empty pair into every new release.
const (
	BeginMarker = "<!-- release-benchmark:begin -->"
	EndMarker   = "<!-- release-benchmark:end -->"
)

const (
	goReleaserFooterLinePrefix = "Released by [GoReleaser]("
	goReleaserFooterRule       = "---"
)

// Splice returns the notes with the section between the markers: replacing
// what they hold, else directly above GoReleaser's footer, else at the end.
func Splice(notes, section string) (string, error) {
	markedSection := BeginMarker + "\n\n" + strings.TrimRight(section, "\n") + "\n\n" + EndMarker
	begin, end := strings.Index(notes, BeginMarker), strings.Index(notes, EndMarker)
	switch {
	case begin >= 0 && end > begin:
		return notes[:begin] + markedSection + notes[end+len(EndMarker):], nil
	case begin >= 0 || end >= 0:
		return "", fmt.Errorf("the release notes have a broken marker pair: want %s, then %s", BeginMarker, EndMarker)
	}
	if footer, found := goReleaserFooterStart(notes); found {
		return afterBlankLine(notes[:footer], markedSection) + "\n\n" + notes[footer:], nil
	}
	return afterBlankLine(notes, markedSection) + "\n", nil
}

// goReleaserFooterStart is where the footer's last "Released by" line
// starts, or the "---" rule directly above it.
func goReleaserFooterStart(notes string) (int, bool) {
	footerLine := strings.LastIndex("\n"+notes, "\n"+goReleaserFooterLinePrefix)
	if footerLine < 0 {
		return 0, false
	}
	aboveFooterLine := strings.TrimRight(notes[:footerLine], "\n")
	ruleLine := strings.LastIndex(aboveFooterLine, "\n") + 1
	if aboveFooterLine[ruleLine:] == goReleaserFooterRule {
		return ruleLine, true
	}
	return footerLine, true
}

func afterBlankLine(notes, block string) string {
	notes = strings.TrimRight(notes, "\n")
	if notes == "" {
		return block
	}
	return notes + "\n\n" + block
}
