package hcl

import (
	"bytes"
	"sort"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// The span-tracked HCL tree. hclsyntax parses; this file records where every
// node's bytes are, because every edit hew makes is a splice against the
// original source and never a re-serialization (§6.3, §8.5).
//
// The tree is deliberately thin: it is hclsyntax's own AST plus, for each
// body, its items in SOURCE ORDER (hclsyntax hands attributes back in a map)
// and, for each item, the LINE region it occupies. Line regions are what
// insertion, removal and `=` realignment all address.

// itemKind discriminates the two things a body holds (§8.5: "an attribute body
// line contains a top-level `=`; a block body line ends in `{`").
type itemKind uint8

const (
	itemAttr itemKind = iota
	itemBlock
)

// itemNode is one direct child of a body.
type itemNode struct {
	kind   itemKind
	name   string   // attribute name, or block type
	labels []string // block only
	body   *bodyNode

	// start..end is the item's LINE region: from the first byte of its first
	// line through the newline that ends its last. Splicing whole lines is
	// what keeps indentation and line structure intact.
	start, end         int
	startLine, endLine int
	indent             int

	// Attribute spans. nameEnd..eqStart is the run of spaces realignment
	// rewrites; exprStart..exprEnd is the value a replace splices.
	nameEnd, eqStart   int
	exprStart, exprEnd int
	expr               hclsyntax.Expression
}

// bodyNode is a body: the document root, or a block's braces' contents.
type bodyNode struct {
	items []*itemNode

	// open is the byte offset of the `{` that opens this body, and doubles as
	// the body's identity across the realignment re-parse. The root body has
	// no brace and uses rootBody.
	open                 int
	innerStart, innerEnd int
	indent               int
	owner                *itemNode // nil at the root
}

// rootBody is the document root's body identity: no `{` opens it.
const rootBody = -1

// bodyIndent is the indentation step this binding writes for a body it
// creates or extends. hclfmt's own step, and the corpus's.
const bodyIndent = 2

type doc struct {
	src  []byte
	root *bodyNode
}

func parseDoc(src []byte) (*doc, error) {
	f, diags := hclsyntax.ParseConfig(src, "target", hcl.InitialPos)
	if diags.HasErrors() {
		return nil, diags
	}
	hb, ok := f.Body.(*hclsyntax.Body)
	if !ok {
		return nil, diags
	}
	d := &doc{src: src}
	d.root = d.buildBody(hb, rootBody, 0, len(src), nil)
	return d, nil
}

func (d *doc) buildBody(hb *hclsyntax.Body, open, innerStart, innerEnd int, owner *itemNode) *bodyNode {
	b := &bodyNode{open: open, innerStart: innerStart, innerEnd: innerEnd, owner: owner}
	for _, a := range hb.Attributes {
		b.items = append(b.items, d.buildAttr(a))
	}
	for _, blk := range hb.Blocks {
		b.items = append(b.items, d.buildBlock(blk))
	}
	sort.Slice(b.items, func(i, j int) bool { return b.items[i].start < b.items[j].start })
	switch {
	case len(b.items) > 0:
		b.indent = b.items[0].indent
	case owner != nil:
		b.indent = owner.indent + bodyIndent
	}
	for _, it := range b.items {
		if it.body != nil {
			it.body.owner = it
		}
	}
	return b
}

func (d *doc) buildAttr(a *hclsyntax.Attribute) *itemNode {
	er := a.Expr.Range()
	it := &itemNode{
		kind:      itemAttr,
		name:      a.Name,
		start:     d.lineStart(a.SrcRange.Start.Byte),
		end:       d.lineEnd(a.SrcRange.End.Byte),
		startLine: a.SrcRange.Start.Line,
		endLine:   a.SrcRange.End.Line,
		nameEnd:   a.NameRange.End.Byte,
		eqStart:   a.EqualsRange.Start.Byte,
		exprStart: er.Start.Byte,
		exprEnd:   er.End.Byte,
		expr:      a.Expr,
	}
	it.indent = d.indentAt(it.start)
	return it
}

func (d *doc) buildBlock(blk *hclsyntax.Block) *itemNode {
	it := &itemNode{
		kind:      itemBlock,
		name:      blk.Type,
		labels:    blk.Labels,
		start:     d.lineStart(blk.TypeRange.Start.Byte),
		end:       d.lineEnd(blk.CloseBraceRange.End.Byte),
		startLine: blk.TypeRange.Start.Line,
		endLine:   blk.CloseBraceRange.End.Line,
	}
	it.indent = d.indentAt(it.start)
	it.body = d.buildBody(blk.Body, blk.OpenBraceRange.Start.Byte,
		blk.OpenBraceRange.End.Byte, blk.CloseBraceRange.Start.Byte, it)
	return it
}

// lineStart is the offset of the first byte of the line containing off.
func (d *doc) lineStart(off int) int {
	if off > len(d.src) {
		off = len(d.src)
	}
	return bytes.LastIndexByte(d.src[:off], '\n') + 1
}

// lineEnd is the offset just past the newline that ends the line containing
// off — or the end of the source for an unterminated last line.
func (d *doc) lineEnd(off int) int {
	if off >= len(d.src) {
		return len(d.src)
	}
	i := bytes.IndexByte(d.src[off:], '\n')
	if i < 0 {
		return len(d.src)
	}
	return off + i + 1
}

// indentAt counts the leading horizontal whitespace of the line starting at
// off.
func (d *doc) indentAt(off int) int {
	n := 0
	for off+n < len(d.src) && (d.src[off+n] == ' ' || d.src[off+n] == '\t') {
		n++
	}
	return n
}

// blankRun reports the extent of the run of blank lines immediately before
// start, bounded below by lo. It is what makes removing a top-level block take
// the blank line that separated it with it (hcl/remove-block).
func (d *doc) blankRunBefore(start, lo int) int {
	s := start
	for s > lo {
		ls := d.lineStart(s - 1)
		if ls < lo || !isBlank(d.src[ls:s]) {
			break
		}
		s = ls
	}
	return s
}

// blankRunAfter is blankRunBefore's mirror, bounded above by hi.
func (d *doc) blankRunAfter(end, hi int) int {
	e := end
	for e < hi {
		le := d.lineEnd(e)
		if le > hi || !isBlank(d.src[e:le]) {
			break
		}
		if le == e {
			break
		}
		e = le
	}
	return e
}

func isBlank(b []byte) bool {
	for _, c := range b {
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			return false
		}
	}
	return len(b) > 0
}

// findBody locates the body whose opening brace sits at open (or the root).
func (d *doc) findBody(open int) *bodyNode {
	if open == rootBody {
		return d.root
	}
	return findBodyIn(d.root, open)
}

func findBodyIn(b *bodyNode, open int) *bodyNode {
	for _, it := range b.items {
		if it.body == nil {
			continue
		}
		if it.body.open == open {
			return it.body
		}
		if found := findBodyIn(it.body, open); found != nil {
			return found
		}
	}
	return nil
}
