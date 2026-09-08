package hewmatch

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// fake is a minimal Node, so these tests pin the TRAVERSAL rather than any
// binding's node type. Scalar comparison is deliberately naive here — what
// "equal" means for a scalar is the binding's job, not this package's.
type fake struct {
	kind  Kind
	text  string
	pairs []pair
	elems []Node
}

type pair struct {
	k string
	v Node
}

func (f *fake) Kind() Kind { return f.kind }

func (f *fake) ScalarEquals(want *yaml.Node) bool { return f.text == want.Value }

func (f *fake) Lookup(key string) Node {
	for _, p := range f.pairs {
		if p.k == key {
			return p.v
		}
	}
	return nil // absent: an untyped nil, which is what the matcher tests for
}

func (f *fake) Elems() []Node { return f.elems }

func (f *fake) Len() int {
	if f.kind == Mapping {
		return len(f.pairs)
	}
	return len(f.elems)
}

func scalar(s string) *fake { return &fake{kind: Scalar, text: s} }

func mapping(kv ...pair) *fake { return &fake{kind: Mapping, pairs: kv} }

func seq(n ...Node) *fake { return &fake{kind: Sequence, elems: n} }

func want(t *testing.T, src string) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatalf("parse want %q: %v", src, err)
	}
	return doc.Content[0]
}

func TestMatches_ScalarIsExact(t *testing.T) {
	if !Matches(scalar("8080"), want(t, "8080")) {
		t.Fatal("an equal scalar must match")
	}
	if Matches(scalar("8080"), want(t, "9090")) {
		t.Fatal("a different scalar must not match")
	}
}

// The subset rule: a context line naming one key of a larger mapping holds.
func TestMatches_MappingIsASubset(t *testing.T) {
	doc := mapping(pair{"host", scalar("localhost")}, pair{"port", scalar("8080")})
	if !Matches(doc, want(t, "{host: localhost}")) {
		t.Fatal("a listed subset of the mapping's keys must match")
	}
	if Matches(doc, want(t, "{host: elsewhere}")) {
		t.Fatal("a listed key with a different value must not match")
	}
	if Matches(doc, want(t, "{missing: x}")) {
		t.Fatal("a key the document does not have must not match")
	}
}

// The subsequence rule: order is required, adjacency is not.
func TestMatches_SequenceIsAnOrderedSubsequence(t *testing.T) {
	doc := seq(scalar("a"), scalar("b"), scalar("c"))
	if !Matches(doc, want(t, "[a, c]")) {
		t.Fatal("a subsequence in order must match")
	}
	if Matches(doc, want(t, "[c, a]")) {
		t.Fatal("out of order must not match")
	}
	if Matches(doc, want(t, "[a, b, c, d]")) {
		t.Fatal("a wanted element the document lacks must not match")
	}
}

// Equals is the same traversal with size checked, which is the whole of the
// difference: the surplus key that Matches tolerates is what makes Equals fail.
func TestEquals_RefusesASupersetThatMatchesWouldAccept(t *testing.T) {
	doc := mapping(pair{"host", scalar("localhost")}, pair{"port", scalar("8080")})
	subset := want(t, "{host: localhost}")
	if !Matches(doc, subset) {
		t.Fatal("precondition: Matches accepts the subset")
	}
	if Equals(doc, subset) {
		t.Fatal("Equals must refuse a mapping carrying an unlisted key")
	}
	if !Equals(doc, want(t, "{host: localhost, port: 8080}")) {
		t.Fatal("Equals must accept the exact mapping")
	}
}

func TestEquals_SequenceMustMatchElementForElement(t *testing.T) {
	doc := seq(scalar("a"), scalar("b"))
	if Equals(doc, want(t, "[a]")) {
		t.Fatal("Equals must refuse a shorter want")
	}
	if Equals(doc, want(t, "[b, a]")) {
		t.Fatal("Equals compares positionally, so a reorder must fail")
	}
	if !Equals(doc, want(t, "[a, b]")) {
		t.Fatal("Equals must accept the exact sequence")
	}
}

func TestWalk_KindMismatchNeverMatches(t *testing.T) {
	if Matches(scalar("a"), want(t, "[a]")) {
		t.Fatal("a scalar is not a sequence")
	}
	if Matches(seq(scalar("a")), want(t, "a")) {
		t.Fatal("a sequence is not a scalar")
	}
	if Matches(mapping(pair{"a", scalar("1")}), want(t, "[a]")) {
		t.Fatal("a mapping is not a sequence")
	}
}

// A nil node is an absent one — the shape Lookup returns for a missing key —
// and it matches nothing rather than panicking.
func TestWalk_NilNodeMatchesNothing(t *testing.T) {
	if Matches(nil, want(t, "a")) {
		t.Fatal("a nil node must not match")
	}
	if Matches(scalar("a"), nil) {
		t.Fatal("a nil want must not match")
	}
}

// Nesting exercises the recursion through both container kinds at once.
func TestMatches_NestedContainers(t *testing.T) {
	doc := mapping(pair{"server", mapping(
		pair{"host", scalar("localhost")},
		pair{"ports", seq(scalar("80"), scalar("443"))},
	)})
	if !Matches(doc, want(t, "{server: {ports: [443]}}")) {
		t.Fatal("a nested subset/subsequence must match")
	}
	if Matches(doc, want(t, "{server: {ports: [8080]}}")) {
		t.Fatal("a nested element the document lacks must not match")
	}
}
