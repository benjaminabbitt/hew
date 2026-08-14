package hewcli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func run(t *testing.T, dir string, argv ...string) (exit int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	exit = Run(argv, dir, strings.NewReader(""), &out, &errb)
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
// target routes to hewjsonc, comments and all, rather than to the JSON
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

func TestApplyEmptyPatchExitsTwo(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "target.json", "{}")
	writeFile(t, dir, "patch.hew", "hew: 1\n\n--- target.json format=json\n")
	exit, _, stderr := run(t, dir, "apply", "patch.hew")
	if exit != 2 {
		t.Fatalf("want exit 2, got %d", exit)
	}
	if !strings.Contains(stderr, "HEW001") {
		t.Fatalf("stderr missing HEW001: %q", stderr)
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

func TestDiffNotImplemented(t *testing.T) {
	dir := t.TempDir()
	exit, stdout, stderr := run(t, dir, "diff", "old.json", "new.json")
	if exit != 2 {
		t.Fatalf("want exit 2, got %d", exit)
	}
	if stdout != "" {
		t.Fatalf("diff stub must never print a silent empty patch to stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "not implemented") {
		t.Fatalf("stderr should say not implemented: %q", stderr)
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
