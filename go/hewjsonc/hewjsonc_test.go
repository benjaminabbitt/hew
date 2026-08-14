package hewjsonc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/benjaminabbitt/hew/go"
	"github.com/benjaminabbitt/hew/go/internal/hewerr"
	"gopkg.in/yaml.v3"
)

// --- helpers ----------------------------------------------------------------

func corpusDir(t *testing.T, rel string) string {
	t.Helper()
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		cand := filepath.Join(root, "..", "..", "corpus", rel)
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

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return b
}

func list(ts ...hew.Transform) hew.TransformList {
	return hew.TransformList{Target: "t.jsonc", Format: hew.FormatJSONC, Transform: ts}
}

// val builds a transform value from a YAML/JSON literal.
func val(t *testing.T, text string) hew.Value {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(text), &n); err != nil || len(n.Content) == 0 {
		t.Fatalf("bad test value %q: %v", text, err)
	}
	return hew.NodeValue(n.Content[0])
}

// cval builds the IR's comment-node value, `{comment: <text>}`.
func cval(text string) hew.Value {
	return hew.NodeValue(&yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "comment"},
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: text},
	}})
}

func p(s string) hew.Path { return hew.MustParsePath(s) }

func mustApply(t *testing.T, src string, ts ...hew.Transform) string {
	t.Helper()
	got, err := Apply([]byte(src), list(ts...))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return string(got)
}

func wantApply(t *testing.T, src, want string, ts ...hew.Transform) {
	t.Helper()
	if got := mustApply(t, src, ts...); got != want {
		t.Fatalf("apply mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func mustFail(t *testing.T, src string, ts ...hew.Transform) *hewerr.Error {
	t.Helper()
	got, err := Apply([]byte(src), list(ts...))
	if err == nil {
		t.Fatalf("expected an error, got:\n%s", got)
	}
	if got != nil {
		t.Fatalf("all-or-nothing violated: bytes returned alongside %v", err)
	}
	he, ok := hewerr.As(err)
	if !ok {
		t.Fatalf("error is not *hewerr.Error: %v", err)
	}
	if he.Component != hewerr.ComponentApplier {
		t.Errorf("component: want applier, got %s", he.Component)
	}
	return he
}

func wantCode(t *testing.T, he *hewerr.Error, code hewerr.Code, path string) {
	t.Helper()
	if he.Code != code {
		t.Errorf("code: want %s, got %s (%v)", code, he.Code, he)
	}
	if path != "" && he.Path != path {
		t.Errorf("path: want %s, got %s", path, he.Path)
	}
}

// --- the corpus cases -------------------------------------------------------

func TestCorpusApplyIRAddWithLeadingComment(t *testing.T) {
	dir := corpusDir(t, "jsonc/add-with-leading-comment")
	tl, err := hew.UnmarshalTransforms(readFile(t, filepath.Join(dir, "transforms.hewt")))
	if err != nil {
		t.Fatalf("UnmarshalTransforms: %v", err)
	}
	got, err := Apply(readFile(t, filepath.Join(dir, "target.jsonc")), tl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := readFile(t, filepath.Join(dir, "expected.jsonc"))
	if string(got) != string(want) {
		t.Fatalf("apply-ir mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func e2e(t *testing.T, rel, patchName, targetName, expectedName string) {
	t.Helper()
	dir := corpusDir(t, rel)
	tls, err := hew.Parse(readFile(t, filepath.Join(dir, patchName)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(tls) != 1 {
		t.Fatalf("want 1 file section, got %d", len(tls))
	}
	got, err := Apply(readFile(t, filepath.Join(dir, targetName)), tls[0])
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := readFile(t, filepath.Join(dir, expectedName))
	if string(got) != string(want) {
		t.Fatalf("e2e(%s) mismatch\ngot:\n%s\nwant:\n%s", rel, got, want)
	}
}

func TestCorpusE2E(t *testing.T) {
	t.Run("add-with-leading-comment", func(t *testing.T) {
		e2e(t, "jsonc/add-with-leading-comment", "patch.hew", "target.jsonc", "expected.jsonc")
	})
	t.Run("delete-key-with-comment", func(t *testing.T) {
		e2e(t, "jsonc/delete-key-with-comment", "patch.hew", "target.jsonc", "expected.jsonc")
	})
	t.Run("tolerance-reformat", func(t *testing.T) {
		e2e(t, "jsonc/tolerance-reformat", "patch.hew", "target.jsonc", "expected.jsonc")
	})
	t.Run("roundtrip-basic", func(t *testing.T) {
		e2e(t, "jsonc/roundtrip-basic", "expected.hew", "old.jsonc", "new.jsonc")
	})
}

func TestCorpusAssertAbsentFail(t *testing.T) {
	dir := corpusDir(t, "jsonc/assert-absent-fail")
	tls, err := hew.Parse(readFile(t, filepath.Join(dir, "patch.hew")))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	target := readFile(t, filepath.Join(dir, "target.jsonc"))
	got, aerr := Apply(target, tls[0])
	if aerr == nil {
		t.Fatal("expected HEW011, apply succeeded")
	}
	if got != nil {
		t.Fatal("all-or-nothing violated")
	}
	he, ok := hewerr.As(aerr)
	if !ok {
		t.Fatalf("not a *hewerr.Error: %v", aerr)
	}
	if he.Code != hewerr.CodeAssertionFailed || he.Path != "/env/ANTHROPIC_API_KEY" || he.PatchLine != 6 {
		t.Fatalf("want HEW011 at /env/ANTHROPIC_API_KEY line 6, got %s %s line %d", he.Code, he.Path, he.PatchLine)
	}
	for _, sub := range []string{"assertion-failed", "absent"} {
		if !strings.Contains(aerr.Error(), sub) {
			t.Errorf("message missing %q: %s", sub, aerr)
		}
	}
}

// --- comment anchoring (§8.2) ----------------------------------------------

const anchorDoc = `{
  // free, blank line below

  // leading for a
  "a": 1, // trailing on a
  "b": 2
}
`

func TestAnchoringClassification(t *testing.T) {
	d, err := parseDoc([]byte(anchorDoc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if n := len(d.root.free); n != 1 {
		t.Fatalf("want 1 free comment, got %d", n)
	}
	if got := d.root.free[0].text; got != "free, blank line below" {
		t.Errorf("free comment text: %q", got)
	}
	a := d.root.memberNamed("a")
	if a == nil || len(a.leading) != 1 || a.leading[0].text != "leading for a" {
		t.Fatalf("a's leading comment not anchored: %+v", a)
	}
	if a.trailing == nil || a.trailing.text != "trailing on a" {
		t.Fatalf("a's trailing comment not anchored: %+v", a)
	}
	b := d.root.memberNamed("b")
	if b == nil || len(b.leading) != 0 || b.trailing != nil {
		t.Errorf("b should carry no comments: %+v", b)
	}
	// Standalone ordinals count free AND leading comments in source order,
	// which is what corpus yaml/set-scalar's /server/#0 pins.
	all := d.root.standalone()
	if len(all) != 2 || all[0].text != "free, blank line below" || all[1].text != "leading for a" {
		t.Fatalf("standalone ordinals wrong: %v", texts(all))
	}
}

func texts(cs []*comment) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.text
	}
	return out
}

func TestRemovingMemberTakesItsAttachedComments(t *testing.T) {
	want := `{
  // free, blank line below

  "b": 2
}
`
	wantApply(t, anchorDoc, want, hew.Transform{Op: hew.OpRemove, Path: p("/a")})
}

func TestRemovingMemberLeavesFreeCommentAtContainerEnd(t *testing.T) {
	src := `{
  "a": 1

  // trailing free comment
}
`
	want := `{

  // trailing free comment
}
`
	wantApply(t, src, want, hew.Transform{Op: hew.OpRemove, Path: p("/a")})
}

func TestRemovingLastMemberDropsThePrecedingComma(t *testing.T) {
	src := `{
  "a": 1,
  "b": 2
}
`
	want := `{
  "a": 1
}
`
	wantApply(t, src, want, hew.Transform{Op: hew.OpRemove, Path: p("/b")})
}

func TestCommentAddressesResolve(t *testing.T) {
	// #0 is the free comment, #1 the leading one, /a/#t the trailing one.
	wantApply(t, anchorDoc, strings.Replace(anchorDoc, "// free, blank line below", "// rewritten", 1),
		hew.Transform{Op: hew.OpReplace, Path: p("/#0"), Value: cval("rewritten")})
	wantApply(t, anchorDoc, strings.Replace(anchorDoc, "// leading for a", "// rewritten", 1),
		hew.Transform{Op: hew.OpReplace, Path: p("/#1"), Value: cval("rewritten")})
	wantApply(t, anchorDoc, strings.Replace(anchorDoc, "// trailing on a", "// rewritten", 1),
		hew.Transform{Op: hew.OpReplace, Path: p("/a/#t"), Value: cval("rewritten")})
}

func TestRemoveCommentNodes(t *testing.T) {
	// A free comment goes with its line; the blank line that made it free stays.
	wantApply(t, anchorDoc, `{

  // leading for a
  "a": 1, // trailing on a
  "b": 2
}
`, hew.Transform{Op: hew.OpRemove, Path: p("/#0")})

	// A leading comment can be removed on its own, leaving its member behind.
	wantApply(t, anchorDoc, `{
  // free, blank line below

  "a": 1, // trailing on a
  "b": 2
}
`, hew.Transform{Op: hew.OpRemove, Path: p("/#1")})

	// A trailing comment is removed with the space that separated it.
	wantApply(t, anchorDoc, `{
  // free, blank line below

  // leading for a
  "a": 1,
  "b": 2
}
`, hew.Transform{Op: hew.OpRemove, Path: p("/a/#t")})
}

func TestAddTrailingComment(t *testing.T) {
	wantApply(t, "{\n  \"a\": 1,\n  \"b\": 2\n}\n", "{\n  \"a\": 1, // why\n  \"b\": 2\n}\n",
		hew.Transform{Op: hew.OpAdd, Path: p("/a/#t"), Value: cval("why")})
}

func TestBlockCommentIsAnchoredAndAddressable(t *testing.T) {
	src := "{\n  /* leads a */\n  \"a\": 1\n}\n"
	d, err := parseDoc([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	c := d.root.standalone()
	if len(c) != 1 || c[0].text != "leads a" || !c[0].block {
		t.Fatalf("block comment mis-scanned: %v", texts(c))
	}
	wantApply(t, src, "{\n}\n", hew.Transform{Op: hew.OpRemove, Path: p("/a")})
}

func TestUnterminatedBlockCommentIsTargetParseError(t *testing.T) {
	he := mustFail(t, "{\n  /* never closed\n  \"a\": 1\n}\n", hew.Transform{Op: hew.OpRemove, Path: p("/a")})
	wantCode(t, he, hewerr.CodeTargetParse, "")
}

func TestTrailingCommasAreTolerated(t *testing.T) {
	src := "{\n  \"a\": 1,\n  \"b\": [1, 2,],\n}\n"
	want := "{\n  \"a\": 2,\n  \"b\": [1, 2,],\n}\n"
	wantApply(t, src, want, hew.Transform{Op: hew.OpReplace, Path: p("/a"), Value: val(t, "2")})
}

// --- placement --------------------------------------------------------------

func TestAddAfterAndBefore(t *testing.T) {
	src := "{\n  \"a\": 1,\n  \"c\": 3\n}\n"
	wantApply(t, src, "{\n  \"a\": 1,\n  \"b\": 2,\n  \"c\": 3\n}\n",
		hew.Transform{Op: hew.OpAdd, Path: p("/b"), Value: val(t, "2"), After: p("/a")})
	wantApply(t, src, "{\n  \"b\": 2,\n  \"a\": 1,\n  \"c\": 3\n}\n",
		hew.Transform{Op: hew.OpAdd, Path: p("/b"), Value: val(t, "2"), Before: p("/a")})
	wantApply(t, src, "{\n  \"a\": 1,\n  \"c\": 3,\n  \"b\": 2\n}\n",
		hew.Transform{Op: hew.OpAdd, Path: p("/b"), Value: val(t, "2")})
}

func TestAddBeforeAMemberTakesItsLeadingComment(t *testing.T) {
	src := "{\n  // leads b\n  \"b\": 2\n}\n"
	wantApply(t, src, "{\n  \"a\": 1,\n  // leads b\n  \"b\": 2\n}\n",
		hew.Transform{Op: hew.OpAdd, Path: p("/a"), Value: val(t, "1"), Before: p("/b")})
}

func TestAddAfterALeadingCommentLandsBetweenItAndItsMember(t *testing.T) {
	src := "{\n  // leads b\n  \"b\": 2\n}\n"
	wantApply(t, src, "{\n  // leads b\n  \"a\": 1,\n  \"b\": 2\n}\n",
		hew.Transform{Op: hew.OpAdd, Path: p("/a"), Value: val(t, "1"), After: p("/#0")})
}

func TestAddAfterAMemberWithATrailingCommentClearsTheComment(t *testing.T) {
	src := "{\n  \"a\": 1 // note\n}\n"
	wantApply(t, src, "{\n  \"a\": 1, // note\n  \"b\": 2\n}\n",
		hew.Transform{Op: hew.OpAdd, Path: p("/b"), Value: val(t, "2"), After: p("/a")})
}

func TestAddIntoEmptyContainers(t *testing.T) {
	wantApply(t, "{\n}\n", "{\n  \"a\": 1\n}\n", hew.Transform{Op: hew.OpAdd, Path: p("/a"), Value: val(t, "1")})
	wantApply(t, `{"a": {}}`, `{"a": { "b": 1 }}`, hew.Transform{Op: hew.OpAdd, Path: p("/a/b"), Value: val(t, "1")})
	wantApply(t, `{"a": []}`, `{"a": [1]}`, hew.Transform{Op: hew.OpAdd, Path: p("/a"), Value: val(t, "1")})
}

func TestFlowContainerInsertAndRemove(t *testing.T) {
	wantApply(t, `{ "a": 1 }`, `{ "a": 1, "b": 2 }`,
		hew.Transform{Op: hew.OpAdd, Path: p("/b"), Value: val(t, "2")})
	wantApply(t, `{ "a": 1, "b": 2 }`, `{ "b": 2 }`, hew.Transform{Op: hew.OpRemove, Path: p("/a")})
	wantApply(t, `{ "a": 1, "b": 2 }`, `{ "a": 1 }`, hew.Transform{Op: hew.OpRemove, Path: p("/b")})
	wantApply(t, `["x", "y"]`, `["x"]`, hew.Transform{Op: hew.OpRemove, Path: p("/1")})
}

// --- arrays -----------------------------------------------------------------

const arrDoc = `{
  "servers": [
    { "name": "a", "port": 1 },
    { "name": "b", "port": 2 }
  ]
}
`

func TestArrayAddressingAndEdits(t *testing.T) {
	wantApply(t, arrDoc, strings.Replace(arrDoc, `"port": 1 }`, `"port": 9 }`, 1),
		hew.Transform{Op: hew.OpReplace, Path: p("/servers/name=a/port"), Value: val(t, "9")})
	wantApply(t, arrDoc, strings.Replace(arrDoc, `"port": 2 }`, `"port": 9 }`, 1),
		hew.Transform{Op: hew.OpReplace, Path: p("/servers/1/port"), Value: val(t, "9")})

	got := mustApply(t, arrDoc, hew.Transform{Op: hew.OpRemove, Path: p("/servers/name=a")})
	if strings.Contains(got, `"name": "a"`) || !strings.Contains(got, `"name": "b"`) {
		t.Fatalf("keyed removal wrong:\n%s", got)
	}
	got = mustApply(t, arrDoc, hew.Transform{Op: hew.OpAdd, Path: p("/servers"),
		Value: val(t, `{name: c}`), After: p("/servers/name=a")})
	if !strings.Contains(got, "{ \"name\": \"c\" },\n    { \"name\": \"b\"") {
		t.Fatalf("element placed wrong:\n%s", got)
	}
}

func TestArrayMatchByBareValue(t *testing.T) {
	src := "{\n  \"tags\": [\"alpha\", \"beta\"]\n}\n"
	wantApply(t, src, "{\n  \"tags\": [\"alpha\"]\n}\n",
		hew.Transform{Op: hew.OpRemove, Path: p("/tags/=beta")})
}

func TestAmbiguousMatchIsHEW012(t *testing.T) {
	src := "{\n  \"tags\": [\"x\", \"x\"]\n}\n"
	he := mustFail(t, src, hew.Transform{Op: hew.OpRemove, Path: p("/tags/=x")})
	wantCode(t, he, hewerr.CodeAmbiguousMatch, "/tags/=x")
}

// --- assertions -------------------------------------------------------------

const testDoc = `{
  "env": { "A": "1", "B": "2" },
  "list": [1, 2, 3],
  "n": 8080
}
`

func TestObjectContextMatchesAsSubset(t *testing.T) {
	// §6.1: an object-valued before-image matches as a SUBSET, which is what
	// makes corpus jsonc/assert-absent-fail's `env: {}` context line pass.
	mustApply(t, testDoc, hew.Transform{Op: hew.OpTest, Path: p("/env"), Value: val(t, "{}")})
	mustApply(t, testDoc, hew.Transform{Op: hew.OpTest, Path: p("/env"), Value: val(t, `{A: "1"}`)})
	he := mustFail(t, testDoc, hew.Transform{Op: hew.OpTest, Path: p("/env"), Value: val(t, `{A: "9"}`)})
	wantCode(t, he, hewerr.CodeStaleTarget, "/env")
	he = mustFail(t, testDoc, hew.Transform{Op: hew.OpTest, Path: p("/env"), Value: val(t, `{C: "1"}`)})
	wantCode(t, he, hewerr.CodeStaleTarget, "/env")
}

func TestArrayContextMatchesAsOrderedSubsequence(t *testing.T) {
	mustApply(t, testDoc, hew.Transform{Op: hew.OpTest, Path: p("/list"), Value: val(t, "[1, 3]")})
	he := mustFail(t, testDoc, hew.Transform{Op: hew.OpTest, Path: p("/list"), Value: val(t, "[3, 1]")})
	wantCode(t, he, hewerr.CodeStaleTarget, "/list")
	he = mustFail(t, testDoc, hew.Transform{Op: hew.OpTest, Path: p("/list"), Value: val(t, "[4]")})
	wantCode(t, he, hewerr.CodeStaleTarget, "/list")
	he = mustFail(t, testDoc, hew.Transform{Op: hew.OpTest, Path: p("/n"), Value: val(t, "[1]")})
	wantCode(t, he, hewerr.CodeStaleTarget, "/n")
}

func TestScalarContextIsExactAfterNativeDecoding(t *testing.T) {
	mustApply(t, testDoc, hew.Transform{Op: hew.OpTest, Path: p("/n"), Value: val(t, "8080")})
	he := mustFail(t, testDoc, hew.Transform{Op: hew.OpTest, Path: p("/n"), Value: val(t, `"8080"`)})
	wantCode(t, he, hewerr.CodeStaleTarget, "/n")
	if he.Want != `"8080"` || he.Got != "8080" {
		t.Errorf("want/got not reported: %q / %q", he.Want, he.Got)
	}
}

func TestMissingContextNodeIsStaleTarget(t *testing.T) {
	he := mustFail(t, testDoc, hew.Transform{Op: hew.OpTest, Path: p("/nope"), Value: val(t, "1")})
	wantCode(t, he, hewerr.CodeStaleTarget, "/nope")
}

func TestAbsentAssertion(t *testing.T) {
	mustApply(t, testDoc, hew.Transform{Op: hew.OpTest, Path: p("/env/C"), Absent: true})
	he := mustFail(t, testDoc, hew.Transform{Op: hew.OpTest, Path: p("/env/A"), Absent: true})
	wantCode(t, he, hewerr.CodeAssertionFailed, "/env/A")
	if !strings.Contains(he.Error(), "absent") {
		t.Errorf("message must name the assertion: %v", he)
	}
}

func TestAbsentAssertionPropagatesAmbiguity(t *testing.T) {
	src := "{\n  \"tags\": [\"x\", \"x\"]\n}\n"
	he := mustFail(t, src, hew.Transform{Op: hew.OpTest, Path: p("/tags/=x"), Absent: true})
	wantCode(t, he, hewerr.CodeAmbiguousMatch, "/tags/=x")
}

func TestCountAndExhaustiveAssertions(t *testing.T) {
	three := 3
	two := 2
	mustApply(t, testDoc, hew.Transform{Op: hew.OpTest, Path: p("/list"), Count: &three})
	he := mustFail(t, testDoc, hew.Transform{Op: hew.OpTest, Path: p("/list"), Count: &two})
	wantCode(t, he, hewerr.CodeAssertionFailed, "/list")
	if he.Want != "2" || he.Got != "3" {
		t.Errorf("count want/got: %q / %q", he.Want, he.Got)
	}
	mustApply(t, testDoc, hew.Transform{Op: hew.OpTest, Path: p("/list"), Exhaustive: true, Count: &three})
	he = mustFail(t, testDoc, hew.Transform{Op: hew.OpTest, Path: p("/list"), Exhaustive: true, Count: &two})
	wantCode(t, he, hewerr.CodeAssertionFailed, "/list")
	if !strings.Contains(he.Error(), "exhaustive") {
		t.Errorf("message must name the assertion: %v", he)
	}
	he = mustFail(t, testDoc, hew.Transform{Op: hew.OpTest, Path: p("/n"), Count: &two})
	wantCode(t, he, hewerr.CodeAssertionFailed, "/n")
}

func TestKindAssertion(t *testing.T) {
	seq, mapk, scalar := hew.KindSeq, hew.KindMap, hew.KindScalar
	mustApply(t, testDoc, hew.Transform{Op: hew.OpTest, Path: p("/list"), NodeKind: &seq})
	mustApply(t, testDoc, hew.Transform{Op: hew.OpTest, Path: p("/env"), NodeKind: &mapk})
	mustApply(t, testDoc, hew.Transform{Op: hew.OpTest, Path: p("/n"), NodeKind: &scalar})
	he := mustFail(t, testDoc, hew.Transform{Op: hew.OpTest, Path: p("/list"), NodeKind: &mapk})
	wantCode(t, he, hewerr.CodeAssertionFailed, "/list")
	if he.Want != "map" || he.Got != "seq" {
		t.Errorf("kind want/got: %q / %q", he.Want, he.Got)
	}
}

func TestCommentAssertionsAreTextMatched(t *testing.T) {
	mustApply(t, anchorDoc, hew.Transform{Op: hew.OpTest, Path: p("/#0"), Value: cval("free, blank line below")})
	// The ordinal a lowered hunk carries counts only the comments the patch
	// listed, so an assertion that states the text finds it wherever it sits
	// — the tolerance corpus/jsonc/delete-key-with-comment needs.
	mustApply(t, anchorDoc, hew.Transform{Op: hew.OpTest, Path: p("/#0"), Value: cval("leading for a")})
	mustApply(t, anchorDoc, hew.Transform{Op: hew.OpTest, Path: p("/a/#t"), Value: cval("trailing on a")})
	he := mustFail(t, anchorDoc, hew.Transform{Op: hew.OpTest, Path: p("/#0"), Value: cval("not there")})
	wantCode(t, he, hewerr.CodeStaleTarget, "/#0")
	if he.Got != "free, blank line below" {
		t.Errorf("got should name the comment actually found: %q", he.Got)
	}
}

func TestCommentAssertionWithoutACommentValue(t *testing.T) {
	he := mustFail(t, anchorDoc, hew.Transform{Op: hew.OpTest, Path: p("/#0"), Value: val(t, "[1]")})
	wantCode(t, he, hewerr.CodeStaleTarget, "/#0")
}

func TestCommentOrdinalOutOfRange(t *testing.T) {
	he := mustFail(t, anchorDoc, hew.Transform{Op: hew.OpRemove, Path: p("/#7")})
	wantCode(t, he, hewerr.CodeNoMatch, "/#7")
}

func TestTrailingCommentAddressMissing(t *testing.T) {
	he := mustFail(t, anchorDoc, hew.Transform{Op: hew.OpRemove, Path: p("/b/#t")})
	wantCode(t, he, hewerr.CodeNoMatch, "/b/#t")
}

func TestCommentAddressNeedsAContainer(t *testing.T) {
	he := mustFail(t, testDoc, hew.Transform{Op: hew.OpRemove, Path: p("/n/#0")})
	wantCode(t, he, hewerr.CodeNoMatch, "/n/#0")
}

func TestCommentNodeHasNoChildren(t *testing.T) {
	he := mustFail(t, anchorDoc, hew.Transform{Op: hew.OpRemove, Path: p("/#0/x")})
	wantCode(t, he, hewerr.CodeNoMatch, "/#0/x")
}

// --- add semantics ----------------------------------------------------------

func TestAddOntoAnExistingKeyIsHEW014(t *testing.T) {
	he := mustFail(t, testDoc, hew.Transform{Op: hew.OpAdd, Path: p("/n"), Value: val(t, "1")})
	wantCode(t, he, hewerr.CodeAlreadyExists, "/n")
}

func TestAddSemanticsVariants(t *testing.T) {
	// ! default keeps what is there; ! upsert replaces it; ! idempotent
	// accepts a value that already matches.
	wantApply(t, testDoc, testDoc,
		hew.Transform{Op: hew.OpAdd, Path: p("/n"), Value: val(t, "1"), OnConflict: hew.ConflictKeep})
	wantApply(t, testDoc, strings.Replace(testDoc, "8080", "1", 1),
		hew.Transform{Op: hew.OpAdd, Path: p("/n"), Value: val(t, "1"), OnConflict: hew.ConflictReplace})
	wantApply(t, testDoc, testDoc,
		hew.Transform{Op: hew.OpAdd, Path: p("/n"), Value: val(t, "8080"), Idempotent: true})
	he := mustFail(t, testDoc, hew.Transform{Op: hew.OpAdd, Path: p("/n"), Value: val(t, "1"), Idempotent: true})
	wantCode(t, he, hewerr.CodeAlreadyExists, "/n")
}

func TestAddIntoAMissingParent(t *testing.T) {
	he := mustFail(t, testDoc, hew.Transform{Op: hew.OpAdd, Path: p("/nope/x"), Value: val(t, "1")})
	wantCode(t, he, hewerr.CodeNoMatch, "/nope")
}

func TestAddUnderAScalarParent(t *testing.T) {
	he := mustFail(t, testDoc, hew.Transform{Op: hew.OpAdd, Path: p("/n/x"), Value: val(t, "1")})
	wantCode(t, he, hewerr.CodeInexpressible, "/n/x")
}

func TestAddAtTheRootFindsTheRootAlreadyThere(t *testing.T) {
	he := mustFail(t, testDoc, hew.Transform{Op: hew.OpAdd, Path: p("/"), Value: val(t, "1")})
	wantCode(t, he, hewerr.CodeAlreadyExists, "/")
}

func TestAddIntoAnArrayRootAppends(t *testing.T) {
	wantApply(t, "[\n  1\n]\n", "[\n  1,\n  2\n]\n", hew.Transform{Op: hew.OpAdd, Path: p("/"), Value: val(t, "2")})
}

func TestCommentAddNeedsACommentValue(t *testing.T) {
	he := mustFail(t, testDoc, hew.Transform{Op: hew.OpAdd, Path: p("/#0"), Value: val(t, "[1, 2]")})
	wantCode(t, he, hewerr.CodeInexpressible, "/#0")
}

func TestTrailingCommentAddNeedsAMember(t *testing.T) {
	he := mustFail(t, testDoc, hew.Transform{Op: hew.OpAdd, Path: p("/#t"), Value: cval("x")})
	wantCode(t, he, hewerr.CodeInexpressible, "/#t")
}

// --- remove / replace / copy ------------------------------------------------

func TestRemoveMissingNode(t *testing.T) {
	he := mustFail(t, testDoc, hew.Transform{Op: hew.OpRemove, Path: p("/nope")})
	wantCode(t, he, hewerr.CodeNoMatch, "/nope")
	wantApply(t, testDoc, testDoc, hew.Transform{Op: hew.OpRemove, Path: p("/nope"), Optional: true})
}

func TestRemoveTheRootIsInexpressible(t *testing.T) {
	he := mustFail(t, testDoc, hew.Transform{Op: hew.OpRemove, Path: p("/")})
	wantCode(t, he, hewerr.CodeInexpressible, "/")
}

func TestReplaceRequiresTheNode(t *testing.T) {
	he := mustFail(t, testDoc, hew.Transform{Op: hew.OpReplace, Path: p("/nope"), Value: val(t, "1")})
	wantCode(t, he, hewerr.CodeNoMatch, "/nope")
	if !strings.Contains(he.Error(), "replace requires the node to exist") {
		t.Errorf("detail not set: %v", he)
	}
}

func TestReplaceCommentNeedsACommentValue(t *testing.T) {
	he := mustFail(t, anchorDoc, hew.Transform{Op: hew.OpReplace, Path: p("/#0"), Value: val(t, "[1]")})
	wantCode(t, he, hewerr.CodeInexpressible, "/#0")
}

func TestCopyTakesSourceBytesVerbatim(t *testing.T) {
	src := "{\n  \"a\": {   \"deep\": 1   },\n  \"b\": {}\n}\n"
	want := "{\n  \"a\": {   \"deep\": 1   },\n  \"b\": { \"copy\": {   \"deep\": 1   } }\n}\n"
	wantApply(t, src, want, hew.Transform{Op: hew.OpCopy, Path: p("/b/copy"), From: p("/a")})
}

func TestCopyOfACommentIsRefused(t *testing.T) {
	he := mustFail(t, anchorDoc, hew.Transform{Op: hew.OpCopy, Path: p("/x"), From: p("/#0")})
	wantCode(t, he, hewerr.CodeInexpressible, "/#0")
}

func TestCopyFromAMissingNode(t *testing.T) {
	he := mustFail(t, testDoc, hew.Transform{Op: hew.OpCopy, Path: p("/x"), From: p("/nope")})
	wantCode(t, he, hewerr.CodeNoMatch, "/nope")
}

func TestUnknownOpIsInexpressible(t *testing.T) {
	he := mustFail(t, testDoc, hew.Transform{Op: hew.OpKind("frobnicate"), Path: p("/n")})
	wantCode(t, he, hewerr.CodeInexpressible, "/n")
}

// --- byte preservation and atomicity ---------------------------------------

func TestUntouchedBytesSurviveExactly(t *testing.T) {
	src := "{\n\t\"big\":   12345678901234567890,\n\t// keep me\n\t\"s\": \"x\\ny\",\n\t\"t\": 1\n}\n"
	got := mustApply(t, src, hew.Transform{Op: hew.OpReplace, Path: p("/t"), Value: val(t, "2")})
	want := strings.Replace(src, "\"t\": 1", "\"t\": 2", 1)
	if got != want {
		t.Fatalf("byte preservation broken\ngot:\n%q\nwant:\n%q", got, want)
	}
}

func TestEveryTestRunsBeforeAnyMutation(t *testing.T) {
	// The failing assertion is LAST in the list; nothing may have been
	// written by the adds that precede it (§9.0, §10.5).
	he := mustFail(t, testDoc,
		hew.Transform{Op: hew.OpAdd, Path: p("/x"), Value: val(t, "1")},
		hew.Transform{Op: hew.OpTest, Path: p("/n"), Value: val(t, "1")})
	wantCode(t, he, hewerr.CodeStaleTarget, "/n")
}

func TestSecondMutationSeesTheFirst(t *testing.T) {
	// The comment does not exist in the bytes the first transform was planned
	// against; the member placed after it can only resolve post-edit.
	wantApply(t, "{\n  \"a\": 1\n}\n", "{\n  \"a\": 1,\n  // note\n  \"b\": 2\n}\n",
		hew.Transform{Op: hew.OpAdd, Path: p("/#0"), Value: cval("note"), After: p("/a")},
		hew.Transform{Op: hew.OpAdd, Path: p("/b"), Value: val(t, "2"), After: p("/#0")})
}

func TestNoMutationsLeavesTheTargetAlone(t *testing.T) {
	wantApply(t, testDoc, testDoc, hew.Transform{Op: hew.OpTest, Path: p("/n"), Value: val(t, "8080")})
}

func TestMalformedTargetIsHEW002(t *testing.T) {
	for _, bad := range []string{"", "{", "{\"a\" 1}", "{\"a\": }", "[1 2]", "{} extra", "{\"a\": 1", "{'a': 1}"} {
		got, err := Apply([]byte(bad), list(hew.Transform{Op: hew.OpRemove, Path: p("/a")}))
		if err == nil {
			t.Fatalf("target %q should not parse, got %q", bad, got)
		}
		he, ok := hewerr.As(err)
		if !ok || he.Code != hewerr.CodeTargetParse {
			t.Fatalf("target %q: want HEW002, got %v", bad, err)
		}
	}
}

func TestScalarLiteralsRoundTrip(t *testing.T) {
	src := "{\n  \"a\": [-1.5e10, true, false, null, \"s\"],\n  \"b\": 0\n}\n"
	mustApply(t, src, hew.Transform{Op: hew.OpTest, Path: p("/a"),
		Value: val(t, `[-1.5e10, true, false, null, "s"]`)})
}

func TestEncodingHouseStyle(t *testing.T) {
	wantApply(t, "{\n  \"a\": 1\n}\n", "{\n  \"a\": 1,\n  \"b\": { \"x\": [1, 2], \"y\": {}, \"z\": [] }\n}\n",
		hew.Transform{Op: hew.OpAdd, Path: p("/b"), Value: val(t, `{x: [1, 2], y: {}, z: []}`)})
}

func TestOverlappingEditsAreHEW030(t *testing.T) {
	if _, err := applyEdits([]byte("abcdef"), []edit{{0, 3, "X"}, {2, 5, "Y"}}); err == nil {
		t.Fatal("overlapping edits must be refused")
	} else if he, ok := hewerr.As(err); !ok || he.Code != hewerr.CodeConflict {
		t.Fatalf("want HEW030, got %v", err)
	}
}

// --- scanner units ----------------------------------------------------------

func TestSplitTriviaBoundaries(t *testing.T) {
	mk := func(nls ...int) []triviaItem {
		out := make([]triviaItem, len(nls))
		for i, n := range nls {
			out[i] = triviaItem{c: &comment{}, nlBefore: n}
		}
		return out
	}
	free, lead := splitTrivia(nil, 1)
	if free != nil || lead != nil {
		t.Error("no comments means no attachment")
	}
	free, lead = splitTrivia(mk(1), 2)
	if len(free) != 1 || len(lead) != 0 {
		t.Error("a blank line before the member frees the comment")
	}
	free, lead = splitTrivia(mk(1, 1), 1)
	if len(free) != 0 || len(lead) != 2 {
		t.Error("an unbroken run is all leading")
	}
	free, lead = splitTrivia(mk(1, 2, 1), 1)
	if len(free) != 1 || len(lead) != 2 {
		t.Errorf("a blank line inside the run splits it: %d free, %d lead", len(free), len(lead))
	}
}

func TestCommentTextStripping(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"// x", "x"}, {"//x", "x"}, {"//  x", " x"}, {"// x   ", "x"}, {"//", ""},
		{"/* x */", "x"}, {"/*x*/", "x"},
	} {
		cm, _, err := scanComment([]byte(c.in), 0)
		if err != nil {
			t.Fatalf("%q: %v", c.in, err)
		}
		if cm.text != c.want {
			t.Errorf("%q: text %q, want %q", c.in, cm.text, c.want)
		}
	}
}

func TestSlotsAreInSourceOrder(t *testing.T) {
	d, err := parseDoc([]byte(anchorDoc))
	if err != nil {
		t.Fatal(err)
	}
	slots := d.root.slots()
	if len(slots) != 3 {
		t.Fatalf("want 3 slots, got %d", len(slots))
	}
	for i := 1; i < len(slots); i++ {
		if slots[i-1].start >= slots[i].start {
			t.Fatalf("slots out of order at %d", i)
		}
	}
	if slots[0].item() || slots[0].comment == nil {
		t.Error("slot 0 is the free comment, which takes no separator comma")
	}
	if !slots[1].item() || slots[1].member == nil {
		t.Error("slot 1 is member a")
	}
	if slots[1].insertAfterPos() <= slots[1].end-1 {
		t.Error("insertAfterPos must clear the trailing comment and comma")
	}
}

func TestLineStartAndLineEndOnlyEatWhitespace(t *testing.T) {
	src := []byte("x  // c  \ny")
	if got := lineStart(src, 3); got != 3 {
		t.Errorf("lineStart must not cross non-whitespace: %d", got)
	}
	if got := lineEnd(src, 7); got != 10 {
		t.Errorf("lineEnd should take the newline: %d", got)
	}
	if got := lineEnd(src, 1); got != 1 {
		t.Errorf("lineEnd must stop at non-whitespace: %d", got)
	}
	if got := lineEnd([]byte("ab  "), 2); got != 2 {
		t.Errorf("lineEnd at EOF with no newline: %d", got)
	}
}

func TestArrayElementsCarryComments(t *testing.T) {
	src := "[\n  // leads one\n  1, // trails one\n  2\n]\n"
	d, err := parseDoc([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(d.root.elems) != 2 {
		t.Fatalf("want 2 elements, got %d", len(d.root.elems))
	}
	e := d.root.elems[0]
	if len(e.leading) != 1 || e.trailing == nil {
		t.Fatalf("element comments not anchored: %+v", e)
	}
	// Removing the element takes both comments with it.
	got, err := Apply([]byte(src), list(hew.Transform{Op: hew.OpRemove, Path: p("/0")}))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "[\n  2\n]\n" {
		t.Fatalf("element removal:\n%q", got)
	}
	// And its trailing comment is addressable.
	got, err = Apply([]byte(src), list(hew.Transform{Op: hew.OpReplace, Path: p("/0/#t"), Value: cval("new")}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "1, // new") {
		t.Fatalf("element trailing comment:\n%s", got)
	}
}

func TestTopLevelCommentsArePreserved(t *testing.T) {
	src := "// header\n{\n  \"a\": 1\n}\n// footer\n"
	got := mustApply(t, src, hew.Transform{Op: hew.OpReplace, Path: p("/a"), Value: val(t, "2")})
	if got != strings.Replace(src, "\"a\": 1", "\"a\": 2", 1) {
		t.Fatalf("top-level comments lost:\n%s", got)
	}
}
