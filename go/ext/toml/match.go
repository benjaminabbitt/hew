package toml

import (
	"strconv"
	"strings"

	"github.com/benjaminabbitt/hew/go"
	"gopkg.in/yaml.v3"
)

// matches implements §6.1's tolerance rules for a before-image value: tables
// match as a SUBSET (every listed key must be present and equal, unlisted keys
// are free), arrays as an ORDERED SUBSEQUENCE, scalars exactly after
// format-native decoding. The surface a table is written at is invisible here,
// which is what makes a context line survive `[a.b]` being respelled as a
// dotted key (§8.4 rule 1).
func matches(n *tnode, want *yaml.Node) bool {
	if n == nil || want == nil {
		return false
	}
	switch want.Kind {
	case yaml.ScalarNode:
		return n.kind == nScalar && scalarEq(n, want)
	case yaml.MappingNode:
		if n.kind != nTable {
			return false
		}
		for i := 0; i+1 < len(want.Content); i += 2 {
			e := n.lookup(want.Content[i].Value)
			if e == nil || !matches(e.val, want.Content[i+1]) {
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
			for i < len(n.elems) && !matches(n.elems[i].val, w) {
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
// merely a superset of it. This is what "the after-image holds" means (§10.6).
func equals(n *tnode, want *yaml.Node) bool {
	if n == nil || want == nil {
		return false
	}
	switch want.Kind {
	case yaml.ScalarNode:
		return n.kind == nScalar && scalarEq(n, want)
	case yaml.MappingNode:
		if n.kind != nTable || len(n.entries)*2 != len(want.Content) {
			return false
		}
		for i := 0; i+1 < len(want.Content); i += 2 {
			e := n.lookup(want.Content[i].Value)
			if e == nil || !equals(e.val, want.Content[i+1]) {
				return false
			}
		}
		return true
	case yaml.SequenceNode:
		if n.kind != nSeq || len(n.elems) != len(want.Content) {
			return false
		}
		for i, w := range want.Content {
			if !equals(n.elems[i].val, w) {
				return false
			}
		}
		return true
	}
	return false
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
	want := scalarNode(seg.Value)
	if seg.Name == "" {
		return n.kind == nScalar && scalarEq(n, want)
	}
	if n.kind != nTable {
		return false
	}
	e := n.lookup(seg.Name)
	return e != nil && e.val.kind == nScalar && scalarEq(e.val, want)
}

// segNamesValue applies §4.2's key-match comparison to a value in hand, for a
// node this run is adding and which therefore has no *tnode to compare — the
// same question matchesSeg asks of a parsed element.
func segNamesValue(seg hew.Segment, v hew.Value) bool {
	n := v.Node()
	if n == nil || seg.Kind != hew.SegMatch {
		return false
	}
	want := scalarNode(seg.Value)
	if seg.Name == "" {
		return n.Kind == yaml.ScalarNode && yamlScalarEq(n, want)
	}
	if n.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == seg.Name {
			return n.Content[i+1].Kind == yaml.ScalarNode && yamlScalarEq(n.Content[i+1], want)
		}
	}
	return false
}

// yamlScalarEq is scalarEq between two YAML scalars: the same rule, with the
// reader's decoding already done on both sides.
func yamlScalarEq(got, want *yaml.Node) bool {
	return scalarEq(&tnode{kind: nScalar, tag: got.ShortTag(), text: got.Value}, want)
}

// scalarNode converts a path segment's identity Scalar into a YAML scalar
// node, so `port=8080` compares as the number and `port="8080"` as the string.
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
