package yaml

import (
	"testing"

	"github.com/benjaminabbitt/hew/go"
	"github.com/benjaminabbitt/hew/go/internal/hewerr"
	"gopkg.in/yaml.v3"
)

func chainVal(t *testing.T, src string) hew.Value {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(src), &n); err != nil {
		t.Fatal(err)
	}
	return hew.NodeValue(n.Content[0])
}

// §9.1 step 5 chains a run of `+` lines, each placed after the one above it.
// The sibling the second add names is not in the parsed document — this patch
// is adding it — so the placement is satisfied from the run's own pending
// inserts, and the three land in the order the patch writes them.
func TestChainedElementAdds(t *testing.T) {
	tl := hew.TransformList{Target: "t.yaml", Format: hew.FormatYAML, Transform: []hew.Transform{
		{Op: hew.OpTest, Path: hew.MustParsePath("/tags/=alpha"), Value: chainVal(t, "alpha")},
		{Op: hew.OpAdd, Path: hew.MustParsePath("/tags"), Value: chainVal(t, "b"), After: hew.MustParsePath("/tags/=alpha")},
		{Op: hew.OpAdd, Path: hew.MustParsePath("/tags"), Value: chainVal(t, "c"), After: hew.MustParsePath("/tags/=b")},
		{Op: hew.OpAdd, Path: hew.MustParsePath("/tags"), Value: chainVal(t, "d"), After: hew.MustParsePath("/tags/=c")},
	}}
	got, err := Apply([]byte("tags:\n  - alpha\n"), tl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := "tags:\n  - alpha\n  - b\n  - c\n  - d\n"
	if string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestChainedMemberAdds(t *testing.T) {
	tl := hew.TransformList{Target: "t.yaml", Format: hew.FormatYAML, Transform: []hew.Transform{
		{Op: hew.OpAdd, Path: hew.MustParsePath("/s/b"), Value: chainVal(t, "2"), After: hew.MustParsePath("/s/a")},
		{Op: hew.OpAdd, Path: hew.MustParsePath("/s/c"), Value: chainVal(t, "3"), After: hew.MustParsePath("/s/b")},
	}}
	got, err := Apply([]byte("s:\n  a: 1\n"), tl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := "s:\n  a: 1\n  b: 2\n  c: 3\n"
	if string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestChainedKeyedElementAdds(t *testing.T) {
	tl := hew.TransformList{Target: "t.yaml", Format: hew.FormatYAML, Transform: []hew.Transform{
		{Op: hew.OpAdd, Path: hew.MustParsePath("/m"), Value: chainVal(t, "name: b\n"), After: hew.MustParsePath("/m/name=a")},
		{Op: hew.OpAdd, Path: hew.MustParsePath("/m"), Value: chainVal(t, "name: c\n"), After: hew.MustParsePath("/m/name=b")},
	}}
	got, err := Apply([]byte("m:\n  - name: a\n"), tl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := "m:\n  - name: a\n  - name: b\n  - name: c\n"
	if string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// A placement naming a sibling that neither the document nor this run has is
// still a no-match: the fallback widens the search, it does not soften the
// contract.
func TestUnresolvablePlacementStillFails(t *testing.T) {
	tl := hew.TransformList{Target: "t.yaml", Format: hew.FormatYAML, Transform: []hew.Transform{
		{Op: hew.OpAdd, Path: hew.MustParsePath("/tags"), Value: chainVal(t, "b"), After: hew.MustParsePath("/tags/=nope")},
	}}
	_, err := Apply([]byte("tags:\n  - alpha\n"), tl)
	if he, ok := hewerr.As(err); !ok || he.Code != hewerr.CodeNoMatch {
		t.Fatalf("want HEW013, got %v", err)
	}
}

// TestBeforeASequentiallyAddedSiblingSucceeds is a FINDING from sequential
// resolution (docs/hew-spec.md §9.2/§9.3, human ruling): a `before:` naming a
// sibling THIS SAME LIST already added used to be refused (see this test's
// prior name, TestBeforeAPendingSiblingFails, and its old reasoning below).
// That refusal was an artifact of the old batch architecture — every add's
// position was an offset computed against the ORIGINAL source, so a `before`
// insertion point relative to a not-yet-real sibling had no splice-safe
// answer. Under sequential resolution the second add is planned against the
// document the first add actually produced, where the sibling is a REAL
// child at a REAL offset: "before" it is exactly as well-defined as "before"
// any child the original document held. Old comment, preserved because the
// hazard it names is real for the case it was written against — a
// FORWARD-referencing before/after relative to something LATER in the list,
// which no reparse can make real ahead of time:
//
//	"A `before:` naming a pending sibling is NOT satisfied from the run: it
//	would have to land ahead of an insert that has already been placed,
//	which the splice order cannot express. Failing is better than
//	reordering silently."
func TestBeforeASequentiallyAddedSiblingSucceeds(t *testing.T) {
	tl := hew.TransformList{Target: "t.yaml", Format: hew.FormatYAML, Transform: []hew.Transform{
		{Op: hew.OpAdd, Path: hew.MustParsePath("/tags"), Value: chainVal(t, "b"), After: hew.MustParsePath("/tags/=alpha")},
		{Op: hew.OpAdd, Path: hew.MustParsePath("/tags"), Value: chainVal(t, "c"), Before: hew.MustParsePath("/tags/=b")},
	}}
	got, err := Apply([]byte("tags:\n  - alpha\n"), tl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := "tags:\n  - alpha\n  - c\n  - b\n"
	if string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
