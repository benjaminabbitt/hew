package hewjsonc

import (
	"github.com/benjaminabbitt/hew"
	"github.com/benjaminabbitt/hew/internal/hewerr"
)

// DiffTree parses source bytes into the format-neutral tree hew.DiffTrees
// walks (§9.4), over the same parser Apply uses.
//
// Comments come out as positional CHILDREN, in source order, which is what
// their `/container/#n` address says they are (§4.5b) — and it is what lets
// the differ notice a comment that arrived alongside the member it documents,
// the capability §8.2 exists for.
func DiffTree(src []byte) (*hew.DiffNode, error) {
	d, err := parseDoc(src)
	if err != nil {
		return nil, &hewerr.Error{Code: hewerr.CodeTargetParse, Component: hewerr.ComponentDiffer,
			Detail: "source does not parse as JSONC: " + err.Error()}
	}
	return diffNode(d.root)
}

func diffNode(n *node) (*hew.DiffNode, error) {
	v, err := n.hewValue()
	if err != nil {
		return nil, &hewerr.Error{Code: hewerr.CodeTargetParse, Component: hewerr.ComponentDiffer,
			Detail: err.Error()}
	}
	out := &hew.DiffNode{Kind: nodeKindOf(n), Value: v}
	if !n.container() {
		return out, nil
	}
	// slots() orders a container's members, elements and free comments by
	// source position, with each item's own leading comments folded into its
	// span; unfolding them here restores the standalone comment nodes §4.5b
	// numbers, in the order the file writes them.
	for _, s := range n.slots() {
		if s.comment != nil {
			out.Children = append(out.Children, hew.DiffChild{Comment: true, Text: s.comment.text})
			continue
		}
		for _, c := range s.leading {
			out.Children = append(out.Children, hew.DiffChild{Comment: true, Text: c.text})
		}
		var key string
		var val *node
		if s.member != nil {
			key, val = s.member.key, s.member.value
		} else {
			val = s.elem.value
		}
		child, cerr := diffNode(val)
		if cerr != nil {
			return nil, cerr
		}
		out.Children = append(out.Children, hew.DiffChild{Key: key, Node: child})
	}
	return out, nil
}
