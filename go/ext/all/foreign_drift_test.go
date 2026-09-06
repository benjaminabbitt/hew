package all

import (
	"strings"
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

// EQUIDISTANT. Symmetrical two-sided drift: one element inserted at each end. No
// anchor lands, and the two duplicates now sit the SAME distance from where the
// advisory points. Position has genuinely run out, and hew refuses rather than
// tossing a coin.
//
// This case is the argument for the neighbour channel. The position signals
// cannot separate these two candidates even in principle — but their
// NEIGHBOURHOODS still can: the target duplicate is still between "a" and "b",
// and the other is still between "b" and "c". Content adjacency survives exactly
// the two-sided drift that defeats both anchors, which is why it is not
// substitutable by any amount of further position work.
func TestForeignSymmetricDriftRefusesAndNeighboursWouldNot(t *testing.T) {
	_, patch, err := foreignDrift(t,
		`{"tags": ["a", "dup", "b", "dup", "c"]}`,
		`{"tags": ["a", "b", "dup", "c"]}`, // the dup at index 1 removed
		`{"tags": ["front", "a", "dup", "b", "dup", "c", "back"]}`)
	if err == nil {
		t.Fatalf("symmetrical drift leaves the candidates indistinguishable by "+
			"position; hew must refuse rather than guess\n%s", patch)
	}
	detail := err.Error()
	for _, want := range []string{"collide", "equally"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("expected an equidistant refusal naming the tie, got: %s", detail)
		}
	}
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
