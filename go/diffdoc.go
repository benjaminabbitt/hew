package hew

import (
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// DiffNode is one node of the format-neutral document tree the differ walks
// (§9.4). A format binding builds it from the SAME tree its applier parses —
// the differ and the applier are format-side inverses (§9), and two parsers
// with two opinions about a document would break that closure.
//
// It is deliberately not hew.Node: Node is the read-only projection Resolve
// addresses through, and it exposes neither a map's key ORDER nor a
// container's comment children (§4.5b) — the two things a diff cannot be
// computed without.
type DiffNode struct {
	// Kind is KindMap, KindSeq or KindScalar. A comment is not a node kind
	// here: comments are positional CHILDREN of a container, which is what
	// their `/container/#n` address says they are.
	Kind NodeKind

	// Children are the container's positional children in SOURCE ORDER,
	// comments interleaved where they stand. Empty for a scalar.
	Children []DiffChild

	// Value is the node's whole value, carrying the source's own scalar
	// quoting and block/flow style so that §9.4-R5 — an added node renders
	// from the new document's own bytes — survives into the rendered patch.
	Value Value
}

// DiffChild is one positional child of a container: a map member, a sequence
// element, or a standalone comment.
type DiffChild struct {
	// Key is the member name. Empty for a sequence element and for a comment.
	Key string

	// Comment marks a standalone comment child (§4.5b); Text is its content,
	// with the marker and one leading space already stripped, which is the
	// form §6.1 compares. Node is nil.
	Comment bool
	Text    string
	// Node is the child's value, nil for a comment.
	Node *DiffNode
}

// scalarNode reports the child's identity value for a named field, or ok=false
// when the field is missing or is not a scalar (§9.4-R4's "present on every
// element, scalar").
func (n *DiffNode) member(name string) (*DiffNode, bool) {
	if n == nil || n.Kind != KindMap {
		return nil, false
	}
	for _, c := range n.Children {
		if !c.Comment && c.Key == name {
			return c.Node, true
		}
	}
	return nil, false
}

// canonical renders a node as a deterministic token string. Node equality
// (§9.4-R1's "Myers over node equality") is defined as equality of these
// tokens, which makes equality total, comment-aware — two JSONC objects that
// differ only in a comment are NOT the same node — and independent of the
// order the bindings happen to build their trees in.
func (n *DiffNode) canonical() string {
	var b strings.Builder
	writeCanonical(&b, n)
	return b.String()
}

func writeCanonical(b *strings.Builder, n *DiffNode) {
	if n == nil {
		b.WriteString("~")
		return
	}
	switch n.Kind {
	case KindMap, KindSeq:
		if n.Kind == KindMap {
			b.WriteByte('{')
		} else {
			b.WriteByte('[')
		}
		for _, c := range n.Children {
			if c.Comment {
				b.WriteByte('#')
				writeToken(b, c.Text)
				continue
			}
			b.WriteByte('k')
			writeToken(b, c.Key)
			writeCanonical(b, c.Node)
		}
		if n.Kind == KindMap {
			b.WriteByte('}')
		} else {
			b.WriteByte(']')
		}
	default:
		b.WriteString(scalarToken(n.Value))
	}
}

// writeToken writes a length-prefixed string, so that no concatenation of
// child tokens can be read two ways.
func writeToken(b *strings.Builder, s string) {
	b.WriteString(strconv.Itoa(len(s)))
	b.WriteByte(':')
	b.WriteString(s)
}

// scalarToken is a value's canonical token: its resolved tag and its text, so
// that the number 8080 and the string "8080" are different nodes (§4.2's
// format-native decoding, applied to equality).
func scalarToken(v Value) string {
	n := v.Node()
	if n == nil {
		return "!!absent:"
	}
	if n.Kind != yaml.ScalarNode {
		return "!!node:" + v.String()
	}
	return n.ShortTag() + ":" + n.Value
}

// sameNode reports node equality, the relation the sequence diff runs over.
func sameNode(a, b *DiffNode) bool { return a.canonical() == b.canonical() }
