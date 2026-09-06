package all

import (
	"strings"
	"testing"

	hew "github.com/benjaminabbitt/hew/go"
)

// Slice 4 of satisfied-recoil: SCORED LOCATION, end to end. Slices 2 and 3 could
// only resolve a duplicate when the collection was still EXACTLY the length the
// patch recorded — any unrelated edit to the same array, however far from the
// element being changed, made the position advisory unusable and the patch
// refused. That is the trade this slice removes: an irrelevant edit should cost
// nothing, while a genuinely ambiguous one must still refuse.
//
// Every binding runs the same core Locate, so these cases assert the answer is
// the same in each: two formats disagreeing about WHICH duplicate a patch meant
// would corrupt a document, and it is the kind of divergence a per-format
// implementation would only be caught out on by luck.

// An APPEND after the patch was written leaves the index from the front intact —
// every element keeps its distance from the start — so the advisory still names
// exactly one of the colliding elements. Before slice 4 the length mismatch alone
// refused this.
func TestDriftedCollectionStillResolvesADuplicate(t *testing.T) {
	for _, c := range []struct {
		name          string
		format        hew.FormatID
		fname, target string
	}{
		// ["keep","dup","dup","extra"] — the patch was written against the first
		// three, and "extra" was appended by someone else in the meantime.
		{"json", hew.FormatJSON, "config.json", `{"tags": ["keep", "dup", "dup", "extra"]}`},
		{"yaml", hew.FormatYAML, "config.yaml", "tags:\n  - keep\n  - dup\n  - dup\n  - extra\n"},
		{"jsonc", hew.FormatJSONC, "config.jsonc", "{\n  \"tags\": [\"keep\", \"dup\", \"dup\", \"extra\"]\n}\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			binding, ok := hew.Lookup(c.format)
			if !ok {
				t.Fatalf("no binding for %s", c.format)
			}
			line := `"dup"`
			if c.format == hew.FormatYAML {
				line = "- dup"
			}
			// Recorded when the array was 3 long: remove the duplicate at index 2.
			patch := "hew: 1\n\n--- " + c.fname + " format=" + string(c.format) +
				"\n\n@@ /tags @@\n- " + line + " ~hew:at=2 ~hew:length=3\n"
			tl, err := hew.ParseSingle([]byte(patch))
			if err != nil {
				t.Fatalf("ParseSingle: %v\npatch=%s", err, patch)
			}
			got, err := binding.Applier([]byte(c.target), tl)
			if err != nil {
				t.Fatalf("an append elsewhere in the array must not cost the patch its "+
					"location: %v\npatch=%s", err, patch)
			}
			doc, err := hew.OpenBytes(c.fname, got, hew.As(c.format))
			if err != nil {
				t.Fatalf("re-open result: %v\n%s", err, got)
			}
			// The SECOND "dup" went, and the appended element is untouched.
			doc.AtPath(hew.MustParsePath("/tags")).Assert([]any{"keep", "dup", "extra"})
			if _, err := doc.Bytes(); err != nil {
				t.Fatalf("the wrong element was removed: %v\ngot=%s", err, got)
			}
		})
	}
}

// The safety property that must survive scoring: it buys robustness against
// IRRELEVANT edits, not permission to guess. Here the collection was edited on
// the side that makes the two position signals contradict — counting from the
// front names one duplicate, counting from the end names the other — and both
// readings are exactly as credible. hew refuses, and says why in terms the reader
// can act on, because the address itself is a digest and cannot be shown.
func TestContradictoryPositionSignalsRefuse(t *testing.T) {
	for _, c := range []struct {
		name          string
		format        hew.FormatID
		fname, target string
	}{
		// ["a","dup","dup","b"]: recorded at 1 of 3, so the front says element 1
		// and the end (one back from last, in a 4-long array) says element 2.
		{"json", hew.FormatJSON, "config.json", `{"tags": ["a", "dup", "dup", "b"]}`},
		{"yaml", hew.FormatYAML, "config.yaml", "tags:\n  - a\n  - dup\n  - dup\n  - b\n"},
		{"jsonc", hew.FormatJSONC, "config.jsonc", "{\n  \"tags\": [\"a\", \"dup\", \"dup\", \"b\"]\n}\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			binding, ok := hew.Lookup(c.format)
			if !ok {
				t.Fatalf("no binding for %s", c.format)
			}
			line := `"dup"`
			if c.format == hew.FormatYAML {
				line = "- dup"
			}
			patch := "hew: 1\n\n--- " + c.fname + " format=" + string(c.format) +
				"\n\n@@ /tags @@\n- " + line + " ~hew:at=1 ~hew:length=3\n"
			tl, err := hew.ParseSingle([]byte(patch))
			if err != nil {
				t.Fatalf("ParseSingle: %v\npatch=%s", err, patch)
			}
			got, err := binding.Applier([]byte(c.target), tl)
			if err == nil {
				t.Fatalf("two signals naming different elements must refuse, not pick "+
					"one; produced:\n%s", got)
			}
			// Explainable (property d): the refusal names BOTH elements the two
			// signals chose, so the author can see the disagreement rather than
			// being told only that something was ambiguous.
			detail := err.Error()
			for _, want := range []string{"front", "end"} {
				if !strings.Contains(detail, want) {
					t.Fatalf("refusal does not say which signals disagreed (missing %q): %s", want, detail)
				}
			}
		})
	}
}
