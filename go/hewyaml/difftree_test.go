package hewyaml

import (
	"testing"

	"github.com/benjaminabbitt/hew"
	"github.com/benjaminabbitt/hew/internal/hewerr"
	"gopkg.in/yaml.v3"
)

func TestDiffTreeShape(t *testing.T) {
	root, err := DiffTree([]byte("name: myapp\nserver:\n  port: 8080\ntags:\n  - a\n  - b\n"))
	if err != nil {
		t.Fatalf("DiffTree: %v", err)
	}
	if root.Kind != hew.KindMap || len(root.Children) != 3 {
		t.Fatalf("root = %+v", root)
	}
	for i, want := range []string{"name", "server", "tags"} {
		if root.Children[i].Key != want {
			t.Fatalf("child %d = %q, want %q", i, root.Children[i].Key, want)
		}
	}
	tags := root.Children[2].Node
	if tags.Kind != hew.KindSeq || len(tags.Children) != 2 {
		t.Fatalf("tags = %+v", tags)
	}
}

// A standalone comment stands where the file writes it, before the member it
// leads — the ordering §4.5b's `/container/#n` counts through.
func TestDiffTreeInterleavesComments(t *testing.T) {
	root, err := DiffTree([]byte("server:\n  # leads port\n  port: 8080\n  timeout: 30\n"))
	if err != nil {
		t.Fatalf("DiffTree: %v", err)
	}
	server := root.Children[0].Node
	if len(server.Children) != 3 {
		t.Fatalf("server children = %+v", server.Children)
	}
	if !server.Children[0].Comment || server.Children[0].Text != "leads port" {
		t.Fatalf("first child = %+v", server.Children[0])
	}
	if server.Children[1].Key != "port" || server.Children[2].Key != "timeout" {
		t.Fatalf("members out of order: %+v", server.Children)
	}
}

// §9.4-R5 rests on this: the value is the node as PARSED, so an added mapping
// still knows it was written in block style and a quoted scalar still knows it
// was quoted.
func TestDiffTreeValuesKeepSourceStyle(t *testing.T) {
	root, err := DiffTree([]byte("block:\n  a: 1\nflow: {a: 1}\nquoted: \"8080\"\nplain: 8080\n"))
	if err != nil {
		t.Fatalf("DiffTree: %v", err)
	}
	by := map[string]*yaml.Node{}
	for _, c := range root.Children {
		by[c.Key] = c.Node.Value.Node()
	}
	if by["block"].Style&yaml.FlowStyle != 0 {
		t.Fatal("a block mapping must not come back flow")
	}
	if by["flow"].Style&yaml.FlowStyle == 0 {
		t.Fatal("a flow mapping must not come back block")
	}
	if by["quoted"].ShortTag() != "!!str" || by["quoted"].Style&yaml.DoubleQuotedStyle == 0 {
		t.Fatalf("quoted scalar = %+v", by["quoted"])
	}
	if by["plain"].ShortTag() != "!!int" {
		t.Fatalf("plain scalar = %s", by["plain"].ShortTag())
	}
}

func TestDiffTreeAliasIsAScalar(t *testing.T) {
	root, err := DiffTree([]byte("base: &b\n  a: 1\nuse: *b\n"))
	if err != nil {
		t.Fatalf("DiffTree: %v", err)
	}
	use := root.Children[1].Node
	if use.Kind != hew.KindScalar {
		t.Fatalf("an alias addresses one node: kind = %v", use.Kind)
	}
}

func TestDiffTreeRejectsMalformedYAML(t *testing.T) {
	_, err := DiffTree([]byte("a: [1,\n"))
	he, ok := hewerr.As(err)
	if !ok || he.Code != hewerr.CodeTargetParse || he.Component != hewerr.ComponentDiffer {
		t.Fatalf("want HEW002 from the differ, got %v", err)
	}
}

func TestDiffTreeSequenceOfMappings(t *testing.T) {
	root, err := DiffTree([]byte("m:\n  - name: a\n    command: x\n  - name: b\n    command: y\n"))
	if err != nil {
		t.Fatalf("DiffTree: %v", err)
	}
	m := root.Children[0].Node
	if m.Kind != hew.KindSeq || len(m.Children) != 2 {
		t.Fatalf("m = %+v", m)
	}
	first := m.Children[0].Node
	if first.Kind != hew.KindMap || first.Children[0].Key != "name" || first.Children[1].Key != "command" {
		t.Fatalf("first element = %+v", first.Children)
	}
}
