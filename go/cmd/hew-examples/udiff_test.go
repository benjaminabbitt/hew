package main

import "testing"

// The transcripts pair every applied file with a conventional unified diff, and
// "conventional" has to mean the same thing GNU diff means or the rendering is
// worse than useless. These cases are `diff -u` output, verbatim.
func TestUnifiedDiffMatchesConvention(t *testing.T) {
	tests := []struct {
		name          string
		before, after string
		want          string
	}{
		{
			name:   "single replacement carries three lines of context",
			before: "provider \"aws\" {\n  region  = \"us-west-1\"\n  profile = \"default\"\n}\n",
			after:  "provider \"aws\" {\n  region  = \"us-west-2\"\n  profile = \"default\"\n}\n",
			want: "--- a\n+++ b\n@@ -1,4 +1,4 @@\n provider \"aws\" {\n" +
				"-  region  = \"us-west-1\"\n+  region  = \"us-west-2\"\n   profile = \"default\"\n }\n",
		},
		{
			name:   "a relocation renders as the deletions GNU diff picks",
			before: "server:\n  host: localhost\n  port: 8080\nnetwork: {}\n",
			after:  "server:\n  port: 8080\nnetwork:\n  host: localhost\n",
			want: "--- a\n+++ b\n@@ -1,4 +1,4 @@\n server:\n-  host: localhost\n" +
				"   port: 8080\n-network: {}\n+network:\n+  host: localhost\n",
		},
		{
			name:   "a pure removal shrinks the after-count",
			before: "a: 1\nb: 2\nc: 3\n",
			after:  "a: 1\nc: 3\n",
			want:   "--- a\n+++ b\n@@ -1,3 +1,2 @@\n a: 1\n-b: 2\n c: 3\n",
		},
		{
			name:   "identical inputs produce nothing at all",
			before: "a: 1\n",
			after:  "a: 1\n",
			want:   "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := unifiedDiff("a", "b", tt.before, tt.after, 3)
			if got != tt.want {
				t.Errorf("unifiedDiff:\ngot:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

// Two distant edits must not be welded into one hunk: the context window is
// what makes a long file's diff readable.
func TestUnifiedDiffSplitsDistantHunks(t *testing.T) {
	before := "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n13\n14\n15\n"
	after := "1\n2\nX\n4\n5\n6\n7\n8\n9\n10\n11\n12\nY\n14\n15\n"
	got := unifiedDiff("a", "b", before, after, 3)
	want := "--- a\n+++ b\n" +
		"@@ -1,6 +1,6 @@\n 1\n 2\n-3\n+X\n 4\n 5\n 6\n" +
		"@@ -10,6 +10,6 @@\n 10\n 11\n 12\n-13\n+Y\n 14\n 15\n"
	if got != want {
		t.Errorf("unifiedDiff:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// A transcript is a terminal session, and a reader should be able to paste a
// line of it into a shell and get the same command back.
func TestShellJoinQuotesOnlyWhatNeedsIt(t *testing.T) {
	tests := []struct {
		argv []string
		want string
	}{
		{[]string{"hew", "apply", "-i", "patch.hew"}, "hew apply -i patch.hew"},
		{[]string{"hew", "apply", "--transforms-out", "-", "p.hew"}, "hew apply --transforms-out - p.hew"},
		{[]string{"hew", "diff", "HEAD:app.json", "app.json"}, "hew diff HEAD:app.json app.json"},
		{[]string{"git", "commit", "-q", "-m", "gateway config"}, "git commit -q -m 'gateway config'"},
		{[]string{"echo", "it's"}, `echo 'it'\''s'`},
	}
	for _, tt := range tests {
		if got := shellJoin(tt.argv); got != tt.want {
			t.Errorf("shellJoin(%q) = %q, want %q", tt.argv, got, tt.want)
		}
	}
}

// The application record's wall-clock stamp is the one thing a second run
// cannot reproduce; everything else on a page must survive redaction untouched.
func TestRedactReplacesOnlyTheClock(t *testing.T) {
	in := "hew-record: 1\napplied_at: \"2026-08-14T14:01:23Z\"\npatch:\n  digest: sha256:b34621bc\n"
	want := "hew-record: 1\napplied_at: \"2026-01-01T00:00:00Z\"\npatch:\n  digest: sha256:b34621bc\n"
	if got := redact(in); got != want {
		t.Errorf("redact() = %q, want %q", got, want)
	}
	untouched := "timeout: 30\nversion: 2026-08-14\n"
	if got := redact(untouched); got != untouched {
		t.Errorf("redact() rewrote something that was not a timestamp: %q", got)
	}
}

// A `.hew` patch is rendered as `diff` deliberately — Shiki has no hew grammar,
// and the diff grammar is nearly right for a margin-carrying mirror.
func TestFenceFor(t *testing.T) {
	tests := map[string]string{
		"region.hew":    "diff",
		"move.hewt":     "yaml",
		"old.patch":     "diff",
		"app.json":      "json",
		"config.yml":    "yaml",
		"pyproject.tml": "text",
		"cfg.toml":      "toml",
	}
	for path, want := range tests {
		if got := fenceFor(path); got != want {
			t.Errorf("fenceFor(%q) = %q, want %q", path, got, want)
		}
	}
}

// A scenario must not be able to claim an exit code without the manifest
// saying enough for the generator to check it.
func TestScenarioValidate(t *testing.T) {
	base := func() *scenario {
		return &scenario{
			Title:       "t",
			Description: "d",
			Steps:       []step{{Run: []string{"apply", "p.hew"}}},
		}
	}
	if err := base().validate(); err != nil {
		t.Fatalf("valid scenario rejected: %v", err)
	}

	noTitle := base()
	noTitle.Title = ""
	if err := noTitle.validate(); err == nil {
		t.Error("scenario without a title was accepted")
	}

	noDesc := base()
	noDesc.Description = ""
	if err := noDesc.validate(); err == nil {
		t.Error("scenario without a description was accepted")
	}

	noSteps := base()
	noSteps.Steps = nil
	if err := noSteps.validate(); err == nil {
		t.Error("scenario without steps was accepted")
	}

	emptyStep := base()
	emptyStep.Steps = []step{{Title: "does nothing"}}
	if err := emptyStep.validate(); err == nil {
		t.Error("step with no run, git or write was accepted")
	}

	badPair := base()
	badPair.Steps[0].Identical = []string{"a.json b.json"}
	if err := badPair.validate(); err == nil {
		t.Error("malformed identical assertion was accepted")
	}
}
