package all

import (
	"testing"

	hew "github.com/benjaminabbitt/hew/go"
	"github.com/benjaminabbitt/hew/go/hewdiff"
)

// FOREIGN DRIFT: somebody else edited the target between the patch being written
// and the patch being applied. This is the scenario the whole locate-by-identity
// task exists for, and the corpus does not sample it — every fixture there
// applies a patch to the document it was computed from.
//
// The cases below are CONSTRUCTED to reach each tier of the scored locator,
// rather than collected from fixtures that happened to exist. That direction
// matters: a tier that no constructed case can reach is unreachable, which is a
// real finding; a tier that no EXISTING case reaches has only shown that nobody
// wrote the fixture.
//
// Each case is a duplicate-bearing array (so the digest collides and position has
// to decide), a patch produced by the real differ, and then a foreign edit of a
// stated shape before the patch is applied.

// foreignDrift diffs before->after, then applies the resulting patch to `drifted`
// instead of `before`.
func foreignDrift(t *testing.T, before, after, drifted string) ([]byte, string, error) {
	t.Helper()
	tl, err := hewdiff.Diff([]byte(before), []byte(after), hew.FormatJSON,
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
	got, err := binding.Applier([]byte(drifted), replay)
	return got, string(patch), err
}

// ONE-SIDED. Someone appended to the array. Every element kept its distance from
// the front, so the front anchor alone survives — and survives uniquely.
func TestForeignAppendStillLocates(t *testing.T) {
	got, patch, err := foreignDrift(t,
		`{"tags": ["a", "dup", "b", "dup", "c"]}`, // before
		`{"tags": ["a", "b", "dup", "c"]}`,        // after: the dup at index 1 removed
		`{"tags": ["a", "dup", "b", "dup", "c", "appended"]}`)
	if err != nil {
		t.Fatalf("an append elsewhere must not cost the patch its location: %v\n%s", err, patch)
	}
	assertTags(t, got, []any{"a", "b", "dup", "c", "appended"})
}

// DISPLACED. Someone edited on BOTH sides — two elements inserted at the front,
// one removed from the end. Neither anchor lands on a candidate, so the only
// evidence left is distance from where the advisory still points. One candidate
// is strictly nearest, and it is the right one.
//
// This is the tier whose evidence is self-undermining: both anchors were derived
// from a coordinate already shown stale. The case exists, and it answers
// correctly, which is the argument for the floor sitting below it.
func TestForeignTwoSidedDriftLocatesTheNearest(t *testing.T) {
	got, patch, err := foreignDrift(t,
		`{"tags": ["a", "dup", "b", "c", "dup", "e"]}`,
		`{"tags": ["a", "b", "c", "dup", "e"]}`, // the dup at index 1 removed
		`{"tags": ["x", "y", "a", "dup", "b", "c", "dup"]}`)
	if err != nil {
		t.Fatalf("two-sided drift with a strict nearest candidate should locate: %v\n%s", err, patch)
	}
	// The dup originally at index 1 now sits at index 3; it is the one to go.
	assertTags(t, got, []any{"x", "y", "a", "b", "c", "dup"})
}

// NEIGHBOURS. Symmetrical two-sided drift: one element inserted at each end. No
// anchor lands, and the two duplicates now sit the SAME distance from where the
// advisory points, so POSITION HAS RUN OUT IN PRINCIPLE — not for want of
// cleverness. Before the neighbour channel this refused, correctly, rather than
// tossing a coin.
//
// Their NEIGHBOURHOODS still separate them: the target duplicate is still between
// "a" and "b", the other still between "b" and "c". Content adjacency survives
// exactly the two-sided drift that defeats both anchors, because both anchors are
// derived from ONE recorded coordinate and adjacency is not. That is why it is
// not substitutable by any amount of further position work, and it is the case
// the neighbour channel was built for.
func TestForeignSymmetricDriftLocatesByNeighbours(t *testing.T) {
	got, patch, err := foreignDrift(t,
		`{"tags": ["a", "dup", "b", "dup", "c"]}`,
		`{"tags": ["a", "b", "dup", "c"]}`, // the dup at index 1 removed
		`{"tags": ["front", "a", "dup", "b", "dup", "c", "back"]}`)
	if err != nil {
		t.Fatalf("the neighbourhood still separates these candidates where position "+
			"cannot: %v\n%s", err, patch)
	}
	// The dup originally at index 1 sits between "a" and "b"; after the foreign
	// inserts it is at index 2, and it is the one to go.
	assertTags(t, got, []any{"front", "a", "b", "dup", "c", "back"})
}

func assertTags(t *testing.T, got []byte, want []any) {
	t.Helper()
	doc, err := hew.OpenBytes("t.json", got, hew.As(hew.FormatJSON))
	if err != nil {
		t.Fatalf("re-open: %v\n%s", err, got)
	}
	doc.AtPath(hew.MustParsePath("/tags")).Assert(want)
	if _, err := doc.Bytes(); err != nil {
		t.Fatalf("wrong element located: %v\ngot: %s", err, got)
	}
}
