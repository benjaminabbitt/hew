package all

import (
	"encoding/json"
	"testing"

	hew "github.com/benjaminabbitt/hew/go"
	"github.com/benjaminabbitt/hew/go/hewdiff"
)

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// A patch's position advisories are all computed against the ORIGINAL array, but
// the appliers resolve sequentially and reparse between transforms — so the
// second edit to one array meets a collection the first edit already changed.
// With duplicates that is exactly when the advisory matters, which makes this the
// case most likely to be wrong and least likely to be noticed: every shape below
// produces a patch that LOOKS right, and only applying it says whether the right
// elements moved.
//
// Round-tripping the differ against its own applier is the check that covers the
// whole grid rather than the shapes someone thought to hand-write.
func TestDuplicateArrayRoundTrips(t *testing.T) {
	for _, c := range []struct {
		name          string
		before, after []any
	}{
		{"remove one of two", []any{"dup", "dup"}, []any{"dup"}},
		{"remove one of three", []any{"dup", "dup", "dup"}, []any{"dup", "dup"}},
		{"remove two of three", []any{"dup", "dup", "dup"}, []any{"dup"}},
		{"remove all three", []any{"dup", "dup", "dup"}, []any{}},
		{"remove the middle of three", []any{"a", "dup", "dup", "dup", "b"}, []any{"a", "dup", "dup", "b"}},
		{"remove two duplicates around a keeper", []any{"dup", "keep", "dup"}, []any{"keep"}},
		{"remove one of two pairs", []any{"x", "x", "y", "y"}, []any{"x", "y", "y"}},
		{"remove one from each pair", []any{"x", "x", "y", "y"}, []any{"x", "y"}},
		{"add beside duplicates", []any{"dup", "dup"}, []any{"dup", "dup", "new"}},
		{"add between duplicates", []any{"dup", "dup"}, []any{"dup", "new", "dup"}},
		{"remove one duplicate and add another value", []any{"dup", "dup", "dup"}, []any{"dup", "dup", "new"}},
		{"duplicates at both ends", []any{"dup", "a", "b", "dup"}, []any{"a", "b", "dup"}},
		{"grow the duplicate run", []any{"dup", "dup"}, []any{"dup", "dup", "dup"}},
		{"replace a duplicate's neighbour", []any{"dup", "a", "dup"}, []any{"dup", "B", "dup"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			old := mustJSON(t, map[string]any{"tags": c.before})
			updated := mustJSON(t, map[string]any{"tags": c.after})

			tl, err := hewdiff.Diff(old, updated, hew.FormatJSON,
				hew.DiffOptions{Target: "t.json", Context: hew.ContextDefault})
			if err != nil {
				t.Fatalf("Diff: %v", err)
			}
			patch, err := hew.Render(tl, hew.RenderOptions{})
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			replay, err := hew.ParseSingle(patch)
			if err != nil {
				t.Fatalf("ParseSingle: %v\n%s", err, patch)
			}
			binding, _ := hew.Lookup(hew.FormatJSON)
			got, err := binding.Applier(old, replay)
			if err != nil {
				t.Fatalf("the differ's own patch must apply to the document it was "+
					"computed from: %v\npatch:\n%s", err, patch)
			}
			doc, err := hew.OpenBytes("t.json", got, hew.As(hew.FormatJSON))
			if err != nil {
				t.Fatalf("re-open: %v\n%s", err, got)
			}
			doc.AtPath(hew.MustParsePath("/tags")).Assert(c.after)
			if _, err := doc.Bytes(); err != nil {
				t.Fatalf("applying the patch did not reproduce the update: %v\ngot:  %s\nwant: %s\npatch:\n%s",
					err, got, updated, patch)
			}
		})
	}
}
