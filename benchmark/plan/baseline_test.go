package plan

import (
	"strings"
	"testing"
)

func TestBaselineReleaseIsThePreviousStableReleaseOfTheSameMajorVersion(t *testing.T) {
	tests := []struct {
		name string
		ref  string
		tags string
		// want is the Baseline release's tag, or "none: " and the reason there is none.
		want string
	}{
		{
			name: "the next patch",
			ref:  "v1.0.1",
			tags: "v0.6.0 v1.0.0-rc.1 v1.0.0 v1.0.1",
			want: "v1.0.0",
		},
		{
			name: "the next minor",
			ref:  "v1.1.0",
			tags: "v1.0.0 v1.0.1 v1.1.0-rc.1 v1.1.0-rc.2 v1.1.0",
			want: "v1.0.1",
		},
		{
			name: "a backport shipped after a newer minor",
			ref:  "v1.0.2",
			tags: "v1.0.0 v1.0.1 v1.1.0 v1.0.2",
			want: "v1.0.1",
		},
		{
			name: "a branch",
			ref:  "main",
			tags: "v0.6.0 v1.0.0 v1.1.0 v1.0.2 v1.2.0-rc.1",
			want: "v1.1.0",
		},
		{
			name: "an rc",
			ref:  "v1.2.0-rc.1",
			tags: "v1.0.0 v1.1.0 v1.0.2 v1.2.0-rc.1",
			want: "v1.1.0",
		},
		{
			name: "a major-version change",
			ref:  "v1.0.0",
			tags: "v0.4 v0.5.1 v0.6.0 v1.0.0-rc.1 v1.0.0",
			want: "none: v0.6.0, the stable release before v1.0.0, is another major version, and divisor's config is only compatible within one major version",
		},
		{
			name: "an rc of a new major version",
			ref:  "v2.0.0-rc.1",
			tags: "v1.0.0 v1.3.0 v2.0.0-rc.1",
			want: "none: v1.3.0, the latest stable release, is another major version than v2.0.0-rc.1, and divisor's config is only compatible within one major version",
		},
		{
			name: "the first stable release",
			ref:  "v0.1.0",
			tags: "v0.1.0",
			want: "none: v0.1.0 is the first stable release",
		},
		{
			name: "a checkout without release tags",
			ref:  "main",
			tags: "",
			want: "none: the checkout has no stable release tag",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decided := decideLocalRun(t, Inputs{Ref: test.ref, Tags: strings.Fields(test.tags)})

			got := decided.BaselineRelease.Tag
			if got == "" {
				got = "none: " + decided.BaselineRelease.NoBaselineReason
			}
			if got != test.want {
				t.Errorf("Baseline release = %q\nwant %q", got, test.want)
			}
		})
	}
}

func TestBaselineReleaseRunsFromItsPublishedImageDirectlyUnderDivisor(t *testing.T) {
	decided := decideLocalRun(t, Inputs{Ref: "main", Tags: []string{"v1.0.0", "v1.1.0"}})

	if len(decided.Contenders) < 2 {
		t.Fatalf("Contenders = %+v, want divisor then its Baseline", decided.Contenders)
	}
	if decided.Contenders[0].ID != DivisorContenderID {
		t.Errorf("first row = %q, want divisor", decided.Contenders[0].Name)
	}
	baseline := decided.Contenders[1]
	want := Contender{
		ID:         BaselineContenderID,
		Name:       "divisor (Baseline)",
		Version:    "v1.1.0",
		Image:      "ghcr.io/aaydin-tr/divisor:v1.1.0",
		Definition: "divisor",
		BuildArgs:  map[string]string{"DIVISOR_IMAGE": "ghcr.io/aaydin-tr/divisor:v1.1.0"},
	}
	if baseline.ID != want.ID || baseline.Name != want.Name || baseline.Version != want.Version ||
		baseline.Image != want.Image || baseline.ImageFromCheckout || baseline.Definition != want.Definition ||
		len(baseline.BuildArgs) != 1 || baseline.BuildArgs["DIVISOR_IMAGE"] != want.BuildArgs["DIVISOR_IMAGE"] {
		t.Errorf("Baseline Contender = %+v\nwant %+v", baseline, want)
	}

	var schedule []string
	for _, turn := range decided.Turns() {
		schedule = append(schedule, turn.Contender.ID)
	}
	if got := strings.Join(schedule, " "); got != "divisor divisor-baseline haproxy nginx traefik divisor divisor-baseline haproxy nginx traefik divisor divisor-baseline haproxy nginx traefik" {
		t.Errorf("turns = %s, want the Baseline right after divisor in every round", got)
	}
}

func TestNoBaselineReleaseLeavesDivisorAlone(t *testing.T) {
	decided := decideLocalRun(t, Inputs{Ref: "v1.0.0", Tags: []string{"v0.6.0", "v1.0.0"}})

	for _, contender := range decided.Contenders {
		if contender.ID == BaselineContenderID {
			t.Errorf("Contenders = %+v, want no Baseline row without a Baseline release", decided.Contenders)
		}
	}
}
