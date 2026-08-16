package conformance

import (
	"testing"

	hew "github.com/benjaminabbitt/hew/go"
	_ "github.com/benjaminabbitt/hew/go/ext/all"
)

// O46 made path parsing format-aware, and the fluent API is where that matters
// most: a Doc knows its format, so an address it cannot mean should be refused
// or reinterpreted AT THE CALL, where it is fixable — not resolved against the
// union of every extension linked into the binary.
//
// `/code:0` is the case that shows it. Markdown claims it as a block segment;
// in a YAML document it is a key that happens to contain a colon. Parsing the
// fluent surface against every registered extension gave the Markdown reading
// to a YAML document.

func TestFluentAddressesParseInTheDocumentsOwnFormat(t *testing.T) {
	d, err := hew.OpenBytes("config.yaml", []byte("code:0: one\n"))
	if err != nil {
		t.Fatal(err)
	}
	d.At("/code:0").Set("two")
	tl, terr := d.Transforms()
	if terr != nil {
		t.Fatalf("Transforms: %v", terr)
	}
	got := tl.Transform[0].Path.Segment(0)
	if got.Kind != hew.SegKey || got.Name != "code:0" {
		t.Fatalf("/code:0 in a YAML document parsed as kind=%v name=%q form=%q; "+
			"Markdown's block form is not YAML's to claim", got.Kind, got.Name, got.Form)
	}
}

// AtPath takes an already-built Path, so the scope question does not arise
// there — but the placement sibling on Sel does, and it parses one segment.
func TestPlacementSiblingParsesInTheDocumentsOwnFormat(t *testing.T) {
	d, err := hew.OpenBytes("config.yaml", []byte("a: 1\ncode:0: two\n"))
	if err != nil {
		t.Fatal(err)
	}
	d.At("/b").Add(2).After("code:0")
	if _, err := d.Transforms(); err != nil {
		t.Fatalf("a sibling named \"code:0\" is an ordinary YAML key: %v", err)
	}
}

// The other half of the ruling: a Markdown document still gets Markdown's
// reading, so tightening is not the same as turning extensions off.
func TestMarkdownStillClaimsItsOwnForms(t *testing.T) {
	p, err := hew.ParsePathIn("markdown", "/code:0")
	if err != nil {
		t.Fatalf("markdown: %v", err)
	}
	if got := p.Segment(0); got.Form != "block" {
		t.Fatalf("markdown lost its block form: kind=%v name=%q form=%q", got.Kind, got.Name, got.Form)
	}
}
