package hewcli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	// hewcli names no format package: every binding reaches it through the
	// registry (Appendix A.6). A test binary is a program too, so it has to
	// link the extensions it expects to exercise — exactly as cmd/hew does.
	_ "github.com/benjaminabbitt/hew/go/ext/all"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func run(t *testing.T, dir string, argv ...string) (exit int, stdout, stderr string) {
	t.Helper()
	return runEnv(t, dir, nil, argv...)
}

// runEnv drives the CLI with a pinned environment. hew takes its environment as
// DATA (Appendix B.1, ruling O37), so a test that pins applied_at neither
// mutates the process nor races another test doing the same.
func runEnv(t *testing.T, dir string, env map[string]string, argv ...string) (exit int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	exit = Run(argv, dir, env, strings.NewReader(""), &out, &errb)
	return exit, out.String(), errb.String()
}

const patchAddTLS = `hew: 1

--- target.json format=json

@@ /server @@
  host: "localhost"
+ tls: true
`

func TestApplyInPlaceDefault(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "target.json", `{
  "server": {
    "host": "localhost"
  }
}
`)
	writeFile(t, dir, "patch.hew", patchAddTLS)
	exit, stdout, stderr := run(t, dir, "apply", "patch.hew")
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr)
	}
	if stdout != "" {
		t.Fatalf("expected empty stdout on success, got %q", stdout)
	}
	got, err := os.ReadFile(filepath.Join(dir, "target.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `"tls": true`) {
		t.Fatalf("target not updated: %s", got)
	}
}

// TestApplyDispatchesJSONCTargets covers the §8.2 binding's wiring: a jsonc
// target routes to ext/jsonc, comments and all, rather than to the JSON
// applier that would refuse them.
func TestApplyDispatchesJSONCTargets(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "target.jsonc", "{\n  // keep me\n  \"port\": 8080\n}\n")
	writeFile(t, dir, "patch.hew", "hew: 1\n\n--- target.jsonc format=jsonc\n\n@@ / @@\n"+
		"  // keep me\n- port: 8080\n+ port: 9090\n")
	exit, _, stderr := run(t, dir, "apply", "patch.hew")
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr)
	}
	got, err := os.ReadFile(filepath.Join(dir, "target.jsonc"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "{\n  // keep me\n  \"port\": 9090\n}\n" {
		t.Fatalf("jsonc target not applied byte-preservingly: %q", got)
	}
}

func TestApplyDryRunWritesNothing(t *testing.T) {
	dir := t.TempDir()
	original := `{
  "server": {
    "host": "localhost"
  }
}
`
	writeFile(t, dir, "target.json", original)
	writeFile(t, dir, "patch.hew", patchAddTLS)
	exit, _, stderr := run(t, dir, "apply", "--dry-run", "patch.hew")
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr)
	}
	got, err := os.ReadFile(filepath.Join(dir, "target.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Fatalf("--dry-run must not write: got %s", got)
	}
}

func TestApplyOutputStdout(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "target.json", `{
  "server": {
    "host": "localhost"
  }
}
`)
	writeFile(t, dir, "patch.hew", patchAddTLS)
	exit, stdout, stderr := run(t, dir, "apply", "-o", "-", "patch.hew")
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr)
	}
	if !strings.Contains(stdout, `"tls": true`) {
		t.Fatalf("stdout missing applied content: %q", stdout)
	}
	got, err := os.ReadFile(filepath.Join(dir, "target.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "tls") {
		t.Fatalf("-o - must not touch the target: %s", got)
	}
}

func TestApplyStaleTargetExitsOneAndLeavesFileUntouched(t *testing.T) {
	dir := t.TempDir()
	original := `{
  "server": {
    "host": "elsewhere"
  }
}
`
	writeFile(t, dir, "target.json", original)
	writeFile(t, dir, "patch.hew", patchAddTLS)
	exit, _, stderr := run(t, dir, "apply", "patch.hew")
	if exit != 1 {
		t.Fatalf("want exit 1 (did not apply), got %d; stderr=%q", exit, stderr)
	}
	if !strings.Contains(stderr, "HEW010") {
		t.Fatalf("stderr missing HEW010: %q", stderr)
	}
	got, err := os.ReadFile(filepath.Join(dir, "target.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Fatalf("failed apply must leave target byte-identical (§10.5): got %s", got)
	}
}

// TestApplyEmptyPatchExitsTwo pins where §10.2's line falls after ruling O38:
// at "did the author say which file this is about". A preamble with no file
// section says nothing about any file and stays HEW001.
func TestApplyEmptyPatchExitsTwo(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "target.json", "{}")
	writeFile(t, dir, "patch.hew", "hew: 1\n")
	exit, _, stderr := run(t, dir, "apply", "patch.hew")
	if exit != 2 {
		t.Fatalf("want exit 2, got %d", exit)
	}
	if !strings.Contains(stderr, "HEW001") {
		t.Fatalf("stderr missing HEW001: %q", stderr)
	}
	if !strings.Contains(stderr, "empty patch is refused") {
		t.Fatalf("stderr does not say why: %q", stderr)
	}
}

// TestApplyNoHunksPatchIsANoOp is the other side of the same line: a preamble
// PLUS a `--- ` line and no hunks is a complete statement — "here is a target,
// and there is nothing to change about it" — so it exits 0 and writes nothing.
func TestApplyNoHunksPatchIsANoOp(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "target.json", "{}")
	writeFile(t, dir, "patch.hew", "hew: 1\n\n--- target.json format=json\n")
	exit, stdout, stderr := run(t, dir, "apply", "-i", "patch.hew")
	if exit != 0 {
		t.Fatalf("want exit 0, got %d (%s)", exit, stderr)
	}
	if stdout != "" {
		t.Fatalf("a no-op apply must say nothing on stdout, got %q", stdout)
	}
	got, err := os.ReadFile(filepath.Join(dir, "target.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "{}" {
		t.Fatalf("a no-op apply rewrote the target: %q", got)
	}
}

func TestApplyUnknownFormatExitsTwo(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "target.xyz", "whatever")
	writeFile(t, dir, "patch.hew", "hew: 1\n\n--- target.xyz\n\n@@ / @@\n+ a: 1\n")
	exit, _, stderr := run(t, dir, "apply", "patch.hew")
	if exit != 2 {
		t.Fatalf("want exit 2 (HEW021 unsupported-format), got %d; stderr=%q", exit, stderr)
	}
	if !strings.Contains(stderr, "HEW021") {
		t.Fatalf("stderr missing HEW021: %q", stderr)
	}
}

func TestApplyTransformsMutuallyExclusiveWithPositional(t *testing.T) {
	dir := t.TempDir()
	exit, _, stderr := run(t, dir, "apply", "--transforms", "x.hewt", "patch.hew")
	if exit != 2 {
		t.Fatalf("want exit 2, got %d", exit)
	}
	if !strings.Contains(stderr, "--transforms") || !strings.Contains(stderr, "mutually exclusive") {
		t.Fatalf("stderr missing usage explanation: %q", stderr)
	}
}

func TestApplyNoPatchGivenExitsTwo(t *testing.T) {
	dir := t.TempDir()
	exit, _, _ := run(t, dir, "apply")
	if exit != 2 {
		t.Fatalf("want exit 2, got %d", exit)
	}
}

func TestDiffMissingSourceExitsTwo(t *testing.T) {
	dir := t.TempDir()
	exit, stdout, stderr := run(t, dir, "diff", "old.json", "new.json")
	if exit != 2 {
		t.Fatalf("want exit 2, got %d", exit)
	}
	if stdout != "" {
		t.Fatalf("a failed diff must never print a silent empty patch to stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "old.json") {
		t.Fatalf("stderr should name the unresolvable descriptor: %q", stderr)
	}
}

func TestUnknownCommandExitsTwo(t *testing.T) {
	dir := t.TempDir()
	exit, _, _ := run(t, dir, "bogus")
	if exit != 2 {
		t.Fatalf("want exit 2, got %d", exit)
	}
}

func TestNoArgsExitsTwo(t *testing.T) {
	dir := t.TempDir()
	exit, _, _ := run(t, dir)
	if exit != 2 {
		t.Fatalf("want exit 2, got %d", exit)
	}
}

const targetServers = `{
  "servers": [
    { "name": "github", "command": "npx" }
  ]
}
`

const patchKeyMatch = `hew: 1

--- target.json format=json

@@ /servers/name=github @@
- "command": "npx"
+ "command": "npx-18"
`

func TestApplyOpsPrintsResolvedFormAndWritesNothing(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "target.json", targetServers)
	writeFile(t, dir, "patch.hew", patchKeyMatch)
	exit, stdout, stderr := run(t, dir, "apply", "--ops", "patch.hew")
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr)
	}
	want := `[
  { "op": "test", "path": "/servers/0/command", "value": "npx" },
  { "op": "replace", "path": "/servers/0/command", "value": "npx-18" }
]
`
	if stdout != want {
		t.Fatalf("stdout:\n%s\nwant:\n%s", stdout, want)
	}
	got, err := os.ReadFile(filepath.Join(dir, "target.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != targetServers {
		t.Fatalf("--ops must write no target: %s", got)
	}
}

func TestApplyOpsMissingTargetExitsTwo(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "patch.hew", patchKeyMatch)
	exit, stdout, stderr := run(t, dir, "apply", "--ops", "patch.hew")
	if exit != 2 {
		t.Fatalf("want exit 2, got %d", exit)
	}
	if stdout != "" {
		t.Fatalf("nothing may reach stdout on failure: %q", stdout)
	}
	if !strings.Contains(stderr, "HEW003") {
		t.Fatalf("stderr: %q", stderr)
	}
}

func TestApplyOpsUnknownFormatExitsTwo(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "target.xyz", "whatever")
	writeFile(t, dir, "patch.hew", "hew: 1\n\n--- target.xyz\n\n@@ / @@\n+ a: 1\n")
	exit, _, stderr := run(t, dir, "apply", "--ops", "patch.hew")
	if exit != 2 || !strings.Contains(stderr, "HEW021") {
		t.Fatalf("want exit 2 with HEW021, got %d: %q", exit, stderr)
	}
}

func TestApplyOpsUnparseableTargetExitsTwo(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "target.json", "{oops")
	writeFile(t, dir, "patch.hew", patchKeyMatch)
	exit, _, stderr := run(t, dir, "apply", "--ops", "patch.hew")
	if exit != 2 || !strings.Contains(stderr, "HEW002") {
		t.Fatalf("want exit 2 with HEW002, got %d: %q", exit, stderr)
	}
	if !strings.Contains(stderr, "target.json") {
		t.Fatalf("the diagnostic must name the target: %q", stderr)
	}
}

// --ops resolves addresses; a path that names nothing cannot be projected,
// and that is a did-not-apply (exit 1), not trouble.
func TestApplyOpsUnresolvablePathExitsOne(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "target.json", targetServers)
	writeFile(t, dir, "patch.hew", `hew: 1

--- target.json format=json

@@ /servers/name=ghost @@
- "command": "npx"
+ "command": "npx-18"
`)
	exit, _, stderr := run(t, dir, "apply", "--ops", "patch.hew")
	if exit != 1 || !strings.Contains(stderr, "HEW013") {
		t.Fatalf("want exit 1 with HEW013, got %d: %q", exit, stderr)
	}
}

// A stale target still resolves: --ops reports addresses, not an apply.
func TestApplyOpsDoesNotEvaluateAssertions(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "target.json", `{
  "servers": [
    { "name": "github", "command": "drifted" }
  ]
}
`)
	writeFile(t, dir, "patch.hew", patchKeyMatch)
	exit, stdout, stderr := run(t, dir, "apply", "--ops", "patch.hew")
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr)
	}
	if !strings.Contains(stdout, `"path": "/servers/0/command"`) {
		t.Fatalf("stdout: %q", stdout)
	}
}

func TestApplyOpsFromTransformsInput(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "target.json", targetServers)
	writeFile(t, dir, "move.hewt", `hew-transforms: 1
target: target.json
format: json
transforms:
  - op: copy
    from: /servers/name=github/command
    path: /servers/0/alias
`)
	exit, stdout, stderr := run(t, dir, "apply", "--ops", "--transforms", "move.hewt")
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr)
	}
	want := `[
  { "op": "copy", "from": "/servers/0/command", "path": "/servers/0/alias" }
]
`
	if stdout != want {
		t.Fatalf("stdout:\n%s\nwant:\n%s", stdout, want)
	}
}

func TestApplyRecordHoldsTheResolvedForm(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "target.json", `{
  "mcpServers": [
    { "name": "ctxloom", "command": "ctxloom" }
  ]
}
`)
	writeFile(t, dir, "patch.hew", `hew: 1

--- target.json format=json

@@ /mcpServers @@
  { "name": "ctxloom" }
+ { "name": "taskloom", "command": "taskloom" }
`)
	exit, _, stderr := run(t, dir, "apply", "-i", "--record", "out.hewt", "patch.hew")
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr)
	}
	got := string(readBack(t, dir, "out.hewt"))
	for _, want := range []string{
		"hew-record: 1\n",
		"applied_at: ",
		"source: patch.hew",
		"digest: sha256:",
		"path: /mcpServers/0/name",
		"path: /mcpServers/1",
		"committed: true",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("record missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "name=ctxloom") {
		t.Fatalf("the record holds the RESOLVED list, not the abstract one (§9.7):\n%s", got)
	}
}

// §9.7's record names one patch source and one digest of it; several patches
// have no spelling there, so the combination is refused rather than guessed.
func TestApplyRecordWithSeveralPatchesIsAUsageError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "target.json", targetServers)
	writeFile(t, dir, "patch.hew", patchKeyMatch)
	writeFile(t, dir, "other.hew", patchKeyMatch)
	exit, _, stderr := run(t, dir, "apply", "--record", "out.hewt", "patch.hew", "other.hew")
	if exit != 2 {
		t.Fatalf("want exit 2, got %d", exit)
	}
	if !strings.Contains(stderr, "single patch source") {
		t.Fatalf("stderr: %q", stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "out.hewt")); err == nil {
		t.Fatal("a refused invocation must write no record")
	}
}

// Every assertion mode and the copy op survive into the record's resolved
// list; only the qualifiers RFC 6901 cannot spell are consumed.
func TestApplyRecordCarriesEveryAssertionMode(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "target.json", targetServers)
	writeFile(t, dir, "all.hewt", `hew-transforms: 1
target: target.json
format: json
transforms:
  - op: test
    path: /servers/name=github/tls
    absent: true
  - op: test
    path: /servers
    count: 1
  - op: test
    path: /servers
    kind: seq
  - op: test
    path: /servers/0
    count: 2
    exhaustive: true
  - op: copy
    from: /servers/name=github/command
    path: /servers/0/alias
    anchor: fork
`)
	exit, _, stderr := run(t, dir, "apply", "--record", "out.hewt", "--transforms", "all.hewt")
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr)
	}
	got := string(readBack(t, dir, "out.hewt"))
	for _, want := range []string{
		"source: all.hewt",
		"path: /servers/0/tls\n",
		"absent: true",
		"count: 1",
		"kind: seq",
		"exhaustive: true",
		"op: copy",
		"from: /servers/0/command",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("record missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "anchor") {
		t.Fatalf("a format-only qualifier must be consumed by resolution (§9.2):\n%s", got)
	}
}

// An `? absent` on a key-match that matches nothing is satisfied by the
// applier and has no RFC 6901 pointer, so `--record` cannot record it. The
// run must then fail with the target untouched, never edit-then-fail.
func TestApplyRecordThatCannotBeResolvedLeavesTheTargetUntouched(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "target.json", targetServers)
	writeFile(t, dir, "patch.hew", `hew: 1

--- target.json format=json

@@ /servers/name=github @@
? absent /servers/name=ghost
- "command": "npx"
+ "command": "npx-18"
`)
	exit, _, stderr := run(t, dir, "apply", "--record", "out.hewt", "patch.hew")
	if exit != 1 || !strings.Contains(stderr, "HEW013") {
		t.Fatalf("want exit 1 with HEW013, got %d: %q", exit, stderr)
	}
	if got := string(readBack(t, dir, "target.json")); got != targetServers {
		t.Fatalf("target must be untouched (§10.5):\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "out.hewt")); err == nil {
		t.Fatal("no record may be written for a run that failed")
	}
	// Without --record the same patch applies: the failure is the record's,
	// and it is not invented for runs that do not ask for one.
	writeFile(t, dir, "target.json", targetServers)
	if exit, _, stderr = run(t, dir, "apply", "patch.hew"); exit != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr)
	}
}

// detectFormat knows the extension, but no binding exists for it yet: that is
// HEW021 too, and by a different branch than an undetectable extension.
// --ops needs the format's binding. Markdown is the durable case: it is
// registered and detectable, so the patch parses and names a real format, but
// it ships no applier at all while §8.7's dialect evaluation is open (O29).
//
// This was a YAML case, then an HCL one; both gained what they lacked. If
// markdown ever ships an applier this test FAILS rather than quietly passing,
// and whatever is genuinely unbound then becomes the case.
func TestApplyOpsKnownFormatWithoutABindingExitsTwo(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "notes.md", "# Title\n\nbody\n")
	writeFile(t, dir, "patch.hew", "hew: 1\n\n--- notes.md\n\n@@ / @@\n- body\n+ text\n")
	exit, _, stderr := run(t, dir, "apply", "--ops", "patch.hew")
	if exit != 2 || !strings.Contains(stderr, "HEW021") {
		t.Fatalf("want exit 2 with HEW021, got %d: %q", exit, stderr)
	}
	if !strings.Contains(stderr, `format "markdown"`) {
		t.Fatalf("stderr should name the unbound format: %q", stderr)
	}
}

func readBack(t *testing.T, dir, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestApplyRecordWritesFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "target.json", `{
  "server": {
    "host": "localhost"
  }
}
`)
	writeFile(t, dir, "patch.hew", patchAddTLS)
	exit, _, stderr := run(t, dir, "apply", "--record", "out.hewt", "patch.hew")
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr)
	}
	b, err := os.ReadFile(filepath.Join(dir, "out.hewt"))
	if err != nil {
		t.Fatalf("record file not written: %v", err)
	}
	if !strings.HasPrefix(string(b), "hew-record: 1\n") {
		t.Fatalf("record must start with hew-record: 1 (§9.7), got:\n%s", b)
	}
	if !strings.Contains(string(b), "sha256:") {
		t.Fatalf("record missing digests: %s", b)
	}
}

func TestApplyNoRecordByDefault(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "target.json", `{
  "server": {
    "host": "localhost"
  }
}
`)
	writeFile(t, dir, "patch.hew", patchAddTLS)
	exit, _, stderr := run(t, dir, "apply", "patch.hew")
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "*.hewt"))
	if len(matches) != 0 {
		t.Fatalf("no --record, no record: found %v", matches)
	}
}

// --- `--reversal` (ruling O40) ------------------------------------------------

// seedYAML writes the target/patch pair the reversal tests share.
func seedYAML(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "config.yaml", "server:\n  host: localhost\n  port: 8080\n  timeout: 30\n")
	writeFile(t, dir, "bump.hew", "hew: 1\n\n--- config.yaml format=yaml\n\n@@ /server @@\n  port: 8080\n- timeout: 30\n+ timeout: 60\n")
	return dir
}

func TestApplyReversalDefaultName(t *testing.T) {
	dir := seedYAML(t)
	// The flag last on the line is the unambiguous spelling of "no value":
	// there is no next argument to mistake for one.
	exit, _, stderr := run(t, dir, "apply", "-i", "bump.hew", "--reversal")
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr)
	}
	got, err := os.ReadFile(filepath.Join(dir, "config.yaml.undo.hew"))
	if err != nil {
		t.Fatalf("no reversal patch at the derived name: %v", err)
	}
	const want = "hew: 1\n\n--- config.yaml format=yaml\n\n@@ /server @@\n  port: 8080\n- timeout: 60\n+ timeout: 30\n"
	if string(got) != want {
		t.Fatalf("reversal patch:\n%s\nwant:\n%s", got, want)
	}
}

// TestApplyReversalIsTheUndo: applying the reversal restores the pre-apply
// bytes, which is the whole of O40's claim. The corpus states it in prose
// because its manifest carries one argv; here it is two invocations.
func TestApplyReversalIsTheUndo(t *testing.T) {
	dir := seedYAML(t)
	before, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if exit, _, stderr := run(t, dir, "apply", "-i", "--reversal", "undo.hew", "bump.hew"); exit != 0 {
		t.Fatalf("forward: exit=%d stderr=%q", exit, stderr)
	}
	if exit, _, stderr := run(t, dir, "apply", "-i", "undo.hew"); exit != 0 {
		t.Fatalf("undo: exit=%d stderr=%q", exit, stderr)
	}
	got, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(before) {
		t.Fatalf("after the undo: %q, want the pre-apply bytes %q", got, before)
	}
}

func TestApplyReversalAttachedValue(t *testing.T) {
	dir := seedYAML(t)
	exit, _, stderr := run(t, dir, "apply", "-i", "--reversal=back.hew", "bump.hew")
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "back.hew")); err != nil {
		t.Fatalf("--reversal=FILE did not write FILE: %v", err)
	}
}

// TestApplyNoReversalByDefault: opt-in always. No flag, no file.
func TestApplyNoReversalByDefault(t *testing.T) {
	dir := seedYAML(t)
	if exit, _, stderr := run(t, dir, "apply", "-i", "bump.hew"); exit != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "*.undo.hew"))
	if len(matches) != 0 {
		t.Fatalf("no --reversal, no file: found %v", matches)
	}
}

// --- applied_at pinning (ruling O37) ------------------------------------------

const targetForRecord = "{\n  \"server\": {\n    \"host\": \"localhost\"\n  }\n}\n"

func TestApplyRecordPinnedByHewAppliedAt(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "target.json", targetForRecord)
	writeFile(t, dir, "patch.hew", patchAddTLS)
	env := map[string]string{"HEW_APPLIED_AT": "2026-08-14T09:31:07Z"}
	exit, _, stderr := runEnv(t, dir, env, "apply", "--record", "out.hewt", "patch.hew")
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr)
	}
	b, err := os.ReadFile(filepath.Join(dir, "out.hewt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `applied_at: "2026-08-14T09:31:07Z"`) {
		t.Fatalf("HEW_APPLIED_AT did not pin applied_at:\n%s", b)
	}
}

// SOURCE_DATE_EPOCH is the cross-ecosystem convention, and it is the fallback
// rather than the first choice (§9.7's precedence).
func TestApplyRecordPinnedBySourceDateEpoch(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "target.json", targetForRecord)
	writeFile(t, dir, "patch.hew", patchAddTLS)
	env := map[string]string{"SOURCE_DATE_EPOCH": "1771061467"}
	exit, _, stderr := runEnv(t, dir, env, "apply", "--record", "out.hewt", "patch.hew")
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr)
	}
	b, err := os.ReadFile(filepath.Join(dir, "out.hewt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `applied_at: "2026-02-14T09:31:07Z"`) {
		t.Fatalf("SOURCE_DATE_EPOCH did not pin applied_at:\n%s", b)
	}
}

func TestApplyHewAppliedAtWinsOverSourceDateEpoch(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "target.json", targetForRecord)
	writeFile(t, dir, "patch.hew", patchAddTLS)
	env := map[string]string{"HEW_APPLIED_AT": "2026-08-14T09:31:07Z", "SOURCE_DATE_EPOCH": "1771061467"}
	if exit, _, stderr := runEnv(t, dir, env, "apply", "--record", "out.hewt", "patch.hew"); exit != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr)
	}
	b, err := os.ReadFile(filepath.Join(dir, "out.hewt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `applied_at: "2026-08-14T09:31:07Z"`) {
		t.Fatalf("HEW_APPLIED_AT must win (§9.7):\n%s", b)
	}
}

// A malformed pin is exit 2, never a silent fallback to the clock: a pin that
// quietly does not pin is worse than no pin, because the artifact still LOOKS
// reproducible. It is refused before anything is read or written.
func TestApplyMalformedPinIsExitTwo(t *testing.T) {
	for _, env := range []map[string]string{
		{"HEW_APPLIED_AT": "yesterday"},
		{"SOURCE_DATE_EPOCH": "half past two"},
	} {
		dir := seedYAML(t)
		exit, _, stderr := runEnv(t, dir, env, "apply", "-i", "bump.hew")
		if exit != 2 {
			t.Fatalf("%v: want exit 2, got %d", env, exit)
		}
		if !strings.Contains(stderr, "usage error") {
			t.Fatalf("%v: stderr %q", env, stderr)
		}
		got, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(got), "timeout: 30") {
			t.Fatalf("%v: a refused pin still applied the patch: %s", env, got)
		}
	}
}
