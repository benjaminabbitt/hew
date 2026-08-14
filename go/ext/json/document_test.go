package json

import (
	"testing"

	"github.com/benjaminabbitt/hew/go"
	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

const docSrc = `{
  "servers": [
    { "name": "github", "command": "npx" },
    "plain"
  ],
  "server": { "host": "localhost" },
  "n": 8080
}`

func mustDocument(t *testing.T, src string) hew.Document {
	t.Helper()
	d, err := Document([]byte(src))
	if err != nil {
		t.Fatalf("Document: %v", err)
	}
	return d
}

func TestDocumentRejectsUnparseableTarget(t *testing.T) {
	_, err := Document([]byte("{oops"))
	he, ok := hewerr.As(err)
	if !ok {
		t.Fatalf("want a *hewerr.Error, got %v", err)
	}
	if he.Code != hewerr.CodeTargetParse {
		t.Fatalf("want HEW002, got %s", he.Code)
	}
}

func TestDocumentNodeKinds(t *testing.T) {
	root := mustDocument(t, docSrc).Root()
	if root.Kind() != hew.KindMap {
		t.Fatalf("root kind %v", root.Kind())
	}
	servers, ok := root.Member("servers")
	if !ok {
		t.Fatal("no servers member")
	}
	if servers.Kind() != hew.KindSeq {
		t.Fatalf("servers kind %v", servers.Kind())
	}
	n, _ := root.Member("n")
	if n.Kind() != hew.KindScalar {
		t.Fatalf("n kind %v", n.Kind())
	}
}

func TestDocumentLen(t *testing.T) {
	root := mustDocument(t, docSrc).Root()
	if root.Len() != 3 {
		t.Fatalf("map Len counts members: got %d", root.Len())
	}
	servers, _ := root.Member("servers")
	if servers.Len() != 2 {
		t.Fatalf("seq Len counts elements: got %d", servers.Len())
	}
	n, _ := root.Member("n")
	if n.Len() != 0 {
		t.Fatalf("scalar Len is 0: got %d", n.Len())
	}
}

func TestDocumentMemberMisses(t *testing.T) {
	root := mustDocument(t, docSrc).Root()
	if _, ok := root.Member("nope"); ok {
		t.Fatal("absent member reported present")
	}
	n, _ := root.Member("n")
	if _, ok := n.Member("anything"); ok {
		t.Fatal("a scalar has no members")
	}
}

func TestDocumentElemBounds(t *testing.T) {
	root := mustDocument(t, docSrc).Root()
	servers, _ := root.Member("servers")
	if _, ok := servers.Elem(-1); ok {
		t.Fatal("negative index reported present")
	}
	if _, ok := servers.Elem(2); ok {
		t.Fatal("index past the end reported present")
	}
	e, ok := servers.Elem(1)
	if !ok || !e.Value().Equal(mustValue(t, "plain")) {
		t.Fatalf("elem 1: ok=%v value=%q", ok, e.Value().String())
	}
	if _, ok := root.Elem(0); ok {
		t.Fatal("a map has no elements")
	}
}

func TestDocumentValueDecodesNativeTypes(t *testing.T) {
	root := mustDocument(t, docSrc).Root()
	n, _ := root.Member("n")
	want, err := hew.ValueOf(8080)
	if err != nil {
		t.Fatal(err)
	}
	if !n.Value().Equal(want) {
		t.Fatalf("8080 must decode as a number, got %q", n.Value().String())
	}
}

// A string carrying a lone surrogate escape is JSON this tree accepts and
// YAML refuses, so the node does not decode. It must then simply match
// nothing, never resolve to a wrong element.
func TestDocumentUndecodableNodeIsTheAbsentValue(t *testing.T) {
	root := mustDocument(t, `{ "bad": "\ud800" }`).Root()
	bad, ok := root.Member("bad")
	if !ok {
		t.Fatal("no bad member")
	}
	if !bad.Value().IsZero() {
		t.Fatalf("want the absent Value, got %q", bad.Value().String())
	}
}

// The document view and the applier must agree about which element a
// key-match segment names; Resolve is what makes that visible.
func TestDocumentDrivesResolve(t *testing.T) {
	tl := hew.TransformList{Target: "target.json", Format: hew.FormatJSON, Transform: []hew.Transform{
		{Op: hew.OpReplace, Path: hew.MustParsePath("/servers/name=github/command"), Value: mustValue(t, "npx-18")},
	}}
	ops, err := hew.Resolve(tl, mustDocument(t, docSrc))
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || ops[0].Path != "/servers/0/command" {
		t.Fatalf("got %+v", ops)
	}
}

func mustValue(t *testing.T, x any) hew.Value {
	t.Helper()
	v, err := hew.ValueOf(x)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
