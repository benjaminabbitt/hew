package markdown

import (
	"strconv"
	"strings"

	hew "github.com/benjaminabbitt/hew/go"
)

// Markdown's segment grammar (§4.5, §8.6), and the reason §8.8 called this the
// clearest case in its audit: `# Setup`, `code:0` and `@name` are prose-document
// vocabulary with no meaning in any config format. They lived in the core's
// segment enum until O48; they live here now, and the core lexes a token and
// offers it to these three forms without knowing what any of them mean.
//
// The forms are named as §4.5 names them, because the name travels in the
// parsed Segment and is what a resolver would switch on.
const (
	FormHeading = "heading"
	FormBlock   = "block"
	FormMarker  = "marker"
)

// BlockKind is the kind of a block segment (§4.5). The set is CLOSED: a
// `<kind>:<n>` spelling with any other kind is an ordinary key, which is what
// keeps `port:0` addressing a key named "port:0" rather than failing.
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

// segmentForms is what the Binding registers. Order is not significant: the
// three shapes are disjoint by their first byte and their punctuation, so no
// token can be claimed by two of them.
func segmentForms() []hew.SegmentForm {
	return []hew.SegmentForm{
		{Name: FormHeading, Claim: claimHeading},
		{Name: FormBlock, Claim: claimBlock},
		{Name: FormMarker, Claim: claimMarker},
	}
}

// claimHeading matches one or more "#" followed by a SPACE (§4.5). The space is
// the whole of what separates a heading from a comment address (§4.5b), which
// is why "#0" is not one and "##0" is a key.
func claimHeading(raw string) (bool, error) {
	level := 0
	for level < len(raw) && raw[level] == '#' {
		level++
	}
	if level == 0 || level >= len(raw) || raw[level] != ' ' {
		return false, nil
	}
	if _, err := unescapeText(raw[level+1:]); err != nil {
		return true, err
	}
	return true, nil
}

// claimBlock matches `<kind>:<ordinal>` for a kind in the closed set. An
// unknown kind is NOT this form — it falls through to an ordinary key — but a
// known kind with an unrepresentable ordinal is this form, malformed, and says
// so rather than degrading into a key that would never resolve.
func claimBlock(raw string) (bool, error) {
	i := strings.IndexByte(raw, ':')
	if i < 0 || i+1 == len(raw) {
		return false, nil
	}
	if !validBlockKind(BlockKind(raw[:i])) {
		return false, nil
	}
	for j := i + 1; j < len(raw); j++ {
		if raw[j] < '0' || raw[j] > '9' {
			return false, nil
		}
	}
	if _, err := strconv.Atoi(raw[i+1:]); err != nil {
		return true, segErr("segment " + strconv.Quote(raw) + ": block ordinal out of range")
	}
	return true, nil
}

// claimMarker matches a managed-marker region, "@" plus a name (§4.5, §8.6).
// A bare "@" is claimed and refused: it is unmistakably a marker and
// unmistakably nameless, and calling it a key would bury the typo in a
// resolution failure much further along.
func claimMarker(raw string) (bool, error) {
	if !strings.HasPrefix(raw, "@") {
		return false, nil
	}
	if len(raw) == 1 {
		return true, segErr(`segment "@": marker segment requires a name (§4.5)`)
	}
	if _, err := unescapeText(raw[1:]); err != nil {
		return true, segErr("segment " + strconv.Quote(raw) + ": " + err.Error())
	}
	return true, nil
}

// --- this extension's own text escaping -------------------------------------
//
// Heading text and marker names take RFC 6901's ~0 and ~1 and NOT hew's ~2
// (§4.1): a heading is not a key-match, so no "=" in it can be confused with
// anything, and the escape is refused rather than quietly accepted. The core
// decodes ~2 everywhere IT decodes, so this is a small duplicate rather than a
// parameter on a shared helper — which is the trade §8.8 makes deliberately:
// an extension owns its grammar, including the boring parts.

func unescapeText(s string) (string, error) {
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
			return "", segErr(`invalid escape "~2" here`)
		default:
			return "", segErr(`invalid escape "~` + string(s[i]) + `"`)
		}
	}
	return b.String(), nil
}

func escapeText(s string) string {
	if !strings.ContainsAny(s, "~/") {
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
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

type segErr string

func (e segErr) Error() string { return string(e) }

// --- constructors and accessors ---------------------------------------------
//
// A core Segment carries a claimed token as bytes; these turn the bytes into
// Markdown's own reading of them and back. They are the whole of what "the
// extension supplies the meaning" amounts to for a family whose applier is not
// built yet (§8.7, O29).

// Heading builds a heading segment: level "#"s, then the text (§4.5).
func Heading(level int, text string) hew.Segment {
	return hew.Segment{
		Kind: hew.SegExtension,
		Form: FormHeading,
		Raw:  strings.Repeat("#", level) + " " + escapeText(text),
	}
}

// Block builds a block-ordinal segment, `<kind>:<n>`.
func Block(kind BlockKind, index int) hew.Segment {
	return hew.Segment{
		Kind: hew.SegExtension,
		Form: FormBlock,
		Raw:  string(kind) + ":" + strconv.Itoa(index),
	}
}

// Marker builds a managed-marker segment, `@name`.
func Marker(name string) hew.Segment {
	return hew.Segment{Kind: hew.SegExtension, Form: FormMarker, Raw: "@" + escapeText(name)}
}

// ParseHeading reads a heading segment's level and text.
func ParseHeading(s hew.Segment) (level int, text string, ok bool) {
	if s.Kind != hew.SegExtension || s.Form != FormHeading {
		return 0, "", false
	}
	for level < len(s.Raw) && s.Raw[level] == '#' {
		level++
	}
	if level == 0 || level >= len(s.Raw) || s.Raw[level] != ' ' {
		return 0, "", false
	}
	text, err := unescapeText(s.Raw[level+1:])
	if err != nil {
		return 0, "", false
	}
	return level, text, true
}

// ParseBlock reads a block segment's kind and ordinal.
func ParseBlock(s hew.Segment) (kind BlockKind, index int, ok bool) {
	if s.Kind != hew.SegExtension || s.Form != FormBlock {
		return "", 0, false
	}
	i := strings.IndexByte(s.Raw, ':')
	if i < 0 {
		return "", 0, false
	}
	k := BlockKind(s.Raw[:i])
	if !validBlockKind(k) {
		return "", 0, false
	}
	n, err := strconv.Atoi(s.Raw[i+1:])
	if err != nil {
		return "", 0, false
	}
	return k, n, true
}

// ParseMarker reads a marker segment's name, without the leading "@".
func ParseMarker(s hew.Segment) (name string, ok bool) {
	if s.Kind != hew.SegExtension || s.Form != FormMarker || len(s.Raw) < 2 {
		return "", false
	}
	name, err := unescapeText(s.Raw[1:])
	if err != nil {
		return "", false
	}
	return name, true
}
