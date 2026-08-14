// Package hewyaml is hew's YAML format binding (§8.3): a byte-preserving
// applier over gopkg.in/yaml.v3's node tree.
//
// yaml.v3 parses a document into a node tree faithfully, but it cannot write
// one back out without reflowing the whole file — Marshal re-wraps long
// lines, normalizes indentation, and re-quotes scalars by its own rules. So
// this binding never marshals the target. It re-anchors every parsed node to
// its exact SOURCE SPAN and applies edits as byte splices against the
// original text, exactly as the JSON binding does over its own parser: an
// untouched sibling keeps its bytes, its comments, its blank lines, and its
// block-or-flow style (§6.3, §8.3's "style is preserved").
package hewyaml

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// nkind is the node vocabulary this binding edits. §8.3 also names anchor and
// document nodes; an anchor is a property of the node it decorates (yaml.v3
// reports it as Node.Anchor) and the document is the root, so neither needs
// its own kind here.
type nkind uint8

const (
	nMap nkind = iota
	nSeq
	nScalar
	nAlias
)

func (k nkind) String() string {
	switch k {
	case nMap:
		return "mapping"
	case nSeq:
		return "sequence"
	case nScalar:
		return "scalar"
	}
	return "alias"
}

// ynode is one parsed YAML node plus the exact byte span of its own text.
// start excludes a leading "&anchor" token (that belongs to the node's
// declaration, not its value) and end is one past the node's last byte.
type ynode struct {
	y     *yaml.Node
	kind  nkind
	start int
	end   int
	flow  bool

	entries []*entry // nMap
	elems   []*elem  // nSeq

	// owner is the mapping entry this node is the value of, or nil for the
	// root and for sequence elements. Converting an empty flow collection to
	// block style needs the owner's colon position.
	owner *entry

	// regionStart is where this container's own line region begins — the
	// start of the line after its owner's key line. Comment children that
	// precede the first entry live between regionStart and that entry.
	regionStart int
}

// entry is one mapping member: the key's span, the value node, and the line
// region the member occupies, leading comment block included (§8.2's
// anchoring rule, which §8.3 inherits: a comment immediately preceding a
// member, with no blank line between, moves and dies with it).
type entry struct {
	key      string
	keyStart int
	keyEnd   int
	colon    int
	val      *ynode
	merge    bool // the "<<" merge key (§8.3)

	lineStart  int
	blockStart int // start of the attached leading-comment block
	blockEnd   int // one past the newline ending the member's last line
	indent     int
}

// elem is one sequence element, with the same line-region treatment.
type elem struct {
	val        *ynode
	dash       int
	lineStart  int
	blockStart int
	blockEnd   int
	indent     int
}

// commentNode is a standalone comment line addressed as /container/#n
// (§4.5b). text is the content after the marker and one leading space, which
// is what §6.1 compares.
type commentNode struct {
	lineStart int
	textStart int
	textEnd   int
	text      string
}

// doc is a parsed YAML target: the source bytes, the span-annotated tree, and
// the anchor bookkeeping §8.3's alias rules need.
type doc struct {
	src     []byte
	root    *ynode
	lines   []int
	anchors map[string]*ynode
	aliases map[string]int // anchor name -> number of alias references
	indent  int            // inferred block indentation step
}

func parseDoc(src []byte) (*doc, error) {
	dec := yaml.NewDecoder(bytes.NewReader(src))
	var y yaml.Node
	if err := dec.Decode(&y); err != nil {
		return nil, err
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err == nil {
		return nil, errors.New("multi-document streams are not supported by this binding")
	}
	if y.Kind != yaml.DocumentNode || len(y.Content) != 1 {
		return nil, errors.New("document has no single root node")
	}
	d := &doc{src: src, anchors: map[string]*ynode{}, aliases: map[string]int{}, indent: 2}
	d.lines = lineStarts(src)
	root, err := d.build(y.Content[0], 0, -1, false)
	if err != nil {
		return nil, err
	}
	d.root = root
	if step, ok := d.inferIndent(root); ok && step > 0 {
		d.indent = step
	}
	return d, nil
}

func lineStarts(src []byte) []int {
	out := []int{0}
	for i, c := range src {
		if c == '\n' {
			out = append(out, i+1)
		}
	}
	return out
}

// inferIndent reads the document's own block indentation step off the first
// nested block mapping it finds, so a container this binding creates matches
// the file rather than a hard-coded house style. ok=false means the document
// has no nested block mapping to learn from.
func (d *doc) inferIndent(n *ynode) (int, bool) {
	switch n.kind {
	case nMap:
		for _, e := range n.entries {
			if e.val.kind == nMap && !e.val.flow && len(e.val.entries) > 0 {
				return e.val.entries[0].indent - e.indent, true
			}
			if step, ok := d.inferIndent(e.val); ok {
				return step, true
			}
		}
	case nSeq:
		for _, el := range n.elems {
			if step, ok := d.inferIndent(el.val); ok {
				return step, true
			}
		}
	}
	return 0, false
}

// pos converts yaml.v3's 1-based (line, column) mark into a byte offset.
// Columns count characters, not bytes, so a multi-byte line is walked rune by
// rune.
func (d *doc) pos(line, col int) int {
	off := d.lines[line-1]
	for c := 1; c < col && off < len(d.src) && d.src[off] != '\n'; c++ {
		_, sz := utf8.DecodeRune(d.src[off:])
		off += sz
	}
	return off
}

func (d *doc) lineStartOf(off int) int {
	i := off
	for i > 0 && d.src[i-1] != '\n' {
		i--
	}
	return i
}

// lineEndOf returns the offset of the newline ending off's line, or len(src).
func (d *doc) lineEndOf(off int) int {
	for i := off; i < len(d.src); i++ {
		if d.src[i] == '\n' {
			return i
		}
	}
	return len(d.src)
}

// blockEndOf is one past the newline ending off's line, clamped to the source
// length so a file with no trailing newline stays in range.
func (d *doc) blockEndOf(off int) int {
	e := d.lineEndOf(off) + 1
	if e > len(d.src) {
		e = len(d.src)
	}
	return e
}

// indentOf is a line's indentation. YAML indentation is spaces only — a tab
// there is a parse error yaml.v3 has already refused — so this counts spaces.
func (d *doc) indentOf(lineStart int) int {
	return d.firstNonSpace(lineStart) - lineStart
}

// firstNonSpace returns the offset of the line's first non-blank byte, or the
// line end for a blank line.
func (d *doc) firstNonSpace(lineStart int) int {
	i := lineStart
	for i < len(d.src) && d.src[i] == ' ' {
		i++
	}
	return i
}

func (d *doc) isCommentLine(lineStart int) bool {
	i := d.firstNonSpace(lineStart)
	return i < len(d.src) && d.src[i] == '#'
}

// build re-anchors one yaml.v3 node to its source span. flow says whether the
// node sits inside a flow collection, where a scalar ends at "," or the
// closing bracket rather than at end of line.
func (d *doc) build(y *yaml.Node, regionStart, baseIndent int, flow bool) (*ynode, error) {
	n := &ynode{y: y, regionStart: regionStart}
	n.start = d.pos(y.Line, y.Column)
	if y.Anchor != "" && y.Kind != yaml.AliasNode {
		n.start = d.skipAnchor(n.start, y.Anchor)
		d.anchors[y.Anchor] = n
	}
	switch y.Kind {
	case yaml.AliasNode:
		n.kind = nAlias
		n.end = d.scanAlias(n.start)
		d.aliases[y.Value]++
	case yaml.ScalarNode:
		n.kind = nScalar
		n.end = d.scanScalar(n.start, baseIndent, flow)
	case yaml.MappingNode:
		n.kind = nMap
		if err := d.buildMap(n, baseIndent); err != nil {
			return nil, err
		}
	case yaml.SequenceNode:
		n.kind = nSeq
		if err := d.buildSeq(n, baseIndent); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported node kind %d at line %d", y.Kind, y.Line)
	}
	return n, nil
}

// skipAnchor steps over a leading "&name" declaration and the whitespace
// after it, so a node's span starts at its value.
func (d *doc) skipAnchor(start int, name string) int {
	i := start
	if i < len(d.src) && d.src[i] == '&' {
		i += 1 + len(name)
	}
	for i < len(d.src) && (d.src[i] == ' ' || d.src[i] == '\t' || d.src[i] == '\n' || d.src[i] == '\r') {
		i++
	}
	return i
}

func (d *doc) buildMap(n *ynode, baseIndent int) error {
	n.flow = n.y.Style&yaml.FlowStyle != 0
	for i := 0; i+1 < len(n.y.Content); i += 2 {
		ky, vy := n.y.Content[i], n.y.Content[i+1]
		e := &entry{key: ky.Value, merge: ky.Tag == "!!merge"}
		e.keyStart = d.pos(ky.Line, ky.Column)
		e.keyEnd = d.scanKey(e.keyStart, n.flow)
		e.colon = d.indexFrom(':', e.keyEnd)
		e.lineStart = d.lineStartOf(e.keyStart)
		e.indent = e.keyStart - e.lineStart
		val, err := d.build(vy, d.blockEndOf(e.keyStart), e.indent, n.flow)
		if err != nil {
			return err
		}
		val.owner = e
		e.val = val
		e.blockEnd = d.blockEndOf(val.end)
		e.blockStart = d.commentBlockStart(e.lineStart, n.regionStart)
		n.entries = append(n.entries, e)
	}
	if n.flow {
		n.end = d.matchBracket(n.start)
		return nil
	}
	if len(n.entries) == 0 {
		return fmt.Errorf("block mapping with no entries at line %d", n.y.Line)
	}
	n.start = n.entries[0].keyStart
	n.end = n.entries[len(n.entries)-1].val.end
	return nil
}

func (d *doc) buildSeq(n *ynode, baseIndent int) error {
	n.flow = n.y.Style&yaml.FlowStyle != 0
	for _, iy := range n.y.Content {
		el := &elem{}
		itemStart := d.pos(iy.Line, iy.Column)
		el.lineStart = d.lineStartOf(itemStart)
		el.dash = d.firstNonSpace(el.lineStart)
		if el.dash >= itemStart || d.src[el.dash] != '-' {
			el.dash = itemStart // flow element, or a dash on an earlier line
		}
		el.indent = el.dash - el.lineStart
		val, err := d.build(iy, el.lineStart, el.indent, n.flow)
		if err != nil {
			return err
		}
		el.val = val
		el.blockEnd = d.blockEndOf(val.end)
		el.blockStart = d.commentBlockStart(el.lineStart, n.regionStart)
		n.elems = append(n.elems, el)
	}
	if n.flow {
		n.end = d.matchBracket(n.start)
		return nil
	}
	if len(n.elems) == 0 {
		return fmt.Errorf("block sequence with no elements at line %d", n.y.Line)
	}
	n.start = n.elems[0].dash
	n.end = n.elems[len(n.elems)-1].val.end
	return nil
}

// commentBlockStart walks back over the comment lines immediately preceding
// lineStart — no blank line between — which are the member's leading comment
// (§8.2). It never walks past the container's own region.
func (d *doc) commentBlockStart(lineStart, regionStart int) int {
	p := lineStart
	for p > regionStart && p > 0 {
		prev := d.lineStartOf(p - 1)
		if !d.isCommentLine(prev) {
			break
		}
		p = prev
	}
	return p
}

func (d *doc) indexFrom(c byte, from int) int {
	for i := from; i < len(d.src); i++ {
		if d.src[i] == c {
			return i
		}
	}
	return -1
}

// scanKey returns one past a mapping key's last byte. A key ends at the ":"
// that introduces its value, at a flow separator, or at end of line.
func (d *doc) scanKey(start int, flow bool) int {
	if start < len(d.src) && (d.src[start] == '\'' || d.src[start] == '"') {
		return d.scanQuoted(start)
	}
	i := start
	for i < len(d.src) {
		c := d.src[i]
		if c == '\n' {
			break
		}
		if c == ':' && (i+1 >= len(d.src) || d.src[i+1] == ' ' || d.src[i+1] == '\n' || (flow && isFlowStop(d.src[i+1]))) {
			break
		}
		if flow && isFlowStop(c) {
			break
		}
		i++
	}
	return d.trimRight(start, i)
}

func isFlowStop(c byte) bool { return c == ',' || c == '}' || c == ']' }

// scanScalar returns one past a scalar's last byte. baseIndent is the
// indentation of the line that owns the scalar, which is what bounds a block
// scalar and a multi-line plain scalar.
func (d *doc) scanScalar(start, baseIndent int, flow bool) int {
	if start >= len(d.src) {
		return len(d.src)
	}
	switch d.src[start] {
	case '\'', '"':
		return d.scanQuoted(start)
	case '|', '>':
		return d.scanBlockScalar(start, baseIndent)
	}
	end := d.scanPlain(start, flow)
	if flow || end == start {
		return end
	}
	// A plain scalar continues on following lines indented deeper than its
	// own key; a blank or a comment line ends it.
	for {
		next := d.blockEndOf(end)
		if next >= len(d.src) || next == end {
			return end
		}
		if d.isCommentLine(next) || d.firstNonSpace(next) >= d.lineEndOf(next) {
			return end
		}
		if d.indentOf(next) <= baseIndent {
			return end
		}
		end = d.trimRight(next, d.lineEndOf(next))
	}
}

func (d *doc) scanPlain(start int, flow bool) int {
	i := start
	for i < len(d.src) {
		c := d.src[i]
		if c == '\n' {
			break
		}
		if flow && isFlowStop(c) {
			break
		}
		if c == '#' && i > start && (d.src[i-1] == ' ' || d.src[i-1] == '\t') {
			break
		}
		i++
	}
	return d.trimRight(start, i)
}

func (d *doc) scanQuoted(start int) int {
	q := d.src[start]
	for i := start + 1; i < len(d.src); i++ {
		switch d.src[i] {
		case '\\':
			if q == '"' {
				i++
			}
		case q:
			if q == '\'' && i+1 < len(d.src) && d.src[i+1] == '\'' {
				i++
				continue
			}
			return i + 1
		}
	}
	return len(d.src)
}

func (d *doc) scanBlockScalar(start, baseIndent int) int {
	end := d.lineEndOf(start)
	for {
		next := d.blockEndOf(end)
		if next >= len(d.src) || next == end {
			return end
		}
		blank := d.firstNonSpace(next) >= d.lineEndOf(next)
		if !blank && d.indentOf(next) <= baseIndent {
			return end
		}
		if blank {
			// A blank line may be interior to the scalar; only keep it if a
			// deeper-indented line follows.
			after := d.blockEndOf(d.lineEndOf(next))
			if after >= len(d.src) || d.indentOf(after) <= baseIndent {
				return end
			}
			end = d.lineEndOf(next)
			continue
		}
		end = d.trimRight(next, d.lineEndOf(next))
	}
}

func (d *doc) scanAlias(start int) int {
	i := start
	if i < len(d.src) && d.src[i] == '*' {
		i++
	}
	for i < len(d.src) {
		c := d.src[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '#' || isFlowStop(c) {
			break
		}
		i++
	}
	return i
}

// matchBracket returns one past the "}" or "]" closing the flow collection
// that opens at start.
func (d *doc) matchBracket(start int) int {
	depth := 0
	for i := start; i < len(d.src); {
		switch c := d.src[i]; c {
		case '\'', '"':
			i = d.scanQuoted(i)
			continue
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth <= 0 {
				return i + 1
			}
		}
		i++
	}
	return len(d.src)
}

func (d *doc) trimRight(start, end int) int {
	for end > start && (d.src[end-1] == ' ' || d.src[end-1] == '\t' || d.src[end-1] == '\r') {
		end--
	}
	return end
}

// --- lookups ---------------------------------------------------------------

func (n *ynode) lookup(key string) *entry {
	for _, e := range n.entries {
		if !e.merge && e.key == key {
			return e
		}
	}
	return nil
}

// mergedLookup finds a key a mapping has only via "<<:" (§8.3), returning the
// inherited entry, the mapping that holds it, and the anchor it came from.
func (d *doc) mergedLookup(n *ynode, key string) (*entry, *ynode, string) {
	for _, m := range n.entries {
		if !m.merge {
			continue
		}
		for _, src := range d.mergeSources(m.val) {
			target, ok := d.anchors[src]
			if !ok || target.kind != nMap {
				continue
			}
			if e := target.lookup(key); e != nil {
				return e, target, src
			}
			if e, holder, anchor := d.mergedLookup(target, key); e != nil {
				return e, holder, anchor
			}
		}
	}
	return nil, nil, ""
}

// mergeSources lists the anchor names a merge value references: one alias, or
// a sequence of them.
func (d *doc) mergeSources(v *ynode) []string {
	switch v.kind {
	case nAlias:
		return []string{v.y.Value}
	case nSeq:
		var out []string
		for _, el := range v.elems {
			if el.val.kind == nAlias {
				out = append(out, el.val.y.Value)
			}
		}
		return out
	}
	return nil
}

// mergeAnchors lists every anchor a mapping merges from, for diagnostics.
func (d *doc) mergeAnchors(n *ynode) []string {
	var out []string
	for _, m := range n.entries {
		if m.merge {
			out = append(out, d.mergeSources(m.val)...)
		}
	}
	return out
}

// commentChildren lists a container's standalone comment nodes in source
// order (§4.5b's "#<n> is kind-scoped within the container"). Comments inside
// a child's own line region belong to that child, not here.
func (d *doc) commentChildren(n *ynode) []*commentNode {
	type span struct{ start, end int }
	var skips []span
	switch n.kind {
	case nMap:
		for _, e := range n.entries {
			skips = append(skips, span{e.lineStart, e.blockEnd})
		}
	case nSeq:
		for _, el := range n.elems {
			skips = append(skips, span{el.lineStart, el.blockEnd})
		}
	default:
		return nil
	}
	var out []*commentNode
	pos := n.regionStart
	for pos < n.end && pos < len(d.src) {
		skipped := false
		for _, s := range skips {
			if pos >= s.start && pos < s.end {
				pos = s.end
				skipped = true
				break
			}
		}
		if skipped {
			continue
		}
		if d.isCommentLine(pos) {
			out = append(out, d.commentAt(pos))
		}
		pos = d.blockEndOf(pos)
		if pos == 0 {
			break
		}
	}
	return out
}

// commentAt builds the comment node whose "#" starts on the given line.
func (d *doc) commentAt(lineStart int) *commentNode {
	hash := d.firstNonSpace(lineStart)
	return d.commentFrom(lineStart, hash)
}

func (d *doc) commentFrom(lineStart, hash int) *commentNode {
	textStart := hash + 1
	if textStart < len(d.src) && d.src[textStart] == ' ' {
		textStart++
	}
	end := d.lineEndOf(hash)
	if textStart > end {
		textStart = end
	}
	return &commentNode{
		lineStart: lineStart,
		textStart: textStart,
		textEnd:   end,
		text:      strings.TrimRight(string(d.src[textStart:end]), " \t"),
	}
}

// trailingComment returns the comment sitting after a node on its own last
// line — the "#t" address of §4.5b.
func (d *doc) trailingComment(n *ynode) *commentNode {
	end := d.lineEndOf(n.end)
	for i := n.end; i < end; i++ {
		if d.src[i] == '#' {
			return d.commentFrom(d.lineStartOf(i), i)
		}
	}
	return nil
}
