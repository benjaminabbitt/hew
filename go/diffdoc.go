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

	// KeyedSet marks a container that HAS NO ADDRESS OF ITS OWN: its children
	// are reachable, individually, by the identity each carries, and the
	// container itself names all of them at once. An HCL set of
	// same-`(type, labels)` blocks is the case that exists (O45): every child
	// is `/provider/"aws"/alias="east"`, and `/provider/"aws"` is HEW012.
	//
	// The differ needs to know, because every op it emits AT a container — an
	// add of a new child, a hunk anchored there — would be an address the
	// applier must refuse. So a child that is added, removed, or re-identified
	// in such a container is HEW020 rather than a patch that cannot apply
	// (§9.4-R6: never silently degrade).
	KeyedSet bool
}

// DiffChild is one positional child of a container: a map member, a sequence
// element, or a standalone comment.
type DiffChild struct {
	// Key is the member name. Empty for a sequence element and for a comment.
	Key string
	// Label marks a child the document addresses by a quoted LABEL rather than
	// by a member name (§4.3): an HCL block's `(type, labels)` tuple spells one
	// address as `/provider/"google"`, whose second step is not a key. Key
	// carries the label text.
	//
	// It lives here rather than in the binding because the differ, not the
	// binding, builds every path it emits — a binding that could not say "this
	// child is spelled as a label" would watch the differ address it as
	// `/provider/google`, which resolves to nothing.
	Label bool
	// MatchField addresses this child by a KEY-MATCH segment (§4.2) on the
	// named field, whose value is MatchValue — `/provider/"aws"/alias="east"`.
	// A binding sets it where the container's children have identity but no
	// key: an HCL set of same-`(type, labels)` blocks, which O45 made an
	// addressable container and which has no member name to be addressed by.
	//
	// It exists as an explicit CHOICE rather than as the identity-field
	// inference §9.4-R4 runs over sequences, because that inference falls back
	// to index addressing when it cannot decide — and an index is not an
	// address a block set has. A binding that cannot name a distinguishing
	// attribute must refuse (§9.4-R6), not degrade.
	MatchField string
	MatchValue Value

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
			// A label, a member name and a key-match are different addresses,
			// so they are different nodes even when they read the same (§4.3).
			switch {
			case c.Label:
				b.WriteByte('l')
			case c.MatchField != "":
				b.WriteByte('m')
				writeToken(b, c.MatchField)
			default:
				b.WriteByte('k')
			}
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
