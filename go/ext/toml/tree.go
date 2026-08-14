// Package toml is hew's TOML format binding (§8.4): a byte-preserving
// applier over a span-tracking TOML reader written for this purpose.
//
// TOML's wrinkle is that `a.b.c = 1`, `[a.b]` + `c = 1` and `[a]` + `b.c = 1`
// denote the same tree at three different SURFACES. A binding that re-emitted
// the document would have to pick one of them for every node it touched. This
// one never re-emits: it reads the target into a tree whose every node
// remembers the exact byte span that defines it and applies edits as splices
// against the original text, exactly as the JSON (§8.1) and YAML (§8.3)
// bindings do. An edit therefore lands at whichever surface the target already
// uses (§8.4 rule 1) for free, and every untouched byte, comment and blank
// line survives (§6.3).
//
// No TOML library is used. go-toml's unstable parser reports positions, but it
// discards the distinction this binding is built around — which physical line
// defines a node, and how many of them there are (§8.4 rule 2) — so the reader
// below tracks it directly.
package toml

import (
	"fmt"
	"strconv"
	"strings"
)

// nkind is the node vocabulary this binding edits. TOML's tables (header,
// dotted and inline), arrays (inline and array-of-tables) and scalars collapse
// onto three kinds; the SURFACE differences live in the fields below, not in
// the kind.
type nkind uint8

const (
	nTable nkind = iota
	nSeq
	nScalar
)

func (k nkind) String() string {
	switch k {
	case nTable:
		return "table"
	case nSeq:
		return "array"
	}
	return "scalar"
}

// tnode is one node of the parsed target.
type tnode struct {
	kind nkind
	path []string // canonical key path from the root, for header emission

	entries []*entry // nTable members, in source order
	elems   []*elem  // nSeq elements, in source order

	// defs counts the node's EXPLICIT definition sites: an assignment whose
	// final key segment names it, or a `[…]` header that opens it. Segments
	// merely traversed on the way (a dotted key's prefix, a header path's
	// prefix) are implicit and do not count. Two or more is §8.4 rule 2's
	// surface ambiguity, which toml/surface-ambiguous pins.
	defs int

	// start/end is the node's own value text: a scalar's literal, an inline
	// table's braces, an inline array's brackets. A table opened by a header
	// has no value text and leaves these zero.
	start, end int
	inline     bool

	// tag/text is a scalar decoded for comparison (§6.1's "format-native
	// decoding"): a YAML short tag plus the canonical text that tag implies,
	// so `0x1e`, `1_0` and `30` all compare as the integer they denote.
	tag  string
	text string

	// holder is the physical table whose body an assignment defining a child
	// of this node must be written into, and prefix is the dotted key path
	// from that table down to here. For the root and for a `[…]` table the
	// holder is the node itself and the prefix is empty; for a table that
	// exists only as a dotted key's prefix they name the line that created it.
	// A nil holder means the node has no physical body at all — it exists only
	// as a prefix of some `[a.b]` header — and a child creation must give it
	// one (§8.4 rule 3).
	holder *tnode
	prefix []string

	// lines are the `key = value` assignments physically written in this
	// table's body, in source order. They are NOT the same set as entries: a
	// dotted key registers its member on the table the dots name, while the
	// line itself lives here, and that is exactly the distinction §8.4 rule 1
	// turns on.
	lines []*entry

	// physical marks a node with a body region of its own: the root, a `[…]`
	// table, or an array-of-tables element. regionStart/regionEnd bound that
	// region for comment addressing; blockStart/blockEnd bound the whole block
	// including its header line and its leading comments.
	physical               bool
	aot                    bool // nSeq whose elements are `[[x]]` blocks
	regionStart, regionEnd int
	blockStart, blockEnd   int
}

// entry is one table member: the key's span, the value node, and the line
// region the member occupies with its leading comment block attached (§8.2's
// anchoring rule, which every commented format in this spec inherits).
type entry struct {
	key  string
	val  *tnode
	keys []string // the assignment's full dotted key, as written

	keyStart, keyEnd int

	lineStart  int
	blockStart int
	blockEnd   int

	// assign marks a member written as its own `key = value` LINE, and holder
	// is the physical table that line sits in. The entries that are neither —
	// a table opened by a `[a.b]` header, a dotted key's intermediate, an
	// inline table's member — occupy no line of their own and must never be
	// used as one.
	assign bool
	holder *tnode
}

// elem is one sequence element: an inline array item, or an array-of-tables
// block.
type elem struct {
	val        *tnode
	blockStart int
	blockEnd   int
}

// commentNode is a standalone comment line addressed as /table/#n (§4.5b).
type commentNode struct {
	lineStart int
	textStart int
	textEnd   int
	text      string
}

// doc is a parsed TOML target: the source bytes and the span-annotated tree.
type doc struct {
	src  []byte
	root *tnode
}

func (n *tnode) lookup(key string) *entry {
	for _, e := range n.entries {
		if e.key == key {
			return e
		}
	}
	return nil
}

// --- reader -----------------------------------------------------------------

type parser struct {
	d       *doc
	src     []byte
	i       int
	cur     *tnode // the physical table lines are currently written into
	curElem *elem  // the array-of-tables element cur belongs to, if any
}

func parseDoc(src []byte) (*doc, error) {
	root := &tnode{kind: nTable, physical: true}
	root.holder = root
	d := &doc{src: src, root: root}
	p := &parser{d: d, src: src, cur: root}
	if err := p.run(); err != nil {
		return nil, err
	}
	return d, nil
}

func (p *parser) run() error {
	for p.i < len(p.src) {
		p.skipBlank()
		if p.i >= len(p.src) {
			break
		}
		switch p.src[p.i] {
		case '#':
			p.i = p.blockEndOf(p.i)
		case '[':
			if err := p.header(); err != nil {
				return err
			}
		default:
			if err := p.assignment(); err != nil {
				return err
			}
		}
	}
	p.close(len(p.src))
	return nil
}

// close finishes the table whose body was open, bounding its comment region at
// the point the next block (or the document) begins.
func (p *parser) close(at int) {
	if p.cur.regionEnd < at {
		p.cur.regionEnd = at
	}
}

// header reads a `[a.b]` or `[[a.b]]` line and makes the table it opens
// current.
func (p *parser) header() error {
	lineStart := p.lineStartOf(p.i)
	blockStart := p.commentBlockStart(lineStart, p.cur.regionStart)
	p.i++
	aot := p.i < len(p.src) && p.src[p.i] == '['
	if aot {
		p.i++
	}
	keys, err := p.keyPath()
	if err != nil {
		return err
	}
	p.skipSpace()
	if err := p.expect(']'); err != nil {
		return err
	}
	if aot {
		if err := p.expect(']'); err != nil {
			return err
		}
	}
	p.close(blockStart)
	regionStart := p.blockEndOf(p.i)
	p.i = regionStart

	parent, err := p.descend(p.d.root, keys[:len(keys)-1], nil, nil)
	if err != nil {
		return err
	}
	name := keys[len(keys)-1]
	if aot {
		return p.openArrayElement(parent, name, blockStart, regionStart)
	}
	return p.openTable(parent, name, blockStart, regionStart)
}

func (p *parser) openTable(parent *tnode, name string, blockStart, regionStart int) error {
	e := parent.lookup(name)
	if e == nil {
		e = p.addEntry(parent, name, &tnode{kind: nTable, path: childPath(parent, name)})
	}
	n := e.val
	if n.kind != nTable {
		return fmt.Errorf("line %d: [%s] redefines a non-table", p.lineOf(blockStart), strings.Join(n.path, "."))
	}
	n.defs++
	n.physical = true
	n.holder, n.prefix = n, nil
	n.regionStart, n.regionEnd = regionStart, regionStart
	n.blockStart, n.blockEnd = blockStart, regionStart
	p.cur, p.curElem = n, nil
	return nil
}

func (p *parser) openArrayElement(parent *tnode, name string, blockStart, regionStart int) error {
	e := parent.lookup(name)
	if e == nil {
		e = p.addEntry(parent, name, &tnode{kind: nSeq, aot: true, path: childPath(parent, name)})
		e.val.defs++
	}
	seq := e.val
	if seq.kind != nSeq || !seq.aot {
		return fmt.Errorf("line %d: [[%s]] redefines a non-array-of-tables", p.lineOf(blockStart), strings.Join(seq.path, "."))
	}
	n := &tnode{kind: nTable, path: seq.path, defs: 1, physical: true,
		regionStart: regionStart, regionEnd: regionStart,
		blockStart: blockStart, blockEnd: regionStart}
	n.holder = n
	el := &elem{val: n, blockStart: blockStart, blockEnd: regionStart}
	seq.elems = append(seq.elems, el)
	p.cur, p.curElem = n, el
	return nil
}

// assignment reads one `key = value` line into the current table.
func (p *parser) assignment() error {
	lineStart := p.lineStartOf(p.i)
	blockStart := p.commentBlockStart(lineStart, p.cur.regionStart)
	keyStart := p.i
	keys, err := p.keyPath()
	if err != nil {
		return err
	}
	keyEnd := p.i
	p.skipSpace()
	if err := p.expect('='); err != nil {
		return err
	}
	p.skipSpace()
	val, err := p.value(false)
	if err != nil {
		return err
	}
	blockEnd := p.blockEndOf(p.i)
	p.i = blockEnd

	parent, err := p.descend(p.cur, keys[:len(keys)-1], p.cur, nil)
	if err != nil {
		return err
	}
	name := keys[len(keys)-1]
	val.path = childPath(parent, name)
	e := p.defineMember(parent, name, val)
	e.keys, e.keyStart, e.keyEnd = keys, keyStart, keyEnd
	e.lineStart, e.blockStart, e.blockEnd = lineStart, blockStart, blockEnd
	e.assign, e.holder = true, p.cur
	p.cur.lines = append(p.cur.lines, e)
	p.cur.blockEnd = blockEnd
	if p.curElem != nil {
		p.curElem.blockEnd = blockEnd
	}
	return nil
}

// descend walks a dotted key's prefix from t, creating the implicit tables it
// names. holder is the physical table those implicit tables' members are
// written into, or nil when the prefix comes from a `[…]` header, whose
// intermediate tables have no body of their own.
func (p *parser) descend(t *tnode, keys []string, holder *tnode, prefix []string) (*tnode, error) {
	for _, k := range keys {
		prefix = append(append([]string{}, prefix...), k)
		e := t.lookup(k)
		if e == nil {
			n := &tnode{kind: nTable, path: childPath(t, k), holder: holder}
			if holder != nil {
				n.prefix = prefix
			}
			e = p.addEntry(t, k, n)
		}
		if e.val.kind != nTable {
			return nil, fmt.Errorf("line %d: %q is used both as a value and as a table",
				p.lineOf(p.i), strings.Join(e.val.path, "."))
		}
		t = e.val
	}
	return t, nil
}

func (p *parser) addEntry(t *tnode, key string, val *tnode) *entry {
	e := &entry{key: key, val: val}
	t.entries = append(t.entries, e)
	return e
}

// defineMember records one definition of key in t, counting it towards §8.4
// rule 2's surface ambiguity. A REPEAT definition shares the node — that is
// what makes both surfaces visible as one ambiguous path — but gets an entry
// of its own rather than overwriting the first one's spans, so each line stays
// separately addressable and neither is mistaken for the other's bytes.
func (p *parser) defineMember(t *tnode, key string, val *tnode) *entry {
	if prior := t.lookup(key); prior != nil {
		prior.val.defs++
		return &entry{key: key, val: prior.val}
	}
	e := p.addEntry(t, key, val)
	e.val.defs++
	return e
}

func childPath(parent *tnode, key string) []string {
	return append(append([]string{}, parent.path...), key)
}

// --- values -----------------------------------------------------------------

// value reads one TOML value. inFlow says the value sits inside an inline
// table or array, where "," and the closing bracket end a bare token.
func (p *parser) value(inFlow bool) (*tnode, error) {
	if p.i >= len(p.src) {
		return nil, fmt.Errorf("line %d: expected a value", p.lineOf(p.i))
	}
	switch p.src[p.i] {
	case '"', '\'':
		return p.stringValue()
	case '[':
		return p.arrayValue()
	case '{':
		return p.inlineTable()
	}
	start := p.i
	j := p.i
	for j < len(p.src) && !bareStop(p.src[j], inFlow) {
		j++
	}
	end := p.trimRight(start, j)
	if end == start {
		return nil, fmt.Errorf("line %d: expected a value", p.lineOf(p.i))
	}
	p.i = end
	tag, text, ok := decodeBare(string(p.src[start:end]))
	if !ok {
		return nil, fmt.Errorf("line %d: %q is not a TOML value", p.lineOf(start), string(p.src[start:end]))
	}
	return &tnode{kind: nScalar, start: start, end: end, tag: tag, text: text}, nil
}

func bareStop(c byte, inFlow bool) bool {
	switch c {
	case '\n', '\r', '#':
		return true
	case ',', ']', '}':
		return inFlow
	}
	return false
}

func (p *parser) stringValue() (*tnode, error) {
	start := p.i
	q := p.src[p.i]
	text, err := p.scanString(q)
	if err != nil {
		return nil, err
	}
	return &tnode{kind: nScalar, start: start, end: p.i, tag: "!!str", text: text}, nil
}

func (p *parser) arrayValue() (*tnode, error) {
	n := &tnode{kind: nSeq, inline: true, start: p.i}
	p.i++
	for {
		p.skipFlowSpace()
		if p.i >= len(p.src) {
			return nil, fmt.Errorf("line %d: unterminated array", p.lineOf(n.start))
		}
		if p.src[p.i] == ']' {
			p.i++
			break
		}
		v, err := p.value(true)
		if err != nil {
			return nil, err
		}
		n.elems = append(n.elems, &elem{val: v, blockStart: v.start, blockEnd: v.end})
		p.skipFlowSpace()
		if p.i < len(p.src) && p.src[p.i] == ',' {
			p.i++
			continue
		}
		// Only the closing bracket may follow an item without a comma; the
		// loop head consumes it. Anything else means this reader has lost the
		// item boundaries, and editing a document it has misread is the one
		// thing worse than refusing it.
		if p.i >= len(p.src) || p.src[p.i] != ']' {
			return nil, fmt.Errorf("line %d: expected \",\" or \"]\" in array", p.lineOf(p.i))
		}
	}
	n.end = p.i
	return n, nil
}

func (p *parser) inlineTable() (*tnode, error) {
	n := &tnode{kind: nTable, inline: true, start: p.i}
	p.i++
	for {
		p.skipFlowSpace()
		if p.i >= len(p.src) {
			return nil, fmt.Errorf("line %d: unterminated inline table", p.lineOf(n.start))
		}
		if p.src[p.i] == '}' {
			p.i++
			break
		}
		keyStart := p.i
		keys, err := p.keyPath()
		if err != nil {
			return nil, err
		}
		keyEnd := p.i
		p.skipSpace()
		if err := p.expect('='); err != nil {
			return nil, err
		}
		p.skipFlowSpace()
		v, err := p.value(true)
		if err != nil {
			return nil, err
		}
		t, err := p.descend(n, keys[:len(keys)-1], nil, nil)
		if err != nil {
			return nil, err
		}
		name := keys[len(keys)-1]
		v.path = childPath(t, name)
		e := p.defineMember(t, name, v)
		e.keys, e.keyStart, e.keyEnd = keys, keyStart, keyEnd
		e.blockStart, e.blockEnd = keyStart, v.end
		p.skipFlowSpace()
		if p.i < len(p.src) && p.src[p.i] == ',' {
			p.i++
			continue
		}
		if p.i >= len(p.src) || p.src[p.i] != '}' {
			return nil, fmt.Errorf("line %d: expected \",\" or \"}\" in inline table", p.lineOf(p.i))
		}
	}
	n.end = p.i
	return n, nil
}

// --- keys and strings -------------------------------------------------------

func (p *parser) keyPath() ([]string, error) {
	var keys []string
	for {
		p.skipSpace()
		k, err := p.keyPart()
		if err != nil {
			return nil, err
		}
		keys = append(keys, k)
		// The blanks after the last key part belong to whatever follows, not
		// to the key: a copy re-keys by splicing over exactly [keyStart,
		// keyEnd), and swallowing them here would eat the source's alignment.
		save := p.i
		p.skipSpace()
		if p.i < len(p.src) && p.src[p.i] == '.' {
			p.i++
			continue
		}
		p.i = save
		return keys, nil
	}
}

func (p *parser) keyPart() (string, error) {
	if p.i >= len(p.src) {
		return "", fmt.Errorf("line %d: expected a key", p.lineOf(p.i))
	}
	if c := p.src[p.i]; c == '"' || c == '\'' {
		return p.scanString(c)
	}
	start := p.i
	for p.i < len(p.src) && isBareKeyByte(p.src[p.i]) {
		p.i++
	}
	if p.i == start {
		return "", fmt.Errorf("line %d: expected a key", p.lineOf(start))
	}
	return string(p.src[start:p.i]), nil
}

func isBareKeyByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
}

// scanString reads a quoted string starting at p.i and returns its decoded
// content, leaving p.i one past the closing quote. It covers all four TOML
// string forms: basic, literal, and their multi-line variants.
func (p *parser) scanString(q byte) (string, error) {
	start := p.i
	multi := p.i+2 < len(p.src) && p.src[p.i+1] == q && p.src[p.i+2] == q
	if multi {
		p.i += 3
		// A newline immediately after the opening delimiter is trimmed.
		if p.i < len(p.src) && p.src[p.i] == '\r' {
			p.i++
		}
		if p.i < len(p.src) && p.src[p.i] == '\n' {
			p.i++
		}
	} else {
		p.i++
	}
	var b strings.Builder
	for p.i < len(p.src) {
		c := p.src[p.i]
		switch {
		case c == q && multi && p.i+2 < len(p.src) && p.src[p.i+1] == q && p.src[p.i+2] == q:
			p.i += 3
			return b.String(), nil
		case c == q && !multi:
			p.i++
			return b.String(), nil
		case c == '\n' && !multi:
			return "", fmt.Errorf("line %d: unterminated string", p.lineOf(start))
		case c == '\\' && q == '"':
			consumed, text, err := p.unescape(multi)
			if err != nil {
				return "", err
			}
			p.i += consumed
			b.WriteString(text)
		default:
			b.WriteByte(c)
			p.i++
		}
	}
	return "", fmt.Errorf("line %d: unterminated string", p.lineOf(start))
}

// unescape decodes the escape sequence at p.i, reporting how many source bytes
// it consumed. A backslash before end-of-line in a multi-line basic string
// swallows the following whitespace instead of producing a character.
func (p *parser) unescape(multi bool) (int, string, error) {
	if p.i+1 >= len(p.src) {
		return 0, "", fmt.Errorf("line %d: dangling escape", p.lineOf(p.i))
	}
	switch c := p.src[p.i+1]; c {
	case 'b':
		return 2, "\b", nil
	case 't':
		return 2, "\t", nil
	case 'n':
		return 2, "\n", nil
	case 'f':
		return 2, "\f", nil
	case 'r':
		return 2, "\r", nil
	case '"':
		return 2, `"`, nil
	case '\\':
		return 2, `\`, nil
	case 'u', 'U':
		width := 4
		if c == 'U' {
			width = 8
		}
		if p.i+2+width > len(p.src) {
			return 0, "", fmt.Errorf("line %d: truncated \\%c escape", p.lineOf(p.i), c)
		}
		v, err := strconv.ParseUint(string(p.src[p.i+2:p.i+2+width]), 16, 32)
		if err != nil {
			return 0, "", fmt.Errorf("line %d: bad \\%c escape", p.lineOf(p.i), c)
		}
		return 2 + width, string(rune(v)), nil
	}
	if multi {
		j := p.i + 1
		for j < len(p.src) && (p.src[j] == ' ' || p.src[j] == '\t' || p.src[j] == '\n' || p.src[j] == '\r') {
			j++
		}
		if j > p.i+1 {
			return j - p.i, "", nil
		}
	}
	return 0, "", fmt.Errorf("line %d: unknown escape \\%c", p.lineOf(p.i), p.src[p.i+1])
}

// --- scalar decoding --------------------------------------------------------

// decodeBare classifies an unquoted TOML value and returns the YAML short tag
// it compares under plus the canonical text that tag implies (§6.1). Integers
// normalize to decimal so `0x1e`, `1_0` and `30` compare as the numbers they
// denote rather than as the spellings they were written in.
func decodeBare(s string) (tag, text string, ok bool) {
	switch s {
	case "true", "false":
		return "!!bool", s, true
	}
	if v, ok := tomlInt(s); ok {
		return "!!int", v, true
	}
	if v, ok := tomlFloat(s); ok {
		return "!!float", v, true
	}
	if isDateTime(s) {
		return "!!timestamp", s, true
	}
	return "", "", false
}

func tomlInt(s string) (string, bool) {
	c := strings.ReplaceAll(s, "_", "")
	base := 10
	digits := strings.TrimLeft(c, "+-")
	if len(digits) > 2 && digits[0] == '0' {
		switch digits[1] {
		case 'x', 'o', 'b':
			base = 0
		}
	}
	v, err := strconv.ParseInt(c, base, 64)
	if err != nil {
		return "", false
	}
	return strconv.FormatInt(v, 10), true
}

func tomlFloat(s string) (string, bool) {
	c := strings.ReplaceAll(s, "_", "")
	if !strings.ContainsAny(c, ".eEni") {
		return "", false
	}
	if _, err := strconv.ParseFloat(c, 64); err != nil {
		// TOML permits a sign on nan; Go's parser does not.
		if !strings.EqualFold(strings.TrimLeft(c, "+-"), "nan") {
			return "", false
		}
	}
	return c, true
}

// isDateTime recognizes TOML's four date-time forms loosely: a digit-led token
// carrying a date or time separator. Hew never computes with a date, so
// recognizing the shape is enough to compare one.
func isDateTime(s string) bool {
	if s == "" || s[0] < '0' || s[0] > '9' {
		return false
	}
	return strings.ContainsAny(s, "-:")
}

// --- source navigation ------------------------------------------------------

func (p *parser) expect(c byte) error {
	if p.i >= len(p.src) || p.src[p.i] != c {
		return fmt.Errorf("line %d: expected %q", p.lineOf(p.i), string(c))
	}
	p.i++
	return nil
}

// skipSpace steps over the blanks within a line.
func (p *parser) skipSpace() {
	for p.i < len(p.src) && (p.src[p.i] == ' ' || p.src[p.i] == '\t') {
		p.i++
	}
}

// skipBlank steps over blanks and line breaks between top-level items.
func (p *parser) skipBlank() {
	for p.i < len(p.src) {
		switch p.src[p.i] {
		case ' ', '\t', '\n', '\r':
			p.i++
		default:
			return
		}
	}
}

// skipFlowSpace steps over the blanks, line breaks and comments that may
// appear inside a multi-line array or inline table.
func (p *parser) skipFlowSpace() {
	for p.i < len(p.src) {
		switch p.src[p.i] {
		case ' ', '\t', '\n', '\r':
			p.i++
		case '#':
			p.i = p.blockEndOf(p.i)
		default:
			return
		}
	}
}

func (p *parser) trimRight(start, end int) int {
	for end > start && (p.src[end-1] == ' ' || p.src[end-1] == '\t' || p.src[end-1] == '\r') {
		end--
	}
	return end
}

func (p *parser) lineStartOf(off int) int { return lineStartOf(p.src, off) }
func (p *parser) blockEndOf(off int) int  { return blockEndOf(p.src, off) }
func (p *parser) lineOf(off int) int      { return lineOf(p.src, off) }
func (d *doc) lineStartOf(off int) int    { return lineStartOf(d.src, off) }
func (d *doc) blockEndOf(off int) int     { return blockEndOf(d.src, off) }
func (d *doc) firstNonSpace(off int) int  { return firstNonSpace(d.src, off) }
func (d *doc) isCommentLine(off int) bool { return isCommentLine(d.src, off) }
func (d *doc) lineEndOf(off int) int      { return lineEndOf(d.src, off) }

func lineStartOf(src []byte, off int) int {
	i := off
	for i > 0 && src[i-1] != '\n' {
		i--
	}
	return i
}

func lineEndOf(src []byte, off int) int {
	for i := off; i < len(src); i++ {
		if src[i] == '\n' {
			return i
		}
	}
	return len(src)
}

// blockEndOf is one past the newline ending off's line, clamped so a file with
// no trailing newline stays in range.
func blockEndOf(src []byte, off int) int {
	e := lineEndOf(src, off) + 1
	if e > len(src) {
		e = len(src)
	}
	return e
}

func lineOf(src []byte, off int) int {
	line := 1
	for i := 0; i < off && i < len(src); i++ {
		if src[i] == '\n' {
			line++
		}
	}
	return line
}

func firstNonSpace(src []byte, lineStart int) int {
	i := lineStart
	for i < len(src) && (src[i] == ' ' || src[i] == '\t') {
		i++
	}
	return i
}

func isCommentLine(src []byte, lineStart int) bool {
	i := firstNonSpace(src, lineStart)
	return i < len(src) && src[i] == '#'
}

// commentBlockStart walks back over the comment lines immediately preceding
// lineStart — no blank line between — which are the member's leading comment
// (§8.2's anchoring rule). It never walks past the container's own region.
func (p *parser) commentBlockStart(lineStart, regionStart int) int {
	q := lineStart
	for q > regionStart {
		prev := p.lineStartOf(q - 1)
		if !isCommentLine(p.src, prev) {
			break
		}
		q = prev
	}
	return q
}

// commentChildren lists a table's standalone comment lines in source order
// (§4.5b's "#<n> is kind-scoped within the container"). A comment inside a
// member's own line region belongs to that member, not here.
func (d *doc) commentChildren(n *tnode) []*commentNode {
	if n == nil || !n.physical {
		return nil
	}
	var out []*commentNode
	pos := n.regionStart
	for pos < n.regionEnd && pos < len(d.src) {
		if e := lineAt(n, pos); e != nil {
			pos = e.blockEnd
			continue
		}
		if d.isCommentLine(pos) {
			out = append(out, d.commentAt(pos))
		}
		// blockEndOf always advances from a position inside the source, so the
		// loop terminates on the bounds above rather than on a guard here.
		pos = d.blockEndOf(pos)
	}
	return out
}

// lineAt finds the assignment line covering pos in a table's body.
func lineAt(n *tnode, pos int) *entry {
	for _, e := range n.lines {
		if pos >= e.blockStart && pos < e.blockEnd {
			return e
		}
	}
	return nil
}

func (d *doc) commentAt(lineStart int) *commentNode {
	return d.commentFrom(lineStart, d.firstNonSpace(lineStart))
}

// commentFrom builds the comment node whose marker sits at hash. The text is
// what follows the marker and one optional space, which is what §6.1 compares;
// the marker itself is never inside the span, so textStart never overruns the
// line end.
func (d *doc) commentFrom(lineStart, hash int) *commentNode {
	textStart := hash + 1
	if textStart < len(d.src) && d.src[textStart] == ' ' {
		textStart++
	}
	end := d.lineEndOf(hash)
	return &commentNode{lineStart: lineStart, textStart: textStart, textEnd: end,
		text: strings.TrimRight(string(d.src[textStart:end]), " \t")}
}

// trailingComment returns the comment sitting after a node on its own line —
// the "#t" address of §4.5b.
func (d *doc) trailingComment(end int) *commentNode {
	stop := d.lineEndOf(end)
	for i := end; i < stop; i++ {
		if d.src[i] == '#' {
			return d.commentFrom(d.lineStartOf(i), i)
		}
	}
	return nil
}
