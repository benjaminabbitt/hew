package hewjsonc

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/benjaminabbitt/hew"
	"gopkg.in/yaml.v3"
)

// yamlNode mirrors a parsed target node as a YAML node, so a subtree can be
// compared against a transform's value without round-tripping the source bytes
// through a YAML parser — which would choke on the very comments this binding
// exists to preserve.
func (n *node) yamlNode() (*yaml.Node, error) {
	switch n.kind {
	case kObj:
		out := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		for _, m := range n.members {
			v, err := m.value.yamlNode()
			if err != nil {
				return nil, err
			}
			out.Content = append(out.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: m.key}, v)
		}
		return out, nil
	case kArr:
		out := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, e := range n.elems {
			v, err := e.value.yamlNode()
			if err != nil {
				return nil, err
			}
			out.Content = append(out.Content, v)
		}
		return out, nil
	case kStr:
		s, err := decodeString(n.raw)
		if err != nil {
			return nil, err
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}, nil
	case kNum:
		tag := "!!int"
		if strings.ContainsAny(n.raw, ".eE") {
			tag = "!!float"
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: n.raw}, nil
	case kBool:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: n.raw}, nil
	default:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}, nil
	}
}

func (n *node) hewValue() (hew.Value, error) {
	y, err := n.yamlNode()
	if err != nil {
		return hew.Value{}, err
	}
	return hew.NodeValue(y), nil
}

// matches implements §6.1's before-image match rules against a target node:
// objects match as a SUBSET (listed keys only), arrays as an ordered
// subsequence, scalars exactly. corpus/jsonc/assert-absent-fail turns on this
// — its `env: {}` context line asserts that /env is an object, not that /env
// is empty.
func (n *node) matches(want *yaml.Node) bool {
	if want == nil {
		return false
	}
	switch want.Kind {
	case yaml.MappingNode:
		if n.kind != kObj {
			return false
		}
		for i := 0; i+1 < len(want.Content); i += 2 {
			m := n.memberNamed(want.Content[i].Value)
			if m == nil || !m.value.matches(want.Content[i+1]) {
				return false
			}
		}
		return true
	case yaml.SequenceNode:
		if n.kind != kArr {
			return false
		}
		i := 0
		for _, w := range want.Content {
			for i < len(n.elems) && !n.elems[i].value.matches(w) {
				i++
			}
			if i == len(n.elems) {
				return false
			}
			i++
		}
		return true
	default:
		got, err := n.hewValue()
		return err == nil && got.Equal(hew.NodeValue(want))
	}
}

func (n *node) memberNamed(key string) *member {
	for _, m := range n.members {
		if m.key == key {
			return m
		}
	}
	return nil
}

// scalarToValue converts a path segment's identity Scalar into a hew.Value,
// for comparing it against a decoded target value (§4.2).
func scalarToValue(s hew.Scalar) hew.Value {
	tag := "!!str"
	switch s.Kind {
	case hew.ScalarBool:
		tag = "!!bool"
	case hew.ScalarNull:
		tag = "!!null"
	case hew.ScalarNumber:
		if strings.ContainsAny(s.Text, ".eE") {
			tag = "!!float"
		} else {
			tag = "!!int"
		}
	}
	return hew.NodeValue(&yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: s.Text})
}

// commentTextOf reads a comment node's text out of a transform value. The IR
// carries a comment as the one-key mapping `{comment: <text>}` (§11.10
// reduction 3, pinned by jsonc/add-with-leading-comment's transforms.hewt); a
// bare string is accepted as the same thing.
func commentTextOf(v hew.Value) (string, bool) {
	n := v.Node()
	if n == nil {
		return "", false
	}
	if n.Kind == yaml.ScalarNode {
		return n.Value, true
	}
	if n.Kind == yaml.MappingNode && len(n.Content) == 2 &&
		n.Content[0].Value == "comment" && n.Content[1].Kind == yaml.ScalarNode {
		return n.Content[1].Value, true
	}
	return "", false
}

// jsonEncode renders a hew.Value as JSONC text in the corpus's house style:
// objects as `{ k: v }` (spaced), arrays as `[v, v2]` (unspaced).
func jsonEncode(v hew.Value) string {
	n := v.Node()
	if n == nil {
		return "null"
	}
	return jsonEncodeNode(n)
}

func jsonEncodeNode(n *yaml.Node) string {
	switch n.Kind {
	case yaml.ScalarNode:
		return jsonScalarLiteral(n)
	case yaml.MappingNode:
		parts := make([]string, 0, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			parts = append(parts, jsonQuote(n.Content[i].Value)+": "+jsonEncodeNode(n.Content[i+1]))
		}
		if len(parts) == 0 {
			return "{}"
		}
		return "{ " + strings.Join(parts, ", ") + " }"
	case yaml.SequenceNode:
		parts := make([]string, 0, len(n.Content))
		for _, c := range n.Content {
			parts = append(parts, jsonEncodeNode(c))
		}
		if len(parts) == 0 {
			return "[]"
		}
		return "[" + strings.Join(parts, ", ") + "]"
	default:
		return "null"
	}
}

func jsonScalarLiteral(n *yaml.Node) string {
	switch n.ShortTag() {
	case "!!bool", "!!int", "!!float":
		return n.Value
	case "!!null":
		return "null"
	default:
		return jsonQuote(n.Value)
	}
}

func jsonQuote(s string) string { return strconv.Quote(s) }

// renderComment writes a comment node's source form. JSONC has two spellings
// and this binding emits the line form, which is what every corpus fixture
// uses and the only one that survives being placed on its own line without
// changing the meaning of the bytes after it.
func renderComment(text string) string { return "// " + text }

func nodeKindOf(n *node) hew.NodeKind {
	switch n.kind {
	case kObj:
		return hew.KindMap
	case kArr:
		return hew.KindSeq
	default:
		return hew.KindScalar
	}
}

func containerCount(n *node) (int, error) {
	if !n.container() {
		return 0, fmt.Errorf("not a container")
	}
	return n.childCount(), nil
}
