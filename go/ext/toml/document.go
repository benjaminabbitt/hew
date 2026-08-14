package toml

import (
	"github.com/benjaminabbitt/hew/go"
	"github.com/benjaminabbitt/hew/go/internal/hewerr"
	"gopkg.in/yaml.v3"
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
//
// Until this existed the TOML binding was a HALF binding — Applier and Differ
// but no reader — and the gap was not cosmetic. Resolve had nothing to resolve
// against, so no §9.7 application record could be produced for a TOML target;
// and on the document API every step recording `readBefore` (Replace, and
// Remove — including an OPTIONAL remove, whose escape hatch fires only on a
// MISSING address, not on an unreadable format) failed outright with
// "this build cannot read \"toml\" documents". A consumer therefore could not
// express a null-delete against TOML at all.
func Document(name string, src []byte) (hew.Document, error) {
	d, err := parseDoc(src)
	if err != nil {
		return nil, &hewerr.Error{Code: hewerr.CodeTargetParse, Component: hewerr.ComponentApplier,
			Target: name, Detail: "target does not parse as TOML: " + err.Error()}
	}
	return tomlDocument{d: d}, nil
}

type tomlDocument struct{ d *doc }

func (td tomlDocument) Root() hew.Node { return tomlNode{n: td.d.root} }

// tomlNode adapts one parsed TOML node to hew.Node.
//
// The projection is over nkind, NOT over the surface a node was written in.
// §8.4's dotted-key / table-header / inline-table duality is a spelling
// difference the tree has already collapsed — a table is nTable however it was
// spelled — so an address resolves the same against `[a.b]`, `a.b.c = 1` and
// `a = {b = {...}}`. That is the whole point of resolving against the applier's
// own tree rather than a second parser's.
type tomlNode struct{ n *tnode }

func (t tomlNode) Kind() hew.NodeKind {
	switch t.n.kind {
	case nTable:
		return hew.KindMap
	case nSeq:
		return hew.KindSeq
	default:
		return hew.KindScalar
	}
}

// Member reads `entries`, the table's MEMBERS, never `lines`. The two are
// deliberately different sets (§8.4 rule 1): a dotted key registers its member
// on the table the dots name while the line itself lives in the body it was
// written in. Addressing is about membership, so entries is the correct set —
// reading lines would miss every dotted-key member and invent members for
// tables that merely hold the line.
func (t tomlNode) Member(name string) (hew.Node, bool) {
	if t.n.kind != nTable {
		return nil, false
	}
	e := t.n.lookup(name)
	if e == nil {
		return nil, false
	}
	return tomlNode{n: e.val}, true
}

func (t tomlNode) Len() int {
	switch t.n.kind {
	case nTable:
		return len(t.n.entries)
	case nSeq:
		return len(t.n.elems)
	default:
		return 0
	}
}

func (t tomlNode) Elem(i int) (hew.Node, bool) {
	if t.n.kind != nSeq || i < 0 || i >= len(t.n.elems) {
		return nil, false
	}
	return tomlNode{n: t.n.elems[i].val}, true
}

// Value carries the scalar's DECODED form (tag plus canonical text), the same
// pair comparedValue hands the key-match comparison, so `0x1e`, `1_0` and `30`
// compare as the integer they denote rather than as three strings. A non-scalar
// yields the absent Value, which compares equal to nothing.
func (t tomlNode) Value() hew.Value {
	if t.n.kind != nScalar {
		return hew.Value{}
	}
	return hew.NodeValue(&yaml.Node{Kind: yaml.ScalarNode, Tag: t.n.tag, Value: t.n.text})
}
