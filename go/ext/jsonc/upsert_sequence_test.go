package jsonc

import (
	"testing"

	hew "github.com/benjaminabbitt/hew/go"
)

// TestSetReplacesAnExistingSequenceRatherThanAppending is the JSONC twin of
// the JSON reduction (json/upsert-key-over-sequence,
// go/ext/json/upsert_sequence_test.go): `Set` (OP-03, `! upsert`) on a path
// whose existing value is a SEQUENCE must REPLACE that node, per the spec's
// OP-03 entry ("Present → replaced regardless of current value", §7.7, §11
// OP-03), not append the new value as a nested element inside the old one.
// This binding is the one `.mcp.json` (a JSONC well-known name) actually
// resolves through, which is ctxloom's exact corruption case.
func TestSetReplacesAnExistingSequenceRatherThanAppending(t *testing.T) {
	src := []byte(`{"s": {"args": ["a", "b"], "cmd": "old"}}`)

	d, err := hew.OpenBytes("config.jsonc", src, hew.As(hew.FormatJSONC))
	if err != nil {
		t.Fatal(err)
	}
	d.At("/s/args").Set([]string{"a", "b", "c"})
	d.At("/s/cmd").Set("new")

	out, err := d.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}

	want := `{"s": {"args": ["a", "b", "c"], "cmd": "new"}}`
	if string(out) != want {
		t.Fatalf("Set over an existing sequence corrupted the document:\n got  %s\n want %s", out, want)
	}
}
