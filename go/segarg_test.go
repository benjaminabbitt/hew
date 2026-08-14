package hew

import (
	"strings"
	"testing"

	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

// Rule 2 and O43: addressing is the §4 path, and a runtime value enters it as
// a typed SEGMENT — never as text spliced into a pattern. These tests are the
// invariant stated as assertions: user data supplied through a constructor is
// never parsed as path text, so it cannot introduce a segment boundary, a
// key-match, an ordinal or an optional marker.

func seg(t *testing.T, a SegmentArg) Segment {
	t.Helper()
	return a.segmentArg()
}

func TestSegmentArgConstructors(t *testing.T) {
	for _, tc := range []struct {
		name string
		arg  SegmentArg
		want Segment
	}{
		{"key", Key("mcpServers"), Segment{Kind: SegKey, Name: "mcpServers"}},
		{"key is opaque data", Key("a/b=c?"), Segment{Kind: SegKey, Name: "a/b=c?"}},
		{"index", Index(2), Segment{Kind: SegIndex, Index: 2}},
		{"append", Append(), Segment{Kind: SegAppend}},
		{"match key", MatchKey("name", "github"),
			Segment{Kind: SegMatch, Name: "name", Value: Scalar{Kind: ScalarString, Text: "github", Quoted: true}}},
		{"match key number", MatchKeyNumber("port", "8080"),
			Segment{Kind: SegMatch, Name: "port", Value: Scalar{Kind: ScalarNumber, Text: "8080"}}},
		{"match key bool", MatchKeyBool("enabled", true),
			Segment{Kind: SegMatch, Name: "enabled", Value: Scalar{Kind: ScalarBool, Text: "true"}}},
		{"match key bool false", MatchKeyBool("enabled", false),
			Segment{Kind: SegMatch, Name: "enabled", Value: Scalar{Kind: ScalarBool, Text: "false"}}},
		{"match key null", MatchKeyNull("owner"),
			Segment{Kind: SegMatch, Name: "owner", Value: Scalar{Kind: ScalarNull, Text: "null"}}},
		{"match value", MatchValue("alpha"),
			Segment{Kind: SegMatch, Value: Scalar{Kind: ScalarString, Text: "alpha", Quoted: true}}},
		{"match value number", MatchValueNumber("3"),
			Segment{Kind: SegMatch, Value: Scalar{Kind: ScalarNumber, Text: "3"}}},
		{"quoted", Quoted("google"), Segment{Kind: SegKey, Name: "google", Quoted: true}},
		{"comment", Comment(1), Segment{Kind: SegComment, Index: 1}},
		{"trailing comment", TrailingComment(), Segment{Kind: SegComment, Trailing: true}},
		{"optional", Optional(Key("tls")), Segment{Kind: SegKey, Name: "tls", Optional: true}},
		{"optional over a match", Optional(MatchKey("name", "x")),
			Segment{Kind: SegMatch, Name: "name", Value: Scalar{Kind: ScalarString, Text: "x", Quoted: true}, Optional: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := seg(t, tc.arg); !got.Equal(tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

// A Segment is itself a SegmentArg: the constructors are the TYPED entry
// points, and the model underneath is the same one the differ and the parser
// build. This is what keeps NewPath's ratified signature change (review point
// 20) from being a rewrite of every caller in the module.
func TestSegmentIsItsOwnArg(t *testing.T) {
	s := Segment{Kind: SegKey, Name: "port"}
	if got := seg(t, s); !got.Equal(s) {
		t.Fatalf("Segment as SegmentArg: %+v", got)
	}
}

// O42: MatchKey always produces a QUOTED string scalar, so a value that looks
// numeric still addresses the string, and the comparison's type is visible at
// the construction site.
func TestMatchKeyAlwaysQuotesItsValue(t *testing.T) {
	p := NewPath(Key("servers"), MatchKey("port", "8080"))
	if got, want := p.String(), `/servers/port="8080"`; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
	back, err := ParsePath(p.String())
	if err != nil {
		t.Fatal(err)
	}
	if !back.Equal(p) {
		t.Fatalf("re-parsed as %+v", back.Segment(1))
	}
	if got := MatchKeyNumber("port", "8080"); seg(t, got).Value.Kind != ScalarNumber {
		t.Fatal("MatchKeyNumber did not produce a number")
	}
}

func TestNewPathFromArgs(t *testing.T) {
	p := NewPath(Key("mcpServers"), MatchKey("name", "github"), Key("command"))
	if got, want := p.String(), `/mcpServers/name="github"/command`; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
	if p.Len() != 3 {
		t.Fatalf("Len() = %d", p.Len())
	}
}

func TestNewPathWithNoArgsIsRoot(t *testing.T) {
	if p := NewPath(); !p.Equal(RootPath()) {
		t.Fatalf("NewPath() = %+v, want the root path", p)
	}
}

// --- At: the pattern language (§4 plus holes) --------------------------------

func atDoc(t *testing.T) *Doc {
	t.Helper()
	toyOnly(t)
	d, err := OpenBytes("config.toy", []byte(toySrc))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestAtParsesALiteralPattern(t *testing.T) {
	d := atDoc(t)
	want := MustParsePath(`/mcpServers/name="github"/command`)
	if got := d.At(`/mcpServers/name="github"/command`).path; !got.Equal(want) {
		t.Fatalf("At: %+v", got)
	}
	if d.err != nil {
		t.Fatal(d.err)
	}
}

// One language, two encodings: At(s) is AtPath(ParsePath(s)) for every pattern
// with no holes. This is the identity A.0 states, pinned so the pattern
// language cannot drift away from §4 by accident.
func TestAtIsAtPathOfParsePath(t *testing.T) {
	d := atDoc(t)
	for _, s := range []string{
		"/", "/port", "/a/b/c", "/servers/0", "/servers/-", `/servers/name="x"`,
		"/servers/port=8080", `/provider/"google"`, "/list/#1", "/list/#t",
		"/deps/@scope~1pkg", "/a/b?", "/a~0b/c~1d",
	} {
		want, err := ParsePath(s)
		if err != nil {
			t.Fatalf("%q: %v", s, err)
		}
		if got := d.At(s).path; !got.Equal(want) {
			t.Fatalf("At(%q) = %+v, want %+v", s, got, want)
		}
	}
	if d.err != nil {
		t.Fatalf("a well-formed pattern latched an error: %v", d.err)
	}
}

func TestAtFillsHolesPositionally(t *testing.T) {
	d := atDoc(t)
	got := d.At("/{}/{}/command", Key("mcpServers"), MatchKey("name", "github")).path
	want := NewPath(Key("mcpServers"), MatchKey("name", "github"), Key("command"))
	if !got.Equal(want) {
		t.Fatalf("At = %q, want %q", got.String(), want.String())
	}
}

// The invariant, as a test: a hole is filled with a STRUCTURAL segment, so a
// value carrying every metacharacter §4 has still names exactly one segment.
func TestHoleArgumentIsNeverParsedAsPathText(t *testing.T) {
	d := atDoc(t)
	for _, hostile := range []string{
		"a/b", "8080", "-", "name=github", `"quoted"`, "tls?", "x[0]", "#1", "{}", "",
	} {
		p := d.At("/deps/{}", Key(hostile)).path
		if p.Len() != 2 {
			t.Fatalf("Key(%q) produced %d segments", hostile, p.Len())
		}
		s := p.Segment(1)
		if s.Kind != SegKey || s.Name != hostile || s.Optional {
			t.Fatalf("Key(%q) became %+v", hostile, s)
		}
	}
	if d.err != nil {
		t.Fatal(d.err)
	}
}

func TestAtHoleCountMismatchIsHEW001(t *testing.T) {
	for _, tc := range []struct {
		name    string
		pattern string
		args    []SegmentArg
	}{
		{"too few", "/a/{}/{}", []SegmentArg{Key("x")}},
		{"too many", "/a/{}", []SegmentArg{Key("x"), Key("y")}},
		{"none wanted", "/a/b", []SegmentArg{Key("x")}},
		{"none given", "/a/{}", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := atDoc(t)
			d.At(tc.pattern, tc.args...)
			he := wantCode(t, d.err, hewerr.CodeParse)
			if he.Component != hewerr.ComponentParser {
				t.Fatalf("component = %v", he.Component)
			}
			if he.Path != tc.pattern {
				t.Fatalf("error does not name the pattern: %q", he.Path)
			}
		})
	}
}

// A hole/argument mismatch is an immediate error and NOT a partial path: the
// selection is dead, and the error is the one the terminal reports.
func TestAtErrorLeavesNoPartialPath(t *testing.T) {
	d := atDoc(t)
	s := d.At("/a/{}")
	if !s.dead {
		t.Fatal("a failed At returned a live selection")
	}
	if !s.path.IsZero() {
		t.Fatalf("a failed At produced a path: %q", s.path.String())
	}
}

func TestAtMalformedPatternIsHEW001(t *testing.T) {
	for _, pattern := range []string{
		"", "no-leading-slash", `/a/"unterminated`, "/a/#99999999999999999999",
	} {
		d := atDoc(t)
		d.At(pattern)
		he := wantCode(t, d.err, hewerr.CodeParse)
		if he.Path != pattern {
			t.Fatalf("error names %q, want the pattern %q", he.Path, pattern)
		}
	}
}

// The one place the pattern language diverges from §4, and it diverges by
// addition only: "{}" is a legal KEY spelling in bare §4, so a document with a
// literal "{}" key needs the quoted form or AtPath.
func TestLiteralBracesKeyNeedsTheQuotedFormOrAtPath(t *testing.T) {
	d := atDoc(t)
	// Written bare, it is a hole and takes an argument.
	if p := d.At("/x/{}", Key("filled")).path; p.Segment(1).Name != "filled" {
		t.Fatalf("bare {} was not a hole: %+v", p.Segment(1))
	}
	// Written quoted, it is the literal key.
	q := d.At(`/x/"{}"`).path
	if got := q.Segment(1); got.Name != "{}" {
		t.Fatalf("quoted {} = %+v", got)
	}
	// Or built structurally, with no pattern at all.
	if got := d.AtPath(NewPath(Key("x"), Key("{}"))).path.Segment(1); got.Kind != SegKey || got.Name != "{}" {
		t.Fatalf("AtPath: %+v", got)
	}
	if d.err != nil {
		t.Fatal(d.err)
	}
}

func TestAtRejectsAPatternCarryingTheHoleSentinel(t *testing.T) {
	d := atDoc(t)
	d.At("/a/\x000")
	wantCode(t, d.err, hewerr.CodeParse)
}

func TestAtRejectsAnOptionalHoleOffTheEnd(t *testing.T) {
	d := atDoc(t)
	d.At("/{}/b", Optional(Key("a")))
	he := wantCode(t, d.err, hewerr.CodeParse)
	if !strings.Contains(he.Detail, "last segment") {
		t.Fatalf("detail = %q", he.Detail)
	}
}

func TestAtPathRejectsTheAbsentPath(t *testing.T) {
	d := atDoc(t)
	d.AtPath(Path{})
	wantCode(t, d.err, hewerr.CodeParse)
}

// Error latching, §10.4: the first error wins and later calls are no-ops.
func TestFirstAtErrorWins(t *testing.T) {
	d := atDoc(t)
	d.At("/a/{}")            // HEW001: hole count
	d.At("no-leading-slash") // would also be HEW001, at a different path
	he := wantCode(t, d.err, hewerr.CodeParse)
	if he.Path != "/a/{}" {
		t.Fatalf("the second error overwrote the first: %q", he.Path)
	}
}

// The quoted-key GRAMMAR is p5/quoted-keys' work (O41/O48). What this package
// owns is the STRUCTURAL side, tested above. This pins the rendering half —
// that a hole's data survives print/parse — and is skipped until the bijection
// lands.
func TestHoleArgumentSurvivesRendering(t *testing.T) {
	for _, hostile := range []string{"@scope/pkg", "8080", "-", "", "{}", "a=b"} {
		p := NewPath(Key("deps"), Key(hostile))
		back, err := ParsePath(p.String())
		if err != nil {
			t.Fatalf("Key(%q) rendered %q, which does not parse: %v", hostile, p.String(), err)
		}
		if !back.Equal(p) {
			t.Fatalf("Key(%q) rendered %q, which re-reads as %+v", hostile, p.String(), back.Segment(1))
		}
	}
}
