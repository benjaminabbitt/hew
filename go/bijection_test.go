package hew

import (
	"strconv"
	"strings"
	"testing"
)

// The bijection (§4.1, O41).
//
//	String() and ParsePath are a bijection on keys. Rendering a path and
//	reparsing it MUST yield the identical path, for every key any target
//	document can contain.
//
// This file is that sentence as a test. It replaces the spellability GUARD's
// suites, which pinned the opposite behaviour — a key the grammar could not
// spell was refused at every emitting seam — because the quoted form spells
// every key and the canonical-rendering rule emits it. A key must now round
// trip, not be refused, and the tables below are the old guard's tables read
// the other way up.
//
// The generator is adversarial on purpose: every class §4.1 enumerates, every
// class the old guard refused, every character the two escaping layers
// (~0/~1/~2 outside quotes, \" and \\ inside) can act on, and the shapes each
// extension-claimed form would take. If any of these keys did not survive, a
// package.json, a tsconfig.json or a port map would produce a transform list
// whose addresses mean something else — which is the live defect O41 closed.

// adversarialKeys is the generator. Every entry is a key some real document
// holds, or a key that probes a boundary of the grammar.
func adversarialKeys() []string {
	return []string{
		// --- ordinary --------------------------------------------------------
		"server", "timeout", "left-pad", "a_b", "Ünïcøde", "key with spaces",
		"UPPER", "x", strings.Repeat("long", 40),

		// --- the classes §4.1 enumerates -------------------------------------
		"",                 // RFC 6901's empty-key member, which had no bare spelling at all
		"8080", "0", "007", // indices
		"-",                                   // the append position
		"@scope/pkg", "@ctxloom:context", "@", // markers (§4.5)
		"# Setup", "## Install", "#0", "#12", "#t", "#foo", "##0", // headings and comment addresses
		`"quoted"`, `"unterminated`, // a key that opens with the literal form's own quote
		"code:0", "para:1", "list:12", "notablock:0", "para:x", // block ordinals (§4.5)
		"opt?", "?", "a?b", // the optional flag (§4.4)
		"a[1]", "a[]", "[0]", "a[1]b", // the IR-only [n] selector
		"*", "**", "a*b", // O44's wildcard reservation

		// --- what the escapes act on ------------------------------------------
		"a/b", "a~b", "a=b", "a/b~c=d", "~", "~0", "~1", "~2", "~9",
		"/", "//", "a//b", "=", "==", "a~1b", "@scope~1pkg",
		`back\slash`, `both"and\`, `\`, `"`, `\"`,

		// --- key-match shapes, as KEYS ----------------------------------------
		"name=github", "count>", "count<", "count!", "count>=5", "a=b=c",

		// --- shapes that only look dangerous ----------------------------------
		"08080", "8080x", "x8080", "-x", "x-", "1.0", "1e9", "true", "false", "null",
		"{}", "{", "}", " ", "  ", " leading", "trailing ",
	}
}

// reparse is the oracle: render, read back, and report what came out.
func reparse(t *testing.T, p Path) Path {
	t.Helper()
	got, err := ParsePath(p.String())
	if err != nil {
		t.Fatalf("ParsePath(%q) failed for a path hew itself rendered: %v", p.String(), err)
	}
	return got
}

// TestKeyBijection is the ruling itself: every key round-trips, in every
// position a key can occupy.
func TestKeyBijection(t *testing.T) {
	for _, key := range adversarialKeys() {
		t.Run(strconv.Quote(key), func(t *testing.T) {
			paths := []Path{
				NewPath(Segment{Kind: SegKey, Name: key}),
				NewPath(Segment{Kind: SegKey, Name: "deps"}, Segment{Kind: SegKey, Name: key}),
				NewPath(Segment{Kind: SegKey, Name: key}, Segment{Kind: SegKey, Name: "version"}),
				NewPath(Segment{Kind: SegKey, Name: key}, Segment{Kind: SegKey, Name: key}),
				NewRelativePath(Segment{Kind: SegKey, Name: key}),
				NewPath(Segment{Kind: SegKey, Name: key, Optional: true}),
				NewPath(Segment{Kind: SegKey, Name: key, Ordinal: ordinalPtr(2)}),
				// The same key as a key-match FIELD and as a match VALUE.
				NewPath(Segment{Kind: SegMatch, Name: key, Value: Scalar{Kind: ScalarString, Text: "v"}}),
				NewPath(Segment{Kind: SegMatch, Name: "name", Value: Scalar{Kind: ScalarString, Text: key}}),
				NewPath(Segment{Kind: SegMatch, Value: Scalar{Kind: ScalarString, Text: key}}),
			}
			for _, p := range paths {
				got := reparse(t, p)
				if !p.Equal(got) {
					t.Errorf("%q rendered as %q and read back as %q (%+v)", key, p.String(), got.String(), got.Segments())
				}
				// Equality is not enough on its own: the DATA has to survive
				// too, or two keys could round-trip onto each other.
				for i := 0; i < got.Len(); i++ {
					s, want := got.Segment(i), p.Segment(i)
					if s.Name != want.Name || s.Value.Text != want.Value.Text {
						t.Errorf("%q: segment %d came back as name=%q value=%q", key, i, s.Name, s.Value.Text)
					}
				}
			}
		})
	}
}

// TestKeyBijectionIsInjective is the other half of "bijection": two different
// keys must never render to one spelling. A rendering that collapsed
// `/x/"8080"` and `/x/8080` would round-trip each path and still be wrong.
func TestKeyBijectionIsInjective(t *testing.T) {
	seen := map[string]string{}
	for _, key := range adversarialKeys() {
		s := NewPath(Segment{Kind: SegKey, Name: "x"}, Segment{Kind: SegKey, Name: key}).String()
		if prev, dup := seen[s]; dup {
			t.Errorf("keys %q and %q both render as %q", prev, key, s)
		}
		seen[s] = key
	}
}

// TestQuotedSpellingIsIdempotent pins that the canonical rendering is a FIXED
// POINT: re-rendering what was parsed changes nothing, so a .hewt written,
// read and written again is byte-identical (§9.6's determinism rests on it).
func TestQuotedSpellingIsIdempotent(t *testing.T) {
	for _, key := range adversarialKeys() {
		p := NewPath(Segment{Kind: SegKey, Name: "x"}, Segment{Kind: SegKey, Name: key})
		once := p.String()
		twice := reparse(t, p).String()
		if once != twice {
			t.Errorf("key %q: first rendering %q, second %q", key, once, twice)
		}
	}
}

// TestCanonicalRenderingQuotesExactlyTheEnumeratedClasses pins §4.1's
// enumeration, so that the rule stays readable as a rule and not only as a
// property. Quoting more than this would be harmless for the bijection and
// noisy for a reviewer, which is why the negative half is here too.
func TestCanonicalRenderingQuotesExactlyTheEnumeratedClasses(t *testing.T) {
	// "entirely digits", not "would parse as an index": §4.1 names the class by
	// its SHAPE, so "08080" is quoted even though RFC 6901's index production
	// would not have taken it.
	quoted := []string{"", "-", "*", "8080", "0", "007", "08080", "@scope/pkg", "#0", "#t",
		"# Setup", `"q"`, "code:0", "notablock:0", "a1:0", "_x-y:12", "opt?", "a[1]", "a[12]"}
	bare := []string{"server", "left-pad", "8080x", "a/b", "a~b", "a=b",
		"para:x", "a[]", "a[1", "0:0", "1a:0", ":0", "a:", "a:b", "-x", "true", "1.0", "{}"}
	for _, k := range quoted {
		if got := (Segment{Kind: SegKey, Name: k}).String(); !strings.HasPrefix(got, `"`) {
			t.Errorf("key %q renders bare as %q; §4.1 requires the quoted form", k, got)
		}
	}
	for _, k := range bare {
		if got := (Segment{Kind: SegKey, Name: k}).String(); strings.HasPrefix(got, `"`) {
			t.Errorf("key %q renders quoted as %q; §4.1's rule is not a licence to quote everything", k, got)
		}
	}
}

// TestQuotedSegmentSpellsTheSpecsExamples runs §4.1's own table.
func TestQuotedSegmentSpellsTheSpecsExamples(t *testing.T) {
	tests := []struct {
		path string
		key  string
	}{
		{`/dependencies/"@scope/pkg"`, "@scope/pkg"}, // NOT a marker segment
		{`/versions/"8080"`, "8080"},                 // NOT an index
		{`/flags/"-"`, "-"},                          // NOT the append position
		{`/paths/"*"`, "*"},                          // a real tsconfig key (§4.7)
		{`/x/""`, ""},                                // RFC 6901's empty-key member
		{`/x/"code:0"`, "code:0"},                    // NOT a block ordinal
		{`/x/"a?"`, "a?"},                            // NOT an optional segment
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			p, err := ParsePath(tc.path)
			if err != nil {
				t.Fatalf("ParsePath(%q): %v", tc.path, err)
			}
			if p.Len() != 2 {
				t.Fatalf("%q parsed into %d segments, want 2 (a quoted \"/\" is literal)", tc.path, p.Len())
			}
			last := p.Segment(1)
			if last.Kind != SegKey || last.Name != tc.key || !last.IsQuoted() {
				t.Fatalf("%q's last segment is %+v, want the quoted key %q", tc.path, last, tc.key)
			}
			if last.Optional {
				t.Errorf("%q: the trailing %q is inside the quotes and is not the optional flag (§4.4)", tc.path, "?")
			}
			if got := p.String(); got != tc.path {
				t.Errorf("re-rendered as %q, want %q", got, tc.path)
			}
		})
	}
}

// TestQuoteAwareSplitting pins the defect §4 says an implementation reaches for
// first: splitting on "/" before honouring the quotes.
func TestQuoteAwareSplitting(t *testing.T) {
	tests := []struct {
		path string
		want []string // the decoded name of each segment
	}{
		{`/dependencies/"@scope/pkg"`, []string{"dependencies", "@scope/pkg"}},
		{`/a/"x/y/z"/b`, []string{"a", "x/y/z", "b"}},
		{`/a/"x/y"/"p/q"`, []string{"a", "x/y", "p/q"}},
		{`/tool/x/id="a/b"`, []string{"tool", "x", "id"}},
		{`/a/b"c`, []string{"a", `b"c`}}, // a quote that opens nothing
		{`/a/"esc\"aped/x"`, []string{"a", `esc"aped/x`}},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			p, err := ParsePath(tc.path)
			if err != nil {
				t.Fatalf("ParsePath(%q): %v", tc.path, err)
			}
			if p.Len() != len(tc.want) {
				t.Fatalf("%q split into %d segments (%v), want %d", tc.path, p.Len(), p.Segments(), len(tc.want))
			}
			for i, want := range tc.want {
				if got := p.Segment(i).Name; got != want {
					t.Errorf("segment %d is %q, want %q", i, got, want)
				}
			}
		})
	}
}

// TestQuotedFieldName pins §4.2's "a field name may itself be quoted when its
// bare spelling would not survive".
func TestQuotedFieldName(t *testing.T) {
	const s = `/deps/"@scope/pkg"=1.0.0`
	p, err := ParsePath(s)
	if err != nil {
		t.Fatalf("ParsePath(%q): %v", s, err)
	}
	seg := p.Segment(1)
	if seg.Kind != SegMatch || seg.Name != "@scope/pkg" || seg.Value.Kind != ScalarString || seg.Value.Text != "1.0.0" {
		t.Fatalf("parsed %+v, want a key-match on the field %q against the string 1.0.0", seg, "@scope/pkg")
	}
	if got := p.String(); got != s {
		t.Errorf("re-rendered as %q, want %q", got, s)
	}
	// The empty-field form keeps its own spelling and is never quoted.
	empty := NewPath(Segment{Kind: SegKey, Name: "tags"},
		Segment{Kind: SegMatch, Value: Scalar{Kind: ScalarString, Text: "gamma"}})
	if got := empty.String(); got != "/tags/=gamma" {
		t.Errorf("the empty-field match renders as %q, want /tags/=gamma (§4.2)", got)
	}
	if _, err := ParsePath(`/tags/""=gamma`); err == nil {
		t.Error(`an empty QUOTED field must be refused; the empty-field form is /tags/=value`)
	}
}

// --- O42: the scalar half of the same rule ------------------------------------

// TestScalarForceQuoting is O42: the spelling of a match value carries its
// type, so a string whose bare rendering would re-decode as a number, a
// boolean, null or an empty token is force-quoted. A `name=8080` that meant
// the string addresses a different element, or none, and raises no error.
func TestScalarForceQuoting(t *testing.T) {
	forced := []string{"8080", "0", "-1", "1.5", "1e9", "true", "false", "null", "", `"x"`}
	for _, text := range forced {
		t.Run(strconv.Quote(text), func(t *testing.T) {
			seg := Segment{Kind: SegMatch, Name: "v", Value: Scalar{Kind: ScalarString, Text: text}}
			got := seg.String()
			if !strings.HasPrefix(got, `v="`) {
				t.Fatalf("the string scalar %q renders as %q; O42 requires the quoted form", text, got)
			}
			p := reparse(t, NewPath(seg))
			back := p.Segment(0).Value
			if back.Kind != ScalarString || back.Text != text {
				t.Fatalf("re-read as %v %q, want the string %q", back.Kind, back.Text, text)
			}
		})
	}
	// A string that cannot be mistaken keeps the bare spelling.
	seg := Segment{Kind: SegMatch, Name: "name", Value: Scalar{Kind: ScalarString, Text: "github"}}
	if got := seg.String(); got != "name=github" {
		t.Errorf("plain string value renders as %q, want name=github", got)
	}
}

// TestScalarKindsRoundTrip pins that the typed spellings survive: the number
// 8080 and the string "8080" are different addresses and stay different.
func TestScalarKindsRoundTrip(t *testing.T) {
	tests := []Scalar{
		{Kind: ScalarString, Text: "8080", Quoted: true},
		{Kind: ScalarString, Text: "8080"},
		{Kind: ScalarNumber, Text: "8080"},
		{Kind: ScalarNumber, Text: "-1.5e10"},
		{Kind: ScalarBool, Text: "true"},
		{Kind: ScalarBool, Text: "false"},
		{Kind: ScalarNull, Text: "null"},
		{Kind: ScalarString, Text: "a b"},
		{Kind: ScalarString, Text: "Bash(curl *)", Quoted: true},
	}
	for _, sc := range tests {
		t.Run(sc.Kind.String()+"/"+sc.Text, func(t *testing.T) {
			p := NewPath(Segment{Kind: SegMatch, Name: "f", Value: sc})
			got := reparse(t, p).Segment(0).Value
			if got.Kind != sc.Kind || got.Text != sc.Text {
				t.Errorf("%v %q re-read as %v %q (spelling %q)", sc.Kind, sc.Text, got.Kind, got.Text, p.String())
			}
		})
	}
}

// TestQuotingIsPresentationForScalars: the authored quoting survives a render
// (RT2 needs the bytes back), but it is not part of what a scalar ADDRESSES —
// the Kind already carries that.
func TestQuotingIsPresentationForScalars(t *testing.T) {
	authored := Segment{Kind: SegMatch, Name: "id", Value: Scalar{Kind: ScalarString, Text: "a b", Quoted: true}}
	built := Segment{Kind: SegMatch, Name: "id", Value: Scalar{Kind: ScalarString, Text: "a b"}}
	if !authored.Equal(built) {
		t.Error("a quoted and an unquoted string scalar address the same element and must compare equal")
	}
	if got := authored.String(); got != `id="a b"` {
		t.Errorf("authored quoting lost: %q", got)
	}
}

// --- the quoted segment against the two container kinds -----------------------

// TestQuotedIsCanonicalNotJustAuthored pins the rule Equal turns on: a key
// built as DATA and the same key read back from TEXT are one segment, because
// IsQuoted answers for the canonical spelling rather than for whoever built it.
func TestQuotedIsCanonicalNotJustAuthored(t *testing.T) {
	built := Segment{Kind: SegKey, Name: "8080"}
	read := Segment{Kind: SegKey, Name: "8080", Quoted: true}
	if !built.IsQuoted() {
		t.Error("a key whose bare spelling reads back as an index is quoted whether or not its builder said so")
	}
	if !built.Equal(read) {
		t.Error("the same key built as data and read from text must be one segment")
	}
	// Where the bare spelling DOES survive, the two spellings stay apart: that
	// is §4.3's label/attribute distinction, and it is the whole reason the
	// flag is compared at all.
	if (Segment{Kind: SegKey, Name: "aws", Quoted: true}).Equal(Segment{Kind: SegKey, Name: "aws"}) {
		t.Error(`/provider/"aws" is a label and /provider/aws is an attribute; they must not compare equal (§4.3)`)
	}
}

// --- O44: the reservations ----------------------------------------------------

// TestReservedTokens pins §4.7: both spellings are HEW001, both messages say
// "reserved", and both name the literal escape the quoted form provides.
func TestReservedTokens(t *testing.T) {
	tests := []struct {
		path  string
		names []string
	}{
		{"/servers/count>=5", []string{"reserved", "count>", `"count>"=5`}},
		{"/servers/count<=5", []string{"reserved", "count<"}},
		{"/servers/done!=true", []string{"reserved", "done!"}},
		{"/paths/*", []string{"reserved", `"*"`}},
		{"/paths/*/x", []string{"reserved", `"*"`}},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			_, err := ParsePath(tc.path)
			if err == nil {
				t.Fatalf("ParsePath(%q) must be HEW001: the spelling is reserved (§4.7)", tc.path)
			}
			for _, want := range tc.names {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("message %q does not name %q", err.Error(), want)
				}
			}
		})
	}
	// The escape hatch is what makes the reservation affordable: every
	// reserved token stays addressable as a literal.
	for _, s := range []string{`/servers/"count>"=5`, `/paths/"*"`, `/paths/"*"/x`} {
		p, err := ParsePath(s)
		if err != nil {
			t.Errorf("the literal spelling %s must still parse: %v", s, err)
			continue
		}
		if got := p.String(); got != s {
			t.Errorf("%s re-rendered as %s", s, got)
		}
	}
}

// TestReservedFieldRendersQuoted: a field name the grammar reserves has to
// render in its literal form, or the IR would emit an address hew itself
// refuses to read.
func TestReservedFieldRendersQuoted(t *testing.T) {
	seg := Segment{Kind: SegMatch, Name: "count>", Value: Scalar{Kind: ScalarNumber, Text: "5"}}
	if got := seg.String(); got != `"count>"=5` {
		t.Fatalf("rendered as %q, want the literal spelling \"count>\"=5", got)
	}
	if !NewPath(seg).spellable() {
		t.Error("a reserved field name must survive the round trip in its literal form")
	}
}

// --- the residue --------------------------------------------------------------
//
// What the quoted form still cannot spell, enumerated rather than discovered.

// TestLineBreakInAKeyIsTheOneRemainingHole: §4.1's quoted form escapes `\"`
// and `\\` and nothing else, and a path is written on one line — a `@@` header
// or a one-line .hewt scalar — so a key holding a newline has no spelling.
// This is the whole of what is left of the old guard.
func TestLineBreakInAKeyIsTheOneRemainingHole(t *testing.T) {
	for _, key := range []string{"a\nb", "a\rb", "\n"} {
		t.Run(strconv.Quote(key), func(t *testing.T) {
			p := NewPath(Segment{Kind: SegKey, Name: key})
			if p.spellable() {
				t.Fatalf("a key holding %q cannot be written on one line", key)
			}
			seg, bad := p.firstUnspellable()
			if !bad || seg.Name != key {
				t.Fatalf("firstUnspellable() = (%+v, %v), want the offending key", seg, bad)
			}
			if got := spellFailure(seg); !strings.Contains(got, "line break") {
				t.Errorf("spellFailure() = %q, want it to name the line break", got)
			}
		})
	}
}

// TestMalformedIRIsStillRefused: the rest of the residue is data that is
// malformed as DATA rather than as notation. No document can produce any of
// it; a caller building an IR by hand can.
func TestMalformedIRIsStillRefused(t *testing.T) {
	tests := []struct {
		name string
		seg  Segment
	}{
		{"negative index", Segment{Kind: SegIndex, Index: -1}},
		{"negative comment ordinal", Segment{Kind: SegComment, Index: -1}},
		{"number scalar that is not a number", Segment{Kind: SegMatch, Name: "mask", Value: Scalar{Kind: ScalarNumber, Text: "0x1f"}}},
		{"bool scalar that is not a bool", Segment{Kind: SegMatch, Name: "on", Value: Scalar{Kind: ScalarBool, Text: "True"}}},
		{"unclaimed extension token", Segment{Kind: SegExtension, Form: "heading", Raw: "# Setup"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if NewPath(tc.seg).spellable() {
				t.Errorf("%+v renders as %q and reads back as something else", tc.seg, tc.seg.String())
			}
		})
	}
}

// --- fuzz ---------------------------------------------------------------------

// FuzzKeyBijection is the property with the table taken away: for ANY string a
// document could use as a key, rendering and reparsing must return the same
// key. The seeds are the classes the table enumerates; the fuzzer's job is the
// ones nobody thought of.
func FuzzKeyBijection(f *testing.F) {
	for _, k := range adversarialKeys() {
		f.Add(k)
	}
	f.Fuzz(func(t *testing.T, key string) {
		if strings.ContainsAny(key, "\r\n") || !utf8Clean(key) {
			return // the enumerated residue, tested above
		}
		p := NewPath(Segment{Kind: SegKey, Name: "x"}, Segment{Kind: SegKey, Name: key})
		s := p.String()
		got, err := ParsePath(s)
		if err != nil {
			t.Fatalf("key %q rendered as %q, which hew cannot read back: %v", key, s, err)
		}
		if !p.Equal(got) {
			t.Fatalf("key %q rendered as %q and read back as %q", key, s, got.String())
		}
		if got.Segment(1).Name != key {
			t.Fatalf("key %q came back as %q", key, got.Segment(1).Name)
		}
	})
}

// FuzzScalarBijection is O42's half: a string match value must come back as
// the SAME string, never as a number, a boolean or null.
func FuzzScalarBijection(f *testing.F) {
	for _, seed := range []string{"github", "8080", "true", "null", "", "a b", "1.5", `"x"`, "a=b", "a/b"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		if strings.ContainsAny(text, "\r\n") || !utf8Clean(text) {
			return
		}
		p := NewPath(Segment{Kind: SegMatch, Name: "f", Value: Scalar{Kind: ScalarString, Text: text}})
		got, err := ParsePath(p.String())
		if err != nil {
			t.Fatalf("string value %q rendered as %q, which hew cannot read back: %v", text, p.String(), err)
		}
		v := got.Segment(0).Value
		if v.Kind != ScalarString || v.Text != text {
			t.Fatalf("string value %q rendered as %q and read back as %v %q", text, p.String(), v.Kind, v.Text)
		}
	})
}

// FuzzParsePathIsIdempotent is the property from the TEXT side: whatever hew
// accepts, it must re-render to something that parses to the same path. This
// is the property .hewt determinism rests on (§9.6).
func FuzzParsePathIsIdempotent(f *testing.F) {
	for _, s := range []string{"/", "/a/b", `/deps/"@scope/pkg"`, "/tags/0", "/tags/-",
		"/x/name=github", `/x/id="a b"`, "/x/#0", "/x/#t", "./port", `/provider/"aws"[1]`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		p, err := ParsePath(s)
		if err != nil {
			return // not a path; nothing is claimed about it
		}
		again, err := ParsePath(p.String())
		if err != nil {
			t.Fatalf("%q rendered as %q, which hew cannot read back: %v", s, p.String(), err)
		}
		if !p.Equal(again) {
			t.Fatalf("%q rendered as %q and read back as %q", s, p.String(), again.String())
		}
		if p.String() != again.String() {
			t.Fatalf("rendering is not a fixed point: %q then %q", p.String(), again.String())
		}
	})
}

// utf8Clean keeps the fuzzers on text a document could hold. A key is a string
// in every format hew binds, and an invalid UTF-8 byte sequence is not one.
func utf8Clean(s string) bool {
	for _, r := range s {
		if r == 0xFFFD {
			return false
		}
	}
	return true
}
