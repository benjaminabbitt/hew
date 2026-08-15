package hew

import (
	"strconv"
	"strings"
	"testing"
)

// O41 made String()/ParsePath a bijection with one hole: a key holding a line
// break had no spelling, because §4.1's quoted form escaped only `\"` and
// `\\`. The hole is closed by extending THAT mechanism — the escape layer
// already exists, in the lexer and the renderer both — rather than adding a
// second one.
//
// The bijection is now total: every string is a spellable key.

func TestAKeyHoldingALineBreakRoundTrips(t *testing.T) {
	for _, key := range []string{"a\nb", "a\rb", "\n", "\r\n", "a\nb\\c\"d"} {
		t.Run(strconv.Quote(key), func(t *testing.T) {
			p := NewPath(Segment{Kind: SegKey, Name: key})
			s := p.String()
			if strings.ContainsAny(s, "\r\n") {
				t.Fatalf("rendered %q still contains a raw line break; the path must stay one line", s)
			}
			back, err := ParsePath(s)
			if err != nil {
				t.Fatalf("%q rendered as %s, which does not parse: %v", key, s, err)
			}
			if got := back.Segment(0); got.Kind != SegKey || got.Name != key {
				t.Fatalf("%q round-tripped as kind=%v name=%q", key, got.Kind, got.Name)
			}
		})
	}
}

// The spellability guard must agree: nothing is unspellable any more.
func TestEveryKeyIsSpellable(t *testing.T) {
	for _, key := range []string{"a\nb", "\r", "plain", "", "-", "8080", "a\\b", `a"b`} {
		p := NewPath(Segment{Kind: SegKey, Name: key})
		if !p.spellable() {
			t.Errorf("%q reports unspellable, but the quoted form can carry it now", key)
		}
		if _, bad := p.firstUnspellable(); bad {
			t.Errorf("%q is blamed as unspellable", key)
		}
	}
}

// A match VALUE carrying a line break is the same lexical problem in the other
// half of §4.2's segment, and closes with the same escape.
func TestAMatchValueHoldingALineBreakRoundTrips(t *testing.T) {
	seg := Segment{Kind: SegMatch, Name: "note", Value: Scalar{Kind: ScalarString, Text: "one\ntwo", Quoted: true}}
	p := NewPath(seg)
	s := p.String()
	if strings.ContainsAny(s, "\r\n") {
		t.Fatalf("rendered %q contains a raw line break", s)
	}
	back, err := ParsePath(s)
	if err != nil {
		t.Fatalf("%s does not parse: %v", s, err)
	}
	if got := back.Segment(0); got.Value.Text != "one\ntwo" {
		t.Fatalf("match value round-tripped as %q", got.Value.Text)
	}
}

// An unknown escape is still refused: extending the set is not opening it.
func TestAnUnknownEscapeIsStillRefused(t *testing.T) {
	if _, err := ParsePath(`/"a\qb"`); err == nil {
		t.Fatal(`\q was accepted; the escape set is closed`)
	}
}
