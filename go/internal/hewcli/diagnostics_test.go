package hewcli

import (
	"encoding/json"
	"strings"
	"testing"
)

// A failure has two readers: a person, and a program acting on their behalf.
// Both need the same four facts — WHICH file, WHICH node, WHAT was expected
// against what was found, and WHERE in the patch the claim was made — and both
// are ill-served by prose that has to be parsed back out. These pin the
// contract §10.3 states, on the paths a caller actually uses.

const diagTarget = "server:\n  host: localhost\n  port: 8080\n  timeout: 45\n"

// stalePatch claims timeout is 30 where the target says 45.
const stalePatch = "hew: 1\n\n--- config.yaml format=yaml\n\n@@ /server @@\n  port: 8080\n- timeout: 30\n+ timeout: 60\n"

func diagDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "config.yaml", diagTarget)
	writeFile(t, dir, "bad.hew", stalePatch)
	return dir
}

// --- 1. the machine-readable channel ----------------------------------------

// `--format-out json` must EMIT JSON. Accepting the flag and printing prose is
// worse than rejecting it: the caller believes it asked for a contract it did
// not get, and discovers otherwise only by failing to parse the output.
func TestFormatOutJSONEmitsAParsableObject(t *testing.T) {
	dir := diagDir(t)
	exit, stdout, _ := run(t, dir, "apply", "--format-out", "json", "bad.hew")
	if exit != 1 {
		t.Fatalf("exit = %d, want 1", exit)
	}
	var got struct {
		Code      string `json:"code"`
		Target    string `json:"target"`
		Path      string `json:"path"`
		Patch     string `json:"patch"`
		PatchLine int    `json:"patchLine"`
		Want      string `json:"want"`
		Got       string `json:"got"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v\ngot: %q", err, stdout)
	}
	if got.Code != "HEW010" {
		t.Errorf("code = %q, want HEW010", got.Code)
	}
	if got.Target != "config.yaml" || got.Path != "/server/timeout" {
		t.Errorf("target/path = %q/%q", got.Target, got.Path)
	}
	if got.Patch != "bad.hew" || got.PatchLine != 7 {
		t.Errorf("patch = %q:%d, want bad.hew:7", got.Patch, got.PatchLine)
	}
	if got.Want != "30" || got.Got != "45" {
		t.Errorf("want/got = %q/%q, want 30/45", got.Want, got.Got)
	}
}

// An unknown --format-out value must be refused, not silently ignored.
func TestFormatOutRejectsAnUnknownValue(t *testing.T) {
	dir := diagDir(t)
	exit, _, stderr := run(t, dir, "apply", "--format-out", "xml", "bad.hew")
	if exit != 2 {
		t.Fatalf("exit = %d, want 2 (usage)", exit)
	}
	if !strings.Contains(stderr, "xml") {
		t.Errorf("stderr should name the rejected value: %q", stderr)
	}
}

// --- 2. two paths, two questions -------------------------------------------

// apply and --ops report DIFFERENT codes for the same absent node, and that is
// correct rather than a bug — they are not asking the same thing:
//
//	apply  asks "does the before-image hold?"  A node that is not there means
//	       it does not, so the target drifted: HEW010, and the remedy is to
//	       re-derive the patch.
//	--ops  is address-only (§9.2) and evaluates nothing, so the only question
//	       it can answer is whether the address resolves: HEW013, and the
//	       remedy is to fix the address.
//
// This is pinned because it LOOKS like an inconsistency, and a future reader
// who "fixes" it collapses two different remedies into one.
func TestApplyAndOpsAnswerDifferentQuestions(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "s.yaml", "servers:\n  - name: web\n    port: 80\n")
	writeFile(t, dir, "m.hew", "hew: 1\n\n--- s.yaml format=yaml\n\n@@ /servers/name=nope @@\n- port: 80\n+ port: 81\n")

	_, _, applyErr := run(t, dir, "apply", "m.hew")
	if !strings.Contains(applyErr, "HEW010") {
		t.Errorf("apply reported %q, want HEW010 stale-target (the before-image does not hold)", applyErr)
	}
	_, _, opsErr := run(t, dir, "apply", "--ops", "m.hew")
	if !strings.Contains(opsErr, "HEW013") {
		t.Errorf("--ops reported %q, want HEW013 no-match (the address resolves to nothing)", opsErr)
	}
}

// --- 3. provenance on every path --------------------------------------------

// The resolve path printed a literal "patch:6:" because it never learned which
// file it was reading. With several patches on the command line that line
// cannot be acted on.
func TestOpsNamesThePatchFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "s.yaml", "servers:\n  - name: web\n")
	writeFile(t, dir, "named.hew", "hew: 1\n\n--- s.yaml format=yaml\n\n@@ /servers/name=nope @@\n- port: 80\n+ port: 81\n")
	_, _, stderr := run(t, dir, "apply", "--ops", "named.hew")
	if !strings.Contains(stderr, "named.hew:") {
		t.Errorf("stderr does not name the patch file: %q", stderr)
	}
	if strings.Contains(stderr, "\n  patch:") {
		t.Errorf("stderr still uses the placeholder name: %q", stderr)
	}
}

// --- 4. where in the TARGET -------------------------------------------------

// §10.3's own example is `config.yaml:6:  found 45`. Without the line number a
// reader is told a file and a path and left to find the node themselves.
func TestStaleTargetNamesTheTargetLine(t *testing.T) {
	dir := diagDir(t)
	_, _, stderr := run(t, dir, "apply", "bad.hew")
	// timeout is the 4th line of diagTarget.
	if !strings.Contains(stderr, "config.yaml:4:") {
		t.Errorf("stderr does not locate the node in the target: %q", stderr)
	}
}

// --- 5. how much else is wrong ----------------------------------------------

// Reporting the first failure in full and stopping is right — nothing is
// written either way — but a reader deserves to know whether fixing it is the
// whole job. A bare count is enough and stays cheap.
//
// The count is per FILE SECTION, not per transform. Counting within a section
// would mean evaluating one `test` outside its hunk, where a paired write can
// converge an assertion that would fail alone, so a per-transform count could
// report failures that are not real. Two sections here, both stale.
func TestSeveralFailuresReportTheRemainingCount(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.yaml", "k: 1\n")
	writeFile(t, dir, "b.yaml", "k: 2\n")
	writeFile(t, dir, "two.hew",
		"hew: 1\n\n--- a.yaml format=yaml\n\n@@ / @@\n- k: 99\n+ k: 3\n"+
			"\n--- b.yaml format=yaml\n\n@@ / @@\n- k: 98\n+ k: 4\n")
	_, _, stderr := run(t, dir, "apply", "two.hew")
	if !strings.Contains(stderr, "1 more") {
		t.Errorf("stderr does not say how many other sections failed: %q", stderr)
	}
}

// --- 6. discoverability -----------------------------------------------------

// --ops and --record are the troubleshooting affordances, and they were
// undiscoverable: `--help` was a usage ERROR and the bare usage line listed no
// flags at all.
func TestHelpListsTheFlags(t *testing.T) {
	dir := t.TempDir()
	exit, stdout, _ := run(t, dir, "--help")
	if exit != 0 {
		t.Fatalf("--help exit = %d, want 0", exit)
	}
	for _, flag := range []string{"--ops", "--record", "--format-out", "--reversal", "-i"} {
		if !strings.Contains(stdout, flag) {
			t.Errorf("help does not mention %s", flag)
		}
	}
}
