package markdown

import (
	"strings"
	"testing"

	hew "github.com/benjaminabbitt/hew/go"
	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

// Markdown's segment vocabulary, tested where it now lives (§8.8). Everything
// here used to be a case in the core's path suite; it moved with the grammar,
// which is the point of the relocation — importing this package is what makes a
// heading a heading, and a build that does not import it sees an ordinary key.

func TestClaimedSegmentsRoundTrip(t *testing.T) {
	for _, s := range []string{
		"/# ctxloom",
		"/# ctxloom/## Notes",
		"/### Deep/para:0",
		"/@ctxloom:context",
		"/# Setup/code:12",
		"/quote:0",
		"/# a~1b",
		"/@name~0with~1escapes",
	} {
		p, err := hew.ParsePath(s)
		if err != nil {
			t.Errorf("ParsePath(%q): %v", s, err)
			continue
		}
		if got := p.String(); got != s {
			t.Errorf("ParsePath(%q).String() = %q", s, got)
		}
	}
}

func TestClaimedSegmentsCarryTheirForm(t *testing.T) {
	for _, c := range []struct {
		path string
		form string
	}{
		{"/# Setup", FormHeading},
		{"/#### Deep", FormHeading},
		{"/code:0", FormBlock},
		{"/table:11", FormBlock},
		{"/@managed", FormMarker},
	} {
		p, err := hew.ParsePath(c.path)
		if err != nil {
			t.Fatalf("ParsePath(%q): %v", c.path, err)
		}
		seg := p.Segment(0)
		if seg.Kind != hew.SegExtension {
			t.Errorf("%q: kind = %s, want an extension-claimed segment", c.path, seg.Kind)
		}
		if seg.Form != c.form {
			t.Errorf("%q: form = %q, want %q", c.path, seg.Form, c.form)
		}
		if seg.Raw != strings.TrimPrefix(c.path, "/") {
			t.Errorf("%q: raw = %q, want the token as authored", c.path, seg.Raw)
		}
	}
}

// TestUnclaimedTokensStayKeys is the other half, and the one that keeps real
// documents patchable: a token that only LOOKS like one of these shapes is an
// ordinary key, so "port:0" and "#foo" address what they say.
func TestUnclaimedTokensStayKeys(t *testing.T) {
	for _, body := range []string{"##0", "#foo", "para:x", "notablock:0", "para:", "#", "8080x"} {
		p, err := hew.ParsePath("/" + body)
		if err != nil {
			t.Errorf("ParsePath(/%s): %v", body, err)
			continue
		}
		if k := p.Segment(0).Kind; k != hew.SegKey {
			t.Errorf("/%s parsed as %s, want an ordinary key", body, k)
		}
	}
}

// TestMalformedClaimsAreRefused pins the third answer a SegmentForm can give.
// "@" is claimed and rejected rather than quietly demoted to a key, because a
// nameless marker is a typo and a key named "@" would fail much later, as a
// resolution miss that names nothing useful.
func TestMalformedClaimsAreRefused(t *testing.T) {
	for _, c := range []struct{ path, want string }{
		{"/@", "marker segment requires a name"},
		{"/# a~2b", `invalid escape "~2"`},
		{"/# a~9b", `invalid escape "~9"`},
		{"/# a~", `dangling "~" escape`},
		{"/@a~2b", `invalid escape "~2"`},
	} {
		_, err := hew.ParsePath(c.path)
		if err == nil {
			t.Errorf("ParsePath(%q) succeeded; want a refusal", c.path)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("ParsePath(%q) error %q does not contain %q", c.path, err, c.want)
		}
	}
}

func TestBlockOrdinalOutOfRange(t *testing.T) {
	_, err := hew.ParsePath("/code:99999999999999999999999")
	if err == nil || !strings.Contains(err.Error(), "block ordinal out of range") {
		t.Fatalf("err = %v, want an out-of-range block ordinal", err)
	}
}

// --- constructors and accessors ---------------------------------------------

func TestHeadingRoundTripsThroughTheGrammar(t *testing.T) {
	for _, c := range []struct {
		level int
		text  string
	}{
		{1, "Setup"},
		{2, ""},
		{3, "a/b"},
		{4, "a~b"},
		{1, "has = signs"},
	} {
		seg := Heading(c.level, c.text)
		p, err := hew.ParsePath("/" + seg.String())
		if err != nil {
			t.Fatalf("Heading(%d, %q) does not parse: %v", c.level, c.text, err)
		}
		if !p.Segment(0).Equal(seg) {
			t.Fatalf("Heading(%d, %q) = %+v, reparsed as %+v", c.level, c.text, seg, p.Segment(0))
		}
		level, text, ok := ParseHeading(seg)
		if !ok || level != c.level || text != c.text {
			t.Fatalf("ParseHeading = (%d, %q, %v), want (%d, %q, true)", level, text, ok, c.level, c.text)
		}
	}
}

func TestBlockAndMarkerRoundTrip(t *testing.T) {
	blk := Block(BlockCode, 2)
	if blk.String() != "code:2" {
		t.Fatalf("Block spelling = %q", blk.String())
	}
	if kind, i, ok := ParseBlock(blk); !ok || kind != BlockCode || i != 2 {
		t.Fatalf("ParseBlock = (%q, %d, %v)", kind, i, ok)
	}

	mk := Marker("ctxloom:context")
	if mk.String() != "@ctxloom:context" {
		t.Fatalf("Marker spelling = %q", mk.String())
	}
	if name, ok := ParseMarker(mk); !ok || name != "ctxloom:context" {
		t.Fatalf("ParseMarker = (%q, %v)", name, ok)
	}
	if slashed := Marker("a/b"); slashed.String() != "@a~1b" {
		t.Fatalf("a marker name containing / must escape it, got %q", slashed.String())
	}
}

func TestAccessorsRejectForeignSegments(t *testing.T) {
	key := hew.Segment{Kind: hew.SegKey, Name: "# Setup"}
	if _, _, ok := ParseHeading(key); ok {
		t.Error("ParseHeading accepted a key segment")
	}
	if _, _, ok := ParseBlock(key); ok {
		t.Error("ParseBlock accepted a key segment")
	}
	if _, ok := ParseMarker(key); ok {
		t.Error("ParseMarker accepted a key segment")
	}
	// A segment of the right kind but another extension's form is equally not
	// ours: Form is what distinguishes two claims, not Kind.
	other := hew.Segment{Kind: hew.SegExtension, Form: "stanza", Raw: "[server]"}
	if _, _, ok := ParseHeading(other); ok {
		t.Error("ParseHeading accepted another extension's form")
	}
}

func TestAllBlockKindsAreClaimed(t *testing.T) {
	for _, k := range []BlockKind{BlockPara, BlockCode, BlockList, BlockTable, BlockQuote, BlockHTML} {
		seg := Block(k, 0)
		p, err := hew.ParsePath("/" + seg.String())
		if err != nil {
			t.Fatalf("%q: %v", seg.String(), err)
		}
		if got := p.Segment(0); got.Form != FormBlock || !got.Equal(seg) {
			t.Fatalf("%q parsed as %+v", seg.String(), got)
		}
	}
}

// --- the spellability guard against a claimed shape --------------------------
//
// The live O41 case belongs here rather than in the core suite: "@scope/pkg" is
// a key any package.json can hold, and it is unspellable precisely BECAUSE this
// extension claims "@…". Link no Markdown and the collision does not exist;
// link it and the guard must refuse, never misapply (§9.3).

func TestEmittingSeamsRefuseKeysThatCollideWithAClaimedShape(t *testing.T) {
	for _, key := range []string{"@scope/pkg", "@", "# Setup", "para:0", "code:12"} {
		tl := hew.TransformList{
			Target: "notes.md", Format: hew.FormatMarkdown,
			Transform: []hew.Transform{{
				Op:    hew.OpTest,
				Path:  hew.NewPath(hew.Segment{Kind: hew.SegKey, Name: key}),
				Value: mustValue(t, "1.0.0"),
			}},
		}
		if _, err := hew.MarshalTransforms(tl); !isInexpressible(err) {
			t.Errorf("MarshalTransforms accepted the unspellable key %q: %v", key, err)
		}
		if _, err := hew.Render(tl, hew.RenderOptions{}); !isInexpressible(err) {
			t.Errorf("Render accepted the unspellable key %q: %v", key, err)
		}
	}
}

func TestRefusalNamesTheClaimedForm(t *testing.T) {
	tl := hew.TransformList{
		Target: "notes.md", Format: hew.FormatMarkdown,
		Transform: []hew.Transform{{
			Op:    hew.OpTest,
			Path:  hew.NewPath(hew.Segment{Kind: hew.SegKey, Name: "@scope/pkg"}),
			Value: mustValue(t, "1.0.0"),
		}},
	}
	_, err := hew.MarshalTransforms(tl)
	he, ok := hewerr.As(err)
	if !ok {
		t.Fatalf("want a *hewerr.Error, got %v", err)
	}
	// "extension" would name the mechanism; the reviewer needs the shape.
	if !strings.Contains(he.Detail, "re-reads as a marker segment") {
		t.Fatalf("detail %q does not name the form the spelling re-reads as", he.Detail)
	}
}

func TestKeysThatOnlyLookLikeAClaimedShapeStillEmit(t *testing.T) {
	for _, key := range []string{"##0", "#foo", "para:x", "notablock:0", "8080x"} {
		tl := hew.TransformList{
			Target: "notes.md", Format: hew.FormatMarkdown,
			Transform: []hew.Transform{{
				Op:    hew.OpTest,
				Path:  hew.NewPath(hew.Segment{Kind: hew.SegKey, Name: key}),
				Value: mustValue(t, "1.0.0"),
			}},
		}
		if _, err := hew.MarshalTransforms(tl); err != nil {
			t.Errorf("MarshalTransforms refused the spellable key %q: %v", key, err)
		}
	}
}

func isInexpressible(err error) bool {
	he, ok := hewerr.As(err)
	return ok && he.Code == hewerr.CodeInexpressible
}

func mustValue(t *testing.T, s string) hew.Value {
	t.Helper()
	v, err := hew.ValueOf(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
