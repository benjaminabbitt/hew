package markdown

import (
	"strings"
	"testing"

	hew "github.com/benjaminabbitt/hew/go"
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

// TestAccessorsRejectMalformedRaw covers the segments a PROGRAM can build that
// the grammar would never produce: Segment is an exported struct, so Raw can
// hold anything, and an accessor must report "not mine" rather than trust the
// Form label and hand back a decoded lie.
func TestAccessorsRejectMalformedRaw(t *testing.T) {
	for _, raw := range []string{"", "#", "###", "nospace", "# a~2b", "# a~"} {
		if _, _, ok := ParseHeading(hew.Segment{Kind: hew.SegExtension, Form: FormHeading, Raw: raw}); ok {
			t.Errorf("ParseHeading accepted the malformed raw %q", raw)
		}
	}
	for _, raw := range []string{"", "code", "nope:0", "code:x", "code:99999999999999999999"} {
		if _, _, ok := ParseBlock(hew.Segment{Kind: hew.SegExtension, Form: FormBlock, Raw: raw}); ok {
			t.Errorf("ParseBlock accepted the malformed raw %q", raw)
		}
	}
	for _, raw := range []string{"", "@", "@a~2b", "@a~"} {
		if _, ok := ParseMarker(hew.Segment{Kind: hew.SegExtension, Form: FormMarker, Raw: raw}); ok {
			t.Errorf("ParseMarker accepted the malformed raw %q", raw)
		}
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

// --- a key that collides with a claimed shape (O41) --------------------------
//
// This is where the live O41 case belonged while the guard existed:
// "@scope/pkg" is a key any package.json can hold, and it was unspellable
// precisely BECAUSE this extension claims "@…", so the emitting seams refused
// the whole patch rather than write an address that read back as a marker.
//
// The ruling replaced the refusal with a spelling. These tests are the same
// cases read the other way up: the key must SURVIVE, in a build that links this
// grammar, and it must not be confused with the marker it is spelled like.

func TestKeysThatCollideWithAClaimedShapeRoundTrip(t *testing.T) {
	for _, key := range []string{"@scope/pkg", "@", "# Setup", "para:0", "code:12"} {
		p := hew.NewPath(hew.Segment{Kind: hew.SegKey, Name: key})
		tl := hew.TransformList{
			Target: "notes.md", Format: hew.FormatMarkdown,
			Transform: []hew.Transform{{Op: hew.OpTest, Path: p, Value: mustValue(t, "1.0.0")}},
		}
		out, err := hew.MarshalTransforms(tl)
		if err != nil {
			t.Errorf("MarshalTransforms refused the key %q: %v", key, err)
			continue
		}
		if !strings.Contains(string(out), `"`+key+`"`) {
			t.Errorf("the .hewt for %q does not carry its literal spelling:\n%s", key, out)
		}
		if _, err := hew.Render(tl, hew.RenderOptions{}); err != nil {
			t.Errorf("Render refused the key %q: %v", key, err)
		}
		// The whole point: it reads back as a KEY, not as this extension's form.
		back, perr := hew.ParsePath(p.String())
		if perr != nil || !back.Equal(p) {
			t.Errorf("%q rendered as %q and did not read back: %v", key, p.String(), perr)
		}
		if got := back.Segment(0); got.Kind != hew.SegKey || got.Name != key {
			t.Errorf("%q read back as %+v", key, got)
		}
	}
}

// TestTheClaimedShapeItselfStillParses is the other side of the same coin: the
// spelling WITHOUT quotes is still this extension's, and quoting is what tells
// the two apart.
func TestTheClaimedShapeItselfStillParses(t *testing.T) {
	for _, raw := range []string{"@name", "# Setup", "para:0"} {
		p, err := hew.ParsePath("/" + raw)
		if err != nil {
			t.Fatalf("ParsePath(/%s): %v", raw, err)
		}
		if got := p.Segment(0); got.Kind != hew.SegExtension || got.Raw != raw {
			t.Errorf("/%s parsed as %+v, want this extension's claim", raw, got)
		}
		q, err := hew.ParsePath(`/"` + raw + `"`)
		if err != nil {
			t.Fatalf(`ParsePath(/"%s"): %v`, raw, err)
		}
		if got := q.Segment(0); got.Kind != hew.SegKey || got.Name != raw {
			t.Errorf(`/"%s" parsed as %+v, want the literal key`, raw, got)
		}
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

func mustValue(t *testing.T, s string) hew.Value {
	t.Helper()
	v, err := hew.ValueOf(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
