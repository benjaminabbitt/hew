package hewyaml

import (
	"sort"

	"github.com/benjaminabbitt/hew/go"
	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

// DiffTree parses source bytes into the format-neutral tree hew.DiffTrees
// walks (§9.4), over the same span-annotated parse Apply edits through.
//
// Every node's Value is the yaml.v3 node exactly as parsed, block-or-flow
// style and scalar quoting intact, which is what lets §9.4-R5 hold: an added
// node renders from the NEW document's own shape rather than from a
// canonicalized re-emission of it.
func DiffTree(src []byte) (*hew.DiffNode, error) {
	d, err := parseDoc(src)
	if err != nil {
		return nil, &hewerr.Error{Code: hewerr.CodeTargetParse, Component: hewerr.ComponentDiffer,
			Detail: "source does not parse as YAML: " + err.Error()}
	}
	return d.diffNode(d.root), nil
}

// diffChildAt is one child with the source position that orders it, so that a
// standalone comment lands between the members it stands between.
type diffChildAt struct {
	at    int
	child hew.DiffChild
}

func (d *doc) diffNode(n *ynode) *hew.DiffNode {
	out := &hew.DiffNode{Kind: kindOf(n), Value: hew.NodeValue(n.y)}
	if n.kind != nMap && n.kind != nSeq {
		return out
	}
	var at []diffChildAt
	switch n.kind {
	case nMap:
		for _, e := range n.entries {
			at = append(at, diffChildAt{e.lineStart, hew.DiffChild{Key: e.key, Node: d.diffNode(e.val)}})
		}
	case nSeq:
		for _, el := range n.elems {
			at = append(at, diffChildAt{el.lineStart, hew.DiffChild{Node: d.diffNode(el.val)}})
		}
	}
	for _, c := range d.commentChildren(n) {
		at = append(at, diffChildAt{c.lineStart, hew.DiffChild{Comment: true, Text: c.text}})
	}
	sort.SliceStable(at, func(i, j int) bool { return at[i].at < at[j].at })
	for _, c := range at {
		out.Children = append(out.Children, c.child)
	}
	return out
}

// kindOf projects this binding's node vocabulary onto the three kinds a diff
// tree distinguishes. An alias is a scalar here: it addresses one node, and
// §8.3's alias policy is an APPLY-time question (`! anchor`), not a shape.
func kindOf(n *ynode) hew.NodeKind {
	switch n.kind {
	case nMap:
		return hew.KindMap
	case nSeq:
		return hew.KindSeq
	default:
		return hew.KindScalar
	}
}
