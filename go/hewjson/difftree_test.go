package hewjson

import (
	"testing"

	"github.com/benjaminabbitt/hew/go"
	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

func TestDiffTreeShape(t *testing.T) {
	root, err := DiffTree([]byte(`{"name": "myapp", "server": {"port": 8080}, "tags": ["a", "b"]}`))
	if err != nil {
		t.Fatalf("DiffTree: %v", err)
	}
	if root.Kind != hew.KindMap || len(root.Children) != 3 {
		t.Fatalf("root = %+v", root)
	}
	// Key order is the document's, not a map's: a diff over reordered members
	// would otherwise be nondeterministic.
	for i, want := range []string{"name", "server", "tags"} {
		if root.Children[i].Key != want {
			t.Fatalf("child %d = %q, want %q", i, root.Children[i].Key, want)
		}
		if root.Children[i].Comment {
			t.Fatalf("JSON has no comment children")
		}
	}
	server := root.Children[1].Node
	if server.Kind != hew.KindMap || server.Children[0].Key != "port" {
		t.Fatalf("server = %+v", server)
	}
	tags := root.Children[2].Node
	if tags.Kind != hew.KindSeq || len(tags.Children) != 2 || tags.Children[1].Key != "" {
		t.Fatalf("tags = %+v", tags)
	}
	if tags.Children[0].Node.Kind != hew.KindScalar {
		t.Fatalf("element kind = %v", tags.Children[0].Node.Kind)
	}
}

// The value carries the source's own type, or the differ would report a
// change every time a number met its own string spelling.
func TestDiffTreeValuesKeepTheirTypes(t *testing.T) {
	root, err := DiffTree([]byte(`{"n": 8080, "s": "8080", "b": true, "z": null}`))
	if err != nil {
		t.Fatalf("DiffTree: %v", err)
	}
	want := map[string]string{"n": "!!int", "s": "!!str", "b": "!!bool", "z": "!!null"}
	for _, c := range root.Children {
		if got := c.Node.Value.Node().ShortTag(); got != want[c.Key] {
			t.Fatalf("%s tagged %s, want %s", c.Key, got, want[c.Key])
		}
	}
}

func TestDiffTreeRejectsMalformedJSON(t *testing.T) {
	_, err := DiffTree([]byte(`{"a": }`))
	he, ok := hewerr.As(err)
	if !ok {
		t.Fatalf("want a hew error, got %v", err)
	}
	if he.Code != hewerr.CodeTargetParse || he.Component != hewerr.ComponentDiffer {
		t.Fatalf("got %s from %s", he.Code, he.Component)
	}
}

func TestDiffTreeEmptyContainers(t *testing.T) {
	root, err := DiffTree([]byte(`{"o": {}, "a": []}`))
	if err != nil {
		t.Fatalf("DiffTree: %v", err)
	}
	if k := root.Children[0].Node.Kind; k != hew.KindMap {
		t.Fatalf("empty object kind = %v", k)
	}
	if k := root.Children[1].Node.Kind; k != hew.KindSeq {
		t.Fatalf("empty array kind = %v", k)
	}
	if len(root.Children[0].Node.Children) != 0 || len(root.Children[1].Node.Children) != 0 {
		t.Fatal("empty containers must have no children")
	}
}

func TestDiffTreeScalarRoot(t *testing.T) {
	root, err := DiffTree([]byte(`42`))
	if err != nil {
		t.Fatalf("DiffTree: %v", err)
	}
	if root.Kind != hew.KindScalar || len(root.Children) != 0 {
		t.Fatalf("root = %+v", root)
	}
}
