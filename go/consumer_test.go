package hew

import (
	"strings"
	"testing"
)

// Three defects found by hew's FIRST consumer, which is the only review that
// catches this class: each one is an API that is wrong in a way its own tests
// could not see, because the tests were written by someone who already knew
// which call to make.

// --- 1. a type the API produces must be accepted by the API that consumes it -

// ValueOf(Value) is the case that bit: Transform.Value and ResolvedOp.Value
// hand a caller a hew.Value, and handing it straight back encoded the STRUCT —
// whose fields are unexported — as an empty mapping. Nothing failed at the
// call; it surfaced much later as a stale-target naming `{}`, which sent the
// reader to the target file instead of to the line that built the assertion.
func TestValueOfAHewValueIsThatValue(t *testing.T) {
	orig, err := ValueOf(map[string]any{"type": "http", "port": 8080})
	if err != nil {
		t.Fatal(err)
	}
	got, err := ValueOf(orig)
	if err != nil {
		t.Fatalf("ValueOf(Value): %v", err)
	}
	if !got.Equal(orig) {
		t.Fatalf("ValueOf(Value) = %s, want the value itself", got.String())
	}
}

// The pointer form too: a caller with a *Value in hand is making the same call.
func TestValueOfAHewValuePointerIsThatValue(t *testing.T) {
	orig, err := ValueOf("plain")
	if err != nil {
		t.Fatal(err)
	}
	got, err := ValueOf(&orig)
	if err != nil {
		t.Fatalf("ValueOf(*Value): %v", err)
	}
	if !got.Equal(orig) {
		t.Fatalf("ValueOf(*Value) = %s, want the value itself", got.String())
	}
}

// The zero Value has no node at all. It must not silently become `{}` either —
// that is the same silent-wrong-assertion bug wearing a different hat.
func TestValueOfTheZeroValueIsRefused(t *testing.T) {
	if _, err := ValueOf(Value{}); err == nil {
		t.Fatal("ValueOf(Value{}) succeeded; an empty Value asserts nothing and must say so")
	}
}

// End to end, on the surface the consumer actually used: an assertion built
// from a transform's own value asserts THAT value.
func TestAssertAcceptsAValueFromATransform(t *testing.T) {
	toyOnly(t)
	d, err := OpenBytes("config.toy", []byte(toySrc))
	if err != nil {
		t.Fatal(err)
	}
	want, err := ValueOf("localhost")
	if err != nil {
		t.Fatal(err)
	}
	d.At("/host").Assert(want)
	tl, err := d.Transforms()
	if err != nil {
		t.Fatalf("Transforms: %v", err)
	}
	if got := tl.Transform[0].Value; !got.Equal(want) {
		t.Fatalf("asserted %s, want %s", got.String(), want.String())
	}
}

// --- 2. the zero RenderOptions must round-trip ------------------------------

// §13.5 pins "render -> parse == identity on the IR". RenderOptions{} — the
// value every first caller reaches for — omitted the "hew: 1" preamble, so
// hew's own output was not hew input. The property must hold for the zero
// value or it is not a property, it is a configuration.
func TestRenderZeroOptionsRoundTripsThroughParse(t *testing.T) {
	tl := TransformList{
		Target: "config.yaml",
		Format: FormatYAML,
		Transform: []Transform{
			{Op: OpTest, Path: MustParsePath("/server/port"), Value: mustVal(t, 8080)},
			{Op: OpReplace, Path: MustParsePath("/server/port"), Value: mustVal(t, 9090)},
		},
	}
	out, err := Render(tl, RenderOptions{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if _, err := ParseSingle(out); err != nil {
		t.Fatalf("hew's own output does not parse (§13.5):\n%s\nerror: %v", out, err)
	}
}

// --- 3. a resolved add must say which add it was ----------------------------

// Transform carries OnConflict (Set/Default/Add -> replace/keep/fail) and
// ResolvedOp did not, so a stored resolved list could not tell a consumer which
// policy produced an add, and any replay had to guess.
func TestResolvedAddCarriesItsConflictPolicy(t *testing.T) {
	toyOnly(t)
	src := []byte(toySrc)
	tl := TransformList{
		Target: "config.toy",
		Format: formatToy,
		Transform: []Transform{
			{Op: OpAdd, Path: MustParsePath("/added"), Value: mustVal(t, 1), OnConflict: ConflictKeep},
		},
	}
	b, ok := Lookup(formatToy)
	if !ok || b.Document == nil {
		t.Skip("the toy binding supplies no reader")
	}
	doc, err := b.Document("config.toy", src)
	if err != nil {
		t.Fatal(err)
	}
	ops, err := Resolve(tl, doc)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(ops) == 0 {
		t.Fatal("Resolve produced no ops")
	}
	if ops[0].OnConflict != ConflictKeep {
		t.Fatalf("resolved add carries OnConflict %q, want %q", ops[0].OnConflict, ConflictKeep)
	}
}

// And the projection must SAY so where a consumer reads it.
func TestResolvedOpsSerializeTheConflictPolicy(t *testing.T) {
	ops := []ResolvedOp{{Op: OpAdd, Path: "/x", Value: mustVal(t, 1), OnConflict: ConflictKeep}}
	got := string(MarshalResolvedOps(ops))
	if !strings.Contains(got, "keep") {
		t.Fatalf("resolved op list drops the conflict policy:\n%s", got)
	}
}
