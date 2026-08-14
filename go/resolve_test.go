package hew

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

// --- a Document over yaml.Node, so Resolve is exercised independently of any
// format binding ------------------------------------------------------------

type ydoc struct{ root *yaml.Node }

func (d ydoc) Root() Node {
	if d.root == nil {
		return nil
	}
	return ynode{d.root}
}

type ynode struct{ n *yaml.Node }

func (y ynode) Kind() NodeKind {
	switch y.n.Kind {
	case yaml.MappingNode:
		return KindMap
	case yaml.SequenceNode:
		return KindSeq
	default:
		return KindScalar
	}
}

func (y ynode) Member(name string) (Node, bool) {
	if y.n.Kind != yaml.MappingNode {
		return nil, false
	}
	for i := 0; i+1 < len(y.n.Content); i += 2 {
		if y.n.Content[i].Value == name {
			return ynode{y.n.Content[i+1]}, true
		}
	}
	return nil, false
}

func (y ynode) Len() int {
	switch y.n.Kind {
	case yaml.SequenceNode:
		return len(y.n.Content)
	case yaml.MappingNode:
		return len(y.n.Content) / 2
	default:
		return 0
	}
}

func (y ynode) Elem(i int) (Node, bool) {
	if y.n.Kind != yaml.SequenceNode || i < 0 || i >= len(y.n.Content) {
		return nil, false
	}
	return ynode{y.n.Content[i]}, true
}

func (y ynode) Value() Value { return NodeValue(y.n) }

func mustDoc(t *testing.T, src string) Document {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(src), &n); err != nil {
		t.Fatalf("fixture does not parse: %v", err)
	}
	if len(n.Content) == 0 {
		t.Fatal("fixture is empty")
	}
	return ydoc{n.Content[0]}
}

const servers = `
servers:
  - name: github
    command: npx
  - name: local
    command: node
tags: [a, b]
server:
  host: localhost
  port: 8080
`

func list(t *testing.T, tl TransformList, doc Document) []ResolvedOp {
	t.Helper()
	ops, err := Resolve(tl, doc)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return ops
}

func tlOf(ts ...Transform) TransformList {
	return TransformList{Target: "target.json", Format: FormatJSON, Transform: ts}
}

func pathsOf(ops []ResolvedOp) []string {
	out := make([]string, len(ops))
	for i, op := range ops {
		out[i] = string(op.Op) + " " + op.Path
	}
	return out
}

func wantPaths(t *testing.T, ops []ResolvedOp, want ...string) {
	t.Helper()
	got := pathsOf(ops)
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("op %d: got %q, want %q (all: %v)", i, got[i], want[i], got)
		}
	}
}

func mustCode(t *testing.T, err error, code hewerr.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("want %s, got no error", code)
	}
	he, ok := hewerr.As(err)
	if !ok {
		t.Fatalf("want a *hewerr.Error, got %T: %v", err, err)
	}
	if he.Code != code {
		t.Fatalf("want %s, got %s (%v)", code, he.Code, err)
	}
	if he.Component != hewerr.ComponentApplier {
		t.Fatalf("resolution errors are the applier's: got component %v", he.Component)
	}
}

func resolveErrOf(t *testing.T, tl TransformList, doc Document) error {
	t.Helper()
	ops, err := Resolve(tl, doc)
	if err == nil {
		t.Fatalf("want an error, got %v", pathsOf(ops))
	}
	return err
}

// --- key-match projection ---------------------------------------------------

func TestResolveKeyMatchBecomesIndex(t *testing.T) {
	doc := mustDoc(t, servers)
	ops := list(t, tlOf(
		Transform{Op: OpTest, Path: MustParsePath("/servers/name=github/command"), Value: mustVal(t, "npx")},
		Transform{Op: OpReplace, Path: MustParsePath("/servers/name=github/command"), Value: mustVal(t, "npx-18")},
		Transform{Op: OpRemove, Path: MustParsePath("/servers/name=local")},
	), doc)
	wantPaths(t, ops,
		"test /servers/0/command",
		"replace /servers/0/command",
		"remove /servers/1",
	)
	if ops[0].Value.String() != "npx" {
		t.Fatalf("value not carried through: %q", ops[0].Value.String())
	}
}

func TestResolveBareValueMatch(t *testing.T) {
	doc := mustDoc(t, servers)
	ops := list(t, tlOf(Transform{Op: OpRemove, Path: MustParsePath("/tags/=b")}), doc)
	wantPaths(t, ops, "remove /tags/1")
}

func TestResolveMatchNoElement(t *testing.T) {
	doc := mustDoc(t, servers)
	err := resolveErrOf(t, tlOf(Transform{Op: OpRemove, Path: MustParsePath("/servers/name=nope")}), doc)
	mustCode(t, err, hewerr.CodeNoMatch)
	if he, _ := hewerr.As(err); he.Path != "/servers/name=nope" {
		t.Fatalf("error should name the failing prefix, got %q", he.Path)
	}
}

func TestResolveMatchAmbiguous(t *testing.T) {
	doc := mustDoc(t, "servers: [{name: a}, {name: a}]")
	err := resolveErrOf(t, tlOf(Transform{Op: OpRemove, Path: MustParsePath("/servers/name=a")}), doc)
	mustCode(t, err, hewerr.CodeAmbiguousMatch)
}

func TestResolveMatchOrdinalSelects(t *testing.T) {
	doc := mustDoc(t, "servers: [{name: a}, {name: b}, {name: a}]")
	ops := list(t, tlOf(Transform{Op: OpRemove, Path: MustParsePath("/servers/name=a[1]")}), doc)
	wantPaths(t, ops, "remove /servers/2")
}

func TestResolveMatchOrdinalOutOfRange(t *testing.T) {
	doc := mustDoc(t, "servers: [{name: a}, {name: b}]")
	err := resolveErrOf(t, tlOf(Transform{Op: OpRemove, Path: MustParsePath("/servers/name=a[1]")}), doc)
	mustCode(t, err, hewerr.CodeNoMatch)
	if !strings.Contains(err.Error(), "ordinal 1 selects nothing among 1 elements") {
		t.Fatalf("message should count the matches: %v", err)
	}
}

func TestResolveMatchOnNonSequence(t *testing.T) {
	doc := mustDoc(t, servers)
	err := resolveErrOf(t, tlOf(Transform{Op: OpRemove, Path: MustParsePath("/server/name=x")}), doc)
	mustCode(t, err, hewerr.CodeNoMatch)
	if !strings.Contains(err.Error(), "not a sequence") {
		t.Fatalf("message: %v", err)
	}
}

func TestResolveMatchSkipsNonMapAndMissingField(t *testing.T) {
	doc := mustDoc(t, "servers: [3, {other: x}, {name: a}]")
	ops := list(t, tlOf(Transform{Op: OpRemove, Path: MustParsePath("/servers/name=a")}), doc)
	wantPaths(t, ops, "remove /servers/2")
}

// --- plain addressing -------------------------------------------------------

func TestResolveRootPointerIsEmptyString(t *testing.T) {
	doc := mustDoc(t, servers)
	ops := list(t, tlOf(Transform{Op: OpTest, Path: RootPath(), NodeKind: kindPtr(KindMap)}), doc)
	if ops[0].Path != "" {
		t.Fatalf("RFC 6901 spells the whole document \"\", got %q", ops[0].Path)
	}
	if ops[0].NodeKind == nil || *ops[0].NodeKind != KindMap {
		t.Fatal("kind assertion must survive resolution")
	}
}

func TestResolveIndexSegment(t *testing.T) {
	doc := mustDoc(t, servers)
	ops := list(t, tlOf(Transform{Op: OpRemove, Path: MustParsePath("/tags/1")}), doc)
	wantPaths(t, ops, "remove /tags/1")
}

func TestResolveIndexOutOfRange(t *testing.T) {
	doc := mustDoc(t, servers)
	err := resolveErrOf(t, tlOf(Transform{Op: OpRemove, Path: MustParsePath("/tags/9")}), doc)
	mustCode(t, err, hewerr.CodeNoMatch)
	if !strings.Contains(err.Error(), "index 9 out of range") {
		t.Fatalf("message: %v", err)
	}
}

func TestResolveIndexOnNonSequence(t *testing.T) {
	doc := mustDoc(t, servers)
	err := resolveErrOf(t, tlOf(Transform{Op: OpRemove, Path: MustParsePath("/server/0")}), doc)
	mustCode(t, err, hewerr.CodeNoMatch)
}

func TestResolveMissingKey(t *testing.T) {
	doc := mustDoc(t, servers)
	err := resolveErrOf(t, tlOf(Transform{Op: OpRemove, Path: MustParsePath("/server/nope")}), doc)
	mustCode(t, err, hewerr.CodeNoMatch)
	if !strings.Contains(err.Error(), `no key "nope"`) {
		t.Fatalf("message: %v", err)
	}
}

func TestResolveKeyOnNonMap(t *testing.T) {
	doc := mustDoc(t, servers)
	err := resolveErrOf(t, tlOf(Transform{Op: OpRemove, Path: MustParsePath("/tags/x")}), doc)
	mustCode(t, err, hewerr.CodeNoMatch)
	if !strings.Contains(err.Error(), "not a map") {
		t.Fatalf("message: %v", err)
	}
}

func TestResolveEscapesPointerTokens(t *testing.T) {
	doc := mustDoc(t, `{"a/b": {"c~d": 1}}`)
	ops := list(t, tlOf(Transform{Op: OpRemove, Path: MustParsePath("/a~1b/c~0d")}), doc)
	wantPaths(t, ops, "remove /a~1b/c~0d")
}

func TestEscapePointerLeavesEqualsAlone(t *testing.T) {
	if got := escapePointer("a=b"); got != "a=b" {
		t.Fatalf(`"~2" is a path spelling, not a pointer one: got %q`, got)
	}
	if got := escapePointer("plain"); got != "plain" {
		t.Fatalf("got %q", got)
	}
	if got := escapePointer("a/b~c"); got != "a~1b~0c" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveAppendSegmentBecomesConcreteIndex(t *testing.T) {
	doc := mustDoc(t, servers)
	ops := list(t, tlOf(Transform{Op: OpTest, Path: MustParsePath("/tags/-"), Absent: true}), doc)
	wantPaths(t, ops, "test /tags/2")
	if !ops[0].Absent {
		t.Fatal("absent must survive resolution")
	}
}

func TestResolveAppendMidPathHasNoChildren(t *testing.T) {
	doc := mustDoc(t, servers)
	err := resolveErrOf(t, tlOf(Transform{Op: OpRemove, Path: MustParsePath("/tags/-/x")}), doc)
	mustCode(t, err, hewerr.CodeNoMatch)
	if !strings.Contains(err.Error(), "no children to descend into") {
		t.Fatalf("message: %v", err)
	}
}

func TestResolveAppendOnNonSequence(t *testing.T) {
	doc := mustDoc(t, servers)
	err := resolveErrOf(t, tlOf(Transform{Op: OpRemove, Path: MustParsePath("/server/-")}), doc)
	mustCode(t, err, hewerr.CodeNoMatch)
}

// --- addressing forms with no RFC 6901 projection ---------------------------

func TestResolveInexpressibleSegments(t *testing.T) {
	doc := mustDoc(t, servers)
	for _, p := range []string{`/"aws"`, "/## Install", "/code:0", "/@managed", "/#0"} {
		err := resolveErrOf(t, tlOf(Transform{Op: OpRemove, Path: MustParsePath(p)}), doc)
		mustCode(t, err, hewerr.CodeInexpressible)
		if !strings.Contains(err.Error(), "no RFC 6901 representation") {
			t.Fatalf("%s: message: %v", p, err)
		}
	}
}

func TestResolveOrdinalOnNonMatchSegment(t *testing.T) {
	doc := mustDoc(t, servers)
	err := resolveErrOf(t, tlOf(Transform{Op: OpRemove, Path: MustParsePath("/server[0]")}), doc)
	mustCode(t, err, hewerr.CodeInexpressible)
	if !strings.Contains(err.Error(), "ordinal selector on a key segment") {
		t.Fatalf("message: %v", err)
	}
}

func TestResolveRejectsRelativeAndMissingPaths(t *testing.T) {
	doc := mustDoc(t, servers)
	rel := NewRelativePath(Segment{Kind: SegKey, Name: "port"})
	err := resolveErrOf(t, tlOf(Transform{Op: OpRemove, Path: rel}), doc)
	mustCode(t, err, hewerr.CodeInexpressible)
	if !strings.Contains(err.Error(), "hunk anchor") {
		t.Fatalf("message: %v", err)
	}

	err = resolveErrOf(t, tlOf(Transform{Op: OpRemove, Path: Path{}}), doc)
	mustCode(t, err, hewerr.CodeInexpressible)
	if !strings.Contains(err.Error(), "missing path") {
		t.Fatalf("message: %v", err)
	}
}

// Every entry point that can be handed a path checks it, not just the one
// that walks the document: an add and an `? absent` both reach the document
// through their own route.
func TestResolveRejectsMissingPathsOnCreatingOps(t *testing.T) {
	doc := mustDoc(t, servers)
	err := resolveErrOf(t, tlOf(Transform{Op: OpAdd, Path: Path{}, Value: mustVal(t, 1)}), doc)
	mustCode(t, err, hewerr.CodeInexpressible)
	if !strings.Contains(err.Error(), "missing path") {
		t.Fatalf("message: %v", err)
	}

	err = resolveErrOf(t, tlOf(Transform{Op: OpTest, Path: Path{}, Absent: true}), doc)
	mustCode(t, err, hewerr.CodeInexpressible)
	if !strings.Contains(err.Error(), "missing path") {
		t.Fatalf("message: %v", err)
	}
}

func TestResolveNegativeOrdinal(t *testing.T) {
	doc := mustDoc(t, "servers: [{name: a}]")
	neg := -1
	p := NewPath(
		Segment{Kind: SegKey, Name: "servers"},
		Segment{Kind: SegMatch, Name: "name", Value: Scalar{Kind: ScalarString, Text: "a"}, Ordinal: &neg},
	)
	err := resolveErrOf(t, tlOf(Transform{Op: OpRemove, Path: p}), doc)
	mustCode(t, err, hewerr.CodeNoMatch)
	if !strings.Contains(err.Error(), "ordinal -1 selects nothing") {
		t.Fatalf("message: %v", err)
	}
}

func TestResolveUnknownOp(t *testing.T) {
	doc := mustDoc(t, servers)
	err := resolveErrOf(t, tlOf(Transform{Op: OpKind("frobnicate"), Path: MustParsePath("/tags/0")}), doc)
	mustCode(t, err, hewerr.CodeInexpressible)
	if !strings.Contains(err.Error(), `unknown op "frobnicate"`) {
		t.Fatalf("message: %v", err)
	}
}

// --- adds and placement -----------------------------------------------------

func TestResolveAddAfterKeyMatchBecomesIndex(t *testing.T) {
	doc := mustDoc(t, servers)
	ops := list(t, tlOf(Transform{
		Op:    OpAdd,
		Path:  MustParsePath("/servers"),
		After: MustParsePath("/servers/name=github"),
		Value: mustVal(t, map[string]any{"name": "new"}),
	}), doc)
	wantPaths(t, ops, "add /servers/1")
}

func TestResolveAddBeforeKeyMatchBecomesIndex(t *testing.T) {
	doc := mustDoc(t, servers)
	ops := list(t, tlOf(Transform{
		Op:     OpAdd,
		Path:   MustParsePath("/servers"),
		Before: MustParsePath("/servers/name=local"),
		Value:  mustVal(t, map[string]any{"name": "new"}),
	}), doc)
	wantPaths(t, ops, "add /servers/1")
}

func TestResolveAddWithNoPlacementAppends(t *testing.T) {
	doc := mustDoc(t, servers)
	ops := list(t, tlOf(Transform{Op: OpAdd, Path: MustParsePath("/servers"), Value: mustVal(t, "x")}), doc)
	wantPaths(t, ops, "add /servers/2")
}

// A placement that resolves but is not a direct element of the container
// leaves the insertion at the end, which is what ext/json's applier does; the
// resolved list must describe the edit that really happened.
func TestResolveAddWithForeignPlacementAppends(t *testing.T) {
	doc := mustDoc(t, servers)
	ops := list(t, tlOf(Transform{
		Op:    OpAdd,
		Path:  MustParsePath("/servers"),
		After: MustParsePath("/server/host"),
		Value: mustVal(t, "x"),
	}), doc)
	wantPaths(t, ops, "add /servers/2")

	ops = list(t, tlOf(Transform{
		Op:     OpAdd,
		Path:   MustParsePath("/servers"),
		Before: MustParsePath("/servers/0/name"),
		Value:  mustVal(t, "x"),
	}), doc)
	wantPaths(t, ops, "add /servers/2")
}

func TestResolveAddPlacementThatDoesNotResolveFails(t *testing.T) {
	doc := mustDoc(t, servers)
	err := resolveErrOf(t, tlOf(Transform{
		Op:    OpAdd,
		Path:  MustParsePath("/servers"),
		After: MustParsePath("/servers/name=ghost"),
		Value: mustVal(t, "x"),
	}), doc)
	mustCode(t, err, hewerr.CodeNoMatch)

	err = resolveErrOf(t, tlOf(Transform{
		Op:     OpAdd,
		Path:   MustParsePath("/servers"),
		Before: MustParsePath("/servers/name=ghost"),
		Value:  mustVal(t, "x"),
	}), doc)
	mustCode(t, err, hewerr.CodeNoMatch)
}

func TestResolveAddNewMapKey(t *testing.T) {
	doc := mustDoc(t, servers)
	ops := list(t, tlOf(Transform{
		Op:         OpAdd,
		Path:       MustParsePath("/server/tls"),
		After:      MustParsePath("/server/host"),
		Value:      mustVal(t, true),
		OnConflict: ConflictKeep,
	}), doc)
	// Placement inside an object has no RFC 6901 meaning and is consumed,
	// as is on_conflict.
	wantPaths(t, ops, "add /server/tls")
}

func TestResolveAddOntoExistingScalarKeepsItsPointer(t *testing.T) {
	doc := mustDoc(t, servers)
	ops := list(t, tlOf(Transform{Op: OpAdd, Path: MustParsePath("/server/port"), Value: mustVal(t, 9090), OnConflict: ConflictReplace}), doc)
	wantPaths(t, ops, "add /server/port")
}

func TestResolveAddUnderMissingParentFails(t *testing.T) {
	doc := mustDoc(t, servers)
	err := resolveErrOf(t, tlOf(Transform{Op: OpAdd, Path: MustParsePath("/nope/child"), Value: mustVal(t, 1)}), doc)
	mustCode(t, err, hewerr.CodeNoMatch)
}

// An add at the root is RFC 6902's whole-document replace, so it resolves to
// "" rather than failing.
func TestResolveAddAtRootIsTheWholeDocument(t *testing.T) {
	doc := mustDoc(t, servers)
	ops := list(t, tlOf(Transform{Op: OpAdd, Path: RootPath(), Value: mustVal(t, 1)}), doc)
	wantPaths(t, ops, "add ")
}

// The root is the one node that cannot be asserted absent or created, because
// it has no parent to hold it.
func TestResolveAbsentTestAtRootFails(t *testing.T) {
	doc := mustDoc(t, servers)
	err := resolveErrOf(t, tlOf(Transform{Op: OpTest, Path: RootPath(), Absent: true}), doc)
	mustCode(t, err, hewerr.CodeInexpressible)
	if !strings.Contains(err.Error(), "document root") {
		t.Fatalf("message: %v", err)
	}
}

func TestResolveAddAtAppendPosition(t *testing.T) {
	doc := mustDoc(t, servers)
	ops := list(t, tlOf(Transform{Op: OpAdd, Path: MustParsePath("/tags/-"), Value: mustVal(t, "c")}), doc)
	wantPaths(t, ops, "add /tags/2")
}

func TestResolveAddAtIndexPastTheEnd(t *testing.T) {
	doc := mustDoc(t, servers)
	ops := list(t, tlOf(Transform{Op: OpAdd, Path: MustParsePath("/tags/2"), Value: mustVal(t, "c")}), doc)
	wantPaths(t, ops, "add /tags/2")
}

func TestResolveAbsentTestOnMissingKey(t *testing.T) {
	doc := mustDoc(t, servers)
	ops := list(t, tlOf(Transform{Op: OpTest, Path: MustParsePath("/server/tls"), Absent: true}), doc)
	wantPaths(t, ops, "test /server/tls")
}

func TestResolveAbsentTestOnUnmatchableKeyMatch(t *testing.T) {
	doc := mustDoc(t, servers)
	err := resolveErrOf(t, tlOf(Transform{Op: OpTest, Path: MustParsePath("/servers/name=ghost"), Absent: true}), doc)
	mustCode(t, err, hewerr.CodeNoMatch)
	if !strings.Contains(err.Error(), "no index for a node that does not exist") {
		t.Fatalf("message: %v", err)
	}
}

func TestResolveAbsentTestOnInexpressibleSegment(t *testing.T) {
	doc := mustDoc(t, servers)
	err := resolveErrOf(t, tlOf(Transform{Op: OpTest, Path: MustParsePath("/server/#0"), Absent: true}), doc)
	mustCode(t, err, hewerr.CodeInexpressible)
}

func TestResolveAbsentTestWithOrdinalOnTheNewNode(t *testing.T) {
	doc := mustDoc(t, servers)
	p := MustParsePath("/server/tls[0]")
	err := resolveErrOf(t, tlOf(Transform{Op: OpTest, Path: p, Absent: true}), doc)
	mustCode(t, err, hewerr.CodeInexpressible)
	if !strings.Contains(err.Error(), "cannot address a node that does not exist") {
		t.Fatalf("message: %v", err)
	}
}

func TestResolveAppendUnderNonSequenceParent(t *testing.T) {
	doc := mustDoc(t, servers)
	err := resolveErrOf(t, tlOf(Transform{Op: OpAdd, Path: MustParsePath("/server/-"), Value: mustVal(t, 1)}), doc)
	mustCode(t, err, hewerr.CodeNoMatch)
}

// --- copy -------------------------------------------------------------------

func TestResolveCopy(t *testing.T) {
	doc := mustDoc(t, servers)
	ops := list(t, tlOf(Transform{
		Op:   OpCopy,
		From: MustParsePath("/servers/name=local/command"),
		Path: MustParsePath("/server/command"),
	}), doc)
	wantPaths(t, ops, "copy /server/command")
	if ops[0].From != "/servers/1/command" {
		t.Fatalf("from must resolve too, got %q", ops[0].From)
	}
}

func TestResolveCopyFromMissingFails(t *testing.T) {
	doc := mustDoc(t, servers)
	err := resolveErrOf(t, tlOf(Transform{
		Op:   OpCopy,
		From: MustParsePath("/servers/name=ghost"),
		Path: MustParsePath("/server/command"),
	}), doc)
	mustCode(t, err, hewerr.CodeNoMatch)
}

// --- qualifier carry-through and consumption --------------------------------

func TestResolveConsumesFormatOnlyQualifiersAndCarriesAssertions(t *testing.T) {
	doc := mustDoc(t, servers)
	three := 3
	ops := list(t, tlOf(
		Transform{Op: OpTest, Path: MustParsePath("/server"), Count: &three},
		Transform{Op: OpTest, Path: MustParsePath("/server"), Count: &three, Exhaustive: true},
		Transform{Op: OpAdd, Path: MustParsePath("/server/tls"), Value: mustVal(t, true),
			Anchor: AnchorFork, Surface: SurfaceTable, Idempotent: true},
		Transform{Op: OpRemove, Path: MustParsePath("/server/port"), Optional: true},
	), doc)
	if ops[0].Count == nil || *ops[0].Count != 3 {
		t.Fatal("count must survive resolution")
	}
	if ops[1].Exhaustive != true {
		t.Fatal("exhaustive must survive resolution")
	}
	if ops[2].Value.IsZero() {
		t.Fatal("value must survive resolution")
	}
	wantPaths(t, ops, "test /server", "test /server", "add /server/tls", "remove /server/port")
}

// --- Resolve's own preconditions --------------------------------------------

func TestResolveWithoutADocument(t *testing.T) {
	err := resolveErrOf(t, tlOf(Transform{Op: OpRemove, Path: MustParsePath("/x")}), nil)
	mustCode(t, err, hewerr.CodeTargetParse)

	err = resolveErrOf(t, tlOf(Transform{Op: OpRemove, Path: MustParsePath("/x")}), ydoc{})
	mustCode(t, err, hewerr.CodeTargetParse)
	if !strings.Contains(err.Error(), "no root node") {
		t.Fatalf("message: %v", err)
	}
}

func TestResolveEmptyListResolvesToNoOps(t *testing.T) {
	ops := list(t, tlOf(), mustDoc(t, servers))
	if len(ops) != 0 {
		t.Fatalf("got %v", ops)
	}
}

func TestResolveErrorNamesTheTarget(t *testing.T) {
	doc := mustDoc(t, servers)
	err := resolveErrOf(t, tlOf(Transform{Op: OpRemove, Path: MustParsePath("/nope"), PatchLine: 7}), doc)
	he, _ := hewerr.As(err)
	if he.Target != "target.json" {
		t.Fatalf("target: %q", he.Target)
	}
	if he.PatchLine != 7 {
		t.Fatalf("patch line provenance lost: %d", he.PatchLine)
	}
}

// --- Scalar.Value -----------------------------------------------------------

func TestScalarValueTags(t *testing.T) {
	cases := []struct {
		s   Scalar
		tag string
	}{
		{Scalar{Kind: ScalarString, Text: "x"}, "!!str"},
		{Scalar{Kind: ScalarBool, Text: "true"}, "!!bool"},
		{Scalar{Kind: ScalarNull, Text: "null"}, "!!null"},
		{Scalar{Kind: ScalarNumber, Text: "8080"}, "!!int"},
		{Scalar{Kind: ScalarNumber, Text: "1.5"}, "!!float"},
		{Scalar{Kind: ScalarNumber, Text: "1e9"}, "!!float"},
	}
	for _, c := range cases {
		got := c.s.Value().Node()
		if got.Tag != c.tag {
			t.Fatalf("%v: tag %q, want %q", c.s, got.Tag, c.tag)
		}
		if got.Value != c.s.Text {
			t.Fatalf("%v: text %q", c.s, got.Value)
		}
	}
}

func TestScalarValueMatchesQuotedStringNotNumber(t *testing.T) {
	doc := mustDoc(t, `ports: [{id: "8080"}, {id: 8080}]`)
	ops := list(t, tlOf(Transform{Op: OpRemove, Path: MustParsePath(`/ports/id="8080"`)}), doc)
	wantPaths(t, ops, "remove /ports/0")

	ops = list(t, tlOf(Transform{Op: OpRemove, Path: MustParsePath("/ports/id=8080")}), doc)
	wantPaths(t, ops, "remove /ports/1")
}

// --- MarshalResolvedOps -----------------------------------------------------

func kindPtr(k NodeKind) *NodeKind { return &k }

func TestMarshalResolvedOpsMatchesTheCorpusShape(t *testing.T) {
	doc := mustDoc(t, `{"servers": [{"name": "github", "command": "npx"}]}`)
	ops := list(t, tlOf(
		Transform{Op: OpTest, Path: MustParsePath("/servers/name=github/command"), Value: mustVal(t, "npx")},
		Transform{Op: OpReplace, Path: MustParsePath("/servers/name=github/command"), Value: mustVal(t, "npx-18")},
	), doc)
	got := MarshalResolvedOps(ops)
	want := `[
  { "op": "test", "path": "/servers/0/command", "value": "npx" },
  { "op": "replace", "path": "/servers/0/command", "value": "npx-18" }
]
`
	if string(got) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestMarshalResolvedOpsEmpty(t *testing.T) {
	got := MarshalResolvedOps(nil)
	if string(got) != "[\n]\n" {
		t.Fatalf("got %q", got)
	}
}

func TestMarshalResolvedOpsAllFields(t *testing.T) {
	two := 2
	ops := []ResolvedOp{
		{Op: OpCopy, From: "/a/0", Path: "/b/1"},
		{Op: OpTest, Path: "/a", Absent: true},
		{Op: OpTest, Path: "/a", Count: &two},
		{Op: OpTest, Path: "/a", NodeKind: kindPtr(KindSeq)},
		{Op: OpTest, Path: "/a", Count: &two, Exhaustive: true},
	}
	got := MarshalResolvedOps(ops)
	want := `[
  { "op": "copy", "from": "/a/0", "path": "/b/1" },
  { "op": "test", "path": "/a", "absent": true },
  { "op": "test", "path": "/a", "count": 2 },
  { "op": "test", "path": "/a", "kind": "seq" },
  { "op": "test", "path": "/a", "count": 2, "exhaustive": true }
]
`
	if string(got) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestMarshalResolvedOpsValueLiterals(t *testing.T) {
	cases := []struct {
		yaml string
		want string
	}{
		{`{a: 1, b: "s"}`, `{"a":1,"b":"s"}`},
		{`[1, true, null]`, `[1,true,null]`},
		{`8080`, `8080`},
		{`1.5`, `1.5`},
		{`true`, `true`},
		{`null`, `null`},
		{`"npx"`, `"npx"`},
	}
	for _, c := range cases {
		var n yaml.Node
		if err := yaml.Unmarshal([]byte(c.yaml), &n); err != nil {
			t.Fatal(err)
		}
		got := MarshalResolvedOps([]ResolvedOp{{Op: OpAdd, Path: "/x", Value: NodeValue(n.Content[0])}})
		want := "[\n  { \"op\": \"add\", \"path\": \"/x\", \"value\": " + c.want + " }\n]\n"
		if string(got) != want {
			t.Fatalf("%s: got:\n%s\nwant:\n%s", c.yaml, got, want)
		}
	}
}

func TestJSONLiteralFallbacks(t *testing.T) {
	if got := jsonLiteral(nil); got != "null" {
		t.Fatalf("got %q", got)
	}
	if got := jsonLiteral(&yaml.Node{Kind: yaml.AliasNode}); got != "null" {
		t.Fatalf("an alias has no JSON literal: got %q", got)
	}
	if got := jsonLiteral(&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!timestamp", Value: "2026-08-14"}); got != `"2026-08-14"` {
		t.Fatalf("got %q", got)
	}
}
