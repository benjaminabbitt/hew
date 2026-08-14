package yaml

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
			Target: name, Detail: "target does not parse as YAML: " + err.Error()}
	}
	return yamlDocument{d: d}, nil
}

type yamlDocument struct{ d *doc }

func (yd yamlDocument) Root() hew.Node { return yamlNode{d: yd.d, n: yd.d.root} }

// yamlNode adapts one parsed YAML node to hew.Node.
//
// Two of §8.3's rules are load-bearing here and both are deliberately the same
// answer the APPLIER gives, because a read-only view that disagreed with the
// applier about what a path names would make `--ops` a second opinion rather
// than a report:
//
//   - An ALIAS is transparent. Every accessor derefs it, so `/defaults/host`
//     resolves through `*base` exactly as a step does.
//   - A MERGE-INHERITED key is NOT a member. lookup skips `<<` entries, which
//     is the rule apply states as "an inherited key is not present at this
//     site" — addressing names what is written here, not what is visible here.
type yamlNode struct {
	d *doc
	n *ynode
}

// deref follows an alias to its anchor. An alias whose anchor is missing stays
// itself: the document is malformed and a reader reports what is there rather
// than inventing a target.
func (y yamlNode) deref() *ynode {
	n := y.n
	if n.kind == nAlias {
		if target, ok := y.d.anchors[n.y.Value]; ok {
			return target
		}
	}
	return n
}

// Kind reuses the binding's own kind projection, which already makes an alias
// report the kind of what it points at.
func (y yamlNode) Kind() hew.NodeKind { return y.d.nodeKind(y.n) }

func (y yamlNode) Member(name string) (hew.Node, bool) {
	n := y.deref()
	if n.kind != nMap {
		return nil, false
	}
	e := n.lookup(name)
	if e == nil {
		return nil, false
	}
	return yamlNode{d: y.d, n: e.val}, true
}

func (y yamlNode) Len() int {
	n := y.deref()
	switch n.kind {
	case nMap:
		return len(n.entries)
	case nSeq:
		return len(n.elems)
	default:
		return 0
	}
}

func (y yamlNode) Elem(i int) (hew.Node, bool) {
	n := y.deref()
	if n.kind != nSeq || i < 0 || i >= len(n.elems) {
		return nil, false
	}
	return yamlNode{d: y.d, n: n.elems[i].val}, true
}

// Value carries the scalar's decoded form, the same pair comparedValue hands
// the key-match comparison. A non-scalar yields the absent Value, which
// compares equal to nothing a key-match segment can carry.
func (y yamlNode) Value() hew.Value {
	n := y.deref()
	if n.kind != nScalar {
		return hew.Value{}
	}
	return hew.NodeValue(n.y)
}
