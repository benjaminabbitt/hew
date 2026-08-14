package hew

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

// Parse reads a hew patch document (§2-§7) and lowers it directly to the
// abstract IR, one TransformList per file section (§2.2 "--- " line). It
// performs NO I/O and opens no target: every Transform it emits — including
// the `test` records context and `-` lines compile into (§9.0) — is fully
// determined by the patch text.
//
// The document grammar lives here; the body of a hunk is read as a mirror
// fragment (mirror.go) and lowered by §9.1's algorithm (lower.go), with §7's
// annotations attached in between (annot.go).
//
// Divergence from Appendix A.2, flagged in the P2 report: Appendix A.2
// proposes Parse returning a *Patch with Files()/Hunks() introspection for
// diagnostics and linting. This implementation returns the already-lowered
// []TransformList directly — the shape every consumer in scope (the corpus
// harness's ParseToHewt hook, the CLI) actually needs — and does not keep a
// retained Hunk/annotation tree for tooling that does not exist yet.
func Parse(src []byte) ([]TransformList, error) {
	p := &parser{lines: splitLines(src)}
	return p.document()
}

// ParseSingle is Parse for a patch with exactly one file section; it fails
// loudly if the patch declares zero or more than one, rather than silently
// picking one — Appendix B.1's --target flag requires exactly this.
func ParseSingle(src []byte) (TransformList, error) {
	tls, err := Parse(src)
	if err != nil {
		return TransformList{}, err
	}
	if len(tls) != 1 {
		return TransformList{}, parseErr(0, "", "patch declares %d file sections, want exactly 1", len(tls))
	}
	return tls[0], nil
}

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

type srcLine struct {
	text string
	num  int
}

func splitLines(src []byte) []srcLine {
	body := strings.TrimSuffix(string(src), "\n") // the trailing newline is optional (§2)
	if body == "" {
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

func formatErr(line int, target, format string) error {
	return &hewerr.Error{
		Code:      hewerr.CodeUnsupportedFormat,
		Component: hewerr.ComponentParser,
		Target:    target,
		PatchLine: line,
		Detail:    fmt.Sprintf("unknown format %q", format),
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

func (p *parser) document() ([]TransformList, error) {
	if err := p.preamble(); err != nil {
		return nil, err
	}
	var out []TransformList
	for p.i < len(p.lines) {
		ln := p.lines[p.i]
		switch {
		case blank(ln.text) || hewComment(ln.text):
			p.i++
		case isTargetLine(ln.text):
			tl, err := p.fileSection()
			if err != nil {
				return nil, err
			}
			out = append(out, tl)
		default:
			return nil, parseErr(ln.num, "", "expected a %q target line (§2.2), got %q", targetPrefix, ln.text)
		}
	}
	if len(out) == 0 {
		return nil, parseErr(0, "", "the patch has no hunks; an empty patch is refused (§10.2)")
	}
	return out, nil
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
			// "A reader seeing an unknown version MUST fail with HEW002
			// rather than attempt a best-effort parse" (§2.1, §10's table).
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
			if !p.defFormat.Valid() {
				return formatErr(ln.num, "", val)
			}
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

func (p *parser) fileSection() (TransformList, error) {
	ln := p.lines[p.i]
	p.i++
	tl := TransformList{Format: p.defFormat}
	target, attrs, err := splitTargetLine(strings.TrimPrefix(ln.text, strings.TrimSpace(targetPrefix)))
	if err != nil {
		return TransformList{}, parseErr(ln.num, "", "%v", err)
	}
	if target == "" {
		return TransformList{}, parseErr(ln.num, "", "target line has no path (§2.2)")
	}
	tl.Target = target
	for _, a := range attrs {
		key, val, ok := strings.Cut(a, "=")
		if !ok {
			return TransformList{}, parseErr(ln.num, "", "target attribute %q is not key=value (§2.2)", a)
		}
		switch key {
		case "format":
			tl.Format = FormatID(unquoteAttr(val))
			if !tl.Format.Valid() {
				return TransformList{}, formatErr(ln.num, target, string(tl.Format))
			}
		default:
			return TransformList{}, parseErr(ln.num, "", "unknown target attribute %q; v0 defines %q (§2.2)", key, "format")
		}
	}

	for p.i < len(p.lines) {
		next := p.lines[p.i]
		switch {
		case blank(next.text) || hewComment(next.text):
			p.i++
		case isTargetLine(next.text):
			return finishSection(tl, ln.num)
		case strings.HasPrefix(next.text, hunkPrefix):
			ts, err := p.hunk()
			if err != nil {
				return TransformList{}, err
			}
			tl.Transform = append(tl.Transform, ts...)
		default:
			return TransformList{}, parseErr(next.num, "", "expected a %q hunk header inside a file section (§2.3), got %q", "@@", next.text)
		}
	}
	return finishSection(tl, ln.num)
}

// finishSection refuses a section that contributed nothing and validates the
// records it did contribute, so an invalid combination of fields is HEW001 at
// the parser rather than a surprise inside a backend (§9.6, §10.2).
func finishSection(tl TransformList, line int) (TransformList, error) {
	if len(tl.Transform) == 0 {
		return TransformList{}, parseErr(line, tl.Target, "file section for %s has no hunks; an empty patch is refused (§10.2)", tl.Target)
	}
	if err := tl.Validate(); err != nil {
		if he, ok := hewerr.As(err); ok && he.PatchLine == 0 {
			he.PatchLine = line
		}
		return TransformList{}, err
	}
	return tl, nil
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

// hunk parses one `@@ anchor @@` block and lowers it (§2.3, §9.1).
func (p *parser) hunk() ([]Transform, error) {
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

	body, err := p.hunkBody()
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, parseErr(ln.num, anchor.String(), "hunk has no body lines (§2.3)")
	}
	return lowerHunk(anchor, ln.num, body, p.filePragma)
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
