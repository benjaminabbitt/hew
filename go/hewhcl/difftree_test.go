package hewhcl

import (
	"strings"
	"testing"

	"github.com/benjaminabbitt/hew"
	"github.com/benjaminabbitt/hew/internal/hewerr"
)

// tree renders a DiffNode as a compact, total shape string. A label child is
// written quoted, exactly as its address spells it.
func tree(n *hew.DiffNode) string {
	var b strings.Builder
	write(&b, n)
	return b.String()
}

func write(b *strings.Builder, n *hew.DiffNode) {
	if n.Kind != hew.KindMap {
		b.WriteString(n.Value.String())
		return
	}
	b.WriteString("{")
	for i, c := range n.Children {
		if i > 0 {
			b.WriteString(" ")
		}
		if c.Label {
			b.WriteString(quoteHCL(c.Key))
		} else {
			b.WriteString(c.Key)
		}
		b.WriteString(":")
		write(b, c.Node)
	}
	b.WriteString("}")
}

func mustTree(t *testing.T, src string) *hew.DiffNode {
	t.Helper()
	n, err := DiffTree([]byte(src))
	if err != nil {
		t.Fatalf("DiffTree: %v", err)
	}
	return n
}

func treeErr(t *testing.T, src string) *hewerr.Error {
	t.Helper()
	_, err := DiffTree([]byte(src))
	he, ok := hewerr.As(err)
	if !ok {
		t.Fatalf("want a *hewerr.Error, got %v", err)
	}
	return he
}

// A block's label is a step of its address (§4.3), so the tree carries it as a
// LABEL child rather than as a member name.
func TestDiffTreeNestsBlockLabels(t *testing.T) {
	got := tree(mustTree(t, "terraform {\n  required_version = \">= 1.6\"\n}\n\nprovider \"google\" {\n  project = \"p\"\n}\n"))
	want := `{terraform:{required_version:'>= 1.6'} provider:{"google":{project:p}}}`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestDiffTreeNestsBothLabelsOfATwoLabelBlock(t *testing.T) {
	got := tree(mustTree(t, "resource \"aws_instance\" \"web\" {\n  ami = \"a\"\n}\n"))
	want := `{resource:{"aws_instance":{"web":{ami:a}}}}`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// Blocks sharing a type share the first step of their address, so they are ONE
// child with one label child each — the shape that lets a hunk anchor at
// /provider/"b" instead of at /provider, which names both.
func TestDiffTreeMergesBlocksSharingAType(t *testing.T) {
	got := tree(mustTree(t, "provider \"a\" {\n  k = \"1\"\n}\n\nprovider \"b\" {\n  k = \"2\"\n}\n"))
	want := `{provider:{"a":{k:"1"} "b":{k:"2"}}}`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// An attribute's value is an expression, not a subtree hew addresses into, so
// it is a leaf whatever it looks like.
func TestDiffTreeTreatsAnAttributeAsALeaf(t *testing.T) {
	n := mustTree(t, "locals {\n  o = { a = 1 }\n  l = [1, 2]\n}\n")
	for _, c := range n.Children[0].Node.Children {
		if c.Node.Kind != hew.KindScalar {
			t.Fatalf("%s: want a leaf, got kind %v", c.Key, c.Node.Kind)
		}
		if len(c.Node.Children) != 0 {
			t.Fatalf("%s: a leaf has no children", c.Key)
		}
	}
}

func TestDiffTreeRejectsUnparseableSource(t *testing.T) {
	he := treeErr(t, "provider \"a\" {\n")
	if he.Code != hewerr.CodeTargetParse || he.Component != hewerr.ComponentDiffer {
		t.Fatalf("want HEW002 from the differ, got %v", he)
	}
	if !strings.Contains(he.Error(), "does not parse as HCL") {
		t.Fatalf("diagnostic does not name the format: %v", he)
	}
}

// A repeated `(type, labels)` tuple has one address for two blocks. §6.4.3
// settles that with an ordinal a REVIEWER writes; the differ says so instead
// of guessing (§9.4-R6).
func TestDiffTreeRefusesARepeatedTuple(t *testing.T) {
	he := treeErr(t, "provider \"aws\" {\n  k = \"1\"\n}\n\nprovider \"aws\" {\n  k = \"2\"\n}\n")
	if he.Code != hewerr.CodeInexpressible || he.Component != hewerr.ComponentDiffer {
		t.Fatalf("want HEW020 from the differ, got %v", he)
	}
	if he.Path != "/" {
		t.Fatalf("want the root body named, got %q", he.Path)
	}
	if !strings.Contains(he.Error(), `provider "aws"`) || !strings.Contains(he.Error(), "ord=") {
		t.Fatalf("diagnostic names neither the tuple nor the remedy: %v", he)
	}
}

func TestDiffTreeNamesTheNestedBodyOfARepeatedTuple(t *testing.T) {
	he := treeErr(t, "terraform {\n  backend \"s3\" {\n    b = \"1\"\n  }\n  backend \"s3\" {\n    b = \"2\"\n  }\n}\n")
	if he.Code != hewerr.CodeInexpressible || he.Path != "/terraform" {
		t.Fatalf("want HEW020 at /terraform, got %s at %q", he.Code, he.Path)
	}
}

// A block whose labels are a PREFIX of another's has no address of its own
// either: /provider/"a" would name both it and the longer block's first step.
func TestDiffTreeRefusesALabelPrefixCollision(t *testing.T) {
	he := treeErr(t, "provider \"a\" {\n  k = \"1\"\n}\n\nprovider \"a\" \"b\" {\n  k = \"2\"\n}\n")
	if he.Code != hewerr.CodeInexpressible || he.Path != `/provider/"a"` {
		t.Fatalf(`want HEW020 at /provider/"a", got %s at %q`, he.Code, he.Path)
	}
}

// §9.1 step 5 chains a run of `+` lines: the second add is placed after the
// first, which is in no parse of the target. The touched body then re-aligns.
func TestChainedAddsPlaceAgainstEachOther(t *testing.T) {
	src := "provider \"a\" {\n  k = \"1\"\n}\n"
	got := apply(t, src, list(
		hew.Transform{Op: hew.OpAdd, Path: p(t, `/provider/"a"/mm`), After: p(t, `/provider/"a"/k`), Value: val(t, `"2"`)},
		hew.Transform{Op: hew.OpAdd, Path: p(t, `/provider/"a"/n`), After: p(t, `/provider/"a"/mm`), Value: val(t, `"3"`)},
	))
	eq(t, got, "provider \"a\" {\n  k  = \"1\"\n  mm = \"2\"\n  n  = \"3\"\n}\n")
}

// The pending-add fallback widens nothing: a placement naming a sibling that
// is in neither the target nor this run is still a no-match, even with an
// unrelated add of this run's already pending in the same body.
func TestAnUnknownPlacementSiblingIsStillANoMatch(t *testing.T) {
	failWith(t, "provider \"a\" {\n  k = \"1\"\n}\n", list(
		hew.Transform{Op: hew.OpAdd, Path: p(t, `/provider/"a"/m`), After: p(t, `/provider/"a"/k`), Value: val(t, `"2"`)},
		hew.Transform{Op: hew.OpAdd, Path: p(t, `/provider/"a"/n`), After: p(t, `/provider/"a"/ghost`), Value: val(t, `"3"`)},
	), hewerr.CodeNoMatch, `/provider/"a"/ghost`, 0)
}
