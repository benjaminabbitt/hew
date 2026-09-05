package all

import (
	"strings"
	"testing"

	hew "github.com/benjaminabbitt/hew/go"
	"github.com/benjaminabbitt/hew/go/hewdiff"
)

// The differ itself emits position for a duplicate array, so `hew diff` produces
// a patch that round-trips AND survives reorder — an index-addressed patch would
// mislocate once the list is shuffled.
func TestDifferEmitsPositionForDuplicateArray(t *testing.T) {
	old := []byte(`{"tags": ["keep", "dup", "dup"]}`)
	updated := []byte(`{"tags": ["keep", "dup"]}`) // one "dup" removed
	tl, err := hewdiff.Diff(old, updated, hew.FormatJSON, hew.DiffOptions{Target: "t.json", Context: hew.ContextDefault})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	patch, err := hew.Render(tl, hew.RenderOptions{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// A remove shows the value on its line (`- dup`) with the hash address derived
	// on parse; the position advisory is what makes the duplicate resolvable.
	if !strings.Contains(string(patch), "~hew:at=") {
		t.Fatalf("differ did not emit a position advisory for the duplicate:\n%s", patch)
	}

	binding, _ := hew.Lookup(hew.FormatJSON)
	replay, err := hew.ParseSingle(patch)
	if err != nil {
		t.Fatalf("ParseSingle: %v\n%s", err, patch)
	}
	// Applying to the ORIGINAL reproduces the update...
	got, err := binding.Applier(old, replay)
	if err != nil {
		t.Fatalf("apply to original: %v\n%s", err, patch)
	}
	if d, err := hew.OpenBytes("t.json", got, hew.As(hew.FormatJSON)); err != nil {
		t.Fatalf("re-open: %v", err)
	} else {
		d.AtPath(hew.MustParsePath("/tags")).Assert([]any{"keep", "dup"})
		if _, err := d.Bytes(); err != nil {
			t.Fatalf("round-trip did not reproduce the update: %v\n%s", err, got)
		}
	}
}

// SLICE 3 of satisfied-recoil: a sequence element carries BOTH a value hash and a
// POSITION (index-from-front + list length), so a DUPLICATE value — which hashes
// alike and, in slice 2, collides into a refuse — becomes addressable: the hash
// finds the candidates, the position picks one. Position rides as trailing
// advisory tags `~hew:at=<front> ~hew:length=<length>` on the element's line; it can
// never fail a match, only disambiguate.
//
// Reduced from the confpatch case: an allowlist with a repeated entry, one of
// which is removed. Slice 2 refuses (HEW012 collision on the shared digest);
// slice 3 resolves it by the recorded position.
func TestDuplicateSequenceElementResolvedByPosition(t *testing.T) {
	// Remove the SECOND "dup" (index 2 of 3) from ["keep","dup","dup"]. The
	// address is the shared hash — a collision slice 2 refuses — and the trailing
	// position tags say which of the two colliding elements is meant. Every format
	// with an array applier must resolve it the same way.
	for _, c := range []struct {
		name          string
		format        hew.FormatID
		fname, target string
	}{
		{"json", hew.FormatJSON, "config.json", `{"tags": ["keep", "dup", "dup"]}`},
		{"yaml", hew.FormatYAML, "config.yaml", "tags:\n  - keep\n  - dup\n  - dup\n"},
		{"jsonc", hew.FormatJSONC, "config.jsonc", "{\n  \"tags\": [\"keep\", \"dup\", \"dup\"]\n}\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			binding, ok := hew.Lookup(c.format)
			if !ok {
				t.Fatalf("no binding for %s", c.format)
			}
			// The removed element's line spelling differs by format (a `- ` YAML
			// marker vs a bare JSON element); the trailing advisory is the same.
			line := `"dup"`
			if c.format == hew.FormatYAML {
				line = "- dup"
			}
			patch := "hew: 1\n\n--- " + c.fname + " format=" + string(c.format) +
				"\n\n@@ /tags @@\n- " + line + " ~hew:at=2 ~hew:length=3\n"
			tl, err := hew.ParseSingle([]byte(patch))
			if err != nil {
				t.Fatalf("ParseSingle: %v\npatch=%s", err, patch)
			}
			got, err := binding.Applier([]byte(c.target), tl)
			if err != nil {
				t.Fatalf("a positioned remove of one duplicate must succeed, not "+
					"collide: %v\npatch=%s", err, patch)
			}
			doc, err := hew.OpenBytes(c.fname, got, hew.As(c.format))
			if err != nil {
				t.Fatalf("re-open result: %v\n%s", err, got)
			}
			doc.AtPath(hew.MustParsePath("/tags")).Assert([]any{"keep", "dup"})
			if _, err := doc.Bytes(); err != nil {
				t.Fatalf("wrong duplicate removed: %v\ngot=%s", err, got)
			}
		})
	}
}
