package plan

import (
	"strings"
	"testing"
)

func TestReleaseTriggeredRunOnAPrereleaseTagIsSkipped(t *testing.T) {
	decided, err := Decide(&Inputs{Trigger: ReleaseTrigger, Ref: "v1.1.0-rc.1", Tags: []string{"v1.1.0-rc.1"}, Commit: commit})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}

	if !decided.Skipped() {
		t.Fatalf("plan = %+v, want a skipped run", decided)
	}
	if !strings.Contains(decided.SkipReason, "v1.1.0-rc.1 is a prerelease tag") {
		t.Errorf("skip reason = %q, want it to name the prerelease tag", decided.SkipReason)
	}
	if len(decided.Turns()) != 0 {
		t.Errorf("a skipped run measures nothing, got turns %+v", decided.Turns())
	}
}

func TestRunsThatAreNeverSkipped(t *testing.T) {
	tests := []struct {
		name   string
		inputs Inputs
	}{
		{"release-triggered stable tag", Inputs{Trigger: ReleaseTrigger, Ref: "v1.1.0", Tags: []string{"v1.1.0"}}},
		{"manual run on an rc", Inputs{Trigger: ManualTrigger, Ref: "v1.1.0-rc.1", Tags: []string{"v1.1.0-rc.1"}}},
		{"manual run on a branch", Inputs{Trigger: ManualTrigger, Ref: "main"}},
		{"local run", Inputs{Trigger: LocalTrigger, Ref: "feature/x"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.inputs.Commit = commit
			decided, err := Decide(&test.inputs)
			if err != nil {
				t.Fatalf("Decide: %v", err)
			}
			if decided.Skipped() {
				t.Errorf("skipped: %s", decided.SkipReason)
			}
		})
	}
}

func TestDivisorImageUnderTest(t *testing.T) {
	tests := []struct {
		name          string
		inputs        Inputs
		wantImage     string
		wantVersion   string
		wantFromBuild bool
	}{
		{"release-triggered tag", Inputs{Trigger: ReleaseTrigger, Ref: "v1.2.0", Tags: []string{"v1.2.0"}},
			"ghcr.io/aaydin-tr/divisor:v1.2.0", "v1.2.0", false},
		{"manual run on an old tag", Inputs{Trigger: ManualTrigger, Ref: "v1.0.0", Tags: []string{"v1.0.0"}},
			"ghcr.io/aaydin-tr/divisor:v1.0.0", "v1.0.0", false},
		{"manual run on an rc", Inputs{Trigger: ManualTrigger, Ref: "v1.1.0-rc.1", Tags: []string{"v1.1.0-rc.1"}},
			"ghcr.io/aaydin-tr/divisor:v1.1.0-rc.1", "v1.1.0-rc.1", false},
		{"manual run on a branch", Inputs{Trigger: ManualTrigger, Ref: "main"},
			CheckoutDivisorImage, "main@aa36fa6", true},
		{"local run on a tag measures the checkout", Inputs{Trigger: LocalTrigger, Ref: "v1.0.0", Tags: []string{"v1.0.0"}, CheckoutHasUncommittedChanges: true},
			CheckoutDivisorImage, "v1.0.0@aa36fa6-dirty", true},
		{"smoke run on a tag measures the checkout", Inputs{Trigger: SmokeTrigger, Ref: "v1.0.0", Tags: []string{"v1.0.0"}},
			CheckoutDivisorImage, "v1.0.0@aa36fa6", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.inputs.Commit = commit
			decided, err := Decide(&test.inputs)
			if err != nil {
				t.Fatalf("Decide: %v", err)
			}
			divisor := decided.Contenders[0]
			if divisor.Name != "divisor" || divisor.Image != test.wantImage || divisor.Version != test.wantVersion || divisor.ImageFromCheckout != test.wantFromBuild {
				t.Errorf("divisor = %s %s on %s (from checkout: %v), want %s on %s (from checkout: %v)",
					divisor.Name, divisor.Version, divisor.Image, divisor.ImageFromCheckout, test.wantVersion, test.wantImage, test.wantFromBuild)
			}
			if divisor.BuildArgs["DIVISOR_IMAGE"] != test.wantImage {
				t.Errorf("build args = %v, want DIVISOR_IMAGE=%s", divisor.BuildArgs, test.wantImage)
			}
			if divisor.ID != "divisor" || divisor.Definition != "divisor" {
				t.Errorf("divisor = ID %q on definition %q, want both %q", divisor.ID, divisor.Definition, "divisor")
			}
		})
	}
}

func TestReleaseTriggeredRunNeedsATag(t *testing.T) {
	_, err := Decide(&Inputs{Trigger: ReleaseTrigger, Ref: "main", Commit: commit})
	if err == nil || !strings.Contains(err.Error(), "main is not a release tag") {
		t.Errorf("Decide error = %v, want one saying main is not a release tag", err)
	}
}
