package hew

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hew-format/hew/internal/hewerr"
)

// corpusDir resolves a corpus case's directory, walking up from the package
// directory to find the repo root — the same discovery the conformance
// harness uses.
func corpusDir(t *testing.T, rel string) string {
	t.Helper()
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		cand := filepath.Join(root, "corpus", rel)
		if st, err := os.Stat(cand); err == nil && st.IsDir() {
			return cand
		}
		parent := filepath.Dir(root)
		if parent == root {
			t.Fatalf("could not find corpus/%s above %s", rel, root)
		}
		root = parent
	}
}

// corpusCase loads patch.hew and (if present) transforms.hewt from a corpus
// case directory.
func corpusCase(t *testing.T, rel string) (patch, transforms []byte, dir string) {
	t.Helper()
	dir = corpusDir(t, rel)
	var err error
	patch, err = os.ReadFile(filepath.Join(dir, "patch.hew"))
	if err != nil {
		t.Fatalf("reading patch.hew: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "transforms.hewt")); err == nil {
		transforms = b
	}
	return patch, transforms, dir
}

func assertParsesTo(t *testing.T, rel string) {
	t.Helper()
	patch, fixture, _ := corpusCase(t, rel)
	if fixture == nil {
		t.Fatalf("%s has no transforms.hewt fixture", rel)
	}
	tls, err := Parse(patch)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(tls) != 1 {
		t.Fatalf("want 1 file section, got %d", len(tls))
	}
	want, err := UnmarshalTransforms(fixture)
	if err != nil {
		t.Fatalf("fixture UnmarshalTransforms: %v", err)
	}
	got := tls[0]
	if !got.Equal(want) {
		gotBytes, _ := MarshalTransforms(got)
		t.Fatalf("parsed IR != fixture (%s)\ngot:\n%s\nwant:\n%s", rel, gotBytes, fixture)
	}
}

func TestParseAgainstCorpusFixtures(t *testing.T) {
	cases := []string{
		"json/add-key",
		"json/set-scalar",
		"json/array-remove-element",
		"json/keyed-array-add",
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) { assertParsesTo(t, c) })
	}
}

func TestParseWholeFileReplace(t *testing.T) {
	patch, _, _ := corpusCase(t, "json/whole-file-replace")
	tls, err := Parse(patch)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(tls) != 1 {
		t.Fatalf("want 1 file section, got %d", len(tls))
	}
	tl := tls[0]
	var replaces, adds, tests int
	for _, tr := range tl.Transform {
		switch tr.Op {
		case OpReplace:
			replaces++
		case OpAdd:
			adds++
		case OpTest:
			tests++
		}
	}
	if replaces != 2 {
		t.Errorf("want 2 replace ops (generated, version), got %d", replaces)
	}
	if adds != 1 {
		t.Errorf("want 1 add op (agents), got %d", adds)
	}
	if tests != 3 {
		t.Errorf("want 3 test ops (2 field tests from the '-' lines, plus 1 exhaustive count test), got %d", tests)
	}
}

func TestParseExhaustiveFail(t *testing.T) {
	patch, _, _ := corpusCase(t, "json/assert-exhaustive-fail")
	tls, err := Parse(patch)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var found bool
	for _, tr := range tls[0].Transform {
		if tr.Exhaustive {
			found = true
			if tr.Count == nil || *tr.Count != 2 {
				t.Fatalf("exhaustive count: want 2, got %v", tr.Count)
			}
			if tr.Path.String() != "/permissions" {
				t.Fatalf("exhaustive path: want /permissions, got %s", tr.Path)
			}
		}
	}
	if !found {
		t.Fatal("no exhaustive transform emitted")
	}
}

func TestParseAssertKind(t *testing.T) {
	patch, _, _ := corpusCase(t, "json/assert-kind-fail")
	tls, err := Parse(patch)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var found bool
	for _, tr := range tls[0].Transform {
		if tr.NodeKind != nil {
			found = true
			if *tr.NodeKind != KindMap {
				t.Fatalf("kind: want map, got %s", *tr.NodeKind)
			}
			if tr.Path.String() != "/mcpServers" {
				t.Fatalf("kind path: want /mcpServers, got %s", tr.Path)
			}
		}
	}
	if !found {
		t.Fatal("no kind assertion emitted")
	}
}

func TestParseCommentAdd(t *testing.T) {
	patch, _, _ := corpusCase(t, "json/comment-inexpressible")
	tls, err := Parse(patch)
	if err != nil {
		t.Fatalf("Parse should succeed for a comment add (the applier refuses it, not the parser): %v", err)
	}
	var found bool
	for _, tr := range tls[0].Transform {
		if tr.Op == OpAdd && tr.Path.Len() > 0 && tr.Path.Segment(tr.Path.Len()-1).Kind == SegComment {
			found = true
		}
	}
	if !found {
		t.Fatal("expected an add transform addressing a comment node")
	}
}

func TestParseEmptyPatchIsError(t *testing.T) {
	if _, err := Parse([]byte("")); err == nil {
		t.Fatal("empty patch must be HEW001 (§10.2)")
	}
	he, ok := hewerr.As(mustErr(t, []byte("")))
	if !ok || he.Code != hewerr.CodeParse {
		t.Fatalf("want HEW001, got %v", he)
	}
}

func TestParseMissingHewDirectiveIsError(t *testing.T) {
	_, err := Parse([]byte("--- t.json format=json\n\n@@ / @@\n  a: 1\n"))
	if err == nil {
		t.Fatal(`missing "hew:" must be HEW001`)
	}
}

func TestParseUnknownMarginIsError(t *testing.T) {
	src := []byte("hew: 1\n\n--- t.json format=json\n\n@@ /a @@\n* bogus\n")
	_, err := Parse(src)
	if err == nil {
		t.Fatal("unknown margin character must be HEW001 (§3)")
	}
}

func TestParseFileSectionWithNoHunksIsError(t *testing.T) {
	src := []byte("hew: 1\n\n--- t.json format=json\n")
	if _, err := Parse(src); err == nil {
		t.Fatal("a file section with no hunks must be HEW001 (§10.2)")
	}
}

func TestParseMultipleFileSections(t *testing.T) {
	src := []byte("hew: 1\n\n--- a.json format=json\n\n@@ / @@\n  x: 1\n+ y: 2\n\n" +
		"--- b.json format=json\n\n@@ / @@\n  p: 1\n+ q: 2\n")
	tls, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(tls) != 2 {
		t.Fatalf("want 2 file sections, got %d", len(tls))
	}
	if tls[0].Target != "a.json" || tls[1].Target != "b.json" {
		t.Fatalf("targets: %q, %q", tls[0].Target, tls[1].Target)
	}
}

func mustErr(t *testing.T, src []byte) error {
	t.Helper()
	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected an error")
	}
	return err
}
