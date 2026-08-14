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
	// SegKey is a NAME step (§4.1). Name holds the decoded text. It is also
	// the floor: a token no other form claims is a key.
	//
	// It carries both spellings of a name, bare and quoted, because O41's
	// quoted segment is not a kind of its own: `"aws"` is the same lexical form
	// as `aws`, said literally, and what it MEANS is decided by the container
	// the resolver is standing on: a key against a mapping, said
	// against a block set (§4.3, §8.8's SegLabel row). Segment.Quoted records
	// which spelling this segment is.
	SegKey SegmentKind = iota
	// SegIndex is a sequence index (§4.1). Index holds it.
	SegIndex
	// SegAppend is RFC 6901's literal "-", the append position (§4.1).
	SegAppend
	// SegMatch is a key-match segment (§4.2): Name is the field name, empty
	// for the `=value` form, and Value is the format-natively decoded value.
	SegMatch
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
// `port="8080"` survive a print/parse round trip byte for byte.
//
// Quoted is PRESENTATION, not identity: the Kind already carries the type a
// value compares as, so `port="8080"` and a programmatically-built string
// scalar `8080` address the same element and compare equal. What Quoted must
// never do is let the spelling lie about the kind, which is why rendering
// force-quotes any string whose bare form would re-decode as something else
// ([O42](§4.2)) — see mustQuoteScalar.
type Scalar struct {
	Kind   ScalarKind
	Text   string
	Quoted bool
}

// equal compares two scalars by what they ADDRESS: kind and text. The
// authored quoting is not part of the answer (see Scalar.Quoted).
func (s Scalar) equal(o Scalar) bool { return s.Kind == o.Kind && s.Text == o.Text }

// Segment is one addressing step. Only the fields its Kind documents are
// meaningful; the rest are zero.
type Segment struct {
	Kind  SegmentKind
	Name  string // SegKey, SegMatch (field)
	Index int    // SegIndex, SegComment
	Value Scalar // SegMatch

	// Quoted spells Name in the literal form of §4.1 — `/deps/"@scope/pkg"`,
	// `/provider/"aws"`, `/deps/"@scope/pkg"=1.0.0`. It is a FLOOR, not the
	// whole truth: rendering also quotes a name whose bare spelling would not
	// reparse as the same segment (§4.1's canonical-rendering rule), so
	// IsQuoted, not this field, is what a reader of a Segment should ask, and
	// what Equal compares.
	//
	// On a SegKey it is also the label/attribute distinction §4.3 turns on: a
	// quoted segment is a key said literally; an unquoted one is an
	// attribute name or a nested block type.
	Quoted bool

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

// IsQuoted reports whether this segment is the LITERAL form of §4.1 — the
// spelling that says "this text is data, not a form". Against a mapping it is
// a key said literally (§4.1); the container decides how a name reads, and the
// other and never both, which is why one spelling serves both (O41).
//
// It answers for the CANONICAL rendering, not for the authored one: a name
// whose bare spelling would not survive a reparse is quoted whether or not
// whoever built the Segment knew that. That is what makes String()/ParsePath a
// bijection over every key a document can hold.
func (s Segment) IsQuoted() bool {
	switch s.Kind {
	case SegKey:
		return s.Quoted || mustQuoteKey(s.Name)
	case SegMatch:
		return s.Quoted || mustQuoteField(s.Name)
	}
	return false
}

// Equal reports structural equality, following Ordinal by value. Quoting is
// compared as IsQuoted answers it — the canonical spelling, so that a key
// built as data and the same key read back from text are one segment.
func (s Segment) Equal(o Segment) bool {
	if s.Kind != o.Kind || s.Name != o.Name || s.Index != o.Index ||
		s.Form != o.Form || s.Raw != o.Raw || !s.Value.equal(o.Value) ||
		s.IsQuoted() != o.IsQuoted() ||
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
		b.WriteString(s.nameString())
	case SegIndex:
		b.WriteString(strconv.Itoa(s.Index))
	case SegAppend:
		b.WriteByte('-')
	case SegMatch:
		b.WriteString(s.nameString())
		b.WriteByte('=')
		b.WriteString(s.Value.pathString())
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

// nameString renders a segment's name in its canonical spelling: the quoted
// form when IsQuoted says so, the tilde-escaped bare form otherwise.
func (s Segment) nameString() string {
	if s.IsQuoted() {
		return quoteSegment(s.Name)
	}
	return escapeKey(s.Name)
}

// --- the canonical-rendering rule (§4.1, O41) --------------------------------
//
// "An implementation that renders a path back to text MUST emit the quoted
// form for any key whose bare spelling would not reparse as the same segment."
// The classes §4.1 enumerates are written out below, and a round-trip safety
// net catches anything the enumeration misses, so the predicate cannot drift
// from the grammar it is about.
//
// The enumeration is FORMAT-INDEPENDENT on purpose, and that is not laziness:
// `@scope/pkg` must render as `"@scope/pkg"` in a JSON patch even in a build
// that links no Markdown extension, because the text is read back by whatever
// build reads it next, and the corpus (json/diff-scoped-key) pins exactly that
// spelling. A rule that quoted only what the CURRENT build's linked extensions
// would misread would make the .hewt bytes depend on the writer's link set.

// mustQuoteKey reports whether §4.1 requires the quoted form for a key.
func mustQuoteKey(name string) bool {
	switch {
	case name == "": // RFC 6901's empty-key member: /x/""
		return true
	case name == "-": // the append position
		return true
	case name == "*": // reserved for a wildcard (§4.7, O44)
		return true
	case name[0] == '@' || name[0] == '#' || name[0] == '"':
		// A marker (§4.5), a comment address or a heading (§4.5b, §4.5), and
		// the quote that would open a literal.
		return true
	case allDigits(name): // an index
		return true
	case strings.HasSuffix(name, "?"): // the optional flag (§4.4)
		return true
	case ordinalStart(name) > 0: // the IR-only [n] selector (§9.6)
		return true
	case blockOrdinalShape(name): // `<kind>:<n>` (§4.5)
		return true
	}
	// The safety net, defined by the round trip itself: anything the core's
	// own lexer reads back as another form, or as a different key.
	got, err := parseSegment(escapeKey(name), true, coreScope)
	return err != nil || got.Kind != SegKey || got.Name != name
}

// mustQuoteField is mustQuoteKey for a key-match FIELD name, with two
// differences. The empty field is the `=value` form of §4.2 and is spelled by
// writing nothing, so it is never quoted; and a field ending `<`, `>` or `!`
// is reserved (§4.7, O44), so its literal spelling is the quoted one.
func mustQuoteField(name string) bool {
	if name == "" {
		return false
	}
	return reservedFieldEnding(name) || mustQuoteKey(name)
}

// reservedFieldEnding reports O44's reservation: a key-match field ending `<`,
// `>` or `!` is HEW001 in v0, because `count>=5` parses today as a match on a
// field named `count>` — a working address a later `>=` operator would
// silently reinterpret (§4.7).
func reservedFieldEnding(name string) bool {
	if name == "" {
		return false
	}
	switch name[len(name)-1] {
	case '<', '>', '!':
		return true
	}
	return false
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

// blockOrdinalShape matches §4.5's `<kind>:<ordinal>` shape WITHOUT knowing
// the kinds: `code:0` is Markdown's, but the core no longer holds that
// vocabulary (§8.8) and a key of that shape is quoted whatever the kind reads
// as, so that ext/markdown can claim it without the rendering having to change.
func blockOrdinalShape(name string) bool {
	i := strings.IndexByte(name, ':')
	if i <= 0 || i == len(name)-1 {
		return false
	}
	if !identShape(name[:i]) {
		return false
	}
	return allDigits(name[i+1:])
}

func identShape(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_':
		case i > 0 && (c >= '0' && c <= '9' || c == '-'):
		default:
			return false
		}
	}
	return s != ""
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
//
// A string is force-quoted whenever its bare spelling would re-decode as
// something else ([O42](§4.2)). §4.2 compares AFTER decoding, so the spelling
// of a value carries its type: a programmatically-built `name=8080` that meant
// the string "8080" addresses a different element, or none, and raises no
// error while doing it. This is O41's rule one level down.
func (s Scalar) pathString() string {
	switch s.Kind {
	case ScalarNull:
		return "null"
	case ScalarBool, ScalarNumber:
		return s.Text
	}
	if s.Quoted || mustQuoteScalar(s.Text) {
		return quoteSegment(s.Text)
	}
	return escapeKey(s.Text)
}

// mustQuoteScalar reports whether a STRING scalar's bare spelling would read
// back as a different scalar: as a number, a boolean, null, the empty token,
// or a quoted string — or would lose its tail to one of the suffixes a segment
// strips before its value is read at all.
func mustQuoteScalar(text string) bool {
	switch text {
	case "", "true", "false", "null":
		return true
	}
	if text[0] == '"' || isNumber(text) {
		return true
	}
	// The `?` of §4.4 and the `[n]` of §9.6 are stripped from the END of the
	// whole segment, which is where a match value sits: `f=opt?` reads as the
	// value "opt" plus an optional flag, and `f=a[1]` as the value "a" plus an
	// ordinal. Both are silent, and both are the value's problem to prevent.
	esc := escapeKey(text)
	return strings.HasSuffix(esc, "?") || ordinalStart("x="+esc) > 0
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

// --- spellability, and what is left of it (O41) -------------------------------
//
// Until the quoted segment landed, a Path was data the §4 grammar could not
// always SPELL: "@scope/pkg" printed as "@scope~1pkg" and read back as a
// marker, "8080" as an index, "-" as the append position. Every seam that
// turned a path into text ran a round-trip guard and REFUSED, because emitting
// an address that means something else is what §9.3 forbids.
//
// O41 dissolved that guard. Every key a document can hold now has a spelling —
// the literal form — and the canonical-rendering rule makes String()/ParsePath
// a bijection, so a key that used to be refused now round-trips. What survives
// here is deliberately narrow, and it is not about keys at all:
//
//   - A rendering that would not reparse identically can now only come from IR
//     that is malformed as DATA rather than as notation: a negative index, a
//     ScalarNumber whose text is not a number, a ScalarBool that is not
//     true/false, an extension-claimed token no linked extension claims.
//   - A name containing a LINE BREAK. §4.1's quoted form has exactly two
//     escapes, `\"` and `\\`, so there is no spelling for a newline — and a
//     path is written on a `@@` line and in a one-line .hewt scalar, both of
//     which a raw newline would tear in half. This is the one class of real
//     document key the grammar still cannot spell; it is enumerated here rather
//     than left to be discovered by a corrupted patch.
//
// The predicate stays defined BY the round trip rather than by a second copy of
// the grammar, so it keeps following §4 wherever §4 goes.

// spellable reports whether the segment survives a print/parse round trip:
// re-reading its §4 spelling yields an Equal segment. It is a segment-local
// judgement — see Path.spellable for what only the whole path can see.
func (s Segment) spellable() bool {
	if strings.ContainsAny(s.Name, "\r\n") || strings.ContainsAny(s.Value.Text, "\r\n") {
		return false
	}
	got, err := parseSegment(s.String(), true, buildScope)
	return err == nil && s.Equal(got)
}

// spellable reports whether the whole path survives the round trip. It is
// stricter than every segment being spellable on its own: a "?" on a non-final
// segment is refused outright (§4.4), which no single segment can see. The
// absent (zero) path is spellable because nothing is emitted for it.
func (p Path) spellable() bool {
	if p.IsZero() {
		return true
	}
	for i := range p.segs {
		if !p.segs[i].spellable() {
			return false
		}
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
	// Every segment survives alone, so the failure is POSITIONAL: a "?" before
	// the last segment. Blame the shortest prefix that stops round-tripping.
	for i := 1; i <= len(p.segs); i++ {
		if !(Path{origin: p.origin, segs: p.segs[:i]}).spellable() {
			return p.segs[i-1], true
		}
	}
	// Unreachable: p is its own longest prefix and is unspellable by the check
	// at the top, so the loop above always returns.
	return Segment{}, true
}

// --- the claim scope (§8.8) ---------------------------------------------------
//
// An extension-claimed segment form can only be resolved once it is known
// WHICH extension may claim (§8.8's second recorded tension). O48 says the core
// "offers the token to the ACTIVE format's extension", and the format is known
// from the `--- ` target line before any hunk is read (§2.2) and from the
// `format:` key of a .hewt document (§9.6) — so the format-scoped functions
// below are the ones the parser, the lowerer and the .hewt codec use, and this
// is the change to the §4 parsing API that registry.go's claimSegment note left
// to O41's work package.
//
// The unscoped ParsePath/ParseAuthoredPath keep BUILD scope, because a caller
// with no target in hand — a test, a CLI argument, a program building an
// address — has no active format to scope to, and refusing to parse without one
// would make the notation unusable outside a patch. What build scope may never
// do is decide RENDERING: see the canonical-rendering rule's note above.
type scope struct {
	format FormatID
	all    bool
}

var (
	// coreScope consults no extension: the five universal shapes of §8.8 only.
	coreScope = scope{}
	// buildScope consults every registered extension.
	buildScope = scope{all: true}
)

// formatScope is the active-format scope of §8.8. An empty id — a patch whose
// format is unknown, which cannot be lowered anyway (O9, HEW021) — consults no
// extension rather than all of them.
func formatScope(f FormatID) scope { return scope{format: f} }

// ParsePath parses a path in the full §4 grammar, including the IR-only `[n]`
// ordinal selector. Malformed input is HEW001 at the parser component.
//
// Extension-claimed segment shapes are BUILD-scoped here; ParsePathIn scopes
// them to one format (§8.8).
func ParsePath(s string) (Path, error) { return parsePath(s, true, buildScope) }

// ParseAuthoredPath parses a path as it may appear in .hew notation. It
// differs from ParsePath in one rule: an `[n]` ordinal selector is HEW001,
// because the notation spells ordinal selection as a visible `! match ord=`
// annotation so a reviewer sees the fragility (§7.2, §11.10 reduction 4).
func ParseAuthoredPath(s string) (Path, error) { return parsePath(s, false, buildScope) }

// ParsePathIn is ParsePath with the segment-claim scope of §8.8: only the
// extension registered for f may claim a token, so a JSON key that happens to
// be spelled like a Markdown heading is a JSON key.
func ParsePathIn(f FormatID, s string) (Path, error) { return parsePath(s, true, formatScope(f)) }

// ParseAuthoredPathIn is ParseAuthoredPath, scoped to one format (§8.8).
func ParseAuthoredPathIn(f FormatID, s string) (Path, error) {
	return parsePath(s, false, formatScope(f))
}

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

func parsePath(s string, allowOrdinal bool, sc scope) (Path, error) {
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

	parts := splitSegments(rest)
	p.segs = make([]Segment, 0, len(parts))
	for i, raw := range parts {
		seg, err := parseSegment(raw, allowOrdinal, sc)
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

// splitSegments splits a path's body on "/" QUOTE-AWARELY (§4): a "/" inside a
// quoted segment is literal, so `/dependencies/"@scope/pkg"` is two segments
// and not three. Splitting on "/" before honouring quotes is the defect §4
// names as the one an implementation reaches for first.
//
// A quote opens a literal only where one can begin: at the start of a segment
// (`"@scope/pkg"`, `"aws"`) or straight after a key-match's "=" (`id="a/b"`).
// Anywhere else a quote is an ordinary character of an ordinary key, which is
// what keeps `a"b` addressing the key `a"b`.
func splitSegments(rest string) []string {
	var out []string
	start, inQuote := 0, false
	for i := 0; i < len(rest); i++ {
		switch {
		case inQuote:
			switch rest[i] {
			case '\\':
				i++ // an escaped character, whatever it is, is not the closer
			case '"':
				inQuote = false
			}
		case rest[i] == '"' && (i == start || rest[i-1] == '='):
			inQuote = true
		case rest[i] == '/':
			out = append(out, rest[start:i])
			start = i + 1
		}
	}
	return append(out, rest[start:])
}

func parseSegment(raw string, allowOrdinal bool, sc scope) (Segment, error) {
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
		// The literal form (§4.1, O41). What it MEANS is the container's
		// business — a key against a mapping
		// (§4.3) — so the parse records the spelling and stops there.
		name, after, err := unquotePrefix(body)
		if err != nil {
			return seg, segErr("segment " + strconv.Quote(raw) + ": " + err.Error())
		}
		switch {
		case after == "":
			seg.Kind, seg.Name, seg.Quoted = SegKey, name, true
			return seg, nil
		case after[0] == '=':
			// A quoted key-match FIELD: /deps/"@scope/pkg"=1.0.0 (§4.2).
			if name == "" {
				return seg, segErr("segment " + strconv.Quote(raw) +
					`: the empty-field key-match is written "/tags/=value", not with an empty quoted field (§4.2)`)
			}
			val, verr := parseMatchValue(after[1:])
			if verr != nil {
				return seg, segErr("segment " + strconv.Quote(raw) + ": " + verr.Error())
			}
			seg.Kind, seg.Name, seg.Quoted, seg.Value = SegMatch, name, true, val
			return seg, nil
		}
		return seg, segErr("segment " + strconv.Quote(raw) +
			": a quoted segment must be the whole segment or a key-match field (§4.1, §4.2)")

	case body == "*":
		// Reserved for a wildcard segment (§4.7, O44). The literal key — `*` is
		// a real key in a tsconfig.json `paths` map — keeps its spelling.
		return seg, segErr("segment " + strconv.Quote(raw) +
			`: a bare "*" segment is reserved in v0 for a wildcard (§4.7); write the literal key as "*"`)

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
	if form, claimed, err := claimSegment(body, sc); claimed {
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
		if reservedFieldEnding(field) {
			// O44: `count>=5` parses today as a match on a field named
			// `count>`, which a later `>=` operator would silently
			// reinterpret. Refusing it now is what keeps O6 addable (§4.7).
			return seg, segErr("segment " + strconv.Quote(raw) + ": a key-match field ending " +
				strconv.Quote(field[len(field)-1:]) + " is reserved in v0 for the comparison operators of §4.7 — " +
				"the field " + strconv.Quote(field) + " is addressable as its literal spelling, " +
				quoteSegment(field) + "=" + body[i+1:])
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
		text, after, err := unquotePrefix(raw)
		if err != nil {
			return Scalar{}, err
		}
		if after != "" {
			return Scalar{}, segErr("trailing text after a quoted key-match value")
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

// unquotePrefix decodes the quoted run that s opens with and returns its
// decoded text and whatever follows the closing quote — "" for a whole quoted
// segment, `=…` for a quoted key-match field (§4.2).
//
// Inside the quotes the escapes are `\"` and `\\`, and those only (§4.1,
// ratified by O41): nothing in there is being disambiguated, so `~0`/`~1`/`~2`
// are neither needed nor interpreted, and a "/" is a literal "/" because
// splitting is quote-aware (§4).
func unquotePrefix(s string) (text, after string, err error) {
	var b strings.Builder
	b.Grow(len(s))
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			if i+1 >= len(s) {
				return "", "", segErr(`dangling "\" escape in quoted segment`)
			}
			i++
			switch s[i] {
			case '"', '\\':
				b.WriteByte(s[i])
			default:
				return "", "", segErr(`invalid escape "\` + string(s[i]) + `" in quoted segment`)
			}
		case '"':
			return b.String(), s[i+1:], nil
		default:
			b.WriteByte(s[i])
		}
	}
	return "", "", segErr("unterminated quoted segment")
}

// quoteSegment is unquotePrefix's inverse: the literal form of §4.1, escaping
// the quote and the backslash and nothing else.
func quoteSegment(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		default:
			b.WriteByte(s[i])
		}
	}
	b.WriteByte('"')
	return b.String()
}
