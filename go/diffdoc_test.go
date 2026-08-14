package hew

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestScalarToken(t *testing.T) {
	if got := scalarToken(Value{}); got != "!!absent:" {
		t.Fatalf("absent token = %q", got)
	}
	if got := scalarToken(dnum("8080").Value); got != "!!int:8080" {
		t.Fatalf("int token = %q", got)
	}
	if got := scalarToken(dstr("8080").Value); got != "!!str:8080" {
		t.Fatalf("str token = %q", got)
	}
	if got := scalarToken(dmap("a", dnum("1")).Value); !strings.HasPrefix(got, "!!node:") {
		t.Fatalf("container token = %q", got)
	}
}

// Length prefixes exist so no concatenation of child tokens can be read two
// ways; without them {"ab": x} and {"a": {"b": x}} could collide.
func TestCanonicalTokensAreUnambiguous(t *testing.T) {
	a := dmap("ab", dnum("1"))
	b := dmap("a", dmap("b", dnum("1")))
	if a.canonical() == b.canonical() {
		t.Fatalf("collision: %q", a.canonical())
	}
	if got := (*DiffNode)(nil).canonical(); got != "~" {
		t.Fatalf("nil canonical = %q", got)
	}
}

func TestElementsSkipsComments(t *testing.T) {
	n := dcomment(dseq(dstr("a"), dstr("b")), 1, "mid")
	if got := elements(n); len(got) != 2 {
		t.Fatalf("elements = %d", len(got))
	}
	if got := memberNames(dcomment(dmap("k", dnum("1")), 0, "lead")); len(got) != 1 || got[0] != "k" {
		t.Fatalf("memberNames = %q", got)
	}
}

func TestAllOfKindRejectsEmpty(t *testing.T) {
	if allOfKind(nil, KindMap) {
		t.Fatal("an empty sequence has no kind to agree on")
	}
	if allOfKind([]*DiffNode{nil}, KindMap) {
		t.Fatal("a nil element cannot agree on a kind")
	}
	if !allOfKind([]*DiffNode{dmap(), dmap()}, KindMap) {
		t.Fatal("two maps are all maps")
	}
}

func TestUniqueValues(t *testing.T) {
	if !uniqueValues([]*DiffNode{dstr("a"), dstr("b")}) {
		t.Fatal("distinct values are unique")
	}
	if uniqueValues([]*DiffNode{dstr("a"), dstr("a")}) {
		t.Fatal("repeated values are not unique")
	}
	// The number 1 and the string "1" are different values (§4.2).
	if !uniqueValues([]*DiffNode{dnum("1"), dstr("1")}) {
		t.Fatal("type is part of the value")
	}
}

func TestQualifyingOnAnEmptySequence(t *testing.T) {
	if got := qualifying(nil); got != nil {
		t.Fatalf("got %q", got)
	}
	if got := usableFields(nil, nil); len(got) != 0 {
		t.Fatalf("got %q", got)
	}
}

func TestPathLabel(t *testing.T) {
	if got := pathLabel(RootPath()); got != "/" {
		t.Fatalf("root label = %q", got)
	}
	if got := pathLabel(MustParsePath("/a/b")); got != "/a/b" {
		t.Fatalf("label = %q", got)
	}
	if got := pathLabel(Path{}); got != "/" {
		t.Fatalf("absent label = %q", got)
	}
}

func TestContainsHelper(t *testing.T) {
	if !contains([]string{"a", "b"}, "b") {
		t.Fatal("want a hit")
	}
	if contains([]string{"a"}, "b") {
		t.Fatal("want a miss")
	}
	if contains(nil, "a") {
		t.Fatal("an empty list contains nothing")
	}
}

func TestJSONTextFollowsAnAlias(t *testing.T) {
	target := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: "1"}
	alias := &yaml.Node{Kind: yaml.AliasNode, Alias: target}
	if got := jsonText(alias); got != "1" {
		t.Fatalf("got %q", got)
	}
}

func TestSubsetValueFromFieldsRejectsANonKeyField(t *testing.T) {
	bad := &Transform{Op: OpTest, Path: MustParsePath("/a/0")}
	if _, err := subsetValueFromFields([]*Transform{bad}, dialectFor(FragmentNative, FormatYAML)); err == nil {
		t.Fatal("want an error for a non-key field test")
	}
}

func TestValueOfAComment(t *testing.T) {
	v := addressing{}.valueOf(&DiffChild{Comment: true, Text: "note"})
	if got, ok := CommentText(v); !ok || got != "note" {
		t.Fatalf("got %q %v", got, ok)
	}
}
