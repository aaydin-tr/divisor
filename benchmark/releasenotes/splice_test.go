package releasenotes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	changelog       = "## Changelog\n* 1a2b3c4 feat: reload backends on SIGHUP\n"
	goReleaserRule  = "---\nReleased by [GoReleaser](https://github.com/goreleaser/goreleaser).\n"
	section         = "## Performance\n\n| Contender | req/s |\n| --- | ---: |\n| divisor | 23,575 |\n"
	splicedSection  = "<!-- release-benchmark:begin -->\n\n" + section + "\n<!-- release-benchmark:end -->"
	emptyMarkerPair = "<!-- release-benchmark:begin -->\n<!-- release-benchmark:end -->"
)

func TestSplice(t *testing.T) {
	tests := []struct {
		name  string
		notes string
		want  string
	}{
		{"markers of a new release: their content is replaced",
			changelog + "\n" + emptyMarkerPair + "\n\n" + goReleaserRule,
			changelog + "\n" + splicedSection + "\n\n" + goReleaserRule},
		{"markers with an earlier section: it is replaced",
			changelog + "\n<!-- release-benchmark:begin -->\n\n## Performance\n\nan earlier run\n\n<!-- release-benchmark:end -->\n\n" + goReleaserRule,
			changelog + "\n" + splicedSection + "\n\n" + goReleaserRule},
		{"markers and no footer: their content is replaced",
			changelog + "\n" + emptyMarkerPair + "\n",
			changelog + "\n" + splicedSection + "\n"},
		{"no markers but the GoReleaser footer: section and markers go directly above it",
			changelog + "\n\n" + goReleaserRule,
			changelog + "\n" + splicedSection + "\n\n" + goReleaserRule},
		{"no markers and a footer without its rule: above the Released by line",
			changelog + "\nReleased by [GoReleaser](https://github.com/goreleaser/goreleaser).\n",
			changelog + "\n" + splicedSection + "\n\nReleased by [GoReleaser](https://github.com/goreleaser/goreleaser).\n"},
		{"neither: the section is appended at the end",
			changelog,
			changelog + "\n" + splicedSection + "\n"},
		{"neither and no trailing newline",
			"Hand-written notes.",
			"Hand-written notes.\n\n" + splicedSection + "\n"},
		{"empty notes",
			"",
			splicedSection + "\n"},
		{"a rule that is not the footer's stays where it is",
			"## Highlights\n\n---\n\n" + changelog,
			"## Highlights\n\n---\n\n" + changelog + "\n" + splicedSection + "\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spliced, err := Splice(test.notes, section)
			if err != nil {
				t.Fatalf("Splice: %v", err)
			}
			if spliced != test.want {
				t.Errorf("spliced notes:\n%s\nwant:\n%s", spliced, test.want)
			}
		})
	}
}

func TestSplicingAnAlreadySplicedBodyChangesNothing(t *testing.T) {
	for _, notes := range []string{
		changelog + "\n" + emptyMarkerPair + "\n\n" + goReleaserRule,
		changelog + "\n\n" + goReleaserRule,
		changelog,
		"",
	} {
		once, err := Splice(notes, section)
		if err != nil {
			t.Fatal(err)
		}
		twice, err := Splice(once, section)
		if err != nil {
			t.Fatal(err)
		}
		if twice != once {
			t.Errorf("splicing again changed the notes:\n%s\nfrom:\n%s", twice, once)
		}
	}
}

// v1.0.0's notes predate the markers: GoReleaser's footer with no slot.
func TestSpliceIntoTheV1_0_0ReleaseNotes(t *testing.T) {
	notes := readTestdata(t, "v1.0.0-notes.md")
	section := readTestdata(t, "section.md")

	spliced, err := Splice(notes, section)
	if err != nil {
		t.Fatalf("Splice: %v", err)
	}

	if want := readTestdata(t, "expected-v1.0.0-notes-spliced.md"); spliced != want {
		t.Errorf("spliced notes:\n%s\nwant:\n%s", spliced, want)
	}
}

func TestSpliceRefusesABrokenMarkerPair(t *testing.T) {
	for name, notes := range map[string]string{
		"begin without end": changelog + "\n<!-- release-benchmark:begin -->\n\n" + goReleaserRule,
		"end before begin":  changelog + "\n<!-- release-benchmark:end -->\n<!-- release-benchmark:begin -->\n",
		"end without begin": changelog + "\n<!-- release-benchmark:end -->\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Splice(notes, section); err == nil || !strings.Contains(err.Error(), "release-benchmark:") {
				t.Errorf("Splice error = %v, want one naming the markers", err)
			}
		})
	}
}

// The slot GoReleaser writes into every new release must be the one Splice fills.
func TestGoReleaserFooterCarriesTheMarkersAboveItsRule(t *testing.T) {
	goReleaserConfig := readFile(t, filepath.Join("..", "..", ".goreleaser.yaml"))

	footerSlot := "    " + BeginMarker + "\n    " + EndMarker + "\n\n    ---\n    Released by [GoReleaser]"
	if !strings.Contains(goReleaserConfig, footerSlot) {
		t.Errorf(".goreleaser.yaml's release footer lacks the marker pair directly above its rule:\n%s", footerSlot)
	}
}

func readTestdata(t *testing.T, name string) string {
	t.Helper()
	return readFile(t, filepath.Join("testdata", name))
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
