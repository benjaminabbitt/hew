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

// --- target comment lines (§3, §8.2) ---------------------------------------

func parseOne(t *testing.T, src string) []Transform {
	t.Helper()
	tls, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(tls) != 1 {
		t.Fatalf("want 1 file section, got %d", len(tls))
	}
	return tls[0].Transform
}

func TestTargetCommentTextStripsEverySyntax(t *testing.T) {
	for _, c := range []struct {
		in, want string
		ok       bool
	}{
		{"// x", "x", true},
		{"//x", "x", true},
		{"//  x", " x", true},
		{"/* x */", "x", true},
		{"/**/", "", true},
		{"# x", "x", true},
		{"#x", "x", true},
		{"port: 8080", "", false},
		{"/ x", "", false},
	} {
		got, ok := targetCommentText(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("targetCommentText(%q) = %q,%v; want %q,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestCommentValueRoundTrip(t *testing.T) {
	v := commentValue("bumped by ctxloom")
	got, ok := commentTextOf(v)
	if !ok || got != "bumped by ctxloom" {
		t.Fatalf("commentTextOf(commentValue(x)) = %q,%v", got, ok)
	}
	if got, ok := commentTextOf(Value{}); ok || got != "" {
		t.Errorf("the absent value is not a comment: %q,%v", got, ok)
	}
	sc, err := ValueOf("bare")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := commentTextOf(sc); !ok || got != "bare" {
		t.Errorf("a bare scalar spells the same comment: %q,%v", got, ok)
	}
	seq, err := ValueOf([]int{1})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := commentTextOf(seq); ok {
		t.Error("a sequence is not a comment node")
	}
}

// TestAddedCommentAnchorsTheMemberBelowIt pins the lowering
// corpus/jsonc/add-with-leading-comment's transforms.hewt fixture states: two
// adds, the member placed after the comment node rather than after the
// context line above them both (§8.2).
func TestAddedCommentAnchorsTheMemberBelowIt(t *testing.T) {
	ts := parseOne(t, "hew: 1\n\n--- t.jsonc format=jsonc\n\n@@ / @@\n  port: 8080\n"+
		"+ // added by taskloom\n+ telemetry: false\n")
	if len(ts) != 3 {
		t.Fatalf("want 3 transforms, got %d: %+v", len(ts), ts)
	}
	if ts[1].Op != OpAdd || ts[1].Path.String() != "/#0" || ts[1].After.String() != "/port" {
		t.Fatalf("comment add: %+v", ts[1])
	}
	if got, ok := commentTextOf(ts[1].Value); !ok || got != "added by taskloom" {
		t.Fatalf("comment value: %q,%v", got, ok)
	}
	if ts[2].Op != OpAdd || ts[2].Path.String() != "/telemetry" || ts[2].After.String() != "/#0" {
		t.Fatalf("member add: %+v", ts[2])
	}
}

// TestRemovedLeadingCommentNeedsNoRemoveRecord pins the other half of §8.2:
// removing a member removes its leading comment, so the comment's own "-"
// line asserts but does not emit a second remove.
func TestRemovedLeadingCommentNeedsNoRemoveRecord(t *testing.T) {
	ts := parseOne(t, "hew: 1\n\n--- t.jsonc format=jsonc\n\n@@ / @@\n"+
		"- // the old opt-out\n- telemetry: false\n  port: 8080\n")
	var removes []string
	for _, tr := range ts {
		if tr.Op == OpRemove {
			removes = append(removes, tr.Path.String())
		}
	}
	if len(removes) != 1 || removes[0] != "/telemetry" {
		t.Fatalf("want exactly the member removed, got %v", removes)
	}
	if ts[0].Op != OpTest || ts[0].Path.String() != "/#0" {
		t.Fatalf("the comment line still asserts: %+v", ts[0])
	}
}

// A standalone "-" comment line — one that does NOT lead a removed member —
// keeps its own remove record.
func TestStandaloneCommentRemovalKeepsItsRecord(t *testing.T) {
	ts := parseOne(t, "hew: 1\n\n--- t.jsonc format=jsonc\n\n@@ / @@\n"+
		"- // stale note\n  port: 8080\n")
	var removes []string
	for _, tr := range ts {
		if tr.Op == OpRemove {
			removes = append(removes, tr.Path.String())
		}
	}
	if len(removes) != 1 || removes[0] != "/#0" {
		t.Fatalf("want the comment node removed, got %v", removes)
	}
}

// A leading comment above a member that is REPLACED (a "-"/"+" pair) is not
// absorbed: the member survives, so the comment's removal stands on its own.
func TestLeadingCommentAboveAReplacedMemberIsNotAbsorbed(t *testing.T) {
	ts := parseOne(t, "hew: 1\n\n--- t.jsonc format=jsonc\n\n@@ / @@\n"+
		"- // stale note\n- timeout: 30\n+ timeout: 60\n")
	var ops []string
	for _, tr := range ts {
		if tr.Op != OpTest {
			ops = append(ops, string(tr.Op)+" "+tr.Path.String())
		}
	}
	if len(ops) != 2 || ops[0] != "remove /#0" || ops[1] != "replace /timeout" {
		t.Fatalf("want the comment removed and the member replaced, got %v", ops)
	}
}
