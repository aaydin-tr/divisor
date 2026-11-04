package plan

import (
	"strings"
	"testing"
)

func TestPublishRequest(t *testing.T) {
	releaseTagged := func(tag, message string) Inputs {
		return Inputs{Trigger: ReleaseTrigger, Ref: tag, Tags: []string{"v1.1.0", tag}, TagMessage: message}
	}
	manualRun := func(ref string, publishTicked, refHasGitHubRelease bool) Inputs {
		return Inputs{Trigger: ManualTrigger, Ref: ref, Tags: []string{"v1.1.0", "v1.2.0", "v1.3.0-rc.1"},
			PublishTicked: publishTicked, RefHasGitHubRelease: refHasGitHubRelease}
	}
	tests := []struct {
		name          string
		inputs        Inputs
		wantRequested bool
		wantReason    string
	}{
		{"tag message with the line", releaseTagged("v1.2.0", "v1.2.0\n\nbenchmark: publish\n"),
			true, "the v1.2.0 tag message has the line `benchmark: publish`"},
		{"tag message with the line among others", releaseTagged("v1.2.0", "v1.2.0\n\nFixes the reload race.\nbenchmark: publish"),
			true, "the v1.2.0 tag message has the line `benchmark: publish`"},
		{"tag message with CRLF line endings", releaseTagged("v1.2.0", "v1.2.0\r\n\r\nbenchmark: publish\r\n"),
			true, "the v1.2.0 tag message has the line `benchmark: publish`"},
		{"tag message without the line", releaseTagged("v1.2.0", "v1.2.0\n"),
			false, "the v1.2.0 tag message has no line `benchmark: publish`"},
		{"lightweight tag", releaseTagged("v1.2.0", ""),
			false, "v1.2.0 has no tag message, so no line `benchmark: publish`"},
		{"near miss: capitalized", releaseTagged("v1.2.0", "v1.2.0\n\nBenchmark: publish"),
			false, "the v1.2.0 tag message has no line `benchmark: publish`"},
		{"near miss: no space", releaseTagged("v1.2.0", "v1.2.0\n\nbenchmark:publish"),
			false, "the v1.2.0 tag message has no line `benchmark: publish`"},
		{"near miss: more on the line", releaseTagged("v1.2.0", "v1.2.0\n\nbenchmark: publish please"),
			false, "the v1.2.0 tag message has no line `benchmark: publish`"},
		{"near miss: indented", releaseTagged("v1.2.0", "v1.2.0\n\n  benchmark: publish"),
			false, "the v1.2.0 tag message has no line `benchmark: publish`"},
		{"near miss: in the subject", releaseTagged("v1.2.0", "v1.2.0 benchmark: publish"),
			false, "the v1.2.0 tag message has no line `benchmark: publish`"},
		{"manual run on a tag, publish ticked", manualRun("v1.2.0", true, true),
			true, "publish was ticked for this manual run"},
		{"manual run on an rc, publish ticked", manualRun("v1.3.0-rc.1", true, true),
			true, "publish was ticked for this manual run"},
		{"manual run on a tag, publish not ticked", manualRun("v1.2.0", false, true),
			false, "publish was not ticked for this manual run"},
		{"manual run on a branch, publish not ticked", manualRun("main", false, false),
			false, "main is not a release tag, and only a release page takes the numbers"},
		{"local run", Inputs{Trigger: LocalTrigger, Ref: "v1.2.0", Tags: []string{"v1.2.0"}},
			false, "a local run never publishes"},
		{"smoke run", Inputs{Trigger: SmokeTrigger, Ref: "main"},
			false, "a smoke run never publishes"},
		{"skipped release-triggered prerelease", releaseTagged("v1.3.0-rc.1", "v1.3.0-rc.1\n\nbenchmark: publish"),
			false, "v1.3.0-rc.1 is a prerelease tag, which a release-triggered run skips"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.inputs.Commit = commit
			decided, err := Decide(&test.inputs)
			if err != nil {
				t.Fatalf("Decide: %v", err)
			}
			if decided.Publish.Requested != test.wantRequested || decided.Publish.Reason != test.wantReason {
				t.Errorf("publish = %+v, want requested %v because %q", decided.Publish, test.wantRequested, test.wantReason)
			}
		})
	}
}

func TestPublishTickedWhereNothingCanBePublishedIsAnError(t *testing.T) {
	tests := []struct {
		name      string
		inputs    Inputs
		wantError string
	}{
		{"manual run on a branch", Inputs{Trigger: ManualTrigger, Ref: "main", Tags: []string{"v1.2.0"}, PublishTicked: true},
			"publish is ticked, but main is not a release tag: only a release page takes the numbers. Run again without publish"},
		{"manual run on a tag without a GitHub release", Inputs{Trigger: ManualTrigger, Ref: "v1.2.0", Tags: []string{"v1.2.0"}, PublishTicked: true},
			"publish is ticked, but v1.2.0 has no GitHub release to publish to"},
		{"local run", Inputs{Trigger: LocalTrigger, Ref: "main", PublishTicked: true},
			"publish is ticked, but a local run never publishes"},
		{"release-triggered run", Inputs{Trigger: ReleaseTrigger, Ref: "v1.2.0", Tags: []string{"v1.2.0"}, PublishTicked: true, RefHasGitHubRelease: true},
			"publish is ticked, but a release-triggered run publishes only when its tag message has the line `benchmark: publish`"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.inputs.Commit = commit
			_, err := Decide(&test.inputs)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Errorf("Decide error = %v, want one containing %q", err, test.wantError)
			}
		})
	}
}
