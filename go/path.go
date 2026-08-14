// Package hew implements the hew structured patch format: paths (spec §4),
// the transform-list IR (§9), and its canonical `.hewt` serialization (§9.6).
//
// The package is the notation-and-IR core. It imports no format library and
// performs no I/O: format bindings (ext/json, ext/yaml, …) and the filesystem
// layer sit above it.
package hew

import (
	"strconv"
	"strings"

	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

// Version is the .hew notation version; TransformsVersion is the version
// integer carried by the `hew-transforms:` key of a .hewt document (§9.6).
const (
	Version           = 1
	TransformsVersion = 1
)

// SegmentKind discriminates the segment forms of §4.
//
// The core knows the UNIVERSAL shapes and no others (§8.8): RFC 6901
// contributes SegKey, SegIndex and SegAppend, and hew adds SegMatch and the
// quoted segment — the shapes every tree-shaped format has, which is why a
// format-neutral pointer standard needed them. Everything else is
// SegExtension: a token the core lexed and a registered extension claimed.
type SegmentKind uint8

const (
	// SegKey is an object member name (§4.1). Name holds the decoded key. It
	// is also the floor: a token no other form claims is a key.
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
	// SegComment is a comment address (§4.5b): Index is the kind-scoped
	// ordinal, or Trailing marks the `#t` form.
	SegComment
	// SegExtension is a segment shape an extension claims (§8.8): a Markdown
	// heading, block ordinal or marker, and whatever a future family adds.
	// Form names the claiming shape and Raw is the token exactly as authored,
	// which is all the core needs to carry it and print it back unchanged. The
	// extension that claimed it is what knows what it MEANS.
	SegExtension
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
	case SegComment:
		return "comment"
	case SegExtension:
		return "extension"
	}
	return "segment(" + strconv.Itoa(int(k)) + ")"
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
	Name  string // SegKey, SegMatch (field), SegLabel
	Index int    // SegIndex, SegComment
	Value Scalar // SegMatch

	// Form and Raw carry a SegExtension: the name of the extension shape that
	// claimed the token, and the token itself. The core stores the bytes and
	// nothing more — decoding a heading's level or a marker's name is the
	// claiming extension's business, and keeping the raw spelling is what makes
	// print/parse exact for a shape the core does not understand.
	Form string
	Raw  string

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
		s.Form != o.Form || s.Raw != o.Raw || s.Value != o.Value ||
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
	case SegExtension:
		b.WriteString(s.Raw)
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

// describe names a segment's shape for a diagnostic. For an extension-claimed
// segment it is the FORM, not the kind: a reader needs to be told the spelling
// re-reads as a "marker", and "extension" would name the mechanism instead of
// the mistake.
func (s Segment) describe() string {
	if s.Kind == SegExtension && s.Form != "" {
		return s.Form
	}
	return s.Kind.String()
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

// NewPath builds an absolute path from typed segment arguments; it lives in
// segarg.go with the constructors that feed it (A.0, review point 20).

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

// --- spellability -----------------------------------------------------------
//
// A Path is data first and notation second: NewPath will build a key segment
// for any string, but the v0 §4 grammar cannot SPELL every string. The key
// "@scope/pkg" prints as "@scope~1pkg" and reads back as a marker; "8080"
// reads back as an index; "-" as the append position. Rendering such a path
// into a .hew or .hewt file emits an address that means something else, which
// §9.3 forbids: refuse, never misapply. The predicates below are how the
// emitting seams tell the two apart, and they are deliberately defined by the
// round trip itself rather than by a second, drift-prone copy of the grammar.
//
// Interim: a quoted-key segment form is ratified-pending (PR #1). When it
// lands, these keys become spellable and the refusals below stop firing on
// their own — the predicate is the round trip, so it follows the grammar.

// spellable reports whether the segment survives a print/parse round trip:
// re-reading its §4 spelling yields an Equal segment. It is a segment-local
// judgement — see Path.spellable for what only the whole path can see.
func (s Segment) spellable() bool {
	got, err := parseSegment(s.String(), true)
	return err == nil && s.Equal(got)
}

// spellable reports whether the whole path survives the round trip. It is
// stricter than every segment being spellable on its own: a single empty key
// prints "/" and reads back as the document root, and a "?" on a non-final
// segment is refused outright (§4.4) — neither is visible to one segment. The
// absent (zero) path is spellable because nothing is emitted for it.
func (p Path) spellable() bool {
	if p.IsZero() {
		return true
	}
	got, err := ParsePath(p.String())
	return err == nil && p.Equal(got)
}

// firstUnspellable returns the segment of p a diagnostic should blame, and
// true, for a path the v0 §4 grammar cannot spell. It reports false for a path
// that round-trips whole, the absent path included — a segment that would not
// survive on its own is not a problem if the path around it rescues it, which
// is why the whole-path verdict is taken first and the blame hunted second.
func (p Path) firstUnspellable() (Segment, bool) {
	if p.spellable() {
		return Segment{}, false
	}
	for i := range p.segs {
		if !p.segs[i].spellable() {
			return p.segs[i], true
		}
	}
	// Every segment survives alone, so the failure is POSITIONAL: a lone empty
	// key printing "/", or a "?" before the last segment. Blame the shortest
	// prefix that stops round-tripping.
	for i := 1; i <= len(p.segs); i++ {
		if !(Path{origin: p.origin, segs: p.segs[:i]}).spellable() {
			return p.segs[i-1], true
		}
	}
	// Unreachable: p is its own longest prefix and is unspellable by the check
	// at the top, so the loop above always returns.
	return Segment{}, true
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

	case body == "-":
		seg.Kind = SegAppend
		return seg, nil
	}

	// The extension-claimed shapes (§8.8), offered the token before the core's
	// remaining fallbacks. The order matters and is today's order: a form is
	// consulted BEFORE key-match and key, because `@name` and `# Setup` are
	// legal spellings of both, and AFTER the quoted segment and `-`, which no
	// extension may reinterpret.
	if form, claimed, err := claimSegment(body); claimed {
		if err != nil {
			return seg, segErr("segment " + strconv.Quote(raw) + ": " + err.Error())
		}
		seg.Kind, seg.Form, seg.Raw = SegExtension, form, body
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
		field, err := unescape(body[:i])
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
	key, err := unescape(body)
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

// isComment matches "#t" or "#" followed by digits (§4.5b). A segment
// starting with "#" that matches neither this nor an extension's shape — a
// Markdown heading is `#`s and a SPACE — is an ordinary key, which is why
// "#foo" and "##0" address keys.
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
	text, err := unescape(raw)
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

// unescape decodes RFC 6901's ~0 and ~1 plus hew's ~2 (§4.1). All three are in
// scope everywhere the CORE decodes: keys, match fields and match values, where
// an unescaped "=" would change the segment's form. An extension's own shape
// decodes its own text — ~2 is not meaningful in a Markdown heading and
// ext/markdown says so itself (§8.8).
func unescape(s string) (string, error) {
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
			b.WriteByte('=')
		default:
			return "", segErr(`invalid escape "~` + string(s[i]) + `"`)
		}
	}
	return b.String(), nil
}

// escapeKey is unescape's inverse for the core's own text: a key, a match
// field or an unquoted match value.
func escapeKey(s string) string {
	if !strings.ContainsAny(s, "~/=") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 4)
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '~':
			b.WriteString("~0")
		case '/':
			b.WriteString("~1")
		case '=':
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
