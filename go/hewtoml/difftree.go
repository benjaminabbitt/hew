package hewtoml

import (
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/benjaminabbitt/hew"
	"github.com/benjaminabbitt/hew/internal/hewerr"
)

// DiffTree parses source bytes into the format-neutral tree hew.DiffTrees
// walks (§9.4), over the SAME span-annotated parse Apply edits through.
//
// Surface duality (§8.4) is the reason this is a projection of that tree and
// not a second reading of the bytes. The reader already recorded which node
// the document SPELLS where: `tool.ctxloom.timeout = 30` registered `timeout`
// as a member of the table `tool.ctxloom`, and `[server]` registered `port` as
// a member of `server`. Walking that structure makes the differ address every
// node the way the document already writes it — /tool/ctxloom/timeout stays a
// dotted key's leaf and /server/port stays a table member — so no hunk ever
// proposes a respelling nobody asked for (§8.4 rule 1).
//
// One P4 limit: an ADDED node has no existing surface to adopt, and §8.4 rule
// 4 leaves that choice to a `! surface` directive the differ does not emit. A
// diff that adds a whole `[table]` therefore proposes an inline table instead
// of a header block.
func DiffTree(src []byte) (*hew.DiffNode, error) {
	d, err := parseDoc(src)
	if err != nil {
		return nil, &hewerr.Error{Code: hewerr.CodeTargetParse, Component: hewerr.ComponentDiffer,
			Detail: "source does not parse as TOML: " + err.Error()}
	}
	return d.diffNode(d.root), nil
}

// diffChildAt is one child with the source offset that orders it, so that a
// standalone comment lands between the members it stands between.
type diffChildAt struct {
	at    int
	child hew.DiffChild
}

func (d *doc) diffNode(n *tnode) *hew.DiffNode {
	out := &hew.DiffNode{Kind: nodeKind(n), Value: hew.NodeValue(d.nodeValue(n))}
	switch n.kind {
	case nTable:
		var at []diffChildAt
		for _, e := range n.entries {
			at = append(at, diffChildAt{entryPos(e), hew.DiffChild{Key: e.key, Node: d.diffNode(e.val)}})
		}
		// commentChildren is the applier's own comment enumeration — the one
		// `/table/#n` resolves through. Sharing it is what keeps the two
		// halves inverses: a comment the differ counts as #1 is the comment
		// the applier will edit (§4.5b, §9).
		for _, c := range d.commentChildren(n) {
			at = append(at, diffChildAt{c.lineStart, hew.DiffChild{Comment: true, Text: c.text}})
		}
		sort.SliceStable(at, func(i, j int) bool { return at[i].at < at[j].at })
		for _, c := range at {
			out.Children = append(out.Children, c.child)
		}
	case nSeq:
		for _, el := range n.elems {
			out.Children = append(out.Children, hew.DiffChild{Node: d.diffNode(el.val)})
		}
	}
	return out
}

// entryPos is the offset of the first line that DEFINES a member — what a
// standalone comment's position is compared against. An assignment and an
// inline-table member each occupy a span of their own, which is exactly what
// a non-zero blockEnd marks; the entries that do not — a table opened by a
// `[a.b]` header, a dotted key's intermediate — take their node's.
func entryPos(e *entry) int {
	if e.blockEnd > 0 {
		return e.blockStart
	}
	return nodePos(e.val)
}

// nodePos is where a node's definition begins. A `[a.b]` table has its header
// line; a table or array that exists only as a prefix has no line of its own
// and takes its first child's, which is the earliest because the reader
// appends children in source order.
func nodePos(n *tnode) int {
	switch {
	case n.physical:
		return n.blockStart
	case len(n.entries) > 0:
		return entryPos(n.entries[0])
	case len(n.elems) > 0:
		return n.elems[0].blockStart
	}
	return n.start
}

// nodeValue is a node's whole value: the before-image a context or "-" line
// shows, and — on the new side — the after-image an added node writes, which
// is §9.4-R5's "from the new document's own bytes". A scalar carries the tag
// the reader decoded it under, so `port = 8080` diffs as the number and
// `port = "8080"` as the string (§4.2).
func (d *doc) nodeValue(n *tnode) *yaml.Node {
	switch n.kind {
	case nScalar:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: n.tag, Value: n.text}
	case nSeq:
		s := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, el := range n.elems {
			s.Content = append(s.Content, d.nodeValue(el.val))
		}
		return s
	default:
		m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		for _, e := range n.entries {
			m.Content = append(m.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: e.key},
				d.nodeValue(e.val))
		}
		return m
	}
}
