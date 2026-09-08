package toml

import (
	"strconv"
	"strings"

	"github.com/benjaminabbitt/hew/go"
	"github.com/benjaminabbitt/hew/go/internal/hewmatch"
	"gopkg.in/yaml.v3"
)

// matches implements §6.1's tolerance rules for a before-image value: tables
// match as a SUBSET (every listed key must be present and equal, unlisted keys
// are free), arrays as an ORDERED SUBSEQUENCE, scalars exactly after
// format-native decoding. The surface a table is written at is invisible here,
// which is what makes a context line survive `[a.b]` being respelled as a
// dotted key (§8.4 rule 1).
func matches(n *tnode, want *yaml.Node) bool {
	if n == nil {
		return false
	}
	return hewmatch.Matches(matchNode{n}, want)
}

// equals is matches without the tolerance: the node is exactly the value, not
// merely a superset of it. This is what "the after-image holds" means (§10.6).
func equals(n *tnode, want *yaml.Node) bool {
	if n == nil {
		return false
	}
	return hewmatch.Equals(matchNode{n}, want)
}

// matchNode presents a *tnode to the shared matcher. Only two things about
// TOML are its own here: which of its node kinds is a mapping — a TABLE, which
// is the same structure under a different name — and what makes two scalars
// equal, since the reader has already normalized TOML's own number spellings
// and `0x1e` therefore equals `30`.
type matchNode struct{ n *tnode }

func (m matchNode) Kind() hewmatch.Kind {
	switch m.n.kind {
	case nScalar:
		return hewmatch.Scalar
	case nTable:
		return hewmatch.Mapping
	case nSeq:
		return hewmatch.Sequence
	}
	return hewmatch.Other
}

func (m matchNode) ScalarEquals(want *yaml.Node) bool { return scalarEq(m.n, want) }

// Lookup returns an UNTYPED nil for an absent key: a (*tnode)(nil) inside the
// interface would not compare equal to nil, and the matcher tests for nil.
// Alike in both bindings by necessity: this is the Node contract being
// implemented over a different node type, which is exactly the split between
// the shared traversal and the per-format shape. There is nothing to factor
// out — the bodies name each binding's own entry and element types.
// reprise:ignore
func (m matchNode) Lookup(key string) hewmatch.Node {
	e := m.n.lookup(key)
	if e == nil || e.val == nil {
		return nil
	}
	return matchNode{e.val}
}

// Alike in both bindings by necessity: this is the Node contract being
// implemented over a different node type, which is exactly the split between
// the shared traversal and the per-format shape. There is nothing to factor
// out — the bodies name each binding's own entry and element types.
// reprise:ignore
func (m matchNode) Elems() []hewmatch.Node {
	out := make([]hewmatch.Node, len(m.n.elems))
	for i, e := range m.n.elems {
		if e == nil || e.val == nil {
			continue // leaves a nil entry, which matches nothing
		}
		out[i] = matchNode{e.val}
	}
	return out
}

func (m matchNode) Len() int {
	if m.n.kind == nSeq {
		return len(m.n.elems)
	}
	return len(m.n.entries)
}

// scalarEq is §6.1's "exact, after format-native decoding". The reader has
// already normalized a TOML integer to decimal, so `0x1e` equals `30`; floats
// compare numerically so `1.0` equals `1e0`; and a string never equals a
// number, whatever they look like.
func scalarEq(n *tnode, want *yaml.Node) bool {
	tag := want.ShortTag()
	if tag != n.tag {
		return false
	}
	switch tag {
	case "!!bool":
		return strings.EqualFold(n.text, want.Value)
	case "!!float":
		a, aerr := strconv.ParseFloat(n.text, 64)
		b, berr := strconv.ParseFloat(want.Value, 64)
		if aerr == nil && berr == nil {
			return a == b
		}
	}
	return n.text == want.Value
}

// matchesSeg reports whether a sequence element satisfies a key-match segment
// (§4.2): "name=beta" against the element's field, "=beta" against the element
// itself. Array-of-tables elements are sequence elements like any other, which
// is what gives TOML the identity addressing of §6.4.2.
func matchesSeg(n *tnode, seg hew.Segment) bool {
	c, ok := comparedNode(n, seg)
	return ok && scalarEq(c, hewmatch.ScalarNode(seg.Value))
}

// comparedValue is comparedNode as a hew.Value, which is what the shared
// no-match diagnostic reads (§10.3, O46).
func comparedValue(n *tnode, seg hew.Segment) (hew.Value, bool) {
	c, ok := comparedNode(n, seg)
	if !ok {
		return hew.Value{}, false
	}
	return hew.NodeValue(&yaml.Node{Kind: yaml.ScalarNode, Tag: c.tag, Value: c.text}), true
}

// comparedNode is the node the segment compares this element against, or false
// when the element has nothing to compare. Splitting it out of matchesSeg is
// what lets a failed match name the NEAR MISS it found (§10.3, O46).
func comparedNode(n *tnode, seg hew.Segment) (*tnode, bool) {
	if seg.Name == "" {
		if n.kind != nScalar {
			return nil, false
		}
		return n, true
	}
	if n.kind != nTable {
		return nil, false
	}
	e := n.lookup(seg.Name)
	if e == nil || e.val.kind != nScalar {
		return nil, false
	}
	return e.val, true
}

// describe renders a node for a diagnostic's "found" half: a scalar shows its
// source text, a container its kind.
func (d *doc) describe(n *tnode) string {
	if n == nil {
		return ""
	}
	if n.kind == nScalar {
		return string(d.src[n.start:n.end])
	}
	return n.kind.String()
}

// nodeKind maps a parsed node onto the §7.1 `? kind` vocabulary.
func nodeKind(n *tnode) hew.NodeKind {
	if n == nil {
		return ""
	}
	switch n.kind {
	case nTable:
		return hew.KindMap
	case nSeq:
		return hew.KindSeq
	}
	return hew.KindScalar
}

// childCount is the child count `? count` and `? exhaustive` assert over.
// ok=false covers both a scalar and a comment address: neither is a container.
func childCount(n *tnode) (int, bool) {
	if n == nil {
		return 0, false
	}
	switch n.kind {
	case nTable:
		return len(n.entries), true
	case nSeq:
		return len(n.elems), true
	}
	return 0, false
}

// commentText is the text a transform at a comment address carries — the
// `{comment: "…"}` shape §11.10's reduction 3 leaves in the IR.
func commentText(v hew.Value) (string, bool) { return hew.CommentText(v) }
