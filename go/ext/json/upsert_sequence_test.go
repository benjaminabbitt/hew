package json

import (
	"testing"

	hew "github.com/benjaminabbitt/hew/go"
)

// TestSetReplacesAnExistingSequenceRatherThanAppending pins the exact
// reduction from the data-corruption report: `Set` (OP-03, `! upsert`) on a
// path whose existing value is a SEQUENCE must REPLACE that node, per the
// spec's OP-03 entry ("Present → replaced regardless of current value",
// §7.7, §11 OP-03). It must not append the new value as a nested element
// inside the old one — which is what ctxloom's MCP-server rewrite hit: every
// server entry carries an `args` array, and a second write touching any
// other field corrupted it to `"args": ["a", "b", ["a", "b", "c"]]`.
func TestSetReplacesAnExistingSequenceRatherThanAppending(t *testing.T) {
	src := []byte(`{"s": {"args": ["a", "b"], "cmd": "old"}}`)

	d, err := hew.OpenBytes("config.json", src, hew.As(hew.FormatJSON))
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
