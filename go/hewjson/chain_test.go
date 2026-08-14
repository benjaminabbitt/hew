package hewjson

import (
	"testing"

	"github.com/hew-format/hew"
	"github.com/hew-format/hew/internal/hewerr"
	"gopkg.in/yaml.v3"
)

func val(t *testing.T, src string) hew.Value {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(src), &n); err != nil {
		t.Fatal(err)
	}
	return hew.NodeValue(n.Content[0])
}

// §9.1 step 5 chains a run of `+` lines, each placed after the one above it.
// The sibling named by the second add is not in the parsed document — this
// patch is adding it — so the placement has to be satisfied from the run's own
// pending inserts, in order.
func TestChainedMemberAdds(t *testing.T) {
	tl := hew.TransformList{Target: "t.json", Format: hew.FormatJSON, Transform: []hew.Transform{
		{Op: hew.OpTest, Path: hew.MustParsePath("/a"), Value: val(t, "1")},
		{Op: hew.OpAdd, Path: hew.MustParsePath("/b"), Value: val(t, "2"), After: hew.MustParsePath("/a")},
		{Op: hew.OpAdd, Path: hew.MustParsePath("/c"), Value: val(t, "3"), After: hew.MustParsePath("/b")},
		{Op: hew.OpAdd, Path: hew.MustParsePath("/d"), Value: val(t, "4"), After: hew.MustParsePath("/c")},
	}}
	got, err := Apply([]byte("{\n  \"a\": 1\n}\n"), tl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := "{\n  \"a\": 1,\n  \"b\": 2,\n  \"c\": 3,\n  \"d\": 4\n}\n"
	if string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestChainedElementAdds(t *testing.T) {
	tl := hew.TransformList{Target: "t.json", Format: hew.FormatJSON, Transform: []hew.Transform{
		{Op: hew.OpTest, Path: hew.MustParsePath(`/tags/="a"`), Value: val(t, `"a"`)},
		{Op: hew.OpAdd, Path: hew.MustParsePath("/tags"), Value: val(t, `"b"`), After: hew.MustParsePath(`/tags/="a"`)},
		{Op: hew.OpAdd, Path: hew.MustParsePath("/tags"), Value: val(t, `"c"`), After: hew.MustParsePath(`/tags/="b"`)},
	}}
	got, err := Apply([]byte("{\n  \"tags\": [\n    \"a\"\n  ]\n}\n"), tl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := "{\n  \"tags\": [\n    \"a\",\n    \"b\",\n    \"c\"\n  ]\n}\n"
	if string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestChainedKeyedElementAdds(t *testing.T) {
	tl := hew.TransformList{Target: "t.json", Format: hew.FormatJSON, Transform: []hew.Transform{
		{Op: hew.OpAdd, Path: hew.MustParsePath("/m"), Value: val(t, `{name: b}`), After: hew.MustParsePath("/m/name=a")},
		{Op: hew.OpAdd, Path: hew.MustParsePath("/m"), Value: val(t, `{name: c}`), After: hew.MustParsePath("/m/name=b")},
	}}
	got, err := Apply([]byte("{\n  \"m\": [\n    {\"name\": \"a\"}\n  ]\n}\n"), tl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := "{\n  \"m\": [\n    {\"name\": \"a\"},\n    { \"name\": \"b\" },\n    { \"name\": \"c\" }\n  ]\n}\n"
	if string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// A placement naming a sibling that neither the document nor this run has is
// still a no-match: the fallback widens the search, it does not soften the
// contract.
func TestUnresolvablePlacementStillFails(t *testing.T) {
	tl := hew.TransformList{Target: "t.json", Format: hew.FormatJSON, Transform: []hew.Transform{
		{Op: hew.OpAdd, Path: hew.MustParsePath("/b"), Value: val(t, "2"), After: hew.MustParsePath("/nope")},
	}}
	_, err := Apply([]byte("{\n  \"a\": 1\n}\n"), tl)
	if he, ok := hewerr.As(err); !ok || he.Code != hewerr.CodeNoMatch {
		t.Fatalf("want HEW013, got %v", err)
	}
	tl.Transform[0].After = hew.Path{}
	tl.Transform[0].Path = hew.MustParsePath("/tags")
	tl.Transform = append(tl.Transform, hew.Transform{Op: hew.OpAdd, Path: hew.MustParsePath("/tags"),
		Value: val(t, `"z"`), After: hew.MustParsePath(`/tags/="missing"`)})
	if _, err := Apply([]byte("{\n  \"tags\": [\n    \"a\"\n  ]\n}\n"), tl); err == nil {
		t.Fatal("want an error for an element placement that names nothing")
	}
}

func TestSegNamesValue(t *testing.T) {
	elem := val(t, `{name: b, other: 1}`)
	cases := []struct {
		path string
		want bool
	}{
		{"/m/name=b", true},
		{"/m/name=c", false},
		{"/m/other=1", true},
		{"/m/missing=b", false},
		{"/m/b", false}, // a key segment is not a key-match
	}
	for _, c := range cases {
		p := hew.MustParsePath(c.path)
		seg := p.Segment(p.Len() - 1)
		if got := segNamesValue(seg, elem); got != c.want {
			t.Fatalf("segNamesValue(%s) = %v, want %v", c.path, got, c.want)
		}
	}
	scalar := val(t, `"x"`)
	p := hew.MustParsePath(`/t/="x"`)
	if !segNamesValue(p.Segment(p.Len()-1), scalar) {
		t.Fatal(`="x" must name the scalar "x"`)
	}
	if segNamesValue(p.Segment(p.Len()-1), hew.Value{}) {
		t.Fatal("an absent value names nothing")
	}
	if segNamesValue(p.Segment(p.Len()-1), elem) {
		t.Fatal("a mapping is not the scalar")
	}
}
