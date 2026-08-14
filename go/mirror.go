package hew

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// The mirror fragment reader. A hunk body mirrors the document's shape (§2.3,
// §5), and the parser must read it without knowing which format it mirrors:
// the parser owns the notation and knows no format mechanics (§9). What the
// six bindings' body spellings have in common is small, and the corpus pins
// all of it:
//
//   - a member is `key: value` (JSON/JSONC/YAML) or `key = value` (TOML/HCL);
//     a key may be bare or double-quoted, and a missing value means the
//     member's children follow, indented (§8.1, §8.5);
//   - a sequence element is `- item` (YAML) or a bare value line (a JSON
//     array element, §8.1);
//   - an HCL block is `type "label"… {` … `}` (§8.5);
//   - a TOML table header is `[a.b]` / `[[a]]` and belongs to the child it
//     introduces, not to the anchor (§8.4);
//   - a comment is `#…` or `//…`, and is a node (§4.5b, §8.2);
//   - values are read as YAML scalars and flow collections, which is exactly
//     the format-native decoding §4.2 and §6.3 ask for — JSON is a YAML
//     subset, and the TOML and HCL scalar spellings coincide with it.
//
// Separating and trailing commas are optional and ignored (§8.1).
type mirrorKind uint8

const (
	mKV      mirrorKind = iota // key: value, key = value, or key with children
	mSeqItem                   // "- …" sequence element
	mScalar                    // a bare value line: a JSON array element
	mComment                   // a comment node
	mBlock                     // an HCL block: type, labels, body
	mTable                     // a TOML [a.b] / [[a]] header and the lines it owns
	mAnnot                     // a `?` or `!` annotation line (§7)
)

// mirrorEntry is one node of the fragment tree. Children carry their own
// margins: a context container may hold `+` and `-` lines — that is what an
// anchored hunk is for — while a `+` or `-` container owns its whole subtree.
type mirrorEntry struct {
	kind   mirrorKind
	margin byte
	line   int
	indent int

	key    string   // mKV
	labels []string // mBlock: block type first, then labels; mTable: dotted components
	array  bool     // mTable: the [[…]] array-of-tables spelling

	inline   Value // mKV with an inline value, mScalar
	comment  string
	annot    annotation
	children []*mirrorEntry

	// Filled by the lowering pass: the line-scoped annotations attached to
	// this entry (§7), and the container's entry list, which `? exhaustive`
	// counts.
	q        quals
	ord      *ordAnnot
	siblings []*mirrorEntry

	// Comment ordinals within the parent container, one per projection (§5):
	// a comment's address is its position among the comments of the image it
	// belongs to (§4.5b). -1 means "not in that image".
	beforeIdx, afterIdx int
}

// inBefore and inAfter report which of the two documents a hunk body defines
// the entry belongs to: the before-image is every context and `-` line, the
// after-image every context and `+` line (§5).
func (e *mirrorEntry) inBefore() bool {
	return e.margin == marginContext || e.margin == marginRemove
}

func (e *mirrorEntry) inAfter() bool {
	return e.margin == marginContext || e.margin == marginAdd
}

// node reports whether the entry denotes a node of the mirrored document, as
// opposed to an annotation about one.
func (e *mirrorEntry) node() bool { return e.kind != mAnnot }

// container reports whether the entry has a mirrored subtree rather than an
// inline value.
func (e *mirrorEntry) container() bool { return len(e.children) > 0 }

// bodyReader walks a hunk's classified body lines into a fragment tree.
type bodyReader struct {
	lines []bodyLine
	i     int
}

// entries reads every entry at the given indentation, stopping at the first
// shallower line.
func (r *bodyReader) entries(indent int) ([]*mirrorEntry, error) {
	var out []*mirrorEntry
	for r.i < len(r.lines) {
		bl := r.lines[r.i]
		if bl.indent < indent {
			return out, nil
		}
		if bl.indent > indent {
			return nil, parseErr(bl.num, "", "unexpected indentation: %d spaces where %d were expected (§2.3)", bl.indent, indent)
		}
		e, err := r.entry()
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

// entry reads one entry and everything indented under it.
func (r *bodyReader) entry() (*mirrorEntry, error) {
	bl := r.lines[r.i]
	r.i++
	e := &mirrorEntry{margin: bl.margin, line: bl.num, indent: bl.indent, beforeIdx: -1, afterIdx: -1}

	if bl.margin == marginAssert || bl.margin == marginDirect {
		a, err := parseAnnotation(bl)
		if err != nil {
			return nil, err
		}
		e.kind, e.annot = mAnnot, a
		return e, nil
	}

	text := trimSeparator(bl.text)
	switch {
	case commentText(text):
		e.kind, e.comment = mComment, commentBody(text)
		return e, nil

	case closerText(text):
		return nil, parseErr(bl.num, "", "unexpected closing delimiter %q (§2.3)", text)

	case tableHeader(text):
		e.kind = mTable
		var err error
		if e.labels, e.array, err = parseTableHeader(text, bl.num); err != nil {
			return nil, err
		}
		// A table header owns the following lines at its own indentation up
		// to the next header: TOML writes a table's members flush with it
		// (§8.4).
		for r.i < len(r.lines) {
			next := r.lines[r.i]
			if next.indent != bl.indent || next.margin == marginAssert || next.margin == marginDirect ||
				tableHeader(trimSeparator(next.text)) {
				break
			}
			child, err := r.entry()
			if err != nil {
				return nil, err
			}
			e.children = append(e.children, child)
		}

	case text == "-" || strings.HasPrefix(text, "- "):
		e.kind = mSeqItem
		if rest := strings.TrimLeft(strings.TrimPrefix(text, "-"), " "); rest != "" {
			// The element's first member shares the dash's line; read it as
			// though it were written indented under the dash.
			r.insert(bodyLine{margin: bl.margin, indent: bl.indent + 2, text: rest, num: bl.num})
		}
		kids, err := r.entries(bl.indent + 2)
		if err != nil {
			return nil, err
		}
		if len(kids) == 1 && kids[0].kind == mScalar {
			// `- alpha` is a scalar element, not a one-member object: the
			// element IS the value, and `=value` is its address (§4.2).
			e.inline = kids[0].inline
		} else {
			e.children = kids
		}

	default:
		return r.memberOrValue(e, bl, text)
	}
	return e, checkSubtreeMargins(e)
}

// memberOrValue reads the three remaining line shapes: a member, an HCL
// block header, and a bare value.
func (r *bodyReader) memberOrValue(e *mirrorEntry, bl bodyLine, text string) (*mirrorEntry, error) {
	if key, val, ok := splitMember(text); ok {
		e.kind, e.key = mKV, key
		if err := r.value(e, val, bl); err != nil {
			return nil, err
		}
		return e, checkSubtreeMargins(e)
	}
	if labels, open, ok := blockHeader(text); ok {
		e.kind, e.labels = mBlock, labels
		if open {
			kids, err := r.children(bl)
			if err != nil {
				return nil, err
			}
			e.children = kids
		}
		if len(e.children) == 0 {
			// `features {}`, or a block whose body the author left empty:
			// the block exists and has no members (§8.5).
			v, err := parseMirrorValue("{}", bl.num)
			if err != nil {
				return nil, err
			}
			e.inline = v
		}
		return e, checkSubtreeMargins(e)
	}
	v, err := parseMirrorValue(text, bl.num)
	if err != nil {
		return nil, err
	}
	e.kind, e.inline = mScalar, v
	return e, nil
}

// value fills a member entry from the text after its separator: an inline
// value, or the indented subtree the separator introduces.
func (r *bodyReader) value(e *mirrorEntry, val string, bl bodyLine) error {
	if val != "" && val != "{" && val != "[" {
		v, err := parseMirrorValue(val, bl.num)
		if err != nil {
			return err
		}
		e.inline = v
		return nil
	}
	kids, err := r.children(bl)
	if err != nil {
		return err
	}
	if len(kids) > 0 {
		e.children = kids
		return nil
	}
	// `key:` with nothing under it is the null value, as YAML reads it;
	// `key: {` with nothing under it is the empty container.
	v, err := parseMirrorValue(emptyFor(val), bl.num)
	if err != nil {
		return err
	}
	e.inline = v
	return nil
}

func emptyFor(opener string) string {
	switch opener {
	case "{":
		return "{}"
	case "[":
		return "[]"
	}
	return "null"
}

// children reads the lines indented under bl, then consumes a closing
// delimiter written at bl's own indentation (`}` in JSON and HCL, §8.1/§8.5).
func (r *bodyReader) children(bl bodyLine) ([]*mirrorEntry, error) {
	var kids []*mirrorEntry
	if r.i < len(r.lines) && r.lines[r.i].indent > bl.indent {
		var err error
		kids, err = r.entries(r.lines[r.i].indent)
		if err != nil {
			return nil, err
		}
	}
	if r.i < len(r.lines) && r.lines[r.i].indent == bl.indent && closerText(trimSeparator(r.lines[r.i].text)) {
		r.i++
	}
	return kids, nil
}

// insert splices a synthetic line at the read position, for a sequence
// element whose first member shares the dash's line.
func (r *bodyReader) insert(bl bodyLine) {
	tail := append([]bodyLine{bl}, r.lines[r.i:]...)
	r.lines = append(append([]bodyLine(nil), r.lines[:r.i]...), tail...)
}

// checkSubtreeMargins enforces that an added or removed container owns its
// whole subtree: a `+` block holding a `-` line is not a shape the two
// projections can express (§5).
func checkSubtreeMargins(e *mirrorEntry) error {
	if e.margin == marginContext {
		return nil
	}
	for _, c := range e.children {
		if c.margin != e.margin {
			return parseErr(c.line, "", "mixed margins inside a %q container: %q where %q was expected (§5)",
				string(e.margin), string(c.margin), string(e.margin))
		}
		if err := checkSubtreeMargins(c); err != nil {
			return err
		}
	}
	return nil
}

// trimSeparator drops one trailing separating comma: in the mirror grammar
// "trailing and separating commas are optional and ignored" (§8.1).
func trimSeparator(text string) string {
	return strings.TrimRight(strings.TrimSuffix(strings.TrimRight(text, " "), ","), " ")
}

func commentText(text string) bool {
	return strings.HasPrefix(text, "//") || text == "#" || strings.HasPrefix(text, "# ")
}

// commentBody strips the comment marker and one leading space, which is what
// comment equality compares (§6.1).
func commentBody(text string) string {
	body := strings.TrimPrefix(text, "//")
	if body == text {
		body = strings.TrimPrefix(text, "#")
	}
	return strings.TrimPrefix(body, " ")
}

func closerText(text string) bool {
	switch text {
	case "}", "]", ")":
		return true
	}
	return false
}

func tableHeader(text string) bool {
	return len(text) > 2 && strings.HasPrefix(text, "[") && strings.HasSuffix(text, "]") &&
		!strings.ContainsAny(text, " \t")
}

// parseTableHeader reads `[a.b]` or `[[a.b]]` into its dotted components
// (§8.4).
func parseTableHeader(text string, line int) ([]string, bool, error) {
	inner := text[1 : len(text)-1]
	array := false
	if strings.HasPrefix(inner, "[") && strings.HasSuffix(inner, "]") {
		array = true
		inner = inner[1 : len(inner)-1]
	}
	if inner == "" {
		return nil, false, parseErr(line, "", "empty table header %q (§8.4)", text)
	}
	parts := strings.Split(inner, ".")
	for i, part := range parts {
		if part == "" {
			return nil, false, parseErr(line, "", "empty component in table header %q (§8.4)", text)
		}
		parts[i] = unquoteAttr(part)
	}
	return parts, array, nil
}

// splitMember finds a member's key/value separator: the first top-level `:`
// followed by a space or end of line, or the first top-level `=`. Everything
// inside quotes, braces, brackets or parentheses is opaque, so
// `{ "name": "x" }` is a value and not a member (§8.1, §8.5).
func splitMember(text string) (key, val string, ok bool) {
	i, ok := memberSep(text)
	if !ok {
		return "", "", false
	}
	key = strings.TrimRight(text[:i], " ")
	val = strings.TrimLeft(text[i+1:], " ")
	if key == "" {
		return "", "", false
	}
	if len(key) >= 2 && key[0] == '"' && key[len(key)-1] == '"' {
		return unquoteAttr(key), val, true
	}
	if strings.ContainsAny(key, " \t\"") {
		return "", "", false // an HCL block header, or Markdown prose
	}
	return key, val, true
}

func memberSep(text string) (int, bool) {
	depth, inQuote := 0, false
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case inQuote:
			if c == '\\' {
				i++
			} else if c == '"' {
				inQuote = false
			}
		case c == '"':
			inQuote = true
		case c == '{' || c == '[' || c == '(':
			depth++
		case c == '}' || c == ']' || c == ')':
			depth--
		case depth != 0:
		case c == ':':
			if i+1 == len(text) || text[i+1] == ' ' {
				return i, true
			}
		case c == '=':
			// Not a separator when it is part of a comparison or an arrow:
			// HCL expressions carry those, and the key would be nonsense.
			if i+1 < len(text) && (text[i+1] == '=' || text[i+1] == '>') {
				i++
				continue
			}
			if i > 0 && strings.IndexByte("=!<>~", text[i-1]) >= 0 {
				continue
			}
			return i, true
		}
	}
	return 0, false
}

// blockHeader reads an HCL block header — `type "label"… {` — into its type
// and labels (§8.5). `features {}` is a block with an empty body.
func blockHeader(text string) ([]string, bool, bool) {
	body, open := strings.CutSuffix(text, "{")
	if !open {
		var empty bool
		if body, empty = strings.CutSuffix(text, "{}"); !empty {
			return nil, false, false
		}
	}
	toks, err := splitTokens(body)
	if err != nil || len(toks) == 0 || strings.Contains(toks[0], `"`) {
		return nil, false, false
	}
	for i, tok := range toks {
		toks[i] = unquoteAttr(tok)
	}
	return toks, open, true
}

// parseMirrorValue decodes a body line's value text. YAML's scalar
// resolution is the format-native decoding §4.2 asks for, and it reads the
// JSON, TOML and HCL scalar and flow-collection spellings unchanged.
func parseMirrorValue(text string, line int) (Value, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return Value{}, parseErr(line, "", "cannot read %q as a value (§5): %v", text, err)
	}
	if len(doc.Content) != 1 {
		return Value{}, parseErr(line, "", "cannot read %q as a single value (§5)", text)
	}
	n := cloneNode(doc.Content[0])
	normalizeValueStyle(n, false)
	return NodeValue(n), nil
}

// normalizeValueStyle strips presentation that is not part of a value:
// quoting style is never matched (§6.3), so every scalar's goes. A
// collection's flow/block spelling is kept — it is the rendering a backend
// adopts for a node it creates (§6.3) — except at the very top of a
// transform's value, whose shape is the .hewt document's to choose. The
// corpus pins both halves: json/keyed-array-add's inline element is written
// out as a block mapping, while toml/surface-directive-table's nested
// `args: [mcp]` stays flow.
func normalizeValueStyle(n *yaml.Node, root bool) {
	if n.Kind == yaml.ScalarNode || root {
		n.Style = 0
	}
	n.HeadComment, n.LineComment, n.FootComment = "", "", ""
	for _, c := range n.Content {
		normalizeValueStyle(c, false)
	}
}

// topValue prepares a value for the top of a transform record: the .hewt
// document chooses that node's shape, so its own style is dropped while
// nested collections keep theirs.
func topValue(v Value) Value {
	if v.IsZero() {
		return v
	}
	n := cloneNode(v.Node())
	n.Style = 0
	return NodeValue(n)
}

// commentValue is a comment node's value: the IR carries comments as
// `{comment: <text>}` at a comment address (§4.5b, §11.10 reduction 3).
func commentValue(text string) Value {
	m := mapNode()
	put(m, "comment", strNode(text))
	return NodeValue(m)
}
