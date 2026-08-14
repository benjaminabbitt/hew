package json

import (
	"github.com/benjaminabbitt/hew/go"
	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

// Document parses target bytes into the read-only view hew.Resolve projects
// the abstract transform list against (§9.2). It is the same tree Apply
// resolves paths through, exposed read-only, so `--ops` and `--record` report
// the addresses the applier would really have used rather than a second
// parser's opinion of them.
//
// name is the target's LABEL and is used for diagnostics only — no I/O happens
// here, and nothing about the name changes how the bytes are read. It is in the
// signature because Appendix A.6 puts it there: an extension exported alongside
// a registry entry has to have the registry's shape.
func Document(name string, src []byte) (hew.Document, error) {
	d, err := parseDoc(src)
	if err != nil {
		return nil, &hewerr.Error{Code: hewerr.CodeTargetParse, Component: hewerr.ComponentApplier,
			Target: name, Detail: "target does not parse as JSON: " + err.Error()}
	}
	return jsonDocument{d: d}, nil
}

type jsonDocument struct{ d *doc }

func (jd jsonDocument) Root() hew.Node { return jsonNode{d: jd.d, n: jd.d.root} }

// jsonNode adapts one parsed JSON value to hew.Node.
type jsonNode struct {
	d *doc
	n *jNode
}

func (j jsonNode) Kind() hew.NodeKind { return nodeKindOf(j.n) }

func (j jsonNode) Member(name string) (hew.Node, bool) {
	if j.n.kind != jObj {
		return nil, false
	}
	for _, m := range j.n.members {
		if m.key == name {
			return jsonNode{d: j.d, n: m.value}, true
		}
	}
	return nil, false
}

func (j jsonNode) Len() int {
	switch j.n.kind {
	case jObj:
		return len(j.n.members)
	case jArr:
		return len(j.n.elems)
	default:
		return 0
	}
}

func (j jsonNode) Elem(i int) (hew.Node, bool) {
	if j.n.kind != jArr || i < 0 || i >= len(j.n.elems) {
		return nil, false
	}
	return jsonNode{d: j.d, n: j.n.elems[i].value}, true
}

// Value decodes the node's own source span. A span that does not decode
// yields the absent Value, which compares equal to nothing a key-match
// segment can carry, so an undecodable node simply does not match.
func (j jsonNode) Value() hew.Value {
	v, err := j.d.nodeValue(j.n)
	if err != nil {
		return hew.Value{}
	}
	return v
}
