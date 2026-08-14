package hewjson

import (
	"github.com/hew-format/hew"
	"github.com/hew-format/hew/internal/hewerr"
)

// DiffTree parses source bytes into the format-neutral tree hew.DiffTrees
// walks (§9.4). It reuses the SAME parser Apply resolves paths through, which
// is what keeps the differ and the applier the inverses §9 says they are — a
// second parser with a second opinion about the document would break that
// closure quietly.
func DiffTree(src []byte) (*hew.DiffNode, error) {
	d, err := parseDoc(src)
	if err != nil {
		return nil, &hewerr.Error{Code: hewerr.CodeTargetParse, Component: hewerr.ComponentDiffer,
			Detail: "source does not parse as JSON: " + err.Error()}
	}
	return d.diffNode(d.root)
}

func (d *doc) diffNode(n *jNode) (*hew.DiffNode, error) {
	v, err := d.nodeValue(n)
	if err != nil {
		return nil, &hewerr.Error{Code: hewerr.CodeTargetParse, Component: hewerr.ComponentDiffer,
			Detail: err.Error()}
	}
	out := &hew.DiffNode{Kind: nodeKindOf(n), Value: v}
	switch n.kind {
	case jObj:
		for _, m := range n.members {
			child, cerr := d.diffNode(m.value)
			if cerr != nil {
				return nil, cerr
			}
			out.Children = append(out.Children, hew.DiffChild{Key: m.key, Node: child})
		}
	case jArr:
		for _, e := range n.elems {
			child, cerr := d.diffNode(e.value)
			if cerr != nil {
				return nil, cerr
			}
			out.Children = append(out.Children, hew.DiffChild{Node: child})
		}
	}
	return out, nil
}
