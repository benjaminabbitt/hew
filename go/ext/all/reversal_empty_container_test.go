package all

import (
	"testing"

	hew "github.com/benjaminabbitt/hew/go"
)

// Invert's contract: applying its result to the after-image yields the
// before-image, byte for byte. An EMPTY flow container is where that broke.
// Seeding `{}` wrote `{ "x": 1 }`, and taking the member back out left `{ }`:
// the insert discarded the container's interior and padded it, and the remove
// kept the padding. A config-patch store that proves its stored reversal
// round-trips (rather than trusting it) then refuses every write to a file
// whose bytes are exactly `{}` — a file `echo '{}' > .mcp.json` produces.

// replayInverse derives Invert(before, after), renders it as .hew text,
// re-parses it and applies it to after: the full path a stored reversal takes.
func replayInverse(t *testing.T, name string, format hew.FormatID, before, after []byte) []byte {
	t.Helper()
	binding, ok := hew.Lookup(format)
	if !ok {
		t.Fatalf("no binding for %s", format)
	}
	tl, err := hew.Invert(format, before, after, hew.DiffOptions{Target: name})
	if err != nil {
		t.Fatalf("Invert: %v", err)
	}
	text, err := hew.Render(tl, hew.RenderOptions{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	parsed, err := hew.ParseSingle(text)
	if err != nil {
		t.Fatalf("ParseSingle: %v\n%s", err, text)
	}
	got, err := binding.Applier(after, parsed)
	if err != nil {
		t.Fatalf("applying the inverse: %v\npatch=%s\nafter=%q", err, text, after)
	}
	return got
}

func removeAt(t *testing.T, name string, format hew.FormatID, src []byte, path string) []byte {
	t.Helper()
	doc, err := hew.OpenBytes(name, src, hew.As(format))
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	doc.AtPath(hew.MustParsePath(path)).Remove()
	out, err := doc.Bytes()
	if err != nil {
		t.Fatalf("Bytes after Remove(%s): %v", path, err)
	}
	return out
}

var flowFormats = []struct {
	name   string
	format hew.FormatID
}{
	{"t.json", hew.FormatJSON},
	{"t.jsonc", hew.FormatJSONC},
}

func TestAddIntoAnEmptyFlowObjectInvertsByteForByte(t *testing.T) {
	cases := []struct{ before, path string }{
		{`{}`, "/x"},
		{"{}\n", "/x"},
		{`{ }`, "/x"},
		{`{  }`, "/x"},
		{"{\n}\n", "/x"},
		{`{"a": 1}`, "/x"},
		{`{ "a": 1 }`, "/x"},
		{`{"m": {}}`, "/m/x"},
		{`{"m": { }}`, "/m/x"},
	}
	for _, f := range flowFormats {
		for _, c := range cases {
			after := setAt(t, f.name, f.format, []byte(c.before), c.path, map[string]any{"k": 1})
			got := replayInverse(t, f.name, f.format, []byte(c.before), after)
			if string(got) != c.before {
				t.Errorf("%s: %q --add %s--> %q --inverse--> %q, want the before-image back", f.format, c.before, c.path, after, got)
			}
		}
	}
}

func TestRemovingTheLastMemberInvertsByteForByte(t *testing.T) {
	cases := []struct{ before, path string }{
		{`{ "a": 1 }`, "/a"},
		{`{"a": 1, "b": 2}`, "/b"},
		{`{ "a": 1, "b": 2 }`, "/a"},
		{`{"m": { "a": 1 }}`, "/m/a"},
	}
	for _, f := range flowFormats {
		for _, c := range cases {
			after := removeAt(t, f.name, f.format, []byte(c.before), c.path)
			got := replayInverse(t, f.name, f.format, []byte(c.before), after)
			if string(got) != c.before {
				t.Errorf("%s: %q --remove %s--> %q --inverse--> %q, want the before-image back", f.format, c.before, c.path, after, got)
			}
		}
	}
}

// An array element seeded into an empty flow array. A path cannot address
// "append to an empty sequence" for Set, so the forward write is hew's own
// diff toward a one-element array, applied to the empty one.
func TestAddIntoAnEmptyFlowArrayInvertsByteForByte(t *testing.T) {
	for _, f := range flowFormats {
		binding, _ := hew.Lookup(f.format)
		for _, before := range []string{`{"l": []}`, `{"l": [ ]}`} {
			tl, err := hew.Invert(f.format, []byte(`{"l": [1]}`), []byte(before), hew.DiffOptions{Target: f.name})
			if err != nil {
				t.Fatalf("Invert toward the seeded array: %v", err)
			}
			after, err := binding.Applier([]byte(before), tl)
			if err != nil {
				t.Fatalf("seeding %q: %v", before, err)
			}
			got := replayInverse(t, f.name, f.format, []byte(before), after)
			if string(got) != before {
				t.Errorf("%s: %q --seed--> %q --inverse--> %q, want the before-image back", f.format, before, after, got)
			}
		}
	}
}

// The forward layout stays what corpus json/sequential-create-then-write pins:
// a member seeded into `{}` is written padded.
func TestSeedingAnEmptyObjectStaysPadded(t *testing.T) {
	for _, f := range flowFormats {
		got := setAt(t, f.name, f.format, []byte(`{"m": {}}`), "/m/x", 1)
		if want := `{"m": { "x": 1 }}`; string(got) != want {
			t.Errorf("%s: got %q, want %q", f.format, got, want)
		}
	}
}
