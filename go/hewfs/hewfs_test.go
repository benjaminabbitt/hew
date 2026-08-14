package hewfs

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/spf13/afero"

	"github.com/benjaminabbitt/hew/go"
	_ "github.com/benjaminabbitt/hew/go/ext/all"
	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

// Every test in this package runs on afero.MemMapFs, which is ruling O50(b):
// a tmpdir-based test where an in-memory filesystem would serve is a defect,
// because it is slower, leaks state between runs, and tests the operating
// system rather than the code. What CANNOT be tested this way is named where
// it comes up — rename atomicity is the backend's (O49's caveat), and no
// in-memory filesystem can prove a property of the one hew will actually run
// on.

const targetYAML = "server:\n  host: localhost\n  port: 8080\n  timeout: 30\n"

const targetAbsentMatch = "servers:\n  - name: web\n    port: 80\n"

const patchAbsentMatch = "hew: 1\n\n--- config.yaml format=yaml\n\n@@ /servers @@\n? absent /servers/name=api\n  - name: web\n-   port: 80\n+   port: 8080\n"

const patchYAML = "hew: 1\n\n--- config.yaml format=yaml\n\n@@ /server @@\n  port: 8080\n- timeout: 30\n+ timeout: 60\n"

const patchedYAML = "server:\n  host: localhost\n  port: 8080\n  timeout: 60\n"

// memfs seeds an in-memory filesystem with the named files.
func memfs(t *testing.T, files map[string]string) afero.Fs {
	t.Helper()
	fsys := afero.NewMemMapFs()
	for name, body := range files {
		if err := afero.WriteFile(fsys, name, []byte(body), 0o644); err != nil {
			t.Fatalf("seeding %s: %v", name, err)
		}
	}
	return fsys
}

func parse(t *testing.T, patch string) []hew.TransformList {
	t.Helper()
	tls, err := hew.Parse([]byte(patch))
	if err != nil {
		t.Fatalf("parsing the fixture patch: %v", err)
	}
	return tls
}

func read(t *testing.T, fsys afero.Fs, name string) string {
	t.Helper()
	b, err := afero.ReadFile(fsys, name)
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(b)
}

// names lists every file in the filesystem, so a test can assert that hew left
// nothing behind: no .rej, no backup, no stray temp file (§10.5).
func names(t *testing.T, fsys afero.Fs) []string {
	t.Helper()
	var out []string
	err := afero.Walk(fsys, "/", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the filesystem: %v", err)
	}
	return out
}

func TestApplyFileWritesTheTarget(t *testing.T) {
	fsys := memfs(t, map[string]string{"/w/config.yaml": targetYAML})
	res, err := ApplyFile(fsys, "/w", parse(t, patchYAML), WriteOptions{})
	if err != nil {
		t.Fatalf("ApplyFile: %v", err)
	}
	if got := read(t, fsys, "/w/config.yaml"); got != patchedYAML {
		t.Errorf("target = %q, want %q", got, patchedYAML)
	}
	if len(res) != 1 {
		t.Fatalf("%d results, want 1", len(res))
	}
	if !res[0].Changed || !res[0].Written {
		t.Errorf("result = %+v, want Changed and Written", res[0])
	}
	if res[0].Target != "config.yaml" {
		t.Errorf("target = %q, want config.yaml", res[0].Target)
	}
	if len(res[0].Ops) != len(parse(t, patchYAML)[0].Transform) {
		t.Errorf("Ops = %d transforms, want the section's own", len(res[0].Ops))
	}
	if res[0].Reversal != "" {
		t.Errorf("Reversal = %q, want empty: no flag, no file (O40)", res[0].Reversal)
	}
}

// TestApplyFileLeavesNoBackupOrTempFile is §10.5 as a property: there is no
// .rej file, no partial output, and NO BACKUP FILE. The temp file the atomic
// write stages through must be gone by the time the call returns.
func TestApplyFileLeavesNoBackupOrTempFile(t *testing.T) {
	fsys := memfs(t, map[string]string{"/w/config.yaml": targetYAML})
	if _, err := ApplyFile(fsys, "/w", parse(t, patchYAML), WriteOptions{}); err != nil {
		t.Fatalf("ApplyFile: %v", err)
	}
	got := names(t, fsys)
	if len(got) != 1 || got[0] != "/w/config.yaml" {
		t.Errorf("filesystem holds %v, want only /w/config.yaml", got)
	}
}

// TestApplyFileFailedApplyLeavesBytesIdentical is the all-or-nothing rule
// across targets (§10.5, ruling O12): the FIRST section applies cleanly and the
// second is stale, so nothing at all may be written — including the section
// that would have succeeded.
func TestApplyFileFailedApplyLeavesBytesIdentical(t *testing.T) {
	fsys := memfs(t, map[string]string{
		"/w/first.yaml":  targetYAML,
		"/w/second.yaml": targetYAML,
	})
	patch := "hew: 1\n\n--- first.yaml format=yaml\n\n@@ /server @@\n- timeout: 30\n+ timeout: 60\n" +
		"\n--- second.yaml format=yaml\n\n@@ /server @@\n- timeout: 999\n+ timeout: 60\n"
	res, err := ApplyFile(fsys, "/w", parse(t, patch), WriteOptions{})
	if err == nil {
		t.Fatal("a stale second section must fail the whole apply")
	}
	if res != nil {
		t.Errorf("results = %+v, want nil alongside a staging failure", res)
	}
	if got := read(t, fsys, "/w/first.yaml"); got != targetYAML {
		t.Errorf("first.yaml was written by a failed apply: %q", got)
	}
	if got := read(t, fsys, "/w/second.yaml"); got != targetYAML {
		t.Errorf("second.yaml = %q, want unchanged", got)
	}
	if got := names(t, fsys); len(got) != 2 {
		t.Errorf("filesystem holds %v, want the two seeded files and nothing else", got)
	}
}

// TestApplyFileSectionErrorNamesTheSection pins the index a CLI needs to say
// WHICH patch file a diagnostic came from (§10.3).
func TestApplyFileSectionErrorNamesTheSection(t *testing.T) {
	fsys := memfs(t, map[string]string{"/w/a.yaml": targetYAML, "/w/b.yaml": targetYAML})
	patch := "hew: 1\n\n--- a.yaml format=yaml\n\n@@ /server @@\n- timeout: 30\n+ timeout: 60\n" +
		"\n--- b.yaml format=yaml\n\n@@ /server @@\n- timeout: 999\n+ timeout: 60\n"
	_, err := ApplyFile(fsys, "/w", parse(t, patch), WriteOptions{})
	var se *SectionError
	if !asSectionError(err, &se) {
		t.Fatalf("error %v is not a *SectionError", err)
	}
	if se.Index != 1 {
		t.Errorf("Index = %d, want 1 (the second section is the stale one)", se.Index)
	}
	if se.Error() != se.Err.Error() {
		t.Errorf("a SectionError must render as the error it wraps, got %q", se.Error())
	}
	if he, ok := hewerr.As(err); !ok || he.Code != hewerr.CodeStaleTarget {
		t.Errorf("hewerr.As must unwrap the section error to HEW010, got %v", err)
	}
}

func asSectionError(err error, out **SectionError) bool {
	se, ok := err.(*SectionError)
	if ok {
		*out = se
	}
	return ok
}

func TestApplyFileMissingTargetIsHEW003(t *testing.T) {
	fsys := memfs(t, nil)
	_, err := ApplyFile(fsys, "/w", parse(t, patchYAML), WriteOptions{})
	he, ok := hewerr.As(err)
	if !ok || he.Code != hewerr.CodeTargetPath {
		t.Fatalf("want HEW003 target-path-error, got %v", err)
	}
	if he.Target != "config.yaml" {
		t.Errorf("Target = %q, want config.yaml", he.Target)
	}
}

func TestApplyFileDryRunWritesNothing(t *testing.T) {
	fsys := memfs(t, map[string]string{"/w/config.json": targetJSON})
	res, err := ApplyFile(fsys, "/w", parse(t, patchJSON), WriteOptions{DryRun: true, Reversal: true, RecordPath: "rec.hewt"})
	if err != nil {
		t.Fatalf("ApplyFile: %v", err)
	}
	if got := read(t, fsys, "/w/config.json"); got != targetJSON {
		t.Errorf("--dry-run wrote the target: %q", got)
	}
	if got := names(t, fsys); len(got) != 1 {
		t.Errorf("--dry-run left %v; it must write nothing at all, record and reversal included", got)
	}
	if !res[0].Changed {
		t.Error("a dry run still reports what WOULD change")
	}
	if res[0].Written {
		t.Error("Written must be false for a dry run")
	}
}

// TestApplyFileNoOpSectionWritesNothing is ruling O38's applier half: a file
// section with no hunks applies as a no-op — exit 0, target byte-unchanged, no
// write — and it does so WITHOUT a format binding, because there is nothing to
// apply and nothing to parse.
func TestApplyFileNoOpSectionWritesNothing(t *testing.T) {
	fsys := memfs(t, map[string]string{"/w/config.yaml": targetYAML, "/w/thing.unknown": "whatever"})
	for _, patch := range []string{
		"hew: 1\n\n--- config.yaml format=yaml\n",
		"hew: 1\n\n--- thing.unknown\n",
	} {
		res, err := ApplyFile(fsys, "/w", parse(t, patch), WriteOptions{})
		if err != nil {
			t.Fatalf("a hunkless section must apply as a no-op (§10.2, O38): %v", err)
		}
		if res[0].Changed || res[0].Written {
			t.Errorf("%q: result = %+v, want neither Changed nor Written", patch, res[0])
		}
	}
	if got := read(t, fsys, "/w/config.yaml"); got != targetYAML {
		t.Errorf("a no-op patch rewrote the target: %q", got)
	}
	if got := names(t, fsys); len(got) != 2 {
		t.Errorf("a no-op patch left %v", got)
	}
}

// TestApplyFileUnsupportedFormatIsHEW021 covers the two ways a format can be
// unapplicable, which the diagnostic must tell apart: NOTHING CLAIMS the
// target's extension, and an extension claims it but ships no applier. Markdown
// is the second kind by construction while §8.7's evaluation is open (O29), and
// a caller told only "unsupported" would go looking for the wrong problem.
func TestApplyFileUnsupportedFormatIsHEW021(t *testing.T) {
	fsys := memfs(t, map[string]string{"/w/thing.unknown": "whatever", "/w/notes.md": "# Title\n"})
	cases := []struct {
		patch, detail string
	}{
		{"hew: 1\n\n--- thing.unknown\n\n@@ / @@\n+ a: 1\n", "no format declared"},
		{"hew: 1\n\n--- notes.md format=markdown\n\n@@ / @@\n+ a: 1\n", `no binding for format "markdown"`},
	}
	for _, tc := range cases {
		_, err := ApplyFile(fsys, "/w", parse(t, tc.patch), WriteOptions{})
		he, ok := hewerr.As(err)
		if !ok || he.Code != hewerr.CodeUnsupportedFormat {
			t.Fatalf("%q: want HEW021, got %v", tc.patch, err)
		}
		if !strings.Contains(he.Detail, tc.detail) {
			t.Errorf("%q: detail %q does not say %q", tc.patch, he.Detail, tc.detail)
		}
	}
}

// TestApplyFileFormatOverride pins WriteOptions.Format as §8.0's override: it
// beats the section's own declaration for every target.
func TestApplyFileFormatOverride(t *testing.T) {
	fsys := memfs(t, map[string]string{"/w/config.conf": targetYAML})
	patch := "hew: 1\n\n--- config.conf\n\n@@ /server @@\n- timeout: 30\n+ timeout: 60\n"
	if _, err := ApplyFile(fsys, "/w", parse(t, patch), WriteOptions{Format: hew.FormatYAML}); err != nil {
		t.Fatalf("ApplyFile with a format override: %v", err)
	}
	if got := read(t, fsys, "/w/config.conf"); got != patchedYAML {
		t.Errorf("target = %q, want the patched bytes", got)
	}
}

// TestApplyTransformsIsTheSamePath: the two entry points are two names for one
// execution, which is what §13.5's round-trip identities require of notation
// and IR.
func TestApplyTransformsIsTheSamePath(t *testing.T) {
	a := memfs(t, map[string]string{"/w/config.yaml": targetYAML})
	b := memfs(t, map[string]string{"/w/config.yaml": targetYAML})
	if _, err := ApplyFile(a, "/w", parse(t, patchYAML), WriteOptions{}); err != nil {
		t.Fatalf("ApplyFile: %v", err)
	}
	if _, err := ApplyTransforms(b, "/w", parse(t, patchYAML), WriteOptions{}); err != nil {
		t.Fatalf("ApplyTransforms: %v", err)
	}
	if read(t, a, "/w/config.yaml") != read(t, b, "/w/config.yaml") {
		t.Error("ApplyFile and ApplyTransforms wrote different bytes")
	}
}

// TestApplyFileOutputRedirectsTheResult covers Appendix B.1's `-o FILE`: the
// named file receives the bytes and the target is left alone.
func TestApplyFileOutputRedirectsTheResult(t *testing.T) {
	fsys := memfs(t, map[string]string{"/w/config.yaml": targetYAML})
	res, err := ApplyFile(fsys, "/w", parse(t, patchYAML), WriteOptions{Output: "out.yaml"})
	if err != nil {
		t.Fatalf("ApplyFile: %v", err)
	}
	if got := read(t, fsys, "/w/out.yaml"); got != patchedYAML {
		t.Errorf("out.yaml = %q, want the patched bytes", got)
	}
	if got := read(t, fsys, "/w/config.yaml"); got != targetYAML {
		t.Errorf("-o must leave the target alone, got %q", got)
	}
	if !res[0].Written {
		t.Error("Written must report the redirected write")
	}
}

// TestApplyFileOutputStdoutWritesNoFile: "-" is not a path, so hewfs writes
// nothing and hands the bytes back for the caller to stream.
func TestApplyFileOutputStdoutWritesNoFile(t *testing.T) {
	fsys := memfs(t, map[string]string{"/w/config.yaml": targetYAML})
	res, err := ApplyFile(fsys, "/w", parse(t, patchYAML), WriteOptions{Output: "-"})
	if err != nil {
		t.Fatalf("ApplyFile: %v", err)
	}
	if string(res[0].After) != patchedYAML {
		t.Errorf("After = %q, want the patched bytes", res[0].After)
	}
	if res[0].Written {
		t.Error("Written must be false: nothing was written to a file")
	}
	if got := names(t, fsys); len(got) != 1 || got[0] != "/w/config.yaml" {
		t.Errorf("filesystem holds %v, want only the untouched target", got)
	}
}

func TestApplyFileOutputWithSeveralSectionsIsUsage(t *testing.T) {
	fsys := memfs(t, map[string]string{"/w/a.yaml": targetYAML, "/w/b.yaml": targetYAML})
	patch := "hew: 1\n\n--- a.yaml format=yaml\n\n@@ /server @@\n- timeout: 30\n+ timeout: 60\n" +
		"\n--- b.yaml format=yaml\n\n@@ /server @@\n- timeout: 30\n+ timeout: 41\n"
	_, err := ApplyFile(fsys, "/w", parse(t, patch), WriteOptions{Output: "out.yaml"})
	if err == nil || !IsUsage(err) {
		t.Fatalf("want a usage error, got %v", err)
	}
}

// --- the reversal patch (O40) -----------------------------------------------

const undoYAML = "hew: 1\n\n--- config.yaml format=yaml\n\n@@ /server @@\n  port: 8080\n- timeout: 60\n+ timeout: 30\n"

func TestApplyFileReversalWritesTheUndoPatch(t *testing.T) {
	fsys := memfs(t, map[string]string{"/w/config.yaml": targetYAML})
	res, err := ApplyFile(fsys, "/w", parse(t, patchYAML), WriteOptions{ReversalPath: "config.yaml.undo.hew"})
	if err != nil {
		t.Fatalf("ApplyFile: %v", err)
	}
	if got := read(t, fsys, "/w/config.yaml.undo.hew"); got != undoYAML {
		t.Errorf("reversal patch =\n%s\nwant\n%s", got, undoYAML)
	}
	if res[0].Reversal != "config.yaml.undo.hew" {
		t.Errorf("Reversal = %q, want the path written", res[0].Reversal)
	}
}

// TestApplyFileReversalIsTheUndo is O40's whole claim: applying the reversal
// patch restores the pre-apply bytes. The corpus states this in prose because
// its manifest holds one argv; here it is two calls and an assertion.
func TestApplyFileReversalIsTheUndo(t *testing.T) {
	fsys := memfs(t, map[string]string{"/w/config.yaml": targetYAML})
	if _, err := ApplyFile(fsys, "/w", parse(t, patchYAML), WriteOptions{Reversal: true}); err != nil {
		t.Fatalf("forward apply: %v", err)
	}
	if got := read(t, fsys, "/w/config.yaml"); got != patchedYAML {
		t.Fatalf("forward apply produced %q", got)
	}
	undo := read(t, fsys, "/w/config.yaml.undo.hew")
	if _, err := ApplyFile(fsys, "/w", parse(t, undo), WriteOptions{}); err != nil {
		t.Fatalf("applying the reversal: %v", err)
	}
	if got := read(t, fsys, "/w/config.yaml"); got != targetYAML {
		t.Errorf("after the undo the target is %q, want the pre-apply bytes %q", got, targetYAML)
	}
}

// TestApplyFileReversalDefaultName pins Appendix B.1's derived name.
func TestApplyFileReversalDefaultName(t *testing.T) {
	fsys := memfs(t, map[string]string{"/w/config.yaml": targetYAML})
	res, err := ApplyFile(fsys, "/w", parse(t, patchYAML), WriteOptions{Reversal: true})
	if err != nil {
		t.Fatalf("ApplyFile: %v", err)
	}
	if res[0].Reversal != "config.yaml.undo.hew" {
		t.Errorf("derived reversal name = %q, want config.yaml.undo.hew", res[0].Reversal)
	}
	if _, err := fsys.Stat("/w/config.yaml.undo.hew"); err != nil {
		t.Errorf("no reversal file at the derived name: %v", err)
	}
}

// TestApplyFileReversalOneFilePerTarget: with several targets the name is
// derived per target, because one path cannot name several files.
func TestApplyFileReversalOneFilePerTarget(t *testing.T) {
	fsys := memfs(t, map[string]string{"/w/a.yaml": targetYAML, "/w/b.yaml": targetYAML})
	patch := "hew: 1\n\n--- a.yaml format=yaml\n\n@@ /server @@\n- timeout: 30\n+ timeout: 60\n" +
		"\n--- b.yaml format=yaml\n\n@@ /server @@\n- timeout: 30\n+ timeout: 41\n"
	res, err := ApplyFile(fsys, "/w", parse(t, patch), WriteOptions{Reversal: true})
	if err != nil {
		t.Fatalf("ApplyFile: %v", err)
	}
	if res[0].Reversal != "a.yaml.undo.hew" || res[1].Reversal != "b.yaml.undo.hew" {
		t.Fatalf("reversal names = %q, %q", res[0].Reversal, res[1].Reversal)
	}
	if !strings.Contains(read(t, fsys, "/w/b.yaml.undo.hew"), "+ timeout: 30") {
		t.Errorf("b's reversal does not restore b:\n%s", read(t, fsys, "/w/b.yaml.undo.hew"))
	}
}

func TestApplyFileReversalNamedPathWithSeveralTargetsIsUsage(t *testing.T) {
	fsys := memfs(t, map[string]string{"/w/a.yaml": targetYAML, "/w/b.yaml": targetYAML})
	patch := "hew: 1\n\n--- a.yaml format=yaml\n\n@@ /server @@\n- timeout: 30\n+ timeout: 60\n" +
		"\n--- b.yaml format=yaml\n\n@@ /server @@\n- timeout: 30\n+ timeout: 41\n"
	_, err := ApplyFile(fsys, "/w", parse(t, patch), WriteOptions{ReversalPath: "one.undo.hew"})
	if err == nil || !IsUsage(err) {
		t.Fatalf("want a usage error, got %v", err)
	}
	if got := read(t, fsys, "/w/a.yaml"); got != targetYAML {
		t.Errorf("a usage error must be raised before any write; a.yaml = %q", got)
	}
}

// TestApplyFileReversalFollowsAMutation: no mutation, no reversal file. The
// ruling says "on successful mutation, ALSO write" — a no-op mutated nothing,
// so there is nothing to undo.
func TestApplyFileReversalFollowsAMutation(t *testing.T) {
	fsys := memfs(t, map[string]string{"/w/config.yaml": targetYAML})
	res, err := ApplyFile(fsys, "/w", parse(t, "hew: 1\n\n--- config.yaml format=yaml\n"), WriteOptions{Reversal: true})
	if err != nil {
		t.Fatalf("ApplyFile: %v", err)
	}
	if res[0].Reversal != "" {
		t.Errorf("Reversal = %q, want none for a no-op", res[0].Reversal)
	}
	if _, err := fsys.Stat("/w/config.yaml.undo.hew"); err == nil {
		t.Error("a no-op apply wrote a reversal patch")
	}
}

// --- the application record (§9.7, O37) --------------------------------------
//
// These run on JSON because a record needs the registry's Document binding to
// resolve §9.2's pointer form, and JSON is the extension that ships one in this
// build. TestApplyFileRecordNeedsADocumentBinding pins what happens when it
// does not, which is the same all-or-nothing rule from the other side.

const targetJSON = "{\n  \"mcpServers\": [\n    { \"name\": \"ctxloom\", \"command\": \"ctxloom\" }\n  ]\n}\n"

const patchJSON = "hew: 1\n\n--- config.json format=json\n\n@@ /mcpServers @@\n  { \"name\": \"ctxloom\" }\n+ { \"name\": \"taskloom\", \"command\": \"taskloom\" }\n"

const patchedJSON = "{\n  \"mcpServers\": [\n    { \"name\": \"ctxloom\", \"command\": \"ctxloom\" },\n    { \"name\": \"taskloom\", \"command\": \"taskloom\" }\n  ]\n}\n"

func TestApplyFileRecordPinnedAppliedAt(t *testing.T) {
	fsys := memfs(t, map[string]string{"/w/config.json": targetJSON})
	pin := time.Date(2026, 8, 14, 9, 31, 7, 0, time.UTC)
	_, err := ApplyFile(fsys, "/w", parse(t, patchJSON), WriteOptions{
		RecordPath: "out.hewt",
		AppliedAt:  pin,
		Patch:      RecordPatch{Source: "patch.hew", Digest: Digest([]byte(patchJSON))},
	})
	if err != nil {
		t.Fatalf("ApplyFile: %v", err)
	}
	rec, err := UnmarshalRecord([]byte(read(t, fsys, "/w/out.hewt")))
	if err != nil {
		t.Fatalf("UnmarshalRecord: %v", err)
	}
	if !rec.AppliedAt.Equal(pin) {
		t.Errorf("applied_at = %s, want the pin %s", rec.AppliedAt, pin)
	}
	if rec.Patch.Source != "patch.hew" || rec.Patch.Digest != Digest([]byte(patchJSON)) {
		t.Errorf("patch = %+v, want the source and digest the caller supplied", rec.Patch)
	}
	if len(rec.Targets) != 1 {
		t.Fatalf("%d target rows, want 1", len(rec.Targets))
	}
	got := rec.Targets[0]
	if got.Before != Digest([]byte(targetJSON)) || got.After != Digest([]byte(patchedJSON)) {
		t.Errorf("digests = %s / %s, want the bytes as read and as written", got.Before, got.After)
	}
	if !got.Committed || got.Format != hew.FormatJSON || got.Target != "config.json" {
		t.Errorf("target row = %+v", got)
	}
	if len(got.Transforms) == 0 {
		t.Error("the record must carry the RESOLVED transforms actually executed")
	}
	for _, op := range got.Transforms {
		if !strings.HasPrefix(op.Path, "/") || strings.Contains(op.Path, "=") {
			t.Errorf("op path %q is not an RFC 6901 pointer; the record carries the RESOLVED list (§9.2)", op.Path)
		}
	}
}

// TestApplyFileRecordIsReproducible is O37's reason for existing: two applies
// of the same patch against the same target with the same pin produce
// byte-identical records.
func TestApplyFileRecordIsReproducible(t *testing.T) {
	pin := time.Date(2026, 8, 14, 9, 31, 7, 0, time.UTC)
	run := func() string {
		fsys := memfs(t, map[string]string{"/w/config.json": targetJSON})
		opt := WriteOptions{RecordPath: "out.hewt", AppliedAt: pin, Patch: RecordPatch{Source: "patch.hew"}}
		if _, err := ApplyFile(fsys, "/w", parse(t, patchJSON), opt); err != nil {
			t.Fatalf("ApplyFile: %v", err)
		}
		return read(t, fsys, "/w/out.hewt")
	}
	if a, b := run(), run(); a != b {
		t.Errorf("a pinned record is not reproducible:\n%s\nvs\n%s", a, b)
	}
}

// TestApplyFileRecordAppliedAtDefaultsToTheClock covers the last row of §9.7's
// precedence table. The environment rows are NOT here: they are the CLI's, and
// this package deliberately reads no environment.
func TestApplyFileRecordAppliedAtDefaultsToTheClock(t *testing.T) {
	fsys := memfs(t, map[string]string{"/w/config.json": targetJSON})
	before := time.Now().Add(-time.Second)
	if _, err := ApplyFile(fsys, "/w", parse(t, patchJSON), WriteOptions{RecordPath: "out.hewt"}); err != nil {
		t.Fatalf("ApplyFile: %v", err)
	}
	rec, err := UnmarshalRecord([]byte(read(t, fsys, "/w/out.hewt")))
	if err != nil {
		t.Fatalf("UnmarshalRecord: %v", err)
	}
	if rec.AppliedAt.Before(before) || rec.AppliedAt.After(time.Now().Add(time.Second)) {
		t.Errorf("applied_at = %s, want roughly now", rec.AppliedAt)
	}
}

// TestApplyFileNoOpRecordSaysNothingHappened is §10.2's amended note: a no-op
// patch with --record produces a record with an empty transforms list and equal
// digests, "a record that truthfully says nothing happened".
func TestApplyFileNoOpRecordSaysNothingHappened(t *testing.T) {
	fsys := memfs(t, map[string]string{"/w/config.json": targetJSON})
	_, err := ApplyFile(fsys, "/w", parse(t, "hew: 1\n\n--- config.json format=json\n"),
		WriteOptions{RecordPath: "out.hewt"})
	if err != nil {
		t.Fatalf("ApplyFile: %v", err)
	}
	rec, err := UnmarshalRecord([]byte(read(t, fsys, "/w/out.hewt")))
	if err != nil {
		t.Fatalf("UnmarshalRecord: %v", err)
	}
	row := rec.Targets[0]
	if len(row.Transforms) != 0 {
		t.Errorf("%d transforms, want none", len(row.Transforms))
	}
	if row.Before != row.After {
		t.Errorf("before %s != after %s; a no-op's digests are equal", row.Before, row.After)
	}
}

// TestApplyFileRecordFailureLeavesTargetUntouched is the ordering §10.5
// requires: the record is BUILT before the commit, so a run that cannot produce
// its record must leave the target byte-identical rather than edit a file and
// then report failure.
//
// The failure used here is a real one and not a contrivance: resolving §9.2's
// pointer form needs the registry's Document binding, and a format whose
// extension ships an applier but no document view — YAML, in this build — can
// apply a patch it cannot record. The applier succeeds, the record build does
// not, and the target must still hold its original bytes.
// The forced failure is §10.5's PERMANENT one, not a borrowed gap: an
// `? absent` assertion on a key-match that matches nothing is satisfied by the
// applier — nothing is there, which is what it asserted — but has no RFC 6901
// pointer, so Resolve cannot project the executed list and no record can be
// built.
//
// The point is the ORDERING. The apply itself succeeds and would have written
// port: 8080; because the record is built first and could not be, the target
// keeps its pre-apply bytes and no record file appears.
func TestApplyFileRecordFailureLeavesTargetUntouched(t *testing.T) {
	fsys := memfs(t, map[string]string{"/w/config.yaml": targetAbsentMatch})
	_, err := ApplyFile(fsys, "/w", parse(t, patchAbsentMatch), WriteOptions{RecordPath: "out.hewt"})
	if err == nil {
		t.Fatal("a record that cannot be built must fail the run")
	}
	// Pin the REASON. Without this the test would keep passing on any error at
	// all — a stale target, a bad patch — and stop testing the ordering it is
	// named for.
	if he, ok := hewerr.As(err); !ok || he.Code != hewerr.CodeNoMatch {
		t.Fatalf("want the record build to fail on the unresolvable key-match (HEW013), got %v", err)
	}
	if got := read(t, fsys, "/w/config.yaml"); got != targetAbsentMatch {
		t.Errorf("target = %q, want the pre-apply bytes: the record is built before the commit", got)
	}
	if _, statErr := fsys.Stat("/w/out.hewt"); statErr == nil {
		t.Error("a failed record build still wrote a record file")
	}
}

// TestTheSameApplyWithoutARecordSucceeds is the other half, and what makes the
// test above about ordering rather than about a broken patch.
func TestTheSameApplyWithoutARecordSucceeds(t *testing.T) {
	fsys := memfs(t, map[string]string{"/w/config.yaml": targetAbsentMatch})
	if _, err := ApplyFile(fsys, "/w", parse(t, patchAbsentMatch), WriteOptions{}); err != nil {
		t.Fatalf("apply without a record: %v", err)
	}
	if got := read(t, fsys, "/w/config.yaml"); got == targetAbsentMatch {
		t.Error("the apply changed nothing, so the record test proves nothing")
	}
}

// --- WriteAtomic (§10.5) ------------------------------------------------------

func TestWriteAtomicCreatesAndOverwrites(t *testing.T) {
	fsys := afero.NewMemMapFs()
	if err := fsys.MkdirAll("/w", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(fsys, "/w/new.txt", []byte("one")); err != nil {
		t.Fatalf("creating: %v", err)
	}
	if got := read(t, fsys, "/w/new.txt"); got != "one" {
		t.Errorf("got %q", got)
	}
	if err := WriteAtomic(fsys, "/w/new.txt", []byte("two")); err != nil {
		t.Fatalf("overwriting: %v", err)
	}
	if got := read(t, fsys, "/w/new.txt"); got != "two" {
		t.Errorf("rename over an existing destination did not take: %q", got)
	}
	if got := names(t, fsys); len(got) != 1 {
		t.Errorf("filesystem holds %v; the temp file must be gone and no backup written", got)
	}
}

func TestWriteAtomicPreservesAnExistingMode(t *testing.T) {
	fsys := afero.NewMemMapFs()
	if err := afero.WriteFile(fsys, "/w/f.txt", []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(fsys, "/w/f.txt", []byte("two")); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	st, err := fsys.Stat("/w/f.txt")
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600 preserved: a patch tool must not widen permissions", st.Mode().Perm())
	}
}

func TestWriteAtomicNewFileGets0644(t *testing.T) {
	fsys := afero.NewMemMapFs()
	if err := fsys.MkdirAll("/w", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(fsys, "/w/f.txt", []byte("one")); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	st, err := fsys.Stat("/w/f.txt")
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want 0644", st.Mode().Perm())
	}
}

// TestWriteAtomicFailureLeavesTheDestinationIdentical is the property O49 says
// holds on every backend: a DETECTABLE failure writes nothing at all.
func TestWriteAtomicFailureLeavesTheDestinationIdentical(t *testing.T) {
	base := afero.NewMemMapFs()
	if err := afero.WriteFile(base, "/w/f.txt", []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	ro := afero.NewReadOnlyFs(base)
	err := WriteAtomic(ro, "/w/f.txt", []byte("replacement"))
	if err == nil {
		t.Fatal("writing through a read-only filesystem must fail")
	}
	if he, ok := hewerr.As(err); !ok || he.Code != hewerr.CodeTargetPath {
		t.Errorf("want HEW003, got %v", err)
	}
	if got := read(t, base, "/w/f.txt"); got != "original" {
		t.Errorf("destination = %q, want the original bytes", got)
	}
}

func TestApplyFileWriteFailureIsReported(t *testing.T) {
	base := memfs(t, map[string]string{"/w/config.yaml": targetYAML})
	ro := afero.NewReadOnlyFs(base)
	_, err := ApplyFile(ro, "/w", parse(t, patchYAML), WriteOptions{})
	if err == nil {
		t.Fatal("a failed commit must be reported")
	}
	if got := read(t, base, "/w/config.yaml"); got != targetYAML {
		t.Errorf("target = %q, want unchanged", got)
	}
}
