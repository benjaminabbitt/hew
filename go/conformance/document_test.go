package conformance

import (
	"testing"

	"github.com/benjaminabbitt/hew/go"
	_ "github.com/benjaminabbitt/hew/go/ext/all"
)

// A format you can WRITE is a format you can ADDRESS. Binding.Applier and
// Binding.Document are two halves of one capability: Resolve (§9.2) projects an
// abstract list onto RFC 6901 against a Document, the §9.7 record is built from
// that projection, and A.0's reads-become-asserts records a `test` beside every
// write by reading the before-image. A binding with an applier and no reader is
// a HALF binding — it can be written to but not addressed, and every one of
// those three capabilities silently disappears for its format.
//
// The gap this pins was invisible for exactly one reason: the document-API
// tests run against a TOY format registered in-package plus JSON, so five real
// formats could ship with no reader and no test would say so.
//
// documentFixtures is keyed by format and MUST cover every applier-bearing
// registered format — a seventh format added without a fixture fails here
// rather than quietly inheriting the gap.
var documentFixtures = map[hew.FormatID]struct {
	name string
	src  string
}{
	"json":  {"config.json", "{\"host\": \"example.com\"}\n"},
	"jsonc": {"config.jsonc", "{\n  // the host\n  \"host\": \"example.com\"\n}\n"},
	"yaml":  {"config.yaml", "host: example.com\n"},
	"toml":  {"config.toml", "host = \"example.com\"\n"},
	"hcl":   {"main.tf", "host = \"example.com\"\n"},
}

// appliableFormats is every registered format that can be written to. Markdown
// is absent by construction: it registers detection and a segment grammar but
// no applier while §8.7's evaluation is open, so it is not yet a format you can
// write, and the invariant does not reach it.
func appliableFormats(t *testing.T) []hew.FormatID {
	t.Helper()
	var out []hew.FormatID
	for _, id := range hew.Formats() {
		if b, ok := hew.Lookup(id); ok && b.Applier != nil {
			out = append(out, id)
		}
	}
	if len(out) == 0 {
		t.Fatal("no appliable formats registered; the ext/all import is not doing its job")
	}
	return out
}

func TestEveryAppliableFormatSuppliesAReader(t *testing.T) {
	for _, id := range appliableFormats(t) {
		t.Run(string(id), func(t *testing.T) {
			b, _ := hew.Lookup(id)
			if b.Document == nil {
				t.Fatalf("format %q registers an Applier but no Document: it can be written to but not addressed, so Resolve, the §9.7 record and the fluent API's reads all fail on it", id)
			}
			fx, ok := documentFixtures[id]
			if !ok {
				t.Fatalf("no document fixture for registered format %q; add one so its reader is actually exercised", id)
			}
			doc, err := b.Document(fx.name, []byte(fx.src))
			if err != nil {
				t.Fatalf("Document: %v", err)
			}
			root := doc.Root()
			if root.Kind() != hew.KindMap {
				t.Fatalf("root kind = %v, want KindMap", root.Kind())
			}
			host, ok := root.Member("host")
			if !ok {
				t.Fatal(`Member("host") not found`)
			}
			// Decode, not String: String is the canonical SPELLING (a JSON
			// string keeps its quotes), and what a key-match compares is the
			// decoded value.
			var got string
			if err := host.Value().Decode(&got); err != nil {
				t.Fatalf("decoding the host value: %v", err)
			}
			if got != "example.com" {
				t.Errorf("host value = %q, want %q", got, "example.com")
			}
		})
	}
}

// The fluent API's Rule 3 — an operation that names an existing node records
// the value it found as a `test` beside the write — needs the reader. Replace
// is the plainest step that takes it.
func TestFluentReadPathWorksOnEveryAppliableFormat(t *testing.T) {
	for _, id := range appliableFormats(t) {
		t.Run(string(id), func(t *testing.T) {
			fx, ok := documentFixtures[id]
			if !ok {
				t.Fatalf("no document fixture for registered format %q", id)
			}
			d, err := hew.OpenBytes(fx.name, []byte(fx.src))
			if err != nil {
				t.Fatalf("OpenBytes: %v", err)
			}
			d.At("/host").Replace("new.example.com")
			tl, err := d.Transforms()
			if err != nil {
				t.Fatalf("Replace needs to read the before-image and could not: %v", err)
			}
			if len(tl.Transform) == 0 {
				t.Fatal("Replace recorded no transforms")
			}
		})
	}
}

// Resolve is the only route to the §9.7 record's resolved ops, and it takes a
// Document. Without a reader a format cannot produce a record at all.
func TestResolveProducesOpsOnEveryAppliableFormat(t *testing.T) {
	for _, id := range appliableFormats(t) {
		t.Run(string(id), func(t *testing.T) {
			fx, ok := documentFixtures[id]
			if !ok {
				t.Fatalf("no document fixture for registered format %q", id)
			}
			b, _ := hew.Lookup(id)
			if b.Document == nil {
				t.Fatalf("format %q has no Document", id)
			}
			d, err := hew.OpenBytes(fx.name, []byte(fx.src))
			if err != nil {
				t.Fatalf("OpenBytes: %v", err)
			}
			d.At("/host").Replace("new.example.com")
			tl, err := d.Transforms()
			if err != nil {
				t.Fatalf("Transforms: %v", err)
			}
			doc, err := b.Document(fx.name, []byte(fx.src))
			if err != nil {
				t.Fatalf("Document: %v", err)
			}
			ops, err := hew.Resolve(tl, doc)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if len(ops) == 0 {
				t.Fatal("Resolve produced no ops")
			}
		})
	}
}
