// Package jsonc is hew's JSONC format binding (§8.2): everything §8.1's
// JSON binding does, plus comments as first-class, addressable nodes.
//
// It is a deliberate FORK of ext/json rather than an extension of it. Three
// things differ below the surface, and each one reaches every function in the
// package:
//
//   - A member's extent is not its key-to-value span. A leading comment
//     (§8.2: immediately preceding, no blank line between) is part of the
//     member for move/delete purposes, so removal spans and insertion anchors
//     are computed over comment-aware "slots", not over key/value spans.
//   - Blank lines are semantic. They are what separates a free comment (bound
//     to the container, never moved by a sibling op) from a leading one, so
//     the scanner must count newlines that plain JSON is free to discard.
//   - Application is sequential, not a single batch of byte splices. A patch
//     may add a comment node and then place a member relative to the comment
//     it just added (jsonc/add-with-leading-comment pins exactly that), which
//     needs each edit's successor to see the post-edit document.
//
// Extending ext/json in place would have meant either exporting most of its
// unexported tree across a package boundary or lifting it into a shared
// internal package — a refactor of a green, corpus-pinned applier for the
// reuse of ~200 lines of scanner. The scanner is re-derived here instead, and
// ext/json keeps rejecting comment ops with HEW020 (json/comment-inexpressible)
// because JSON, not JSONC, is what it parses.
package jsonc

import (
	"fmt"
	"sort"
	"strconv"
)

type kind int

const (
	kObj kind = iota
	kArr
	kStr
	kNum
	kBool
	kNull
)

// comment is one JSONC comment token with its exact source span. text is the
// content after the marker is stripped along with one leading space, which is
// the form §6.1's "Comment ... exact text" row compares.
type comment struct {
	start, end int
	text       string
	block      bool
}

// member is one object member: its key, its value, its own trailing comma (or
// -1), and the comments §8.2 anchors to it.
type member struct {
	keyStart, keyEnd int
	key              string
	valStart, valEnd int
	value            *node
	commaPos         int
	leading          []*comment
	trailing         *comment
}

// element is one array element, with the same comment anchoring as a member.
type element struct {
	valStart, valEnd int
	value            *node
	commaPos         int
	leading          []*comment
	trailing         *comment
}

// node is one parsed JSONC value with its exact source span. free holds the
// comments bound to this container rather than to any one child (§8.2's free
// comments): separated from the next child by a blank line, or standing at the
// container's end.
type node struct {
	kind       kind
	start, end int
	members    []*member
	elems      []*element
	free       []*comment
	raw        string
}

type parseError struct {
	pos int
	msg string
}

func (e *parseError) Error() string { return fmt.Sprintf("offset %d: %s", e.pos, e.msg) }

// doc is a parsed JSONC target: the source bytes and the tree over them.
type doc struct {
	src  []byte
	root *node
}

// parseDoc parses a complete JSONC document: one value, optionally wrapped in
// comments and whitespace at the top level.
func parseDoc(src []byte) (*doc, error) {
	p := &parser{src: src}
	pos, _, _, err := p.trivia(0)
	if err != nil {
		return nil, err
	}
	root, next, err := p.value(pos)
	if err != nil {
		return nil, err
	}
	next, _, _, err = p.trivia(next)
	if err != nil {
		return nil, err
	}
	if next != len(src) {
		return nil, &parseError{next, "trailing content after top-level value"}
	}
	return &doc{src: src, root: root}, nil
}

type parser struct{ src []byte }

// triviaItem is one comment found in a run of whitespace-and-comments, with
// the number of newlines separating it from whatever preceded it. One newline
// means "the next line"; two or more mean a blank line stood between, which is
// what demotes a comment from leading to free (§8.2).
type triviaItem struct {
	c        *comment
	nlBefore int
}

// trivia scans whitespace and comments from pos. It returns the first
// non-trivia position, the comments found, and the newline count between the
// last comment (or pos, if there were none) and that position.
func (p *parser) trivia(pos int) (int, []triviaItem, int, error) {
	var items []triviaItem
	nl := 0
	for pos < len(p.src) {
		switch c := p.src[pos]; {
		case c == '\n':
			nl++
			pos++
		case c == ' ' || c == '\t' || c == '\r':
			pos++
		case isCommentStart(p.src, pos):
			cm, next, err := scanComment(p.src, pos)
			if err != nil {
				return 0, nil, 0, err
			}
			items = append(items, triviaItem{c: cm, nlBefore: nl})
			nl = 0
			pos = next
		default:
			return pos, items, nl, nil
		}
	}
	return pos, items, nl, nil
}

func isCommentStart(src []byte, pos int) bool {
	return pos+1 < len(src) && src[pos] == '/' && (src[pos+1] == '/' || src[pos+1] == '*')
}

// scanComment reads one `//` or `/* */` comment token.
func scanComment(src []byte, pos int) (*comment, int, error) {
	if src[pos+1] == '/' {
		i := pos + 2
		for i < len(src) && src[i] != '\n' {
			i++
		}
		return &comment{start: pos, end: i, text: commentText(string(src[pos+2 : i]))}, i, nil
	}
	for i := pos + 2; i+1 < len(src); i++ {
		if src[i] == '*' && src[i+1] == '/' {
			end := i + 2
			return &comment{start: pos, end: end, block: true,
				text: commentText(string(src[pos+2 : i]))}, end, nil
		}
	}
	return nil, 0, &parseError{pos, "unterminated block comment"}
}

// commentText strips one leading space from a comment's body (§6.1), after
// dropping the trailing whitespace a line comment picks up from its line
// ending and the space a block comment's closing marker is spaced by.
func commentText(body string) string {
	body = trimRightSpace(body)
	if len(body) > 0 && body[0] == ' ' {
		body = body[1:]
	}
	return body
}

func trimRightSpace(s string) string {
	i := len(s)
	for i > 0 {
		switch s[i-1] {
		case ' ', '\t', '\r', '\n':
			i--
			continue
		}
		break
	}
	return s[:i]
}

func (p *parser) value(pos int) (*node, int, error) {
	if pos >= len(p.src) {
		return nil, pos, &parseError{pos, "unexpected end of input"}
	}
	switch c := p.src[pos]; {
	case c == '{':
		return p.object(pos)
	case c == '[':
		return p.array(pos)
	case c == '"':
		end, err := scanString(p.src, pos)
		if err != nil {
			return nil, pos, err
		}
		return &node{kind: kStr, start: pos, end: end, raw: string(p.src[pos:end])}, end, nil
	case c == 't':
		return literal(p.src, pos, "true", kBool)
	case c == 'f':
		return literal(p.src, pos, "false", kBool)
	case c == 'n':
		return literal(p.src, pos, "null", kNull)
	case c == '-' || (c >= '0' && c <= '9'):
		end := scanNumber(p.src, pos)
		if end == pos {
			return nil, pos, &parseError{pos, "malformed number"}
		}
		return &node{kind: kNum, start: pos, end: end, raw: string(p.src[pos:end])}, end, nil
	default:
		return nil, pos, &parseError{pos, fmt.Sprintf("unexpected character %q", c)}
	}
}

func literal(src []byte, pos int, lit string, k kind) (*node, int, error) {
	if pos+len(lit) > len(src) || string(src[pos:pos+len(lit)]) != lit {
		return nil, pos, &parseError{pos, fmt.Sprintf("expected %q", lit)}
	}
	end := pos + len(lit)
	return &node{kind: k, start: pos, end: end, raw: lit}, end, nil
}

func scanString(src []byte, pos int) (int, error) {
	if src[pos] != '"' {
		return pos, &parseError{pos, "expected string"}
	}
	for i := pos + 1; i < len(src); {
		switch src[i] {
		case '"':
			return i + 1, nil
		case '\\':
			i += 2
		default:
			i++
		}
	}
	return pos, &parseError{pos, "unterminated string"}
}

func scanNumber(src []byte, pos int) int {
	i := pos
	if i < len(src) && src[i] == '-' {
		i++
	}
	i = scanDigits(src, i)
	if i < len(src) && src[i] == '.' {
		i = scanDigits(src, i+1)
	}
	if i < len(src) && (src[i] == 'e' || src[i] == 'E') {
		i++
		if i < len(src) && (src[i] == '+' || src[i] == '-') {
			i++
		}
		i = scanDigits(src, i)
	}
	return i
}

func scanDigits(src []byte, i int) int {
	for i < len(src) && src[i] >= '0' && src[i] <= '9' {
		i++
	}
	return i
}

func decodeString(raw string) (string, error) {
	s, err := strconv.Unquote(raw)
	if err != nil {
		return "", &parseError{0, "malformed string " + raw}
	}
	return s, nil
}

func (p *parser) object(pos int) (*node, int, error) {
	n := &node{kind: kObj, start: pos}
	pos++
	prevComma, first := -1, true
	for {
		next, items, nlAfter, err := p.trivia(pos)
		if err != nil {
			return nil, 0, err
		}
		pos = next
		if pos >= len(p.src) {
			return nil, 0, &parseError{pos, "unterminated object"}
		}
		if p.src[pos] == '}' {
			n.free = append(n.free, commentsOf(items)...)
			n.end = pos + 1
			return n, pos + 1, nil
		}
		if !first && prevComma < 0 {
			return nil, 0, &parseError{pos, "expected ',' or '}'"}
		}
		first = false
		m, after, err := p.member(pos)
		if err != nil {
			return nil, 0, err
		}
		free, lead := splitTrivia(items, nlAfter)
		n.free = append(n.free, free...)
		m.leading = lead
		n.members = append(n.members, m)
		prevComma = m.commaPos
		pos = after
	}
}

func (p *parser) member(pos int) (*member, int, error) {
	if p.src[pos] != '"' {
		return nil, 0, &parseError{pos, "expected object key"}
	}
	keyEnd, err := scanString(p.src, pos)
	if err != nil {
		return nil, 0, err
	}
	key, err := decodeString(string(p.src[pos:keyEnd]))
	if err != nil {
		return nil, 0, err
	}
	colon, _, _, err := p.trivia(keyEnd)
	if err != nil {
		return nil, 0, err
	}
	if colon >= len(p.src) || p.src[colon] != ':' {
		return nil, 0, &parseError{colon, "expected ':'"}
	}
	valStart, _, _, err := p.trivia(colon + 1)
	if err != nil {
		return nil, 0, err
	}
	val, next, err := p.value(valStart)
	if err != nil {
		return nil, 0, err
	}
	m := &member{keyStart: pos, keyEnd: keyEnd, key: key,
		valStart: valStart, valEnd: next, value: val, commaPos: -1}
	after, err := p.tail(next, &m.commaPos, &m.trailing)
	if err != nil {
		return nil, 0, err
	}
	return m, after, nil
}

func (p *parser) array(pos int) (*node, int, error) {
	n := &node{kind: kArr, start: pos}
	pos++
	prevComma, first := -1, true
	for {
		next, items, nlAfter, err := p.trivia(pos)
		if err != nil {
			return nil, 0, err
		}
		pos = next
		if pos >= len(p.src) {
			return nil, 0, &parseError{pos, "unterminated array"}
		}
		if p.src[pos] == ']' {
			n.free = append(n.free, commentsOf(items)...)
			n.end = pos + 1
			return n, pos + 1, nil
		}
		if !first && prevComma < 0 {
			return nil, 0, &parseError{pos, "expected ',' or ']'"}
		}
		first = false
		val, valEnd, err := p.value(pos)
		if err != nil {
			return nil, 0, err
		}
		e := &element{valStart: pos, valEnd: valEnd, value: val, commaPos: -1}
		after, err := p.tail(valEnd, &e.commaPos, &e.trailing)
		if err != nil {
			return nil, 0, err
		}
		free, lead := splitTrivia(items, nlAfter)
		n.free = append(n.free, free...)
		e.leading = lead
		n.elems = append(n.elems, e)
		prevComma = e.commaPos
		pos = after
	}
}

// tail consumes what follows a child's value on its OWN line: an optional
// comma and an optional same-line comment, in either order. A comment reached
// only by crossing a newline is not a trailing comment (§8.2) and is left for
// the container loop's trivia scan to classify.
func (p *parser) tail(pos int, commaPos *int, trailing **comment) (int, error) {
	for {
		q := pos
		for q < len(p.src) && (p.src[q] == ' ' || p.src[q] == '\t') {
			q++
		}
		if q < len(p.src) && p.src[q] == ',' && *commaPos < 0 {
			*commaPos = q
			pos = q + 1
			continue
		}
		if isCommentStart(p.src, q) && *trailing == nil {
			cm, next, err := scanComment(p.src, q)
			if err != nil {
				return 0, err
			}
			*trailing = cm
			pos = next
			continue
		}
		return pos, nil
	}
}

// splitTrivia applies §8.2's anchoring rule to the comments standing between
// the previous child and the one about to be parsed: the maximal run reaching
// that child with no blank line anywhere inside it is its leading comment
// block; everything before that run is free.
func splitTrivia(items []triviaItem, nlAfter int) (free, lead []*comment) {
	if len(items) == 0 {
		return nil, nil
	}
	if nlAfter > 1 {
		return commentsOf(items), nil
	}
	k := len(items) - 1
	for k > 0 && items[k].nlBefore <= 1 {
		k--
	}
	return commentsOf(items[:k]), commentsOf(items[k:])
}

func commentsOf(items []triviaItem) []*comment {
	if len(items) == 0 {
		return nil
	}
	out := make([]*comment, len(items))
	for i, it := range items {
		out[i] = it.c
	}
	return out
}

// slot is one positional child of a container: either a free comment, or a
// member/element together with the comments §8.2 anchors to it. start and end
// bound the whole thing, which is what makes "removing a member removes its
// leading comment" fall out of ordinary span arithmetic.
type slot struct {
	start, end int
	commaPos   int
	valEnd     int // member/element value end, where a separator comma belongs
	comment    *comment
	member     *member
	elem       *element
	leading    []*comment
}

// item reports whether the slot is a member or element — the two things a
// separator comma stands between. Free comments never take commas.
func (s slot) item() bool { return s.comment == nil }

// insertAfterPos is the byte position a new sibling placed after this slot
// starts at: past the slot's own trailing comment and past its comma.
func (s slot) insertAfterPos() int {
	pos := s.end
	if s.commaPos+1 > pos {
		pos = s.commaPos + 1
	}
	return pos
}

// slots lists a container's positional children in source order.
func (n *node) slots() []slot {
	out := make([]slot, 0, len(n.free)+len(n.members)+len(n.elems))
	for _, c := range n.free {
		out = append(out, slot{start: c.start, end: c.end, commaPos: -1, valEnd: c.end, comment: c})
	}
	for _, m := range n.members {
		out = append(out, slot{start: spanStart(m.leading, m.keyStart), end: spanEnd(m.trailing, m.valEnd),
			commaPos: m.commaPos, valEnd: m.valEnd, member: m, leading: m.leading})
	}
	for _, e := range n.elems {
		out = append(out, slot{start: spanStart(e.leading, e.valStart), end: spanEnd(e.trailing, e.valEnd),
			commaPos: e.commaPos, valEnd: e.valEnd, elem: e, leading: e.leading})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].start < out[j].start })
	return out
}

func spanStart(leading []*comment, fallback int) int {
	if len(leading) > 0 {
		return leading[0].start
	}
	return fallback
}

func spanEnd(trailing *comment, fallback int) int {
	if trailing != nil && trailing.end > fallback {
		return trailing.end
	}
	return fallback
}

// standalone lists the container's addressable comment nodes in source order:
// free comments and leading comments alike, which is what corpus
// yaml/set-scalar's pinned `/server/#0` (a LEADING comment, addressed as the
// container's first comment node) settles. Trailing comments are excluded —
// they have their own `#t` address (§4.5b).
func (n *node) standalone() []*comment {
	var out []*comment
	for _, s := range n.slots() {
		if s.comment != nil {
			out = append(out, s.comment)
			continue
		}
		out = append(out, s.leading...)
	}
	return out
}

func (n *node) container() bool { return n.kind == kObj || n.kind == kArr }

func (n *node) childCount() int {
	if n.kind == kArr {
		return len(n.elems)
	}
	return len(n.members)
}

// lineOf is the 1-based line the byte at off sits on, for §10.3's `found`
// line. Zero for an offset outside the source.
func lineOf(src []byte, off int) int {
	if off < 0 || off > len(src) {
		return 0
	}
	n := 1
	for _, c := range src[:off] {
		if c == '\n' {
			n++
		}
	}
	return n
}
