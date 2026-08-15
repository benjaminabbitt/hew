package hew

import (
	"bytes"
	"errors"
	"strings"

	"gopkg.in/yaml.v3"
)

// Value is a transform's value (spec §9.6: "the value, in YAML"). It holds the
// YAML node exactly as authored — native scalar type, key order, and block or
// flow style — because the .hewt codec's output is pinned byte-for-byte by the
// corpus and because a format binding needs the authored type (`8080` the
// number is not `"8080"` the string) to write the target correctly.
//
// The zero Value is the absent value, distinct from an explicit `value: null`.
type Value struct {
	node *yaml.Node
}

// IsZero reports whether the value is absent, as opposed to an explicit null.
func (v Value) IsZero() bool { return v.node == nil }

// Node returns the underlying YAML node. It is the format bindings' entry
// point into a value; treat it as read-only, and copy before mutating.
func (v Value) Node() *yaml.Node { return v.node }

// Decode unmarshals the value into out, as gopkg.in/yaml.v3 would.
func (v Value) Decode(out any) error {
	if v.node == nil {
		return nil
	}
	return v.node.Decode(out)
}

// ValueOf builds a Value from an ordinary Go value. Mappings built this way
// lose key order (Go maps are unordered), so the codec preserves decoded nodes
// rather than re-encoding through this path.
func ValueOf(x any) (Value, error) {
	// A Value handed back in is the value it already is. Transform.Value and
	// ResolvedOp.Value give a caller this type, so the API that CONSUMES a
	// value has to accept the one the API PRODUCES — encoding the struct
	// instead yields `{}`, because its only field is unexported, and an
	// assertion of `{}` fails much later against the target while naming the
	// wrong problem entirely.
	switch v := x.(type) {
	case Value:
		if v.IsZero() {
			return Value{}, errors.New("the zero Value carries no node; it asserts nothing")
		}
		return v, nil
	case *Value:
		if v == nil || v.IsZero() {
			return Value{}, errors.New("the zero Value carries no node; it asserts nothing")
		}
		return *v, nil
	}
	var n yaml.Node
	if err := n.Encode(x); err != nil {
		return Value{}, err
	}
	return Value{node: &n}, nil
}

// NodeValue wraps an already-built YAML node.
func NodeValue(n *yaml.Node) Value { return Value{node: n} }

// CommentValue builds the value a transform at a comment address carries: the
// `{comment: "…"}` mapping §11.10's reduction 3 leaves behind once the mirror
// grammar's comment-attachment spelling is desugared into an op at a comment
// address (§4.5b). The corpus pins the shape — see
// jsonc/add-with-leading-comment's and yaml/set-scalar's transforms fixtures.
func CommentValue(text string) Value {
	return Value{node: &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "comment"},
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: text},
	}}}
}

// CommentText is CommentValue's inverse: the comment text a value carries, or
// ok=false if the value is not a comment value.
func CommentText(v Value) (string, bool) {
	n := v.node
	if n == nil || n.Kind != yaml.MappingNode || len(n.Content) != 2 {
		return "", false
	}
	if n.Content[0].Value != "comment" || n.Content[1].Kind != yaml.ScalarNode {
		return "", false
	}
	return n.Content[1].Value, true
}

// Equal compares two values structurally: node kind, resolved tag, scalar
// text, and children in order. Presentation — flow vs block style, quoting
// choice, comments, source position — is deliberately not compared, so that
// Equal answers "same value" rather than "same bytes".
func (v Value) Equal(o Value) bool { return nodesEqual(v.node, o.node) }

func nodesEqual(a, b *yaml.Node) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Kind != b.Kind || a.ShortTag() != b.ShortTag() || a.Value != b.Value {
		return false
	}
	if len(a.Content) != len(b.Content) {
		return false
	}
	for i := range a.Content {
		if !nodesEqual(a.Content[i], b.Content[i]) {
			return false
		}
	}
	return true
}

// String renders the value as compact single-line YAML, for diagnostics
// (hewerr's Want/Got fields).
func (v Value) String() string {
	if v.node == nil {
		return ""
	}
	flow := flowCopy(v.node)
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(flow); err != nil {
		return v.node.Value
	}
	_ = enc.Close()
	return strings.TrimRight(buf.String(), "\n")
}

func flowCopy(n *yaml.Node) *yaml.Node {
	c := *n
	c.HeadComment, c.LineComment, c.FootComment = "", "", ""
	if n.Kind == yaml.MappingNode || n.Kind == yaml.SequenceNode {
		c.Style = yaml.FlowStyle
	}
	c.Content = make([]*yaml.Node, len(n.Content))
	for i, child := range n.Content {
		c.Content[i] = flowCopy(child)
	}
	return &c
}

// cloneNode deep-copies a node and clears its source positions, so that a
// value decoded from one document can be emitted into another without
// carrying stale line numbers that would perturb the emitter.
func cloneNode(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	c := *n
	c.Line, c.Column = 0, 0
	c.Content = make([]*yaml.Node, len(n.Content))
	for i, child := range n.Content {
		c.Content[i] = cloneNode(child)
	}
	if n.Alias != nil {
		c.Alias = cloneNode(n.Alias)
	}
	return &c
}
