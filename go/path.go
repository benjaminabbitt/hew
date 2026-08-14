// Package hew implements the hew structured patch format: paths (spec §4),
// the transform-list IR (§9), and its canonical `.hewt` serialization (§9.6).
//
// The package is the notation-and-IR core. It imports no format library and
// performs no I/O: format bindings (hewjson, hewyaml, …) and the filesystem
// layer sit above it.
package hew

import (
	"strconv"
	"strings"

	"github.com/benjaminabbitt/hew/internal/hewerr"
)

// Version is the .hew notation version; TransformsVersion is the version
// integer carried by the `hew-transforms:` key of a .hewt document (§9.6).
const (
	Version           = 1
	TransformsVersion = 1
)

// SegmentKind discriminates the segment forms of §4. RFC 6901 contributes
// SegKey, SegIndex and SegAppend; the other six are hew's extensions.
type SegmentKind uint8

const (
	// SegKey is an object member name (§4.1). Name holds the decoded key.
	SegKey SegmentKind = iota
	// SegIndex is a sequence index (§4.1). Index holds it.
	SegIndex
	// SegAppend is RFC 6901's literal "-", the append position (§4.1).
	SegAppend
	// SegMatch is a key-match segment (§4.2): Name is the field name, empty
	// for the `=value` form, and Value is the format-natively decoded value.
	SegMatch
	// SegLabel is a quoted HCL block label (§4.3). Name holds the label.
	SegLabel
	// SegHeading is a Markdown heading (§4.5): Level is the ATX level, Name
	// the heading text.
	SegHeading
	// SegBlock is a Markdown block ordinal (§4.5): Block is the kind, Index
	// the ordinal within that kind within the section.
	SegBlock
	// SegMarker is a Markdown managed-marker region (§4.5, §8.6). Name is the
	// marker name, without the leading "@".
	SegMarker
	// SegComment is a comment address (§4.5b): Index is the kind-scoped
	// ordinal, or Trailing marks the `#t` form.
	SegComment
)

func (k SegmentKind) String() string {
	switch k {
	case SegKey:
		return "key"
	case SegIndex:
		return "index"
	case SegAppend:
		return "append"
	case SegMatch:
		return "match"
	case SegLabel:
		return "label"
	case SegHeading:
		return "heading"
	case SegBlock:
		return "block"
	case SegMarker:
		return "marker"
	case SegComment:
		return "comment"
	}
	return "segment(" + strconv.Itoa(int(k)) + ")"
}

// BlockKind is the kind of a Markdown block segment (§4.5). The set is closed:
// a `<kind>:<n>` spelling with any other kind is an ordinary key.
type BlockKind string

const (
	BlockPara  BlockKind = "para"
	BlockCode  BlockKind = "code"
	BlockList  BlockKind = "list"
	BlockTable BlockKind = "table"
	BlockQuote BlockKind = "quote"
	BlockHTML  BlockKind = "html"
)

func validBlockKind(k BlockKind) bool {
	switch k {
	case BlockPara, BlockCode, BlockList, BlockTable, BlockQuote, BlockHTML:
		return true
	}
	return false
}

// ScalarKind is the format-native type a key-match value decodes to (§4.2).
// Decoding happens at parse time so that `port=8080` addresses the number
// 8080 while `port="8080"` addresses the string.
type ScalarKind uint8

const (
	// ScalarString is an unquoted token that is not a number, boolean or
	// null, or any quoted token.
	ScalarString ScalarKind = iota
	ScalarNumber
	ScalarBool
	ScalarNull
)

func (k ScalarKind) String() string {
	switch k {
	case ScalarString:
		return "string"
	case ScalarNumber:
		return "number"
	case ScalarBool:
		return "bool"
	case ScalarNull:
		return "null"
	}
	return "scalar(" + strconv.Itoa(int(k)) + ")"
}

// Scalar is a decoded key-match value (§4.2). Text is the decoded content:
// for ScalarString the unquoted characters, for the other kinds the literal
// token. Quoted records the authored spelling, which is what makes
// `port="8080"` survive a print/parse round trip as a string.
type Scalar struct {
	Kind   ScalarKind
	Text   string
	Quoted bool
}

// Segment is one addressing step. Only the fields its Kind documents are
// meaningful; the rest are zero.
type Segment struct {
	Kind  SegmentKind
	Name  string    // SegKey, SegMatch (field), SegLabel, SegHeading (text), SegMarker
	Index int       // SegIndex, SegBlock, SegComment
	Level int       // SegHeading
	Block BlockKind // SegBlock
	Value Scalar    // SegMatch

	// Trailing marks the `#t` comment form (§4.5b).
	Trailing bool

	// Optional is the trailing `?` of §4.4: match it, or create it. Legal
	// only on a path's last segment, which ParsePath enforces. The further
	// rule that `?` is legal only on a hunk anchor is the .hew parser's to
	// enforce — a Path merely carries the flag.
	Optional bool

	// Ordinal is the `[n]` selector, which selects among siblings a path
	// cannot distinguish (§9.6, §11.10 reduction 4). It is IR-only: the
	// notation spells the same choice as a visible `! match ord=`
	// annotation (§7.2), so ParseAuthoredPath rejects it.
	Ordinal *int
}

// Equal reports structural equality, following Ordinal by value.
func (s Segment) Equal(o Segment) bool {
	if s.Kind != o.Kind || s.Name != o.Name || s.Index != o.Index ||
		s.Level != o.Level || s.Block != o.Block || s.Value != o.Value ||
		s.Trailing != o.Trailing || s.Optional != o.Optional {
		return false
	}
	switch {
	case s.Ordinal == nil && o.Ordinal == nil:
		return true
	case s.Ordinal == nil || o.Ordinal == nil:
		return false
	}
	return *s.Ordinal == *o.Ordinal
}

// String renders the segment in the §4 notation, without a leading "/".
func (s Segment) String() string {
	var b strings.Builder
	switch s.Kind {
	case SegKey:
		b.WriteString(escapeKey(s.Name))
	case SegIndex:
		b.WriteString(strconv.Itoa(s.Index))
	case SegAppend:
		b.WriteByte('-')
	case SegMatch:
		b.WriteString(escapeKey(s.Name))
		b.WriteByte('=')
		b.WriteString(s.Value.pathString())
	case SegLabel:
		b.WriteString(quoteLabel(s.Name))
	case SegHeading:
		b.WriteString(strings.Repeat("#", s.Level))
		b.WriteByte(' ')
		b.WriteString(escapeText(s.Name))
	case SegBlock:
		b.WriteString(string(s.Block))
		b.WriteByte(':')
		b.WriteString(strconv.Itoa(s.Index))
	case SegMarker:
		b.WriteByte('@')
		b.WriteString(escapeText(s.Name))
	case SegComment:
		b.WriteByte('#')
		if s.Trailing {
			b.WriteByte('t')
		} else {
			b.WriteString(strconv.Itoa(s.Index))
		}
	}
	if s.Ordinal != nil {
		b.WriteByte('[')
		b.WriteString(strconv.Itoa(*s.Ordinal))
		b.WriteByte(']')
	}
	if s.Optional {
		b.WriteByte('?')
	}
	return b.String()
}

// pathString renders a key-match value as it appears inside a segment.
func (s Scalar) pathString() string {
	switch s.Kind {
	case ScalarNull:
		return "null"
	case ScalarBool, ScalarNumber:
		return s.Text
	}
	if s.Quoted {
		return quoteLabel(s.Text)
	}
	return escapeKey(s.Text)
}

type pathOrigin uint8

const (
	originUnset pathOrigin = iota
	originAbsolute
	originRelative
)

// Path addresses a node (§4). The zero Path is the *absent* path — what an
// unset Transform.From or Transform.Before is — and is distinct from the
// document root, which is an absolute path with no segments and prints "/".
type Path struct {
	origin pathOrigin
	segs   []Segment
}

// RootPath is the document root, "/".
func RootPath() Path { return Path{origin: originAbsolute} }

// NewPath builds an absolute path from segments. NewPath() is RootPath.
func NewPath(segs ...Segment) Path {
	return Path{origin: originAbsolute, segs: append([]Segment(nil), segs...)}
}

// NewRelativePath builds a `.`-relative path (§4.6), resolved against the
// enclosing hunk's anchor.
func NewRelativePath(segs ...Segment) Path {
	return Path{origin: originRelative, segs: append([]Segment(nil), segs...)}
}

// IsZero reports whether this is the absent path — no path at all, as opposed
// to the root path.
func (p Path) IsZero() bool { return p.origin == originUnset }

// IsRelative reports whether the path is anchor-relative (§4.6).
func (p Path) IsRelative() bool { return p.origin == originRelative }

// Len is the number of segments; the root path has zero.
func (p Path) Len() int { return len(p.segs) }

// Segments returns a copy of the path's segments.
func (p Path) Segments() []Segment { return append([]Segment(nil), p.segs...) }

// Segment returns the i'th segment. It panics for an out-of-range index.
func (p Path) Segment(i int) Segment { return p.segs[i] }

// Append returns a copy of p extended with segs. Appending to the zero path
// yields the zero path.
func (p Path) Append(segs ...Segment) Path {
	if p.IsZero() {
		return p
	}
	out := make([]Segment, 0, len(p.segs)+len(segs))
	out = append(out, p.segs...)
	out = append(out, segs...)
	return Path{origin: p.origin, segs: out}
}

// Parent returns the path with its last segment dropped. It reports false for
// the zero path and for a path with no segments.
func (p Path) Parent() (Path, bool) {
	if len(p.segs) == 0 {
		return Path{}, false
	}
	return Path{origin: p.origin, segs: append([]Segment(nil), p.segs[:len(p.segs)-1]...)}, true
}

// Equal reports structural equality, origin included.
func (p Path) Equal(o Path) bool {
	if p.origin != o.origin || len(p.segs) != len(o.segs) {
		return false
	}
	for i := range p.segs {
		if !p.segs[i].Equal(o.segs[i]) {
			return false
		}
	}
	return true
}

// HasOrdinal reports whether any segment carries an IR-only `[n]` selector.
func (p Path) HasOrdinal() bool {
	for _, s := range p.segs {
		if s.Ordinal != nil {
			return true
		}
	}
	return false
}

// String renders the path in the §4 notation. The zero path renders as "",
// the root path as "/", the relative root as ".".
func (p Path) String() string {
	if p.origin == originUnset {
		return ""
	}
	var b strings.Builder
	if p.origin == originRelative {
		b.WriteByte('.')
	} else if len(p.segs) == 0 {
		return "/"
	}
	for i := range p.segs {
		b.WriteByte('/')
		b.WriteString(p.segs[i].String())
	}
	return b.String()
}

// ParsePath parses a path in the full §4 grammar, including the IR-only `[n]`
// ordinal selector. Malformed input is HEW001 at the parser component.
func ParsePath(s string) (Path, error) { return parsePath(s, true) }

// ParseAuthoredPath parses a path as it may appear in .hew notation. It
// differs from ParsePath in one rule: an `[n]` ordinal selector is HEW001,
// because the notation spells ordinal selection as a visible `! match ord=`
// annotation so a reviewer sees the fragility (§7.2, §11.10 reduction 4).
func ParseAuthoredPath(s string) (Path, error) { return parsePath(s, false) }

// MustParsePath is ParsePath for paths known to be well-formed; it panics on
// error. Intended for constants and tests.
func MustParsePath(s string) Path {
	p, err := ParsePath(s)
	if err != nil {
		panic(err)
	}
	return p
}

func pathErr(path, detail string) error {
	return &hewerr.Error{
		Code:      hewerr.CodeParse,
		Component: hewerr.ComponentParser,
		Path:      path,
		Detail:    detail,
	}
}

func parsePath(s string, allowOrdinal bool) (Path, error) {
	if s == "" {
		return Path{}, pathErr(s, "empty path")
	}
	var p Path
	var rest string
	switch s[0] {
	case '/':
		p.origin = originAbsolute
		rest = s[1:]
		if rest == "" {
			return p, nil // "/" is the document root
		}
	case '.':
		p.origin = originRelative
		rest = s[1:]
		if rest == "" {
			return p, nil // "." is the enclosing hunk's anchor (§4.6)
		}
		if rest[0] != '/' {
			return Path{}, pathErr(s, `relative path must continue with "/" after "."`)
		}
		rest = rest[1:]
	default:
		return Path{}, pathErr(s, `path must begin with "/" or "." (§4)`)
	}

	parts := strings.Split(rest, "/")
	p.segs = make([]Segment, 0, len(parts))
	for i, raw := range parts {
		seg, err := parseSegment(raw, allowOrdinal)
		if err != nil {
			return Path{}, pathErr(s, err.Error())
		}
		if seg.Optional && i != len(parts)-1 {
			return Path{}, pathErr(s, `trailing "?" is legal only on the last segment (§4.4)`)
		}
		p.segs = append(p.segs, seg)
	}
	return p, nil
}

type segErr string

func (e segErr) Error() string { return string(e) }

func parseSegment(raw string, allowOrdinal bool) (Segment, error) {
	var seg Segment
	body := raw
	if strings.HasSuffix(body, "?") {
		seg.Optional = true
		body = body[:len(body)-1]
	}
	if i := ordinalStart(body); i > 0 {
		if !allowOrdinal {
			return seg, segErr("segment " + strconv.Quote(raw) +
				`: "[n]" ordinal selectors are IR-only; write "! match ord=" in .hew notation (§7.2)`)
		}
		n, err := strconv.Atoi(body[i+1 : len(body)-1])
		if err != nil {
			return seg, segErr("segment " + strconv.Quote(raw) + ": ordinal selector out of range")
		}
		seg.Ordinal = &n
		body = body[:i]
	}

	switch {
	case strings.HasPrefix(body, `"`):
		name, err := unquote(body)
		if err != nil {
			return seg, segErr("segment " + strconv.Quote(raw) + ": " + err.Error())
		}
		seg.Kind, seg.Name = SegLabel, name
		return seg, nil

	case isHeading(body):
		level := 0
		for level < len(body) && body[level] == '#' {
			level++
		}
		text, err := unescape(body[level+1:], false)
		if err != nil {
			return seg, segErr("segment " + strconv.Quote(raw) + ": " + err.Error())
		}
		seg.Kind, seg.Level, seg.Name = SegHeading, level, text
		return seg, nil

	case isComment(body):
		seg.Kind = SegComment
		if body == "#t" {
			seg.Trailing = true
			return seg, nil
		}
		n, err := strconv.Atoi(body[1:])
		if err != nil {
			return seg, segErr("segment " + strconv.Quote(raw) + ": comment ordinal out of range")
		}
		seg.Index = n
		return seg, nil

	case strings.HasPrefix(body, "@"):
		if len(body) == 1 {
			return seg, segErr(`segment "@": marker segment requires a name (§4.5)`)
		}
		name, err := unescape(body[1:], false)
		if err != nil {
			return seg, segErr("segment " + strconv.Quote(raw) + ": " + err.Error())
		}
		seg.Kind, seg.Name = SegMarker, name
		return seg, nil

	case body == "-":
		seg.Kind = SegAppend
		return seg, nil
	}

	if kind, ord, ok := splitBlock(body); ok {
		n, err := strconv.Atoi(ord)
		if err != nil {
			return seg, segErr("segment " + strconv.Quote(raw) + ": block ordinal out of range")
		}
		seg.Kind, seg.Block, seg.Index = SegBlock, kind, n
		return seg, nil
	}
	if isIndex(body) {
		n, err := strconv.Atoi(body)
		if err != nil {
			return seg, segErr("segment " + strconv.Quote(raw) + ": index out of range")
		}
		seg.Kind, seg.Index = SegIndex, n
		return seg, nil
	}
	if i := unescapedEq(body); i >= 0 {
		field, err := unescape(body[:i], true)
		if err != nil {
			return seg, segErr("segment " + strconv.Quote(raw) + ": " + err.Error())
		}
		val, err := parseMatchValue(body[i+1:])
		if err != nil {
			return seg, segErr("segment " + strconv.Quote(raw) + ": " + err.Error())
		}
		seg.Kind, seg.Name, seg.Value = SegMatch, field, val
		return seg, nil
	}
	key, err := unescape(body, true)
	if err != nil {
		return seg, segErr("segment " + strconv.Quote(raw) + ": " + err.Error())
	}
	seg.Kind, seg.Name = SegKey, key
	return seg, nil
}

// ordinalStart returns the index of the "[" opening a trailing "[n]" selector,
// or -1. A selector needs a non-empty segment before it, so index 0 never
// starts one.
func ordinalStart(body string) int {
	if !strings.HasSuffix(body, "]") {
		return -1
	}
	i := strings.LastIndexByte(body, '[')
	if i <= 0 || i+1 == len(body)-1 {
		return -1
	}
	for _, c := range body[i+1 : len(body)-1] {
		if c < '0' || c > '9' {
			return -1
		}
	}
	return i
}

// isHeading matches one or more "#" followed by a space (§4.5). The space is
// what separates a heading from a comment address (§4.5b).
func isHeading(body string) bool {
	i := 0
	for i < len(body) && body[i] == '#' {
		i++
	}
	return i > 0 && i < len(body) && body[i] == ' '
}

// isComment matches "#t" or "#" followed by digits (§4.5b). A segment
// starting with "#" that matches neither this nor isHeading is an ordinary
// key, which is why "#foo" and "##0" address keys.
func isComment(body string) bool {
	if len(body) < 2 || body[0] != '#' {
		return false
	}
	if body == "#t" {
		return true
	}
	for i := 1; i < len(body); i++ {
		if body[i] < '0' || body[i] > '9' {
			return false
		}
	}
	return true
}

func splitBlock(body string) (BlockKind, string, bool) {
	i := strings.IndexByte(body, ':')
	if i < 0 || i+1 == len(body) {
		return "", "", false
	}
	kind := BlockKind(body[:i])
	if !validBlockKind(kind) {
		return "", "", false
	}
	for j := i + 1; j < len(body); j++ {
		if body[j] < '0' || body[j] > '9' {
			return "", "", false
		}
	}
	return kind, body[i+1:], true
}

// isIndex matches RFC 6901's array index production: "0" or a digit string
// with no leading zero.
func isIndex(body string) bool {
	if body == "" {
		return false
	}
	if body == "0" {
		return true
	}
	if body[0] < '1' || body[0] > '9' {
		return false
	}
	for i := 1; i < len(body); i++ {
		if body[i] < '0' || body[i] > '9' {
			return false
		}
	}
	return true
}

// unescapedEq finds the first "=" that is not part of a "~2" escape,
// scanning left to right so that escape sequences are never split.
func unescapedEq(body string) int {
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '~':
			i++ // skip the escape's second byte, whatever it is
		case '=':
			return i
		}
	}
	return -1
}

func parseMatchValue(raw string) (Scalar, error) {
	if strings.HasPrefix(raw, `"`) {
		text, err := unquote(raw)
		if err != nil {
			return Scalar{}, err
		}
		return Scalar{Kind: ScalarString, Text: text, Quoted: true}, nil
	}
	text, err := unescape(raw, true)
	if err != nil {
		return Scalar{}, err
	}
	switch text {
	case "true", "false":
		return Scalar{Kind: ScalarBool, Text: text}, nil
	case "null":
		return Scalar{Kind: ScalarNull, Text: text}, nil
	}
	if isNumber(text) {
		return Scalar{Kind: ScalarNumber, Text: text}, nil
	}
	return Scalar{Kind: ScalarString, Text: text}, nil
}

// isNumber recognizes the JSON number grammar. Anything else — including a
// leading-zero token like "08080" — is a string, which is the conservative
// reading of §4.2's "format-native decoding".
func isNumber(s string) bool {
	i := 0
	if i < len(s) && s[i] == '-' {
		i++
	}
	start := i
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == start || (s[start] == '0' && i-start > 1) {
		return false
	}
	if i < len(s) && s[i] == '.' {
		i++
		frac := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == frac {
			return false
		}
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		exp := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == exp {
			return false
		}
	}
	return i == len(s)
}

// unescape decodes RFC 6901's ~0 and ~1 plus hew's ~2 (§4.1). eq selects
// whether ~2 is in scope: it is for keys, match fields and match values,
// where an unescaped "=" would change the segment's form, and it is not for
// heading text or marker names, which no "=" can be confused with.
func unescape(s string, eq bool) (string, error) {
	if !strings.ContainsRune(s, '~') {
		return s, nil
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '~' {
			b.WriteByte(s[i])
			continue
		}
		if i+1 >= len(s) {
			return "", segErr(`dangling "~" escape`)
		}
		i++
		switch s[i] {
		case '0':
			b.WriteByte('~')
		case '1':
			b.WriteByte('/')
		case '2':
			if !eq {
				return "", segErr(`invalid escape "~2" here`)
			}
			b.WriteByte('=')
		default:
			return "", segErr(`invalid escape "~` + string(s[i]) + `"`)
		}
	}
	return b.String(), nil
}

func escapeKey(s string) string { return escape(s, true) }

func escapeText(s string) string { return escape(s, false) }

func escape(s string, eq bool) string {
	if !strings.ContainsAny(s, "~/=") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 4)
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '~':
			b.WriteString("~0")
		case s[i] == '/':
			b.WriteString("~1")
		case s[i] == '=' && eq:
			b.WriteString("~2")
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// unquote decodes a double-quoted segment or match value. Quoting uses
// backslash escapes for the quote and the backslash itself; "/" and "~" still
// use the tilde escapes, because a path is split on "/" before quoting is
// considered.
func unquote(s string) (string, error) {
	var b strings.Builder
	b.Grow(len(s))
	closed := false
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			if i+1 >= len(s) {
				return "", segErr(`dangling "\" escape in quoted segment`)
			}
			i++
			switch s[i] {
			case '"', '\\':
				b.WriteByte(s[i])
			default:
				return "", segErr(`invalid escape "\` + string(s[i]) + `" in quoted segment`)
			}
		case '~':
			if i+1 >= len(s) {
				return "", segErr(`dangling "~" escape in quoted segment`)
			}
			i++
			switch s[i] {
			case '0':
				b.WriteByte('~')
			case '1':
				b.WriteByte('/')
			default:
				return "", segErr(`invalid escape "~` + string(s[i]) + `" in quoted segment`)
			}
		case '"':
			if i != len(s)-1 {
				return "", segErr("unescaped quote inside quoted segment")
			}
			closed = true
		default:
			b.WriteByte(s[i])
		}
	}
	if !closed {
		return "", segErr("unterminated quoted segment")
	}
	return b.String(), nil
}

func quoteLabel(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '~':
			b.WriteString("~0")
		case '/':
			b.WriteString("~1")
		default:
			b.WriteByte(s[i])
		}
	}
	b.WriteByte('"')
	return b.String()
}
