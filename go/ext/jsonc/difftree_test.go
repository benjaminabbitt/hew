package jsonc

import (
	"testing"

	"github.com/benjaminabbitt/hew/go"
	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

// A comment is a positional CHILD, standing where the file writes it — a
// leading comment before the member it documents, a free comment on its own.
// That ordering is what lets a differ place an added comment relative to its
// member instead of at the end of the container.
func TestDiffTreeInterleavesComments(t *testing.T) {
	root, err := DiffTree([]byte(`{
  // free comment

  "a": 1,
  // leads b
  "b": 2
}`))
	if err != nil {
		t.Fatalf("DiffTree: %v", err)
	}
	want := []struct {
		comment bool
		text    string
		key     string
	}{
		{true, "free comment", ""},
		{false, "", "a"},
		{true, "leads b", ""},
		{false, "", "b"},
	}
	if len(root.Children) != len(want) {
		t.Fatalf("got %d children, want %d: %+v", len(root.Children), len(want), root.Children)
	}
	for i, w := range want {
		c := root.Children[i]
		if c.Comment != w.comment || c.Text != w.text || c.Key != w.key {
			t.Fatalf("child %d = %+v, want %+v", i, c, w)
		}
		if w.comment && c.Node != nil {
			t.Fatalf("a comment child carries no node")
		}
	}
}

func TestDiffTreeArrayComments(t *testing.T) {
	root, err := DiffTree([]byte("[\n  \"a\",\n  // mid\n  \"b\"\n]"))
	if err != nil {
		t.Fatalf("DiffTree: %v", err)
	}
	if root.Kind != hew.KindSeq {
		t.Fatalf("kind = %v", root.Kind)
	}
	if len(root.Children) != 3 || !root.Children[1].Comment || root.Children[1].Text != "mid" {
		t.Fatalf("children = %+v", root.Children)
	}
}

// A trailing comment is NOT a standalone child: §4.5b gives it the `#t`
// address of the node it trails, and counting it here would shift every
// `#<n>` after it.
func TestDiffTreeTrailingCommentIsNotAChild(t *testing.T) {
	root, err := DiffTree([]byte("{\n  \"a\": 1, // trails a\n  \"b\": 2\n}"))
	if err != nil {
		t.Fatalf("DiffTree: %v", err)
	}
	for _, c := range root.Children {
		if c.Comment {
			t.Fatalf("trailing comment surfaced as a child: %+v", root.Children)
		}
	}
}

func TestDiffTreeValueTypes(t *testing.T) {
	root, err := DiffTree([]byte(`{"n": 1, "f": 1.5, "s": "x", "b": false, "z": null}`))
	if err != nil {
		t.Fatalf("DiffTree: %v", err)
	}
	want := map[string]string{"n": "!!int", "f": "!!float", "s": "!!str", "b": "!!bool", "z": "!!null"}
	for _, c := range root.Children {
		if got := c.Node.Value.Node().ShortTag(); got != want[c.Key] {
			t.Fatalf("%s tagged %s, want %s", c.Key, got, want[c.Key])
		}
	}
}

func TestDiffTreeRejectsMalformedJSONC(t *testing.T) {
	_, err := DiffTree([]byte(`{"a": `))
	he, ok := hewerr.As(err)
	if !ok || he.Code != hewerr.CodeTargetParse || he.Component != hewerr.ComponentDiffer {
		t.Fatalf("want HEW002 from the differ, got %v", err)
	}
}

func TestDiffTreeNesting(t *testing.T) {
	root, err := DiffTree([]byte(`{"s": {"deep": [1, {"k": "v"}]}}`))
	if err != nil {
		t.Fatalf("DiffTree: %v", err)
	}
	deep := root.Children[0].Node.Children[0].Node
	if deep.Kind != hew.KindSeq || len(deep.Children) != 2 {
		t.Fatalf("deep = %+v", deep)
	}
	if deep.Children[1].Node.Children[0].Key != "k" {
		t.Fatalf("nested object lost its key: %+v", deep.Children[1].Node)
	}
}
