package hewcli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initGitRepo commits every file already in dir; it reports false when git is
// not installed, which is a skip, not a failure.
func initGitRepo(t *testing.T, dir string) bool {
	t.Helper()
	for _, args := range [][]string{{"init", "-q", "."}, {"add", "."}, {"commit", "-q", "-m", "base"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Logf("git %v: %v: %s", args, err, out)
			return false
		}
	}
	return true
}

func runStdin(t *testing.T, dir, stdin string, argv ...string) (exit int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	exit = Run(argv, dir, nil, strings.NewReader(stdin), &out, &errb)
	return exit, out.String(), errb.String()
}

// twoSided writes an old/new pair and returns the directory.
func twoSided(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "old.yaml", "server:\n  host: localhost\n  port: 8080\n  timeout: 30\n")
	writeFile(t, dir, "new.yaml", "server:\n  host: localhost\n  port: 8080\n  timeout: 60\n")
	return dir
}

func TestDiffWritesAPatchToStdout(t *testing.T) {
	dir := twoSided(t)
	exit, stdout, stderr := run(t, dir, "diff", "old.yaml", "new.yaml")
	if exit != 0 {
		t.Fatalf("exit %d (%s)", exit, stderr)
	}
	// The `--- ` line names the OLD side (§9.4-R7, Appendix B.2.1, ruling
	// O39): the patch applies to old, so naming new would stamp a file the
	// applier never opens.
	want := "hew: 1\n\n--- old.yaml format=yaml\n\n@@ /server @@\n  port: 8080\n- timeout: 30\n+ timeout: 60\n"
	if stdout != want {
		t.Fatalf("stdout:\n%q\nwant:\n%q", stdout, want)
	}
}

// TestDiffProducesAnApplicablePatch is the composition O39 exists to protect,
// and the reason "which side" is not a matter of taste: the patch `hew diff`
// writes must apply to the file it names.
func TestDiffProducesAnApplicablePatch(t *testing.T) {
	dir := twoSided(t)
	if exit, _, stderr := run(t, dir, "diff", "old.yaml", "new.yaml", "-o", "p.hew"); exit != 0 {
		t.Fatalf("diff: exit %d (%s)", exit, stderr)
	}
	if exit, _, stderr := run(t, dir, "apply", "p.hew"); exit != 0 {
		t.Fatalf("apply: exit %d (%s)", exit, stderr)
	}
	got, err := os.ReadFile(filepath.Join(dir, "old.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join(dir, "new.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("apply(diff(old, new), old) = %q, want %q", got, want)
	}
}

// TestDiffOfIdenticalSourcesIsAPreambleOnlyPatch is ruling O38: two identical
// inputs produce `hew: 1` plus a `--- ` line and nothing else, at exit 0. Not
// zero bytes — zero bytes is a file `hew apply` refuses as HEW001, which would
// turn "nothing changed" into an error one command later.
func TestDiffOfIdenticalSourcesIsAPreambleOnlyPatch(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.yaml", "k: 1\n")
	writeFile(t, dir, "b.yaml", "k: 1\n")
	exit, stdout, stderr := run(t, dir, "diff", "a.yaml", "b.yaml")
	const want = "hew: 1\n\n--- a.yaml format=yaml\n"
	if exit != 0 || stdout != want {
		t.Fatalf("exit %d stdout %q stderr %q, want exit 0 and %q", exit, stdout, stderr, want)
	}
}

// TestDiffThenApplyComposesWhenNothingChanged is the pipeline the ruling names:
// `hew diff a b > p.hew && hew apply p.hew` must not break the day the answer
// is "no change".
func TestDiffThenApplyComposesWhenNothingChanged(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.yaml", "k: 1\n")
	writeFile(t, dir, "b.yaml", "k: 1\n")
	if exit, _, stderr := run(t, dir, "diff", "a.yaml", "b.yaml", "-o", "p.hew"); exit != 0 {
		t.Fatalf("diff: exit %d (%s)", exit, stderr)
	}
	exit, _, stderr := run(t, dir, "apply", "p.hew")
	if exit != 0 {
		t.Fatalf("apply of a no-op patch: exit %d (%s)", exit, stderr)
	}
	got, err := os.ReadFile(filepath.Join(dir, "a.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "k: 1\n" {
		t.Fatalf("a no-op apply rewrote the target: %q", got)
	}
}

// TestDiffStdinOldSideBorrowsTheNewLabel is Appendix B.2.1's second corollary:
// a stdin old side has no name to give, so the new side's label stands in.
func TestDiffStdinOldSideBorrowsTheNewLabel(t *testing.T) {
	dir := twoSided(t)
	exit, stdout, stderr := runStdin(t, dir, "server:\n  host: localhost\n  port: 8080\n  timeout: 30\n",
		"diff", "-", "new.yaml")
	if exit != 0 {
		t.Fatalf("exit %d (%s)", exit, stderr)
	}
	if !strings.Contains(stdout, "--- new.yaml") {
		t.Fatalf("want the new side's label to stand in for stdin, got:\n%s", stdout)
	}
}

func TestDiffOutputFlag(t *testing.T) {
	dir := twoSided(t)
	for _, flag := range []string{"-o", "--output"} {
		exit, stdout, stderr := run(t, dir, "diff", flag, "p.hew", "old.yaml", "new.yaml")
		if exit != 0 {
			t.Fatalf("%s: exit %d (%s)", flag, exit, stderr)
		}
		if stdout != "" {
			t.Fatalf("%s: stdout must be empty when -o names a file, got %q", flag, stdout)
		}
		b, err := os.ReadFile(filepath.Join(dir, "p.hew"))
		if err != nil {
			t.Fatalf("%s: %v", flag, err)
		}
		if !strings.Contains(string(b), "+ timeout: 60") {
			t.Fatalf("%s: file:\n%s", flag, b)
		}
	}
}

func TestDiffTransformsOut(t *testing.T) {
	dir := twoSided(t)
	exit, _, stderr := run(t, dir, "diff", "--transforms-out", "p.hewt", "old.yaml", "new.yaml")
	if exit != 0 {
		t.Fatalf("exit %d (%s)", exit, stderr)
	}
	b, err := os.ReadFile(filepath.Join(dir, "p.hewt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"hew-transforms: 1", "op: replace", "path: /server/timeout"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("missing %q in:\n%s", want, b)
		}
	}
	if strings.Contains(string(b), "@@") {
		t.Fatal("--transforms-out emits the IR INSTEAD of notation")
	}
}

func TestDiffTransformsOutToStdout(t *testing.T) {
	dir := twoSided(t)
	exit, stdout, _ := run(t, dir, "diff", "--transforms-out", "-", "old.yaml", "new.yaml")
	if exit != 0 || !strings.Contains(stdout, "hew-transforms: 1") {
		t.Fatalf("exit %d stdout %q", exit, stdout)
	}
}

func TestDiffContextFlag(t *testing.T) {
	dir := twoSided(t)
	for _, flag := range []string{"-U", "--context"} {
		_, tight, _ := run(t, dir, "diff", flag, "0", "old.yaml", "new.yaml")
		if strings.Contains(tight, "port: 8080") {
			t.Fatalf("%s 0 must emit no context:\n%s", flag, tight)
		}
		_, all, _ := run(t, dir, "diff", flag, "all", "old.yaml", "new.yaml")
		if !strings.Contains(all, "  host: localhost") {
			t.Fatalf("%s all must emit every sibling:\n%s", flag, all)
		}
		_, two, _ := run(t, dir, "diff", flag, "2", "old.yaml", "new.yaml")
		if !strings.Contains(two, "  host: localhost") {
			t.Fatalf("%s 2 must reach the second sibling:\n%s", flag, two)
		}
	}
}

func TestDiffRejectsBadContext(t *testing.T) {
	dir := twoSided(t)
	for _, v := range []string{"-1", "lots", ""} {
		exit, stdout, stderr := run(t, dir, "diff", "-U", v, "old.yaml", "new.yaml")
		if exit != 2 || stdout != "" {
			t.Fatalf("-U %q: exit %d stdout %q", v, exit, stdout)
		}
		// Appendix B.2 requires the help text to say the radius is a
		// strictness dial, not a verbosity one.
		if !strings.Contains(stderr, "STRICTNESS dial") {
			t.Fatalf("-U %q: stderr must explain the dial: %q", v, stderr)
		}
	}
}

func TestDiffKeyFieldsFlag(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "old.yaml", "m:\n  - slug: a\n    v: 1\n  - slug: b\n    v: 2\n")
	writeFile(t, dir, "new.yaml", "m:\n  - slug: a\n    v: 1\n  - slug: b\n    v: 2\n  - slug: c\n    v: 3\n")
	exit, stdout, stderr := run(t, dir, "diff", "--key-fields", "slug,other", "old.yaml", "new.yaml")
	if exit != 0 {
		t.Fatalf("exit %d (%s)", exit, stderr)
	}
	if !strings.Contains(stdout, "  - slug: b") {
		t.Fatalf("stdout:\n%s", stdout)
	}
	exit, _, stderr = run(t, dir, "diff", "--key-fields", " , ", "old.yaml", "new.yaml")
	if exit != 2 || !strings.Contains(stderr, "at least one field") {
		t.Fatalf("exit %d stderr %q", exit, stderr)
	}
}

// §9.4-R4's note surfaces as the patch's own leading comment: an index address
// is fragile, and the patch has to say why it chose one.
func TestDiffAmbiguousIdentityEmitsANoteComment(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "old.yaml", "m:\n  - alpha: a1\n    beta: b1\n")
	writeFile(t, dir, "new.yaml", "m:\n  - alpha: a1\n    beta: b1\n  - alpha: a2\n    beta: b2\n")
	exit, stdout, stderr := run(t, dir, "diff", "old.yaml", "new.yaml")
	if exit != 0 {
		t.Fatalf("exit %d (%s)", exit, stderr)
	}
	if !strings.HasPrefix(stdout, "# ") || !strings.Contains(stdout, "index") {
		t.Fatalf("want a leading note comment:\n%s", stdout)
	}
}

func TestDiffFormatOverride(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "old.conf", "{\"a\": 1}")
	writeFile(t, dir, "new.conf", "{\"a\": 2}")
	exit, _, stderr := run(t, dir, "diff", "old.conf", "new.conf")
	if exit != 2 || !strings.Contains(stderr, "HEW021") {
		t.Fatalf("an undetectable format must fail loud: exit %d stderr %q", exit, stderr)
	}
	exit, stdout, stderr := run(t, dir, "diff", "--format", "json", "old.conf", "new.conf")
	if exit != 0 {
		t.Fatalf("exit %d (%s)", exit, stderr)
	}
	if !strings.Contains(stdout, `+ "a": 2`) {
		t.Fatalf("stdout:\n%s", stdout)
	}
}

func TestDiffStdinSide(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "old.yaml", "k: 1\n")
	exit, stdout, stderr := runStdin(t, dir, "k: 2\n", "diff", "old.yaml", "-")
	if exit != 0 {
		t.Fatalf("exit %d (%s)", exit, stderr)
	}
	// A stdin new side has no name of its own, so the patch targets the side
	// that does.
	if !strings.Contains(stdout, "--- old.yaml format=yaml") {
		t.Fatalf("stdout:\n%s", stdout)
	}
	if !strings.Contains(stdout, "+ k: 2") {
		t.Fatalf("stdout:\n%s", stdout)
	}
}

func TestDiffStdinTwiceIsAUsageError(t *testing.T) {
	exit, stdout, stderr := runStdin(t, t.TempDir(), "x", "diff", "-", "-")
	if exit != 2 || stdout != "" {
		t.Fatalf("exit %d stdout %q", exit, stdout)
	}
	if !strings.Contains(stderr, "usage error") || !strings.Contains(stderr, "at most once") {
		t.Fatalf("stderr %q", stderr)
	}
}

func TestDiffArgCount(t *testing.T) {
	for _, argv := range [][]string{
		{"diff"},
		{"diff", "only.yaml"},
		{"diff", "a.yaml", "b.yaml", "c.yaml"},
	} {
		exit, _, stderr := run(t, t.TempDir(), argv...)
		if exit != 2 || !strings.Contains(stderr, "exactly two source descriptors") {
			t.Fatalf("%v: exit %d stderr %q", argv, exit, stderr)
		}
	}
}

func TestDiffUnknownFlag(t *testing.T) {
	exit, _, stderr := run(t, t.TempDir(), "diff", "--nope", "a", "b")
	if exit != 2 || !strings.Contains(stderr, `unknown flag "--nope"`) {
		t.Fatalf("exit %d stderr %q", exit, stderr)
	}
}

func TestDiffFlagsNeedValues(t *testing.T) {
	for _, flag := range []string{"-U", "--context", "--format", "--key-fields", "--transforms-out", "-o"} {
		exit, _, stderr := run(t, t.TempDir(), "diff", "a.yaml", "b.yaml", flag)
		if exit != 2 || !strings.Contains(stderr, "requires a value") {
			t.Fatalf("%s: exit %d stderr %q", flag, exit, stderr)
		}
	}
}

// §9.5: git absent plus a ":" descriptor is exit 2 and a usage error, never a
// silent fallback to a filename. The CLI cannot un-install git, so this drives
// the boundary through a descriptor git itself will reject.
func TestDiffGitAnchorOutsideARepositoryFails(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config.yaml", "k: 1\n")
	exit, stdout, stderr := run(t, dir, "diff", "HEAD:config.yaml", "config.yaml")
	if exit != 2 || stdout != "" {
		t.Fatalf("exit %d stdout %q stderr %q", exit, stdout, stderr)
	}
	if !strings.Contains(stderr, "HEAD:config.yaml") {
		t.Fatalf("stderr must name the descriptor: %q", stderr)
	}
}

func TestDiffGitAnchorResolvesFromTheCommit(t *testing.T) {
	dir := t.TempDir()
	for k, v := range map[string]string{
		"GIT_CONFIG_GLOBAL": os.DevNull, "GIT_CONFIG_SYSTEM": os.DevNull,
		"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@invalid",
		"GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@invalid",
	} {
		t.Setenv(k, v)
	}
	writeFile(t, dir, "config.yaml", "server:\n  port: 8080\n  timeout: 30\n")
	if !initGitRepo(t, dir) {
		t.Skip("git unavailable")
	}
	writeFile(t, dir, "config.yaml", "server:\n  port: 8080\n  timeout: 60\n")

	exit, stdout, stderr := run(t, dir, "diff", "HEAD:config.yaml", "config.yaml")
	if exit != 0 {
		t.Fatalf("exit %d (%s)", exit, stderr)
	}
	want := "hew: 1\n\n--- config.yaml format=yaml\n\n@@ /server @@\n  port: 8080\n- timeout: 30\n+ timeout: 60\n"
	if stdout != want {
		t.Fatalf("stdout:\n%q\nwant:\n%q", stdout, want)
	}
}

func TestDiffUnreadableOutputPathIsExitTwo(t *testing.T) {
	dir := twoSided(t)
	exit, _, stderr := run(t, dir, "diff", "-o", "no/such/dir/p.hew", "old.yaml", "new.yaml")
	if exit != 2 || !strings.Contains(stderr, "no/such/dir/p.hew") {
		t.Fatalf("exit %d stderr %q", exit, stderr)
	}
}

// §9.4-R6: a change the mirror grammar cannot express is HEW020, and for diff
// that is trouble (exit 2), not "did not apply".
func TestDiffInexpressibleRootChangeIsExitTwo(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "old.json", `{"a": 1}`)
	writeFile(t, dir, "new.json", `[1]`)
	exit, stdout, stderr := run(t, dir, "diff", "old.json", "new.json")
	if exit != 2 || stdout != "" {
		t.Fatalf("exit %d stdout %q", exit, stdout)
	}
	if !strings.Contains(stderr, "HEW020") {
		t.Fatalf("stderr %q", stderr)
	}
}

func TestParseContext(t *testing.T) {
	cases := []struct {
		in   string
		want int
		bad  bool
	}{
		{"all", -1, false},
		{"0", -2, false},
		{"1", 1, false},
		{"7", 7, false},
		{"-1", 0, true},
		{"x", 0, true},
	}
	for _, c := range cases {
		got, err := parseContext(c.in)
		if (err != nil) != c.bad {
			t.Fatalf("parseContext(%q) err = %v, bad = %v", c.in, err, c.bad)
		}
		if err == nil && got != c.want {
			t.Fatalf("parseContext(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestSplitFields(t *testing.T) {
	if got := splitFields("a, b ,,c"); strings.Join(got, "|") != "a|b|c" {
		t.Fatalf("got %q", got)
	}
	if got := splitFields(" , "); len(got) != 0 {
		t.Fatalf("got %q", got)
	}
}
