package hew

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/ctxloom/hew/internal/hewerr"
)

// Patch is a parsed .hew document (spec Appendix A.2). It is produced by
// Parse and is fully determined by the patch text: no target was opened, no
// format mechanics consulted.
type Patch struct {
	version int
	files   []*FileSection
}

// Version is the notation version the preamble declared (§2.1). It is always
// Version for a patch this build accepted.
func (p *Patch) Version() int { return p.version }

// Files returns the document's file sections in source order. Two sections
// may name the same target (§2.2); merging them is the caller's business.
func (p *Patch) Files() []*FileSection { return append([]*FileSection(nil), p.files...) }

// FileSection is one `--- target [attrs]` section (§2.2) with the hunks that
// follow it.
type FileSection struct {
	target string
	format FormatID
	line   int
	hunks  []*Hunk
}

// Target is the target path exactly as the `--- ` line spelled it. The parser
// neither resolves it against an apply root nor checks traversal — HEW003 is
// the resolver's (§10, §2.2).
func (f *FileSection) Target() string { return f.target }

// Format is the section's declared format: the `format=` attribute, else the
// preamble's `format:`, else "" for extension inference (§2.2, §8.0).
func (f *FileSection) Format() FormatID { return f.format }

// Line is the 1-based line number of the `--- ` target line.
func (f *FileSection) Line() int { return f.line }

// Transforms is the parser's product: the abstract IR for this file section
// (§9.2). Context lines and `-` lines have already become OpTest records here
// — loud staleness is established at parse time, not at apply time (§9.0).
func (f *FileSection) Transforms() TransformList {
	tl := TransformList{Target: f.target, Format: f.format}
	for _, h := range f.hunks {
		tl.Transform = append(tl.Transform, h.transforms...)
	}
	return tl
}

// Hunks is retained for diagnostics and linting only. The applier never sees
// it (Appendix A.2).
func (f *FileSection) Hunks() []*Hunk { return append([]*Hunk(nil), f.hunks...) }

// Hunk is one `@@ anchor @@` block (§2.3).
type Hunk struct {
	anchor     Path
	line       int
	assertOnly bool
	transforms []Transform
}

// Anchor is the hunk's anchor path, carrying any `! match ord=` selector the
// hunk attached to it (§7.2, §9.1 step 1).
func (h *Hunk) Anchor() Path { return h.anchor }

// Line is the 1-based line number of the `@@` header.
func (h *Hunk) Line() int { return h.line }

// AssertOnly reports a hunk with no `+` or `-` body lines: it changes
// nothing and contributes only test transforms (§7.4).
func (h *Hunk) AssertOnly() bool { return h.assertOnly }

// Transforms returns the transforms this hunk lowered to, in order.
func (h *Hunk) Transforms() []Transform { return append([]Transform(nil), h.transforms...) }

// The margin characters of §3. Column 1 is the margin, column 2 a mandatory
// single space, and body text begins at column 3.
const (
	marginContext = ' '
	marginRemove  = '-'
	marginAdd     = '+'
	marginAssert  = '?'
	marginDirect  = '!'
	marginComment = '#'
)

// Preamble keys (§2.1). No others are defined in v0; an unknown key is
// HEW001 rather than a forward-compatible shrug (O9).
const (
	keyHew           = "hew"
	keyPreFormat     = "format"
	keyPreIdempotent = "idempotent"
)

const (
	targetPrefix = "--- "
	hunkPrefix   = "@@ "
	hunkSuffix   = " @@"
)

// Parse reads a hew patch document (§2). It performs NO I/O, opens no target,
// and knows no format mechanics: its output is fully determined by the patch
// text (§9).
//
// Errors are HEW001 parse-error at the parser component, carrying the patch
// line, except an unknown `hew:` version, which is HEW002 (§10).
func Parse(src []byte) (*Patch, error) {
	p := &parser{lines: splitLines(src)}
	return p.document()
}

type srcLine struct {
	text string
	num  int
}

func splitLines(src []byte) []srcLine {
	body := string(src)
	if strings.HasSuffix(body, "\n") {
		body = body[:len(body)-1]
	}
	if body == "" && len(src) == 0 {
		return nil
	}
	parts := strings.Split(body, "\n")
	out := make([]srcLine, len(parts))
	for i, t := range parts {
		out[i] = srcLine{text: t, num: i + 1}
	}
	return out
}

type parser struct {
	lines []srcLine
	i     int

	version    int
	defFormat  FormatID
	filePragma bool // preamble `idempotent: true` (§2.1, ruling O3)
}

func parseErr(line int, path, format string, args ...any) error {
	return &hewerr.Error{
		Code:      hewerr.CodeParse,
		Component: hewerr.ComponentParser,
		Path:      path,
		PatchLine: line,
		Detail:    fmt.Sprintf(format, args...),
	}
}

func assertErr(line int, path, format string, args ...any) error {
	return &hewerr.Error{
		Code:      hewerr.CodeAssertionFailed,
		Component: hewerr.ComponentParser,
		Path:      path,
		PatchLine: line,
		Detail:    fmt.Sprintf(format, args...),
	}
}

// blank reports a line that is insignificant everywhere: empty, or whitespace
// only (§3).
func blank(text string) bool { return strings.TrimSpace(text) == "" }

// hewComment reports a `#` comment line of the file grammar (§2): the margin
// character alone, or followed by a space. `#foo` is not a comment — the
// margin's mandatory space (§3) is what makes the distinction decidable.
func hewComment(text string) bool {
	return text == string(marginComment) || strings.HasPrefix(text, "# ")
}

func (p *parser) document() (*Patch, error) {
	if err := p.preamble(); err != nil {
		return nil, err
	}
	patch := &Patch{version: p.version}
	for p.i < len(p.lines) {
		ln := p.lines[p.i]
		switch {
		case blank(ln.text) || hewComment(ln.text):
			p.i++
		case isTargetLine(ln.text):
			f, err := p.fileSection()
			if err != nil {
				return nil, err
			}
			patch.files = append(patch.files, f)
		default:
			return nil, parseErr(ln.num, "", "expected a %q target line (§2.2), got %q", targetPrefix, ln.text)
		}
	}
	if len(patch.files) == 0 {
		return nil, parseErr(0, "", "the patch has no hunks; an empty patch is refused (§10.2)")
	}
	return patch, nil
}

// preamble reads the directives before the first file section (§2.1).
func (p *parser) preamble() error {
	seen := map[string]bool{}
	for p.i < len(p.lines) {
		ln := p.lines[p.i]
		if blank(ln.text) || hewComment(ln.text) {
			p.i++
			continue
		}
		if isTargetLine(ln.text) {
			break
		}
		key, val, ok := strings.Cut(ln.text, ":")
		if !ok {
			return parseErr(ln.num, "", "expected a preamble directive %q or a %q target line (§2), got %q", "key: value", targetPrefix, ln.text)
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if seen[key] {
			return parseErr(ln.num, "", "duplicate preamble key %q", key)
		}
		seen[key] = true
		if p.version == 0 && key != keyHew {
			return parseErr(ln.num, "", "%q must be the first significant line of a hew file (§2.1)", "hew: 1")
		}
		switch key {
		case keyHew:
			n, err := strconv.Atoi(val)
			if err != nil || n != Version {
				return &hewerr.Error{
					Code:      hewerr.CodeTargetParse,
					Component: hewerr.ComponentParser,
					PatchLine: ln.num,
					Detail:    fmt.Sprintf("unknown hew version %q; this reader implements %d (§2.1)", val, Version),
				}
			}
			p.version = n
		case keyPreFormat:
			p.defFormat = FormatID(val)
		case keyPreIdempotent:
			b, err := strconv.ParseBool(val)
			if err != nil {
				return parseErr(ln.num, "", "%s: expected a boolean, got %q (§2.1)", keyPreIdempotent, val)
			}
			p.filePragma = b
		default:
			return parseErr(ln.num, "", "unknown preamble key %q; v0 defines %q, %q and %q (§2.1)",
				key, keyHew, keyPreFormat, keyPreIdempotent)
		}
		p.i++
	}
	if p.version == 0 {
		return parseErr(0, "", "missing %q; it is required and must come first (§2.1)", "hew: 1")
	}
	return nil
}

// isTargetLine reports a `--- ` file-section line. Three dashes and a space
// can never be a removal line, whose text would have to begin `-- ` (§2.2).
func isTargetLine(text string) bool {
	return strings.HasPrefix(text, targetPrefix) || text == strings.TrimSpace(targetPrefix)
}

func (p *parser) fileSection() (*FileSection, error) {
	ln := p.lines[p.i]
	p.i++
	f := &FileSection{line: ln.num, format: p.defFormat}
	target, attrs, err := splitTargetLine(strings.TrimPrefix(ln.text, strings.TrimSpace(targetPrefix)))
	if err != nil {
		return nil, parseErr(ln.num, "", "%v", err)
	}
	if target == "" {
		return nil, parseErr(ln.num, "", "target line has no path (§2.2)")
	}
	f.target = target
	for _, a := range attrs {
		key, val, ok := strings.Cut(a, "=")
		if !ok {
			return nil, parseErr(ln.num, "", "target attribute %q is not key=value (§2.2)", a)
		}
		switch key {
		case "format":
			f.format = FormatID(unquoteAttr(val))
		default:
			return nil, parseErr(ln.num, "", "unknown target attribute %q; v0 defines %q (§2.2)", key, "format")
		}
	}

	for p.i < len(p.lines) {
		next := p.lines[p.i]
		switch {
		case blank(next.text) || hewComment(next.text):
			p.i++
		case isTargetLine(next.text):
			return p.finishSection(f)
		case strings.HasPrefix(next.text, hunkPrefix):
			h, err := p.hunk(f)
			if err != nil {
				return nil, err
			}
			f.hunks = append(f.hunks, h)
		default:
			return nil, parseErr(next.num, "", "expected a %q hunk header inside a file section (§2.3), got %q", "@@", next.text)
		}
	}
	return p.finishSection(f)
}

func (p *parser) finishSection(f *FileSection) (*FileSection, error) {
	if len(f.hunks) == 0 {
		return nil, parseErr(f.line, "", "file section for %s has no hunks; an empty patch is refused (§10.2)", f.target)
	}
	if err := f.Transforms().Validate(); err != nil {
		if he, ok := hewerr.As(err); ok && he.PatchLine == 0 {
			he.PatchLine = f.line
		}
		return nil, err
	}
	return f, nil
}

// splitTargetLine splits a target line's tail into its path and attributes. A
// path containing a space must be quoted (§2.2).
func splitTargetLine(rest string) (string, []string, error) {
	toks, err := splitTokens(rest)
	if err != nil {
		return "", nil, err
	}
	if len(toks) == 0 {
		return "", nil, nil
	}
	return unquoteAttr(toks[0]), toks[1:], nil
}

func unquoteAttr(tok string) string {
	if len(tok) >= 2 && tok[0] == '"' && tok[len(tok)-1] == '"' {
		return strings.ReplaceAll(tok[1:len(tok)-1], `\"`, `"`)
	}
	return tok
}

// splitTokens splits on whitespace, keeping double-quoted runs and bracketed
// runs (`label=["a", "b"]`) whole.
func splitTokens(s string) ([]string, error) {
	var toks []string
	var cur strings.Builder
	inQuote, depth := false, 0
	flush := func() {
		if cur.Len() > 0 {
			toks = append(toks, cur.String())
			cur.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inQuote:
			cur.WriteByte(c)
			if c == '\\' && i+1 < len(s) {
				i++
				cur.WriteByte(s[i])
				continue
			}
			if c == '"' {
				inQuote = false
			}
		case c == '"':
			inQuote = true
			cur.WriteByte(c)
		case c == '[' || c == '{':
			depth++
			cur.WriteByte(c)
		case c == ']' || c == '}':
			depth--
			if depth < 0 {
				return nil, fmt.Errorf("unbalanced %q", string(c))
			}
			cur.WriteByte(c)
		case (c == ' ' || c == '\t') && depth == 0:
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	if inQuote {
		return nil, fmt.Errorf("unterminated quoted token")
	}
	if depth != 0 {
		return nil, fmt.Errorf("unbalanced brackets")
	}
	flush()
	return toks, nil
}

func (p *parser) hunk(f *FileSection) (*Hunk, error) {
	ln := p.lines[p.i]
	p.i++
	inner, attrs, err := splitHunkHeader(ln.text)
	if err != nil {
		return nil, parseErr(ln.num, "", "%v", err)
	}
	if len(attrs) > 0 {
		return nil, parseErr(ln.num, "", "unknown hunk attribute %q; v0 defines none (§2.3)", attrs[0])
	}
	anchor, err := ParseAuthoredPath(inner)
	if err != nil {
		if he, ok := hewerr.As(err); ok {
			he.PatchLine = ln.num
		}
		return nil, err
	}
	h := &Hunk{anchor: anchor, line: ln.num}

	body, err := p.hunkBody()
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, parseErr(ln.num, anchor.String(), "hunk has no body lines (§2.3)")
	}
	if err := h.lower(body, p.filePragma); err != nil {
		return nil, err
	}
	return h, nil
}

// splitHunkHeader splits `@@ address @@ [attrs]` into its address and
// attribute tokens (§2.3).
func splitHunkHeader(text string) (string, []string, error) {
	rest := strings.TrimPrefix(text, hunkPrefix)
	end := strings.Index(rest, hunkSuffix)
	if end < 0 {
		return "", nil, fmt.Errorf("hunk header is not closed with %q (§2.3)", hunkSuffix)
	}
	inner := rest[:end]
	attrs, err := splitTokens(rest[end+len(hunkSuffix):])
	if err != nil {
		return "", nil, err
	}
	return inner, attrs, nil
}

// hunkBody collects the body lines up to the next hunk header, target line,
// or end of file, classifying each margin (§3). Blank lines and hew comments
// are dropped: they are insignificant and are in neither projection (§5).
func (p *parser) hunkBody() ([]bodyLine, error) {
	var out []bodyLine
	for p.i < len(p.lines) {
		ln := p.lines[p.i]
		if isTargetLine(ln.text) || strings.HasPrefix(ln.text, hunkPrefix) {
			break
		}
		p.i++
		if blank(ln.text) || hewComment(ln.text) {
			continue
		}
		bl, err := classify(ln)
		if err != nil {
			return nil, err
		}
		out = append(out, bl)
	}
	return out, nil
}

// bodyLine is one classified body line: its margin, the indentation of its
// text relative to column 3, and the text itself.
type bodyLine struct {
	margin byte
	indent int
	text   string
	num    int
}

func classify(ln srcLine) (bodyLine, error) {
	m := ln.text[0]
	switch m {
	case marginContext, marginRemove, marginAdd, marginAssert, marginDirect, marginComment:
	default:
		return bodyLine{}, parseErr(ln.num, "",
			"%q is not a margin character; column 1 must be one of \" -+?!#\" (§3)", string(m))
	}
	if len(ln.text) > 1 && ln.text[1] != ' ' {
		return bodyLine{}, parseErr(ln.num, "",
			"column 2 must be a single space after the %q margin (§3), got %q", string(m), ln.text)
	}
	text := ""
	if len(ln.text) > 2 {
		text = strings.TrimRight(ln.text[2:], " \t")
	}
	indent := len(text) - len(strings.TrimLeft(text, " "))
	return bodyLine{margin: m, indent: indent, text: text[indent:], num: ln.num}, nil
}

// annotation is a `?` or `!` line split into its verb and the rest of the
// line (§7). The rest is kept verbatim: an assertion's value may contain
// spaces, so only the verb can be tokenized here.
type annotation struct {
	verb   string
	rest   string
	line   int
	margin byte
}

func parseAnnotation(bl bodyLine) (annotation, error) {
	verb, rest := cutToken(bl.text)
	if verb == "" {
		return annotation{}, parseErr(bl.num, "", "annotation line carries no directive (§7)")
	}
	return annotation{verb: verb, rest: rest, line: bl.num, margin: bl.margin}, nil
}
