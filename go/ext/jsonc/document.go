package jsonc

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
			Target: name, Detail: "target does not parse as JSONC: " + err.Error()}
	}
	return jsoncDocument{d: d}, nil
}

type jsoncDocument struct{ d *doc }

func (jd jsoncDocument) Root() hew.Node { return jsoncNode{n: jd.d.root} }

// jsoncNode adapts one parsed JSONC value to hew.Node.
//
// COMMENTS ARE NOT MEMBERS. A comment is addressed as `/container/#n` (§4.5b),
// a form with no RFC 6901 projection at all, so Len counts members and elements
// only and a comment child is invisible here. That is the same split the core's
// Node contract states — the Markdown kinds and comments have no pointer — and
// keeping it means an index resolved through this view cannot silently shift
// because someone wrote a comment above the element.
type jsoncNode struct{ n *node }

func (j jsoncNode) Kind() hew.NodeKind { return nodeKindOf(j.n) }

func (j jsoncNode) Member(name string) (hew.Node, bool) {
	if j.n.kind != kObj {
		return nil, false
	}
	m := j.n.memberNamed(name)
	if m == nil {
		return nil, false
	}
	return jsoncNode{n: m.value}, true
}

func (j jsoncNode) Len() int {
	switch j.n.kind {
	case kObj:
		return len(j.n.members)
	case kArr:
		return len(j.n.elems)
	default:
		return 0
	}
}

func (j jsoncNode) Elem(i int) (hew.Node, bool) {
	if j.n.kind != kArr || i < 0 || i >= len(j.n.elems) {
		return nil, false
	}
	return jsoncNode{n: j.n.elems[i].value}, true
}

// Value decodes the node's own source span. A span that does not decode yields
// the absent Value, which compares equal to nothing a key-match segment can
// carry, so an undecodable node simply does not match.
func (j jsoncNode) Value() hew.Value {
	v, err := j.n.hewValue()
	if err != nil {
		return hew.Value{}
	}
	return v
}
