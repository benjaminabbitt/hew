package hewyaml

import (
	"strings"

	"github.com/hew-format/hew"
	"gopkg.in/yaml.v3"
)

// matches implements §6.1's tolerance rules for a before-image value:
// mappings match as a SUBSET (every listed key must be present and equal,
// unlisted keys are free), sequences as an ORDERED SUBSEQUENCE, scalars
// exactly after format-native decoding. yaml/assert-count-fail is what pins
// subset/subsequence here: its context line lists one element of a
// three-element sequence and must pass, so that the `? count` it guards is
// the assertion that fails.
func (d *doc) matches(n *ynode, want *yaml.Node) bool {
	if n == nil || want == nil {
		return false
	}
	switch want.Kind {
	case yaml.ScalarNode:
		return n.kind == nScalar && scalarEq(n.y, want)
	case yaml.MappingNode:
		if n.kind != nMap {
			return false
		}
		for i := 0; i+1 < len(want.Content); i += 2 {
			e := n.lookup(want.Content[i].Value)
			if e == nil || !d.matches(e.val, want.Content[i+1]) {
				return false
			}
		}
		return true
	case yaml.SequenceNode:
		if n.kind != nSeq {
			return false
		}
		i := 0
		for _, w := range want.Content {
			for i < len(n.elems) && !d.matches(n.elems[i].val, w) {
				i++
			}
			if i >= len(n.elems) {
				return false
			}
			i++
		}
		return true
	}
	return false
}

// equals is matches without the tolerance: the node is exactly the value, not
// merely a superset of it. This is what "the after-image holds" means
// (§10.6) — a mapping that gained the patched key plus six others has not
// already had this patch applied to it.
func (d *doc) equals(n *ynode, want *yaml.Node) bool {
	if n == nil || want == nil {
		return false
	}
	switch want.Kind {
	case yaml.ScalarNode:
		return n.kind == nScalar && scalarEq(n.y, want)
	case yaml.MappingNode:
		if n.kind != nMap || len(n.entries)*2 != len(want.Content) {
			return false
		}
		for i := 0; i+1 < len(want.Content); i += 2 {
			e := n.lookup(want.Content[i].Value)
			if e == nil || !d.equals(e.val, want.Content[i+1]) {
				return false
			}
		}
		return true
	case yaml.SequenceNode:
		if n.kind != nSeq || len(n.elems) != len(want.Content) {
			return false
		}
		for i, w := range want.Content {
			if !d.equals(n.elems[i].val, w) {
				return false
			}
		}
		return true
	}
	return false
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
	want := scalarNode(seg.Value)
	if seg.Name == "" {
		return n.kind == nScalar && scalarEq(n.y, want)
	}
	if n.kind != nMap {
		return false
	}
	e := n.lookup(seg.Name)
	return e != nil && e.val.kind == nScalar && scalarEq(e.val.y, want)
}

// scalarNode converts a path segment's identity Scalar into a YAML scalar
// node, so `port=8080` compares as the number and `port="8080"` as the
// string.
func scalarNode(s hew.Scalar) *yaml.Node {
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
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: s.Text}
}

// describe renders a node for a diagnostic's "found" half: a scalar shows its
// source text, a container its kind.
func (d *doc) describe(n *ynode) string {
	if n.kind == nScalar {
		return string(d.src[n.start:n.end])
	}
	return n.kind.String()
}

// nodeKind maps a parsed node onto the §7.1 `? kind` vocabulary.
func (d *doc) nodeKind(n *ynode) hew.NodeKind {
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
func childCount(n *ynode) (int, bool) {
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
