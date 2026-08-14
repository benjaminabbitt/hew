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

// A `before:` naming a pending sibling is NOT satisfied from the run: it would
// have to land ahead of an insert that has already been placed, which the
// splice order cannot express. Failing is better than reordering silently.
func TestBeforeAPendingSiblingFails(t *testing.T) {
	tl := hew.TransformList{Target: "t.yaml", Format: hew.FormatYAML, Transform: []hew.Transform{
		{Op: hew.OpAdd, Path: hew.MustParsePath("/tags"), Value: chainVal(t, "b"), After: hew.MustParsePath("/tags/=alpha")},
		{Op: hew.OpAdd, Path: hew.MustParsePath("/tags"), Value: chainVal(t, "c"), Before: hew.MustParsePath("/tags/=b")},
	}}
	if _, err := Apply([]byte("tags:\n  - alpha\n"), tl); err == nil {
		t.Fatal("want an error")
	}
}

func TestSegNamesValue(t *testing.T) {
	elem := chainVal(t, "name: b\nother: 1\n")
	cases := []struct {
		path string
		want bool
	}{
		{"/m/name=b", true},
		{"/m/name=c", false},
		{"/m/other=1", true},
		{"/m/missing=b", false},
		{"/m/b", false},
	}
	for _, c := range cases {
		p := hew.MustParsePath(c.path)
		if got := segNamesValue(p.Segment(p.Len()-1), elem); got != c.want {
			t.Fatalf("segNamesValue(%s) = %v, want %v", c.path, got, c.want)
		}
	}
	p := hew.MustParsePath("/t/=x")
	seg := p.Segment(p.Len() - 1)
	if !segNamesValue(seg, chainVal(t, "x")) {
		t.Fatal("=x must name the scalar x")
	}
	if segNamesValue(seg, hew.Value{}) {
		t.Fatal("an absent value names nothing")
	}
	if segNamesValue(seg, elem) {
		t.Fatal("a mapping is not the scalar")
	}
}
