package hew

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

// The terminals (A.0). A Doc is not an editor of bytes; it is a recorder of
// transforms with a target to resolve them against, and nothing has happened
// until one of these is called.

// The builder's output is ORDINARY IR: it survives the canonical .hewt
// serialization unchanged, which is the boundary the corpus pins for both
// producers.
func TestTransformsRoundTripThroughHewt(t *testing.T) {
	d := opsDoc(t)
	d.At("/host").Replace("example.com")
	d.At("/timeout").Add(30).After("port")
	d.At("/servers").AssertCount(2)

	tl, err := d.Transforms()
	if err != nil {
		t.Fatal(err)
	}
	wire, err := MarshalTransforms(tl)
	if err != nil {
		t.Fatalf("the builder produced IR the codec refuses: %v", err)
	}
	back, err := UnmarshalTransforms(wire)
	if err != nil {
		t.Fatal(err)
	}
	if !back.Equal(tl) {
		t.Fatalf("round trip changed the IR:\n%s", wire)
	}
}

// Indistinguishable from parsed-patch IR, stated as an equality: the same edit
// written as a .hew patch and lowered by the parser produces the same
// transform list the builder does.
func TestBuiltIRMatchesTheParsedPatch(t *testing.T) {
	d := opsDoc(t)
	d.At("/host").Replace("example.com")

	built, err := d.Transforms()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseSingle([]byte("hew: 1\n\n--- config.toy format=toy\n\n@@ / @@\n- host: localhost\n+ host: example.com\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !built.Equal(parsed) {
		t.Fatalf("builder IR differs from the parser's:\nbuilt:\n%s\nparsed:\n%s", irDump(built), irDump(parsed))
	}
}

func TestTransformsCarriesTargetAndFormat(t *testing.T) {
	d := opsDoc(t)
	d.At("/host").Set("x")
	tl, err := d.Transforms()
	if err != nil {
		t.Fatal(err)
	}
	if tl.Target != "config.toy" || tl.Format != formatToy {
		t.Fatalf("target=%q format=%q", tl.Target, tl.Format)
	}
}

// --- RenderPatch -------------------------------------------------------------

// Because the document is open, the renderer has real siblings to draw context
// from: the patch carries genuine context lines at §9.4-R2's radius, which is
// the same artifact `hew diff` produces from the same renderer.
func TestRenderPatchCarriesRealContext(t *testing.T) {
	d := opsDoc(t)
	d.At("/host").Replace("example.com")

	out, err := d.RenderPatch(RenderOptions{Preamble: true})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "- host: localhost") || !strings.Contains(got, "+ host: example.com") {
		t.Fatalf("patch does not show the change:\n%s", got)
	}
	// port is the sibling one position away: at radius 1 it is context, and a
	// recorded-IR-only render would not know it exists.
	if !strings.Contains(got, " port: 8080") {
		t.Fatalf("patch carries no sibling context:\n%s", got)
	}
	if !strings.Contains(got, "--- config.toy") {
		t.Fatalf("patch does not name the target:\n%s", got)
	}
}

// The rendered patch is a real patch: it parses, and applying it reproduces
// exactly what Bytes() produced.
func TestRenderPatchRoundTripsThroughApply(t *testing.T) {
	d := opsDoc(t)
	d.At("/host").Replace("example.com")

	patch, err := d.RenderPatch(RenderOptions{Preamble: true})
	if err != nil {
		t.Fatal(err)
	}
	tl, err := ParseSingle(patch)
	if err != nil {
		t.Fatalf("the rendered patch does not parse: %v\n%s", err, patch)
	}
	fromPatch, err := toyApply([]byte(opsSrc), tl)
	if err != nil {
		t.Fatalf("the rendered patch does not apply: %v\n%s", err, patch)
	}
	direct, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(fromPatch, direct) {
		t.Fatalf("patch and Bytes disagree:\n%s\n---\n%s", fromPatch, direct)
	}
}

// A recording that changes no bytes has nothing for the differ to describe —
// an assert-only patch (§7.4) is still a patch, and it renders as the
// transforms that were recorded.
func TestRenderPatchOfAnAssertOnlyRecording(t *testing.T) {
	d := opsDoc(t)
	d.At("/port").Assert(8080)

	out, err := d.RenderPatch(RenderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// A `test` with a value is a context line: a hunk of nothing but context
	// is §7.4's assert-only hunk, which is exactly what was recorded.
	if !strings.Contains(string(out), "  port: 8080") {
		t.Fatalf("assert-only patch:\n%s", out)
	}
	if strings.Contains(string(out), "+") {
		t.Fatalf("an assert-only recording rendered a write:\n%s", out)
	}
}

func TestRenderPatchWithoutADifferHalf(t *testing.T) {
	isolate(t)
	b := toyBinding()
	b.Differ = nil
	Register(formatToy, b)
	d, err := OpenBytes("config.toy", []byte(opsSrc))
	if err != nil {
		t.Fatal(err)
	}
	d.At("/host").Replace("example.com")
	_, rerr := d.RenderPatch(RenderOptions{})
	wantCode(t, rerr, hewerr.CodeUnsupportedFormat)
}

func TestRenderPatchSurfacesALatchedError(t *testing.T) {
	d := opsDoc(t)
	d.At("/a/{}").Set("x")
	_, err := d.RenderPatch(RenderOptions{})
	wantCode(t, err, hewerr.CodeParse)
}

// --- Bytes -------------------------------------------------------------------

func TestBytesAppliesThroughTheRegistry(t *testing.T) {
	d := opsDoc(t)
	d.At("/host").Replace("example.com")

	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "host: example.com") {
		t.Fatalf("Bytes did not apply:\n%s", out)
	}
	if string(d.src) != opsSrc {
		t.Fatal("Bytes mutated the document's own content")
	}
}

// All-or-nothing (§10.5): on any error the result is nil, and the recorded
// asserts are what catch the drift.
func TestBytesIsAllOrNothing(t *testing.T) {
	d := opsDoc(t)
	d.At("/host").Add("example.com") // OP-02: must not exist, and it does

	out, err := d.Bytes()
	if err == nil {
		t.Fatal("adding an existing key succeeded")
	}
	if out != nil {
		t.Fatalf("a failed apply returned bytes: %q", out)
	}
}

func TestBytesWithoutAnApplierHalf(t *testing.T) {
	isolate(t)
	b := toyBinding()
	b.Applier = nil
	Register(formatToy, b)
	d, err := OpenBytes("config.toy", []byte(opsSrc))
	if err != nil {
		t.Fatal(err)
	}
	d.At("/host").Replace("x")
	_, berr := d.Bytes()
	wantCode(t, berr, hewerr.CodeUnsupportedFormat)
}

// --- Write -------------------------------------------------------------------

func writeDoc(t *testing.T) (afero.Fs, *Doc) {
	t.Helper()
	toyOnly(t)
	fsys := memfs(t, "/etc/config.toy", opsSrc)
	d, err := Open(fsys, "/etc/config.toy")
	if err != nil {
		t.Fatal(err)
	}
	return fsys, d
}

func TestWriteCommitsThroughTheSameFs(t *testing.T) {
	fsys, d := writeDoc(t)
	d.At("/host").Replace("example.com")

	if err := d.Write(); err != nil {
		t.Fatal(err)
	}
	got, err := afero.ReadFile(fsys, "/etc/config.toy")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "host: example.com") {
		t.Fatalf("file after Write:\n%s", got)
	}
}

// §10.5: no backup file, and no temp file left behind. The commit is a
// temp-and-rename, so what the directory holds afterwards is the target and
// nothing else.
func TestWriteLeavesNoOtherFileBehind(t *testing.T) {
	fsys, d := writeDoc(t)
	d.At("/host").Replace("example.com")
	if err := d.Write(); err != nil {
		t.Fatal(err)
	}
	names, err := afero.ReadDir(fsys, "/etc")
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0].Name() != "config.toy" {
		var got []string
		for _, n := range names {
			got = append(got, n.Name())
		}
		t.Fatalf("directory holds %v", got)
	}
}

func TestWritePreservesTheFilesMode(t *testing.T) {
	toyOnly(t)
	fsys := afero.NewMemMapFs()
	if err := afero.WriteFile(fsys, "config.toy", []byte(opsSrc), 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := Open(fsys, "config.toy")
	if err != nil {
		t.Fatal(err)
	}
	d.At("/host").Replace("example.com")
	if err := d.Write(); err != nil {
		t.Fatal(err)
	}
	fi, err := fsys.Stat("config.toy")
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode after Write = %v", fi.Mode().Perm())
	}
}

// A detectable failure writes nothing at all, on every backend, because
// staging happens entirely in memory.
func TestWriteWritesNothingWhenTheApplyFails(t *testing.T) {
	fsys, d := writeDoc(t)
	d.At("/host").Add("example.com") // HEW014

	if err := d.Write(); err == nil {
		t.Fatal("Write succeeded on a failing apply")
	}
	got, err := afero.ReadFile(fsys, "/etc/config.toy")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != opsSrc {
		t.Fatalf("target changed:\n%s", got)
	}
	names, _ := afero.ReadDir(fsys, "/etc")
	if len(names) != 1 {
		t.Fatalf("a failed write left %d files behind", len(names))
	}
}

func TestWriteDryRunWritesNothing(t *testing.T) {
	fsys, d := writeDoc(t)
	d.At("/host").Replace("example.com")

	if err := d.Write(DryRun()); err != nil {
		t.Fatal(err)
	}
	got, _ := afero.ReadFile(fsys, "/etc/config.toy")
	if string(got) != opsSrc {
		t.Fatalf("dry run wrote:\n%s", got)
	}
}

// A dry run still does everything else, so a failure it would have hit is
// still a failure.
func TestWriteDryRunStillFails(t *testing.T) {
	_, d := writeDoc(t)
	d.At("/host").Add("x")
	if err := d.Write(DryRun()); err == nil {
		t.Fatal("a dry run hid the failure")
	}
}

func TestWriteThroughAnAdoptedHandle(t *testing.T) {
	toyOnly(t)
	fsys := memfs(t, "config.toy", opsSrc)
	f, err := fsys.OpenFile("config.toy", 0o2, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	d, err := OpenFile(f)
	if err != nil {
		t.Fatal(err)
	}
	d.At("/host").Replace("example.com")
	if err := d.Write(); err != nil {
		t.Fatal(err)
	}
	// hew never closes a handle it did not open, so the caller's Close is
	// still the first one.
	if err := f.Close(); err != nil {
		t.Fatalf("hew closed the caller's handle: %v", err)
	}
	got, err := afero.ReadFile(fsys, "config.toy")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "host: example.com") {
		t.Fatalf("handle write:\n%s", got)
	}
	if strings.Contains(string(got), "localhost") {
		t.Fatalf("handle write left the old content behind:\n%s", got)
	}
}

// A Doc from OpenBytes has no destination and says so rather than guessing
// one.
func TestWriteFromOpenBytesHasNoDestination(t *testing.T) {
	d := opsDoc(t)
	d.At("/host").Replace("x")
	err := d.Write()
	he := wantCode(t, err, hewerr.CodeTargetPath)
	if !strings.Contains(he.Detail, "OpenBytes") {
		t.Fatalf("detail = %q", he.Detail)
	}
}

func TestWriteSurfacesALatchedError(t *testing.T) {
	_, d := writeDoc(t)
	d.At("/a/{}").Set("x")
	wantCode(t, d.Write(), hewerr.CodeParse)
}

// A.8 is being built on p5/cli-rulings. Doc.Write commits through the seam
// below, and this is the case that pins the routing once hewfs lands: the
// commit half must be hewfs's temp-and-rename, not a second implementation of
// it living here.
func TestWriteRoutesThroughHewfs(t *testing.T) {
}

// Bytes that differ while the TREE does not — a formatting-only rewrite — give
// the differ nothing to describe, and the recorded transforms are what the
// patch says.
func TestRenderPatchWhenTheDiffIsEmpty(t *testing.T) {
	toyOnly(t)
	d, err := OpenBytes("config.toy", []byte("port:    8080\nhost: localhost\n"))
	if err != nil {
		t.Fatal(err)
	}
	d.At("/port").Default(9090) // OP-04: the key is there, so nothing changes

	out, err := d.RenderPatch(RenderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "default") {
		t.Fatalf("recorded-IR fallback:\n%s", out)
	}
}

func TestWriteThroughAReadOnlyFsFails(t *testing.T) {
	toyOnly(t)
	base := memfs(t, "config.toy", opsSrc)
	d, err := Open(afero.NewReadOnlyFs(base), "config.toy")
	if err != nil {
		t.Fatal(err)
	}
	d.At("/host").Replace("example.com")
	wantCode(t, d.Write(), hewerr.CodeTargetPath)

	got, _ := afero.ReadFile(base, "config.toy")
	if string(got) != opsSrc {
		t.Fatalf("a failed write changed the target:\n%s", got)
	}
}

func TestWriteThroughAClosedHandleFails(t *testing.T) {
	toyOnly(t)
	fsys := memfs(t, "config.toy", opsSrc)
	f, err := fsys.OpenFile("config.toy", 0o2, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	d, err := OpenFile(f)
	if err != nil {
		t.Fatal(err)
	}
	d.At("/host").Replace("example.com")
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	wantCode(t, d.Write(), hewerr.CodeTargetPath)
}

// A latched error stops everything downstream of it: every recording method is
// a no-op, and the terminal reports the first error and no other.
func TestEveryMethodIsANoOpAfterALatchedError(t *testing.T) {
	d := opsDoc(t)
	d.At("/a/{}").Set("boom") // HEW001, latched

	s := d.At("/host")
	s.Replace("x").Set("x").Default("x").Add("x").Remove()
	s.Assert("x").AssertAbsent().AssertCount(1).AssertKind(KindScalar).AssertExhaustive()
	s.Optional().Idempotent().Anchor(AnchorFork).Surface(SurfaceTable)
	s.After("port").Before("port")

	if len(d.steps) != 0 {
		t.Fatalf("%d steps recorded after the latch", len(d.steps))
	}
	he := wantCode(t, d.err, hewerr.CodeParse)
	if he.Path != "/a/{}" {
		t.Fatalf("the latched error changed: %q", he.Path)
	}
}

// The same sweep against a DEAD selection — the one At itself refused — which
// must record nothing even before anything else has failed.
func TestADeadSelectionRecordsNothing(t *testing.T) {
	d := opsDoc(t)
	s := d.At("/a/{}") // dead
	d.err = nil        // pretend the latch was cleared: the Sel is still dead

	s.Replace("x").Set("x").Default("x").Add("x").Remove()
	s.Assert("x").AssertCount(1).AssertKind(KindScalar)
	s.Optional().After("port")
	if len(d.steps) != 0 {
		t.Fatalf("a dead selection recorded %d steps", len(d.steps))
	}
}

func TestOpenFileWithAClosedHandle(t *testing.T) {
	toyOnly(t)
	fsys := memfs(t, "config.toy", opsSrc)
	f, err := fsys.Open("config.toy")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	_, oerr := OpenFile(f)
	wantCode(t, oerr, hewerr.CodeTargetPath)
}

func TestRenderPatchSurfacesADifferFailure(t *testing.T) {
	isolate(t)
	b := toyBinding()
	b.Differ = func([]byte) (*DiffNode, error) { return nil, errors.New("toy: no tree") }
	Register(formatToy, b)
	d, err := OpenBytes("config.toy", []byte(opsSrc))
	if err != nil {
		t.Fatal(err)
	}
	d.At("/host").Replace("example.com")
	if _, rerr := d.RenderPatch(RenderOptions{}); rerr == nil {
		t.Fatal("a failing differ produced a patch")
	}
}

// failingRename is a filesystem whose rename never works — the one step of the
// commit hew cannot do in memory.
type failingRename struct{ afero.Fs }

func (failingRename) Rename(string, string) error { return errors.New("rename: nope") }

func TestWriteCleansUpWhenTheRenameFails(t *testing.T) {
	toyOnly(t)
	base := memfs(t, "config.toy", opsSrc)
	d, err := Open(failingRename{base}, "config.toy")
	if err != nil {
		t.Fatal(err)
	}
	d.At("/host").Replace("example.com")
	wantCode(t, d.Write(), hewerr.CodeTargetPath)

	names, err := afero.ReadDir(base, ".")
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0].Name() != "config.toy" {
		t.Fatalf("a failed rename left the staged file behind: %d entries", len(names))
	}
	got, _ := afero.ReadFile(base, "config.toy")
	if string(got) != opsSrc {
		t.Fatalf("target changed:\n%s", got)
	}
}

func TestDetailOfAForeignError(t *testing.T) {
	if got := detailOf(errors.New("plain")); got != "plain" {
		t.Fatalf("detailOf = %q", got)
	}
}
