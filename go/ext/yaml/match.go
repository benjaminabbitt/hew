package yaml

import (
	"github.com/benjaminabbitt/hew/go"
	"github.com/benjaminabbitt/hew/go/internal/hewmatch"
	"gopkg.in/yaml.v3"
)

// matches implements §6.1's tolerance rules for a before-image value:
// mappings match as a SUBSET (every listed key must be present and equal,
// unlisted keys are free), sequences as an ORDERED SUBSEQUENCE, scalars
// exactly after format-native decoding. yaml/assert-count-fail is what pins
// subset/subsequence here: its context line lists one element of a
// three-element sequence and must pass, so that the `? count` it guards is
// the assertion that fails.
func matches(n *ynode, want *yaml.Node) bool {
	if n == nil {
		return false
	}
	return hewmatch.Matches(matchNode{n}, want)
}

// equals is matches without the tolerance: the node is exactly the value, not
// merely a superset of it. This is what "the after-image holds" means
// (§10.6) — a mapping that gained the patched key plus six others has not
// already had this patch applied to it.
func equals(n *ynode, want *yaml.Node) bool {
	if n == nil {
		return false
	}
	return hewmatch.Equals(matchNode{n}, want)
}

// matchNode presents a *ynode to the shared matcher. The two YAML-specific
// facts stay here: scalar equality after YAML's own decoding, and that lookup
// skips a merge-key entry, so an inherited key is not found by a direct
// lookup.
//
// Neither matches nor equals ever read the *doc they used to hang off; the
// receiver was threaded through the recursion doing nothing, and dropping it
// is what makes them the same function the TOML binding calls.
type matchNode struct{ n *ynode }

func (m matchNode) Kind() hewmatch.Kind {
	switch m.n.kind {
	case nScalar:
		return hewmatch.Scalar
	case nMap:
		return hewmatch.Mapping
	case nSeq:
		return hewmatch.Sequence
	}
	return hewmatch.Other
}

func (m matchNode) ScalarEquals(want *yaml.Node) bool { return scalarEq(m.n.y, want) }

// Lookup returns an UNTYPED nil for an absent key: a (*ynode)(nil) inside the
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

// scalarEq is §6.1's "exact, after format-native decoding": 8080 is 8080 in
// any quoting or spacing, and 8080 is not "8080".
func scalarEq(a, b *yaml.Node) bool {
	return a.ShortTag() == b.ShortTag() && a.Value == b.Value
}

// matchesSeg reports whether a sequence element satisfies a key-match segment
// (§4.2): "name=github" against the element's field, "=beta" against the
// element itself.
func (d *doc) matchesSeg(n *ynode, seg hew.Segment) bool {
	v, ok := d.comparedValue(n, seg)
	return ok && scalarEq(v.Node(), hewmatch.ScalarNode(seg.Value))
}

// comparedValue is the value the segment compares this element against, or
// false when the element has nothing to compare. Splitting it out of matchesSeg
// is what lets a failed match name the NEAR MISS it found (§10.3, O46).
func (d *doc) comparedValue(n *ynode, seg hew.Segment) (hew.Value, bool) {
	if seg.Name == "" {
		if n.kind != nScalar {
			return hew.Value{}, false
		}
		return hew.NodeValue(n.y), true
	}
	if n.kind != nMap {
		return hew.Value{}, false
	}
	e := n.lookup(seg.Name)
	if e == nil || e.val.kind != nScalar {
		return hew.Value{}, false
	}
	return hew.NodeValue(e.val.y), true
}

// describe renders a node for a diagnostic's "found" half: a scalar shows its
// source text, a container its kind.
func (d *doc) describe(n *ynode) string {
	if n.kind == nScalar {
		return string(d.src[n.start:n.end])
	}
	return n.kind.String()
}

// nodeKind maps a parsed node onto the §7.1 `? kind` vocabulary. A comment
// address resolves to no node and matches no kind — §7.1's vocabulary has no
// entry for a comment — so the assertion fails rather than crashing.
func (d *doc) nodeKind(n *ynode) hew.NodeKind {
	if n == nil {
		return ""
	}
	switch n.kind {
	case nMap:
		return hew.KindMap
	case nSeq:
		return hew.KindSeq
	case nAlias:
		if target, ok := d.anchors[n.y.Value]; ok {
			return d.nodeKind(target)
		}
	}
	return hew.KindScalar
}

// childCount is the child count `? count` and `? exhaustive` assert over.
// ok=false covers both a scalar and a comment address: neither is a container.
func childCount(n *ynode) (int, bool) {
	if n == nil {
		return 0, false
	}
	switch n.kind {
	case nMap:
		return len(n.entries), true
	case nSeq:
		return len(n.elems), true
	}
	return 0, false
}

// commentText is the text a transform at a comment address carries — the
// `{comment: "…"}` shape §11.10's reduction 3 leaves in the IR.
func commentText(v hew.Value) (string, bool) { return hew.CommentText(v) }
