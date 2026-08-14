package hew

import (
	"strings"
	"testing"

	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

// The exits are the seams where a Path becomes TEXT: MarshalTransforms and
// MarshalTransformStream for .hewt, Render for .hew. Before this guard, a key
// the §4 grammar cannot spell went out as a spelling that reads back as
// something else — "8080" as an index, "@scope/pkg" as a marker — and the
// corruption was invisible until someone applied the patch. §9.3's rule is
// refuse, never misapply.

// unspellableKey is a live case: a port number used as a key, which any
// port-map in a real config has. Its bare spelling, "8080", re-reads as a
// sequence index.
//
// The other live case — "@scope/pkg", a scoped npm dependency, re-reading as a
// Markdown marker — is exercised in ext/markdown's suite instead, because it is
// a collision with an EXTENSION-claimed shape and this package registers none
// (§8.8). Same guard, same refusal; the difference is only which grammar has to
// be linked for the collision to exist at all.
const unspellableKey = "8080"

func badKeyPath() Path {
	return NewPath(Segment{Kind: SegKey, Name: "dependencies"}, Segment{Kind: SegKey, Name: unspellableKey})
}

func goodPath() Path { return MustParsePath("/dependencies/left-pad") }

// fieldCases is one transform per path-valued field, each valid in every other
// respect, so the only reason any of them can fail is the unspellable key.
func fieldCases(t *testing.T) []struct {
	field string
	tl    TransformList
} {
	t.Helper()
	val := mustVal(t, "1.0.0")
	return []struct {
		field string
		tl    TransformList
	}{
		{"path", TransformList{Target: "package.json", Format: FormatJSON, Transform: []Transform{
			{Op: OpTest, Path: badKeyPath(), Value: val}}}},
		{"from", TransformList{Target: "package.json", Format: FormatJSON, Transform: []Transform{
			{Op: OpCopy, Path: goodPath(), From: badKeyPath()}}}},
		{"before", TransformList{Target: "package.json", Format: FormatJSON, Transform: []Transform{
			{Op: OpAdd, Path: goodPath(), Value: val, Before: badKeyPath()}}}},
		{"after", TransformList{Target: "package.json", Format: FormatJSON, Transform: []Transform{
			{Op: OpAdd, Path: goodPath(), Value: val, After: badKeyPath()}}}},
	}
}

// assertUnspellableRefusal pins the whole diagnostic contract: HEW020 at the
// named component, the best-effort spelling as the path (so the reviewer sees
// the text that would have been written), the offending key as data, the field
// it sat in, and the pointer to the ratified-pending quoted-key form.
func assertUnspellableRefusal(t *testing.T, err error, field string, comp hewerr.Component) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: an unspellable key must be refused, not written", field)
	}
	he, ok := hewerr.As(err)
	if !ok {
		t.Fatalf("%s: want a *hewerr.Error, got %T: %v", field, err, err)
	}
	if he.Code != hewerr.CodeInexpressible {
		t.Errorf("%s: code = %s, want %s inexpressible", field, he.Code, hewerr.CodeInexpressible)
	}
	if he.Component != comp {
		t.Errorf("%s: component = %s, want %s", field, he.Component, comp)
	}
	if he.Path != "/dependencies/8080" {
		t.Errorf("%s: path = %q, want the best-effort spelling /dependencies/8080", field, he.Path)
	}
	for _, want := range []string{unspellableKey, field, "PR #1", "§9.3"} {
		if !strings.Contains(he.Detail, want) {
			t.Errorf("%s: detail %q does not name %q", field, he.Detail, want)
		}
	}
}

func TestMarshalTransformsRefusesUnspellablePaths(t *testing.T) {
	for _, tc := range fieldCases(t) {
		t.Run(tc.field, func(t *testing.T) {
			out, err := MarshalTransforms(tc.tl)
			assertUnspellableRefusal(t, err, tc.field, hewerr.ComponentRenderer)
			if out != nil {
				t.Errorf("refused marshal still produced %d bytes: %s", len(out), out)
			}
		})
	}
}

func TestMarshalTransformStreamRefusesUnspellablePaths(t *testing.T) {
	for _, tc := range fieldCases(t) {
		t.Run(tc.field, func(t *testing.T) {
			// A healthy document first: the guard must refuse the whole stream,
			// not emit the documents that came before the bad one.
			healthy := TransformList{Target: "other.json", Format: FormatJSON, Transform: []Transform{
				{Op: OpTest, Path: goodPath(), Value: mustVal(t, "1.0.0")}}}
			out, err := MarshalTransformStream([]TransformList{healthy, tc.tl})
			assertUnspellableRefusal(t, err, tc.field, hewerr.ComponentRenderer)
			if out != nil {
				t.Errorf("refused stream still produced %d bytes: %s", len(out), out)
			}
		})
	}
}

func TestRenderRefusesUnspellablePaths(t *testing.T) {
	for _, tc := range fieldCases(t) {
		t.Run(tc.field, func(t *testing.T) {
			out, err := Render(tc.tl, RenderOptions{Preamble: true, Fragment: FragmentNative})
			assertUnspellableRefusal(t, err, tc.field, hewerr.ComponentRenderer)
			if out != nil {
				t.Errorf("refused render still produced %d bytes: %s", len(out), out)
			}
		})
	}
}

// TestExitsNameTheTransformThatBroke pins that the refusal locates the record
// in a multi-record list rather than only naming the list.
func TestExitsNameTheTransformThatBroke(t *testing.T) {
	tl := TransformList{Target: "package.json", Format: FormatJSON, Transform: []Transform{
		{Op: OpTest, Path: goodPath(), Value: mustVal(t, "1.0.0")},
		{Op: OpTest, Path: goodPath(), Value: mustVal(t, "1.0.0")},
		{Op: OpReplace, Path: badKeyPath(), Value: mustVal(t, "2.0.0")},
	}}
	_, err := MarshalTransforms(tl)
	he, ok := hewerr.As(err)
	if !ok {
		t.Fatalf("want a *hewerr.Error, got %v", err)
	}
	if !strings.Contains(he.Detail, "transform 2") {
		t.Errorf("detail %q does not name transform 2", he.Detail)
	}
}

// TestExitsRefuseTheOtherUnspellableClasses walks the corruption classes past
// the scoped-package one, at a real exit, so that each is pinned as a refusal
// rather than as a silently different address.
func TestExitsRefuseTheOtherUnspellableClasses(t *testing.T) {
	tests := []struct {
		name string
		seg  Segment
	}{
		{"append token", Segment{Kind: SegKey, Name: "-"}},
		{"comment address", Segment{Kind: SegKey, Name: "#0"}},
		{"trailing comment", Segment{Kind: SegKey, Name: "#t"}},
		{"trailing question mark", Segment{Kind: SegKey, Name: "opt?"}},
		{"trailing ordinal", Segment{Kind: SegKey, Name: "a[1]"}},
		{"leading double quote", Segment{Kind: SegKey, Name: `"quoted`}},
		{"empty key", Segment{Kind: SegKey, Name: ""}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tl := TransformList{Target: "t.json", Format: FormatJSON, Transform: []Transform{
				{Op: OpTest, Path: NewPath(tc.seg), Value: mustVal(t, 1)}}}
			if _, err := MarshalTransforms(tl); err == nil {
				t.Errorf("marshal accepted the unspellable key %q", tc.seg.Name)
			} else if he, ok := hewerr.As(err); !ok || he.Code != hewerr.CodeInexpressible {
				t.Errorf("want HEW020, got %v", err)
			}
			if _, err := Render(tl, RenderOptions{}); err == nil {
				t.Errorf("render accepted the unspellable key %q", tc.seg.Name)
			} else if he, ok := hewerr.As(err); !ok || he.Code != hewerr.CodeInexpressible {
				t.Errorf("want HEW020, got %v", err)
			}
		})
	}
}

// TestExitsStillPassSpellablePaths is the guard's other half: it must not
// refuse anything the grammar CAN spell, including keys that only look
// dangerous — "08080" is not an index, "#foo" is not a comment address.
func TestExitsStillPassSpellablePaths(t *testing.T) {
	for _, key := range []string{"left-pad", "08080", "8080x", "#foo", "##0", "para:x", "a[]", "a/b~c=d"} {
		t.Run(key, func(t *testing.T) {
			tl := TransformList{Target: "t.json", Format: FormatJSON, Transform: []Transform{
				{Op: OpTest, Path: NewPath(Segment{Kind: SegKey, Name: key}), Value: mustVal(t, 1)}}}
			if _, err := MarshalTransforms(tl); err != nil {
				t.Errorf("marshal refused the spellable key %q: %v", key, err)
			}
			if _, err := Render(tl, RenderOptions{}); err != nil {
				t.Errorf("render refused the spellable key %q: %v", key, err)
			}
		})
	}
}
