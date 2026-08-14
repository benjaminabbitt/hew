package hew

import (
	"errors"
	"testing"

	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

// Rule 3 (A.0): reads become asserts. An operation that names an existing node
// records the value it FOUND as a test transform beside the write it performs
// — §9.1's lowering with the before-image taken from the open document instead
// of from a `-` line — and the two unasserted forms are spelled as themselves.

const opsSrc = `port: 8080
host: localhost
servers:
  - name: alpha
    command: a
  - name: beta
    command: b
`

func opsDoc(t *testing.T) *Doc {
	t.Helper()
	toyOnly(t)
	d, err := OpenBytes("config.toy", []byte(opsSrc))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func val(t *testing.T, x any) Value {
	t.Helper()
	v, err := ValueOf(x)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// str is the value a scalar read out of the toy document compares against:
// the document's own node, not a re-encoded copy.
func wantIR(t *testing.T, d *Doc, want ...Transform) TransformList {
	t.Helper()
	tl, err := d.Transforms()
	if err != nil {
		t.Fatal(err)
	}
	if len(tl.Transform) != len(want) {
		t.Fatalf("recorded %d transforms, want %d:\n%s", len(tl.Transform), len(want), irDump(tl))
	}
	for i := range want {
		if !tl.Transform[i].Equal(want[i]) {
			t.Fatalf("transform %d:\n got %+v\nwant %+v", i, tl.Transform[i], want[i])
		}
	}
	return tl
}

func irDump(tl TransformList) string {
	b, err := MarshalTransforms(tl)
	if err != nil {
		return err.Error()
	}
	return string(b)
}

func TestReplaceRecordsTheValueItFound(t *testing.T) {
	d := opsDoc(t)
	d.At("/host").Replace("example.com")

	p := MustParsePath("/host")
	wantIR(t, d,
		Transform{Op: OpTest, Path: p, Value: val(t, "localhost")},
		Transform{Op: OpReplace, Path: p, Value: val(t, "example.com")},
	)
}

func TestReplaceReadsThroughAKeyMatch(t *testing.T) {
	d := opsDoc(t)
	d.At("/servers/{}/command", MatchKey("name", "beta")).Replace("npx")

	p := NewPath(Key("servers"), MatchKey("name", "beta"), Key("command"))
	wantIR(t, d,
		Transform{Op: OpTest, Path: p, Value: val(t, "b")},
		Transform{Op: OpReplace, Path: p, Value: val(t, "npx")},
	)
}

// The document is the BEFORE-image and stays one: two writes to the same node
// both record what the file holds now, not what the previous call would have
// left there. A patch is a statement about the document it was derived from.
func TestReadsAllSeeOneBeforeImage(t *testing.T) {
	d := opsDoc(t)
	d.At("/host").Replace("a")
	d.At("/host").Replace("b")

	p := MustParsePath("/host")
	wantIR(t, d,
		Transform{Op: OpTest, Path: p, Value: val(t, "localhost")},
		Transform{Op: OpReplace, Path: p, Value: val(t, "a")},
		Transform{Op: OpTest, Path: p, Value: val(t, "localhost")},
		Transform{Op: OpReplace, Path: p, Value: val(t, "b")},
	)
}

func TestReplaceOfAnAbsentNodeIsHEW013AtTheTerminal(t *testing.T) {
	d := opsDoc(t)
	d.At("/nope").Replace("x")
	if d.err != nil {
		t.Fatalf("the read happened before the terminal: %v", d.err)
	}
	_, err := d.Transforms()
	he := wantCode(t, err, hewerr.CodeNoMatch)
	if he.Path != "/nope" {
		t.Fatalf("error names %q", he.Path)
	}
}

func TestReplaceOfAnAmbiguousMatchIsHEW012(t *testing.T) {
	toyOnly(t)
	d, err := OpenBytes("config.toy", []byte("servers:\n  - name: a\n  - name: a\n"))
	if err != nil {
		t.Fatal(err)
	}
	d.At("/servers/{}", MatchKey("name", "a")).Replace("x")
	_, terr := d.Transforms()
	wantCode(t, terr, hewerr.CodeAmbiguousMatch)
}

func TestSetIsAnUnassertedUpsert(t *testing.T) {
	d := opsDoc(t)
	d.At("/host").Set("example.com")

	wantIR(t, d, Transform{Op: OpAdd, Path: MustParsePath("/host"),
		Value: val(t, "example.com"), OnConflict: ConflictReplace})
}

func TestDefaultIsAnUnassertedKeep(t *testing.T) {
	d := opsDoc(t)
	d.At("/timeout").Default(30)

	wantIR(t, d, Transform{Op: OpAdd, Path: MustParsePath("/timeout"),
		Value: val(t, 30), OnConflict: ConflictKeep})
}

// Set and Default name nodes that need not exist, so neither reads: an
// unasserted write is unasserted, and the absence of a test is the whole
// difference §7.7 makes visible.
func TestSetAndDefaultNeverRead(t *testing.T) {
	d := opsDoc(t)
	d.At("/nope").Set("x")
	d.At("/also-nope").Default("y")
	if _, err := d.Transforms(); err != nil {
		t.Fatalf("an unasserted write read the document: %v", err)
	}
}

func TestAddMustNotExist(t *testing.T) {
	d := opsDoc(t)
	d.At("/timeout").Add(30)

	wantIR(t, d, Transform{Op: OpAdd, Path: MustParsePath("/timeout"),
		Value: val(t, 30), OnConflict: ConflictFail})
}

func TestRemoveRecordsTheValueItFound(t *testing.T) {
	d := opsDoc(t)
	d.At("/port").Remove()

	p := MustParsePath("/port")
	wantIR(t, d,
		Transform{Op: OpTest, Path: p, Value: val(t, 8080)},
		Transform{Op: OpRemove, Path: p},
	)
}

func TestRemoveOfAnAbsentNodeIsHEW013(t *testing.T) {
	d := opsDoc(t)
	d.At("/nope").Remove()
	_, err := d.Transforms()
	wantCode(t, err, hewerr.CodeNoMatch)
}

// OP-06, remove-key-if-present: `! optional` is exactly the case where there
// may be nothing to read, so an optional remove of an absent node records the
// remove alone rather than failing on a before-image that does not exist.
func TestOptionalRemoveOfAnAbsentNodeRecordsNoAssert(t *testing.T) {
	d := opsDoc(t)
	d.At("/legacy").Remove().Optional()

	wantIR(t, d, Transform{Op: OpRemove, Path: MustParsePath("/legacy"), Optional: true})
}

func TestOptionalRemoveOfAPresentNodeStillAsserts(t *testing.T) {
	d := opsDoc(t)
	d.At("/port").Optional().Remove()

	p := MustParsePath("/port")
	wantIR(t, d,
		Transform{Op: OpTest, Path: p, Value: val(t, 8080), Optional: true},
		Transform{Op: OpRemove, Path: p, Optional: true},
	)
}

// Qualifiers ride the transforms their Sel recorded, exactly as a `!`
// directive rides a hunk's — so the order they are written in does not change
// what they qualify.
func TestQualifierOrderDoesNotMatter(t *testing.T) {
	before := opsDoc(t)
	before.At("/port").Optional().Remove()
	after := opsDoc(t)
	after.At("/port").Remove().Optional()

	a, err := before.Transforms()
	if err != nil {
		t.Fatal(err)
	}
	b, err := after.Transforms()
	if err != nil {
		t.Fatal(err)
	}
	if !a.Equal(b) {
		t.Fatalf("qualifier order changed the IR:\n%s\n%s", irDump(a), irDump(b))
	}
}

func TestIdempotentRidesTheAssertAndTheWrite(t *testing.T) {
	d := opsDoc(t)
	d.At("/host").Replace("example.com").Idempotent()

	p := MustParsePath("/host")
	wantIR(t, d,
		Transform{Op: OpTest, Path: p, Value: val(t, "localhost"), Idempotent: true},
		Transform{Op: OpReplace, Path: p, Value: val(t, "example.com"), Idempotent: true},
	)
}

// Optional is legal on remove and test only (A.1): riding it onto a replace
// would produce IR that does not validate, so it rides what it may — which is
// what the lowerer does with the same directive.
func TestOptionalOnAReplaceRidesOnlyTheAssert(t *testing.T) {
	d := opsDoc(t)
	d.At("/host").Replace("example.com").Optional()

	p := MustParsePath("/host")
	wantIR(t, d,
		Transform{Op: OpTest, Path: p, Value: val(t, "localhost"), Optional: true},
		Transform{Op: OpReplace, Path: p, Value: val(t, "example.com")},
	)
}

func TestAnchorRidesTheTransformsWhenTheFormatOwnsIt(t *testing.T) {
	d := opsDoc(t)
	d.At("/host").Set("example.com").Anchor(AnchorFork)

	wantIR(t, d, Transform{Op: OpAdd, Path: MustParsePath("/host"),
		Value: val(t, "example.com"), OnConflict: ConflictReplace, Anchor: AnchorFork})
}

// The toy owns "anchor" and not "surface": a qualifier this format does not
// declare is HEW020, never silently ignored (§9.3, A.4).
func TestQualifierTheFormatDoesNotOwnIsHEW020(t *testing.T) {
	d := opsDoc(t)
	d.At("/host").Set("example.com").Surface(SurfaceTable)
	_, err := d.Transforms()
	he := wantCode(t, err, hewerr.CodeInexpressible)
	if he.Component != hewerr.ComponentApplier {
		t.Fatalf("component = %v", he.Component)
	}
}

func TestSurfaceRidesTheAddWhenTheFormatOwnsIt(t *testing.T) {
	toyOnly(t)
	Register("toy3", Binding{Document: toyDocument, Qualifiers: []string{"surface"},
		Detect: DetectRule{Extensions: []string{".toy3"}}})
	d, err := OpenBytes("config.toy3", []byte(opsSrc))
	if err != nil {
		t.Fatal(err)
	}
	d.At("/host").Set("x").Surface(SurfaceDotted)
	tl, err := d.Transforms()
	if err != nil {
		t.Fatal(err)
	}
	if tl.Transform[0].Surface != SurfaceDotted {
		t.Fatalf("surface did not ride: %+v", tl.Transform[0])
	}
}

func TestAnchorRejectsAnUnknownMode(t *testing.T) {
	d := opsDoc(t)
	d.At("/host").Set("x").Anchor("sideways")
	wantCode(t, d.err, hewerr.CodeParse)
}

func TestSurfaceRejectsAnUnknownPlacement(t *testing.T) {
	toyOnly(t)
	Register("toy3", Binding{Document: toyDocument, Qualifiers: []string{"surface"},
		Detect: DetectRule{Extensions: []string{".toy3"}}})
	d, err := OpenBytes("config.toy3", []byte(opsSrc))
	if err != nil {
		t.Fatal(err)
	}
	d.At("/host").Set("x").Surface("sideways")
	wantCode(t, d.err, hewerr.CodeParse)
}

// --- placement (OP-11 … OP-13) -----------------------------------------------

func TestAddAfterASibling(t *testing.T) {
	d := opsDoc(t)
	d.At("/timeout").Add(30).After("host")

	wantIR(t, d, Transform{Op: OpAdd, Path: MustParsePath("/timeout"), Value: val(t, 30),
		OnConflict: ConflictFail, After: MustParsePath("/host")})
}

func TestAddBeforeASibling(t *testing.T) {
	d := opsDoc(t)
	d.At("/timeout").Add(30).Before("host")

	wantIR(t, d, Transform{Op: OpAdd, Path: MustParsePath("/timeout"), Value: val(t, 30),
		OnConflict: ConflictFail, Before: MustParsePath("/host")})
}

func TestPlacementTakesAWholeAddress(t *testing.T) {
	d := opsDoc(t)
	d.At("/timeout").Add(30).After("/host")

	wantIR(t, d, Transform{Op: OpAdd, Path: MustParsePath("/timeout"), Value: val(t, 30),
		OnConflict: ConflictFail, After: MustParsePath("/host")})
}

// A sequence add addresses the CONTAINER, so a bare sibling token names an
// element of that same sequence rather than of its parent.
func TestPlacementInASequenceNamesTheSequencesOwnChildren(t *testing.T) {
	d := opsDoc(t)
	d.At("/servers/-").Add(map[string]string{"name": "gamma"}).After(`name="alpha"`)

	tl, err := d.Transforms()
	if err != nil {
		t.Fatal(err)
	}
	want := NewPath(Key("servers"), MatchKey("name", "alpha"))
	if !tl.Transform[0].After.Equal(want) {
		t.Fatalf("after = %q, want %q", tl.Transform[0].After.String(), want.String())
	}
}

func TestPlacementIsMutuallyExclusiveAndLastWins(t *testing.T) {
	d := opsDoc(t)
	d.At("/timeout").Add(30).After("host").Before("port")

	tl, err := d.Transforms()
	if err != nil {
		t.Fatal(err)
	}
	if !tl.Transform[0].After.IsZero() || tl.Transform[0].Before.IsZero() {
		t.Fatalf("both placements survived: %+v", tl.Transform[0])
	}
}

func TestPlacementRejectsAMalformedSibling(t *testing.T) {
	d := opsDoc(t)
	d.At("/timeout").Add(30).After(`"unterminated`)
	wantCode(t, d.err, hewerr.CodeParse)
}

// --- assertions (§7.4) -------------------------------------------------------

func TestAssertOnlyTransforms(t *testing.T) {
	kind := KindSeq
	count := 2
	for _, tc := range []struct {
		name string
		rec  func(*Doc)
		want Transform
	}{
		{"expect", func(d *Doc) { d.At("/port").Assert(8080) },
			Transform{Op: OpTest, Path: MustParsePath("/port"), Value: mustEncode(8080)}},
		{"absent", func(d *Doc) { d.At("/nope").AssertAbsent() },
			Transform{Op: OpTest, Path: MustParsePath("/nope"), Absent: true}},
		{"count", func(d *Doc) { d.At("/servers").AssertCount(2) },
			Transform{Op: OpTest, Path: MustParsePath("/servers"), Count: &count}},
		{"kind", func(d *Doc) { d.At("/servers").AssertKind(KindSeq) },
			Transform{Op: OpTest, Path: MustParsePath("/servers"), NodeKind: &kind}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := opsDoc(t)
			tc.rec(d)
			wantIR(t, d, tc.want)
		})
	}
}

// `? exhaustive` is always paired with a count (§9.1 step 3), and because the
// document is open the count is one more read that becomes an assert.
func TestAssertExhaustiveCountsTheOpenDocument(t *testing.T) {
	d := opsDoc(t)
	d.At("/servers").AssertExhaustive()

	tl, err := d.Transforms()
	if err != nil {
		t.Fatal(err)
	}
	got := tl.Transform[0]
	if !got.Exhaustive || got.Count == nil || *got.Count != 2 {
		t.Fatalf("exhaustive: %+v", got)
	}
}

func TestAssertExhaustiveOnAnAbsentNodeIsHEW013(t *testing.T) {
	d := opsDoc(t)
	d.At("/nope").AssertExhaustive()
	_, err := d.Transforms()
	wantCode(t, err, hewerr.CodeNoMatch)
}

func TestAssertKindRejectsAnUndeclaredKind(t *testing.T) {
	d := opsDoc(t)
	d.At("/servers").AssertKind("sideways")
	wantCode(t, d.err, hewerr.CodeParse)
}

func TestAssertCountRejectsANegativeCount(t *testing.T) {
	d := opsDoc(t)
	d.At("/servers").AssertCount(-1)
	wantCode(t, d.err, hewerr.CodeParse)
}

// --- error latching (§10.4) --------------------------------------------------

func TestFirstErrorWinsAndLaterCallsNoOp(t *testing.T) {
	d := opsDoc(t)
	d.At("/a/{}").Set("x")     // HEW001: hole count
	d.At("/host").Replace("y") // would be perfectly good IR

	_, err := d.Transforms()
	he := wantCode(t, err, hewerr.CodeParse)
	if he.Path != "/a/{}" {
		t.Fatalf("a later call overwrote the first error: %q", he.Path)
	}
	if len(d.steps) != 0 {
		t.Fatalf("calls after a latched error recorded %d steps", len(d.steps))
	}
}

type unencodable struct{}

func (unencodable) MarshalYAML() (any, error) { return nil, errors.New("no") }

func TestValueThatCannotBeEncodedIsAnError(t *testing.T) {
	d := opsDoc(t)
	d.At("/host").Replace(unencodable{})
	wantCode(t, d.err, hewerr.CodeInexpressible)

	assert := opsDoc(t)
	assert.At("/host").Assert(unencodable{})
	wantCode(t, assert.err, hewerr.CodeInexpressible)
}

func TestNoOperationsIsAnError(t *testing.T) {
	d := opsDoc(t)
	_, err := d.Transforms()
	wantCode(t, err, hewerr.CodeParse)
}

// A document the binding cannot parse fails at the first READ, not at open:
// nothing has happened until a terminal is called.
func TestUnreadableDocumentSurfacesAtTheTerminal(t *testing.T) {
	toyOnly(t)
	d, err := OpenBytes("config.toy", []byte("\tnot: yaml\n  at all\n"))
	if err != nil {
		t.Fatal(err)
	}
	d.At("/host").Replace("x")
	_, terr := d.Transforms()
	wantCode(t, terr, hewerr.CodeTargetParse)
}

func TestAFormatWithNoDocumentHalfCannotRead(t *testing.T) {
	isolate(t)
	Register("halfless", Binding{Detect: DetectRule{Extensions: []string{".half"}}})
	d, err := OpenBytes("config.half", []byte(opsSrc))
	if err != nil {
		t.Fatal(err)
	}
	d.At("/host").Replace("x")
	_, terr := d.Transforms()
	wantCode(t, terr, hewerr.CodeUnsupportedFormat)
}

func mustEncode(x any) Value {
	v, err := ValueOf(x)
	if err != nil {
		panic(err)
	}
	return v
}

func TestPlacementSwitchesBetweenAnAddressAndASegment(t *testing.T) {
	d := opsDoc(t)
	d.At("/timeout").Add(30).After("/servers/name=\"alpha\"").Before("port")

	tl, err := d.Transforms()
	if err != nil {
		t.Fatal(err)
	}
	if !tl.Transform[0].After.IsZero() {
		t.Fatalf("the earlier placement survived: %+v", tl.Transform[0])
	}
	if got, want := tl.Transform[0].Before.String(), "/port"; got != want {
		t.Fatalf("before = %q, want %q", got, want)
	}
}

// A sequence-style add addresses the container itself, so a bare sibling names
// one of THAT container's children.
func TestPlacementWhenTheAddressIsTheSequence(t *testing.T) {
	d := opsDoc(t)
	d.At("/servers").Add(map[string]string{"name": "gamma"}).Before(`name="beta"`)

	tl, err := d.Transforms()
	if err != nil {
		t.Fatal(err)
	}
	want := NewPath(Key("servers"), MatchKey("name", "beta"))
	if !tl.Transform[0].Before.Equal(want) {
		t.Fatalf("before = %q, want %q", tl.Transform[0].Before.String(), want.String())
	}
}

// `! optional` tolerates "there is nothing here" and nothing else: an
// ambiguous address is still ambiguous.
func TestOptionalDoesNotTolerateAmbiguity(t *testing.T) {
	toyOnly(t)
	d, err := OpenBytes("config.toy", []byte("servers:\n  - name: a\n  - name: a\n"))
	if err != nil {
		t.Fatal(err)
	}
	d.At("/servers/{}", MatchKey("name", "a")).Optional().Remove()
	_, terr := d.Transforms()
	wantCode(t, terr, hewerr.CodeAmbiguousMatch)
}

func TestAtPathRejectsARelativePath(t *testing.T) {
	d := opsDoc(t)
	d.AtPath(NewRelativePath(Segment{Kind: SegKey, Name: "port"}))
	wantCode(t, d.err, hewerr.CodeParse)
}

func TestABindingThatReturnsNoDocument(t *testing.T) {
	isolate(t)
	Register("hollow", Binding{
		Document: func(string, []byte) (Document, error) { return nil, nil },
		Detect:   DetectRule{Extensions: []string{".hollow"}},
	})
	d, err := OpenBytes("config.hollow", []byte(opsSrc))
	if err != nil {
		t.Fatal(err)
	}
	d.At("/host").Replace("x")
	_, terr := d.Transforms()
	wantCode(t, terr, hewerr.CodeTargetParse)
}

func TestADocumentWithNoRoot(t *testing.T) {
	isolate(t)
	Register("rootless", Binding{
		Document: func(string, []byte) (Document, error) { return rootlessDoc{}, nil },
		Detect:   DetectRule{Extensions: []string{".rootless"}},
	})
	d, err := OpenBytes("config.rootless", []byte(opsSrc))
	if err != nil {
		t.Fatal(err)
	}
	d.At("/host").Replace("x")
	_, terr := d.Transforms()
	wantCode(t, terr, hewerr.CodeTargetParse)
}

type rootlessDoc struct{}

func (rootlessDoc) Root() Node { return nil }
