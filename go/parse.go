package hew

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/hew-format/hew/internal/hewerr"
	"gopkg.in/yaml.v3"
)

// Parse reads a hew patch document (§2-§7) and lowers it directly to the
// abstract IR, one TransformList per file section (§2.2 "--- " line). It
// performs NO I/O and opens no target: every Transform it emits — including
// the `test` records context and `-` lines compile into (§9.0) — is fully
// determined by the patch text.
//
// Divergence from Appendix A.2, flagged in the P2 report: Appendix A.2
// proposes Parse returning a *Patch with Files()/Hunks() introspection for
// diagnostics and linting. This implementation returns the already-lowered
// []TransformList directly — the shape every consumer in scope (the corpus
// harness's ParseToHewt hook, the CLI) actually needs — and does not keep a
// retained Hunk/annotation tree for tooling that does not exist yet.
func Parse(src []byte) ([]TransformList, error) {
	lines := splitLines(src)
	p := &parser{lines: lines}
	return p.run()
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

func splitLines(src []byte) []string {
	s := string(src)
	s = strings.TrimSuffix(s, "\n") // trailing newline is optional (§2)
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
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

type parser struct {
	lines []string
	i     int // 0-based index into lines; current line is lines[i]
}

func (p *parser) lineNo() int { return p.i + 1 }
func (p *parser) done() bool  { return p.i >= len(p.lines) }
func (p *parser) peek() string {
	if p.done() {
		return ""
	}
	return p.lines[p.i]
}

func isBlank(s string) bool { return strings.TrimSpace(s) == "" }

func (p *parser) run() ([]TransformList, error) {
	defaultFormat := FormatID("")
	sawHew := false
	filePragmaIdempotent := false

	// Preamble: (comment | blank | directive)*, up to the first "--- " line.
	for !p.done() {
		line := p.peek()
		if strings.HasPrefix(line, "--- ") {
			break
		}
		if isBlank(line) {
			p.i++
			continue
		}
		if strings.HasPrefix(line, "#") {
			p.i++
			continue
		}
		key, val, ok := splitDirective(line)
		if !ok {
			return nil, parseErr(p.lineNo(), "", "malformed preamble line %q (want a comment, blank, or \"key: value\" directive)", line)
		}
		if !sawHew && key != "hew" {
			return nil, parseErr(p.lineNo(), "", `"hew:" must be the first non-comment, non-blank line (§2.1)`)
		}
		switch key {
		case "hew":
			n, err := strconv.Atoi(val)
			if err != nil || n != Version {
				return nil, parseErr(p.lineNo(), "", "unsupported hew version %q, want %d", val, Version)
			}
			sawHew = true
		case "format":
			defaultFormat = FormatID(val)
			if !defaultFormat.Valid() {
				return nil, &hewerr.Error{Code: hewerr.CodeUnsupportedFormat, Component: hewerr.ComponentParser,
					PatchLine: p.lineNo(), Detail: fmt.Sprintf("unknown format %q", val)}
			}
		case "idempotent":
			b, err := strconv.ParseBool(val)
			if err != nil {
				return nil, parseErr(p.lineNo(), "", "idempotent: expected a boolean, got %q", val)
			}
			filePragmaIdempotent = b
		default:
			return nil, parseErr(p.lineNo(), "", "unknown preamble key %q (§2.1)", key)
		}
		p.i++
	}
	if !sawHew {
		return nil, parseErr(p.lineNo(), "", `missing required "hew:" directive (§2.1); an empty patch is refused (§10.2)`)
	}
	if p.done() {
		return nil, parseErr(p.lineNo(), "", "patch has no file sections; an empty patch is refused (§10.2)")
	}

	var out []TransformList
	for !p.done() {
		tl, err := p.parseFileSection(defaultFormat, filePragmaIdempotent)
		if err != nil {
			return nil, err
		}
		out = append(out, tl)
	}
	return out, nil
}

// splitDirective splits a preamble "key: value" line.
func splitDirective(line string) (key, val string, ok bool) {
	i := strings.IndexByte(line, ':')
	if i < 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:i])
	val = strings.TrimSpace(line[i+1:])
	if key == "" {
		return "", "", false
	}
	return key, val, true
}

func (p *parser) parseFileSection(defaultFormat FormatID, filePragmaIdempotent bool) (TransformList, error) {
	line := p.peek()
	target, formatAttr, err := parseTargetLine(line)
	if err != nil {
		return TransformList{}, parseErr(p.lineNo(), "", "%s", err.Error())
	}
	startLine := p.lineNo()
	p.i++

	format := defaultFormat
	if formatAttr != "" {
		format = FormatID(formatAttr)
	}
	if format != "" && !format.Valid() {
		return TransformList{}, &hewerr.Error{Code: hewerr.CodeUnsupportedFormat, Component: hewerr.ComponentParser,
			PatchLine: startLine, Target: target, Detail: fmt.Sprintf("unknown format %q", string(format))}
	}

	tl := TransformList{Target: target, Format: format}
	sawHunk := false
	for !p.done() {
		line := p.peek()
		if strings.HasPrefix(line, "--- ") {
			break // next file section
		}
		if isBlank(line) {
			p.i++
			continue
		}
		if strings.HasPrefix(line, "#") {
			p.i++
			continue
		}
		if !strings.HasPrefix(line, "@@ ") {
			return TransformList{}, parseErr(p.lineNo(), "", "expected a hunk header (\"@@ ... @@\"), a comment, or a new file section, got %q", line)
		}
		hunkTransforms, err := p.parseHunk(filePragmaIdempotent)
		if err != nil {
			return TransformList{}, err
		}
		tl.Transform = append(tl.Transform, hunkTransforms...)
		sawHunk = true
	}
	if !sawHunk {
		return TransformList{}, parseErr(startLine, target, "file section for %q has no hunks; an empty patch is refused (§10.2)", target)
	}
	return tl, nil
}

// parseTargetLine parses "--- path [format=fmt]".
func parseTargetLine(line string) (target, format string, err error) {
	rest := strings.TrimPrefix(line, "--- ")
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return "", "", fmt.Errorf("target line has no path (§2.2)")
	}
	target, rest, err = takeToken(rest)
	if err != nil {
		return "", "", err
	}
	rest = strings.TrimSpace(rest)
	for rest != "" {
		var attr string
		attr, rest, err = takeToken(rest)
		if err != nil {
			return "", "", err
		}
		rest = strings.TrimSpace(rest)
		k, v, ok := strings.Cut(attr, "=")
		if !ok {
			return "", "", fmt.Errorf("malformed target attribute %q", attr)
		}
		switch k {
		case "format":
			format = v
		default:
			return "", "", fmt.Errorf("unknown target attribute %q", k)
		}
	}
	return target, format, nil
}

// takeToken reads one whitespace-separated token from s, honoring a leading
// double-quote as a quoted token (for a target path containing a space,
// §2.2). It returns the (unquoted) token and the remainder of s.
func takeToken(s string) (tok, rest string, err error) {
	if strings.HasPrefix(s, `"`) {
		end := strings.IndexByte(s[1:], '"')
		if end < 0 {
			return "", "", fmt.Errorf("unterminated quoted token in %q", s)
		}
		return s[1 : 1+end], s[1+end+1:], nil
	}
	if i := strings.IndexByte(s, ' '); i >= 0 {
		return s[:i], s[i+1:], nil
	}
	return s, "", nil
}

// bodyLine is one classified line of a hunk body.
type bodyLine struct {
	margin byte // ' ', '-', '+', '?', '!'
	text   string
	line   int
}

// parseHunk parses one hunk (header + body lines) and lowers it to a
// []Transform (§9.1's lowering algorithm).
func (p *parser) parseHunk(filePragmaIdempotent bool) ([]Transform, error) {
	header := p.peek()
	headerLine := p.lineNo()
	anchorText, err := parseHunkHeader(header)
	if err != nil {
		return nil, parseErr(headerLine, "", "%s", err.Error())
	}
	anchor, err := ParseAuthoredPath(anchorText)
	if err != nil {
		return nil, err
	}
	p.i++

	var body []bodyLine
	for !p.done() {
		line := p.peek()
		if strings.HasPrefix(line, "@@ ") || strings.HasPrefix(line, "--- ") {
			break
		}
		if isBlank(line) {
			p.i++
			continue
		}
		if strings.HasPrefix(line, "#") {
			p.i++ // hew comment margin: ignored entirely (§3)
			continue
		}
		if len(line) < 2 || line[1] != ' ' {
			return nil, parseErr(p.lineNo(), "", "line has no valid margin in column 1 (§3): %q", line)
		}
		margin := line[0]
		switch margin {
		case ' ', '-', '+', '?', '!':
		default:
			return nil, parseErr(p.lineNo(), "", "unknown margin %q (§3)", string(margin))
		}
		body = append(body, bodyLine{margin: margin, text: line[2:], line: p.lineNo()})
		p.i++
	}
	if len(body) == 0 {
		return nil, parseErr(headerLine, anchor.String(), "hunk has no body lines")
	}

	return lowerHunk(anchor, headerLine, body, filePragmaIdempotent)
}

// parseHunkHeader parses "@@ " address " @@" [ WSP attrlist ]. The attrlist
// is accepted but unused: no corpus case in P2's scope carries one (the
// examples that need a per-hunk directive spell it as the first body line's
// "! match"/"! idempotent", per §7's attachment table).
func parseHunkHeader(line string) (address string, err error) {
	rest := strings.TrimPrefix(line, "@@ ")
	end := strings.Index(rest, " @@")
	if end < 0 {
		return "", fmt.Errorf("malformed hunk header (want \"@@ <address> @@\"): %q", line)
	}
	return rest[:end], nil
}

// member is one classified direct child of the hunk's anchor: a context,
// "-", or "+" line (mapped to a map field, a sequence element, or a comment
// node), or a free-standing "?" assertion collected separately.
type member struct {
	bl      bodyLine
	seg     Segment     // the direct-child address relative to anchor
	fields  []fieldTest // non-nil: a keyed/subset-matched element's listed fields
	value   Value       // the member's own value (scalar/opaque; ADD's Value)
	isField bool        // true when seg addresses a map field or comment node directly (ADD path = anchor+seg, not anchor)
	dir     directives  // the `!` qualifiers in force for this line (§9.1 step 6)
}

// directives is the annotation state that rides a transform as a qualifier
// (§9.1 step 6: "`!` directives emit no transform of their own"). It is
// carried per member so a line-scoped `!` line affects exactly the body line
// it precedes, and a hunk-scoped one (the first body line, §7) affects them
// all.
type directives struct {
	// testIdempotent tolerates a failed before-image assert when the
	// after-image holds; opIdempotent makes the WRITE converge instead of
	// refusing. They are two flags rather than one because `! strict` opts a
	// hunk's writes back out of a file-level `idempotent:` pragma without
	// retracting the tolerance the pragma already granted the asserts — which
	// is what separates the line the corpus expects in
	// yaml/pragma-strict-override (patch_line 9, the strict WRITE) from the
	// one in yaml/reapply-not-idempotent (patch_line 6, the strict ASSERT).
	testIdempotent bool
	opIdempotent   bool
	optional       bool       // §7.6
	onConflict     OnConflict // §7.7
	anchor         AnchorMode // §8.3
}

// set applies one parsed `!` line to a directive set.
func (d *directives) set(b bangDirective) error {
	switch b.kind {
	case "idempotent":
		d.testIdempotent, d.opIdempotent = true, true
	case "strict":
		d.opIdempotent = false
	case "optional":
		d.optional = true
	case "default":
		d.onConflict = ConflictKeep
	case "upsert":
		d.onConflict = ConflictReplace
	case "anchor":
		m := AnchorMode(b.arg)
		if m != AnchorRewrite && m != AnchorFork {
			return fmt.Errorf("! anchor takes \"rewrite\" or \"fork\" (§8.3), got %q", b.arg)
		}
		d.anchor = m
	case "match":
		// Ordinal selection (§7.2): accepted syntactically. Applying it
		// against a target is the HCL binding's business and is not lowered
		// here.
	case "surface":
		// TOML placement (§8.4): accepted syntactically, lowered by the TOML
		// binding's own slice.
	default:
		return fmt.Errorf("unknown directive %q (§7)", b.kind)
	}
	return nil
}

type fieldTest struct {
	seg   Segment
	value Value
}

// lowerHunk implements §9.1's lowering algorithm for one hunk, plus the
// free-standing `?` assertions (§7.1) and `? exhaustive` (§9.1 step 3).
//
// Scope: the algorithm addresses one level of the anchor's DIRECT children
// (map fields, or sequence elements, classified per §6.1/§6.4.2). A member
// whose value runs across several body lines — a nested block mapping, or a
// keyed sequence element spelled over two lines — is grouped by indentation
// into one member with a nested value (groupBody), not one member per line.
// The value it carries is matched by the format binding under §6.1's subset
// and subsequence rules, so a listed nested field constrains only itself.
func lowerHunk(anchor Path, headerLine int, body []bodyLine, filePragmaIdempotent bool) ([]Transform, error) {
	hunk := directives{testIdempotent: filePragmaIdempotent, opIdempotent: filePragmaIdempotent}
	var pending *directives
	// Comment addresses are kind-scoped ordinals within the container (§4.5b),
	// and the before-image and the after-image are two different documents
	// (§5): a removed comment is #0 of the before-image while the comment
	// replacing it is #0 of the after-image, which is what makes the pair
	// lower to one `replace` rather than a remove plus an add.
	beforeComments, afterComments := 0, 0
	var members []member
	var freeAsserts []Transform
	exhaustive := false
	exhaustiveLine := headerLine

	for _, u := range groupBody(body) {
		bl := u.bl
		switch bl.margin {
		case '!':
			b, err := parseBangDirective(bl.text)
			if err != nil {
				return nil, parseErr(bl.line, "", "%s", err.Error())
			}
			target := &hunk
			if len(members) > 0 {
				if pending == nil {
					line := hunk
					pending = &line
				}
				target = pending
			}
			if err := target.set(b); err != nil {
				return nil, parseErr(bl.line, "", "%s", err.Error())
			}
			continue
		case '?':
			a, err := parseQuestionDirective(bl.text)
			if err != nil {
				return nil, parseErr(bl.line, "", "%s", err.Error())
			}
			if a.kind == "exhaustive" {
				exhaustive = true
				exhaustiveLine = bl.line
				continue
			}
			tr, err := lowerFreeAssertion(anchor, bl.line, a)
			if err != nil {
				return nil, err
			}
			tr.Anchor = hunk.anchor
			freeAsserts = append(freeAsserts, tr)
			continue
		}

		m, err := classifyMember(bl, u.text, &beforeComments, &afterComments)
		if err != nil {
			return nil, err
		}
		m.dir = hunk
		if pending != nil {
			m.dir = *pending
			pending = nil
		}
		members = append(members, m)
	}
	if pending != nil {
		return nil, parseErr(headerLine, anchor.String(),
			"a line-scoped directive must be followed by a body line (§7)")
	}

	var out []Transform

	// Step 2: a test per context/"-" member, body order; a keyed/subset
	// element emits one test per listed field instead of one for the whole
	// element (§9.1 step 2).
	listedCount := 0
	for _, m := range members {
		if m.bl.margin != ' ' && m.bl.margin != '-' {
			continue
		}
		listedCount++
		if len(m.fields) > 0 {
			for _, f := range m.fields {
				out = append(out, m.test(anchor.Append(m.seg, f.seg), f.value))
			}
		} else {
			out = append(out, m.test(anchor.Append(m.seg), m.value))
		}
	}
	out = append(out, freeAsserts...)
	if exhaustive {
		n := listedCount
		out = append(out, Transform{Op: OpTest, Path: anchor, Exhaustive: true, Count: &n,
			PatchLine: exhaustiveLine, Anchor: hunk.anchor})
	}

	// Steps 4-5. Group members into runs bounded by context lines: within a
	// run, a "-" and a "+" sharing the same address (§9.1: "at the same
	// position") become one replace; everything left over is a plain remove
	// or add. Placement for a surviving add is computed by scanning outward
	// from its own position (§6.2): the nearest preceding context-or-"-"
	// member, else the nearest following context member, else append.
	type runSlot struct{ start, end int } // [start,end) indices into members, margin != ' '
	var runs []runSlot
	runStart := -1
	for i, m := range members {
		if m.bl.margin == ' ' {
			if runStart >= 0 {
				runs = append(runs, runSlot{runStart, i})
				runStart = -1
			}
			continue
		}
		if runStart < 0 {
			runStart = i
		}
	}
	if runStart >= 0 {
		runs = append(runs, runSlot{runStart, len(members)})
	}

	for _, r := range runs {
		matched := make([]bool, r.end-r.start)
		for i := r.start; i < r.end; i++ {
			if members[i].bl.margin != '-' {
				continue
			}
			rmKey := members[i].seg.String()
			pair := -1
			for j := r.start; j < r.end; j++ {
				if members[j].bl.margin != '+' || matched[j-r.start] {
					continue
				}
				if members[j].seg.String() == rmKey {
					pair = j
					break
				}
			}
			if pair >= 0 {
				matched[pair-r.start] = true
				add := members[pair]
				out = append(out, Transform{
					Op: OpReplace, Path: anchor.Append(members[i].seg), Value: add.value,
					PatchLine: add.bl.line, Idempotent: add.dir.opIdempotent, Anchor: add.dir.anchor,
				})
			} else {
				out = append(out, Transform{
					Op: OpRemove, Path: anchor.Append(members[i].seg), PatchLine: members[i].bl.line,
					Optional: members[i].dir.optional, Idempotent: members[i].dir.opIdempotent,
					Anchor: members[i].dir.anchor,
				})
			}
		}
		for j := r.start; j < r.end; j++ {
			if members[j].bl.margin != '+' || matched[j-r.start] {
				continue
			}
			add := members[j]
			path := anchor
			if add.isField {
				path = anchor.Append(add.seg)
			}
			tr := Transform{Op: OpAdd, Path: path, Value: add.value, PatchLine: add.bl.line,
				Idempotent: add.dir.opIdempotent, OnConflict: add.dir.onConflict, Anchor: add.dir.anchor}
			if before, after, ok := placement(members, j); ok {
				if after {
					tr.After = anchor.Append(before)
				} else {
					tr.Before = anchor.Append(before)
				}
			}
			out = append(out, tr)
		}
	}

	return out, nil
}

// placement finds the sibling an unmatched add at members[idx] is placed
// relative to (§6.2). after=true means "after"; after=false means "before".
// ok=false means append at the end of the container.
func placement(members []member, idx int) (sib Segment, after bool, ok bool) {
	for j := idx - 1; j >= 0; j-- {
		if members[j].bl.margin == ' ' || members[j].bl.margin == '-' {
			return members[j].seg, true, true
		}
	}
	for j := idx + 1; j < len(members); j++ {
		if members[j].bl.margin == ' ' {
			return members[j].seg, false, true
		}
	}
	return Segment{}, false, false
}

// test builds the before-image assertion a context or "-" line compiles into
// (§9.0), carrying the line's own qualifiers.
func (m member) test(path Path, v Value) Transform {
	return Transform{Op: OpTest, Path: path, Value: v, PatchLine: m.bl.line,
		Optional: m.dir.optional, Idempotent: m.dir.testIdempotent, Anchor: m.dir.anchor}
}

// unit is one body member's lines: the margin line that opens it plus the
// continuation lines indented under it. A block-style value written across
// several body lines is ONE member of the anchor, not one per line — the
// shape yaml/canonical-four-ops (a keyed element spelled over two lines) and
// yaml/keyed-element-inner-add (a nested mapping) both need.
type unit struct {
	bl   bodyLine
	text string // the member's lines, dedented to the opening line's column
}

func groupBody(body []bodyLine) []unit {
	var out []unit
	for i := 0; i < len(body); {
		bl := body[i]
		if bl.margin == '!' || bl.margin == '?' {
			out = append(out, unit{bl: bl, text: bl.text})
			i++
			continue
		}
		base := leadingSpaces(bl.text)
		lines := []string{bl.text[base:]}
		j := i + 1
		for j < len(body) && body[j].margin == bl.margin && leadingSpaces(body[j].text) > base {
			lines = append(lines, body[j].text[base:])
			j++
		}
		out = append(out, unit{bl: bl, text: strings.Join(lines, "\n")})
		i = j
	}
	return out
}

func leadingSpaces(s string) int {
	n := 0
	for n < len(s) && s[n] == ' ' {
		n++
	}
	return n
}

// classifyMember turns one body member (its opening line plus any
// continuation lines, already dedented) into a member: a map-anchor field
// ("key: value", whose value may be a nested block), a comment node
// ("# text"), or a sequence-anchor element (a bare scalar, a flow object, or
// a "- " block element, addressed by content per §4.2's empty-field and
// named-field forms).
func classifyMember(bl bodyLine, text string, beforeComments, afterComments *int) (member, error) {
	if strings.TrimSpace(text) == "" {
		return member{}, parseErr(bl.line, "", "empty body line")
	}
	if strings.HasPrefix(text, "#") {
		txt := strings.TrimPrefix(strings.TrimPrefix(text, "#"), " ")
		ord := beforeComments
		switch bl.margin {
		case '+':
			ord = afterComments
		case ' ':
			*afterComments++ // a context comment is in both images
		}
		seg := Segment{Kind: SegComment, Index: *ord}
		*ord++
		return member{bl: bl, seg: seg, value: CommentValue(txt), isField: true}, nil
	}

	// A "- " line is always a sequence element, even when its remaining text
	// parses as a single "key: value" pair — that shape means a keyed element
	// subset-matched by its one listed field, not a map field of the anchor.
	dashElement := strings.HasPrefix(text, "- ")
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(text), &n); err != nil {
		return member{}, parseErr(bl.line, "", "malformed body value %q: %v", text, err)
	}
	if len(n.Content) == 0 {
		return member{}, parseErr(bl.line, "", "empty body value %q", text)
	}
	val := n.Content[0]
	if dashElement {
		if val.Kind != yaml.SequenceNode || len(val.Content) != 1 {
			return member{}, parseErr(bl.line, "", "body member %q does not denote a single sequence element", text)
		}
		return elementMember(bl, val.Content[0])
	}
	if val.Kind == yaml.MappingNode && len(val.Content) == 2 && !strings.HasPrefix(text, "{") {
		key := val.Content[0].Value
		return member{bl: bl, seg: Segment{Kind: SegKey, Name: key},
			value: canonicalValue(val.Content[1]), isField: true}, nil
	}
	return elementMember(bl, val)
}

// elementMember classifies a sequence element: a keyed/subset-matched object
// (§5.1, §6.4.2) or a bare scalar addressed by content (§4.2).
func elementMember(bl bodyLine, val *yaml.Node) (member, error) {
	if val.Kind == yaml.MappingNode && len(val.Content) >= 2 {
		var fields []fieldTest
		var names []string
		for i := 0; i+1 < len(val.Content); i += 2 {
			name := val.Content[i].Value
			names = append(names, name)
			fields = append(fields, fieldTest{seg: Segment{Kind: SegKey, Name: name}, value: canonicalValue(val.Content[i+1])})
		}
		idName := identityField(names)
		var idScalar Scalar
		for i, name := range names {
			if name == idName {
				idScalar = fields[i].value.pathScalar()
				break
			}
		}
		seg := Segment{Kind: SegMatch, Name: idName, Value: idScalar}
		return member{bl: bl, seg: seg, fields: fields, value: canonicalValue(val), isField: false}, nil
	}
	sc, err := scalarOfNode(val)
	if err != nil {
		return member{}, parseErr(bl.line, "", "%v", err)
	}
	return member{bl: bl, seg: Segment{Kind: SegMatch, Value: sc}, value: canonicalValue(val), isField: false}, nil
}

// identityField picks the sequence-element identity field among a keyed
// context/remove line's listed fields, per the differ's §9.4-R4 preference
// order applied here to the parser too (spec gap, flagged in the P2
// report: §9.1/§7.2 do not state a parser-side rule for which of several
// listed fields becomes the address when more than one is present; R4 is
// stated only for the differ).
func identityField(names []string) string {
	for _, cand := range []string{"name", "id", "key"} {
		for _, n := range names {
			if n == cand {
				return cand
			}
		}
	}
	if len(names) > 0 {
		return names[0]
	}
	return ""
}

// scalarOfNode converts a YAML scalar node into a Scalar for a path
// segment's identity value. Quoted reflects whether the PATH representation
// needs quoting (§4.2's `id="a b"` example: quoted because of the space),
// not the source token's own style — a JSON string is always source-quoted
// (`"github"`) but the segment `/servers/name=github` is written unquoted,
// which is what json/keyed-array-add and json/array-remove-element pin.
// canonicalValue builds a Transform's Value from a parsed body node,
// clearing quoting/flow style so the .hewt serialization is canonical
// rather than an accident of how the body happened to be authored (a JSON
// body always quotes strings; the mirror grammar's own values don't have
// to). Without this, a JSON-sourced "value: localhost" would marshal back
// as the source-quoted "value: \"localhost\"", failing byte comparison
// against a hand-authored transforms.hewt fixture that (correctly) has no
// reason to quote it — json/add-key and json/roundtrip-basic's render seam
// both pin this.
func canonicalValue(n *yaml.Node) Value {
	return NodeValue(resetStyle(cloneNode(n)))
}

func resetStyle(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	n.Style = 0
	for _, c := range n.Content {
		resetStyle(c)
	}
	return n
}

func scalarOfNode(n *yaml.Node) (Scalar, error) {
	if n.Kind != yaml.ScalarNode {
		return Scalar{}, fmt.Errorf("value %q is not addressable: only scalars and flow objects are supported as sequence elements", n.Value)
	}
	quoted := n.ShortTag() == "!!str" && strings.ContainsAny(n.Value, " \t")
	switch n.ShortTag() {
	case "!!bool":
		return Scalar{Kind: ScalarBool, Text: n.Value}, nil
	case "!!null":
		return Scalar{Kind: ScalarNull, Text: n.Value}, nil
	case "!!int", "!!float":
		return Scalar{Kind: ScalarNumber, Text: n.Value}, nil
	default:
		return Scalar{Kind: ScalarString, Text: n.Value, Quoted: quoted}, nil
	}
}

// pathScalar converts a decoded Value into the Scalar a Segment carries, for
// building a SegMatch identity segment out of a keyed element's own field
// value (which was decoded generically via NodeValue, not scalarOfNode).
func (v Value) pathScalar() Scalar {
	n := v.Node()
	if n == nil {
		return Scalar{Kind: ScalarNull, Text: "null"}
	}
	sc, err := scalarOfNode(n)
	if err != nil {
		return Scalar{Kind: ScalarString, Text: v.String()}
	}
	return sc
}

// bangDirective is a parsed "!" line: its keyword and, for the directives
// that take one (`! anchor rewrite`, `! surface table`), its argument.
type bangDirective struct{ kind, arg string }

func parseBangDirective(text string) (bangDirective, error) {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 {
		return bangDirective{}, fmt.Errorf("empty directive")
	}
	b := bangDirective{kind: fields[0]}
	if len(fields) > 1 {
		b.arg = fields[1]
	}
	return b, nil
}

// questionAssertion is a parsed "?" line (§7.1).
type questionAssertion struct {
	kind  string // "expect", "absent", "count", "kind", "exhaustive"
	path  string
	value string // raw text after "="
}

func parseQuestionDirective(text string) (questionAssertion, error) {
	text = strings.TrimSpace(text)
	fields := strings.SplitN(text, " ", 2)
	kind := fields[0]
	rest := ""
	if len(fields) > 1 {
		rest = strings.TrimSpace(fields[1])
	}
	switch kind {
	case "exhaustive":
		return questionAssertion{kind: kind}, nil
	case "absent":
		if rest == "" {
			return questionAssertion{}, fmt.Errorf("? absent requires a path (§7.1)")
		}
		return questionAssertion{kind: kind, path: rest}, nil
	case "expect", "count", "kind":
		p, v, ok := strings.Cut(rest, "=")
		if !ok {
			return questionAssertion{}, fmt.Errorf("? %s requires \"<path> = <value>\" (§7.1)", kind)
		}
		return questionAssertion{kind: kind, path: strings.TrimSpace(p), value: strings.TrimSpace(v)}, nil
	default:
		return questionAssertion{}, fmt.Errorf("unknown assertion %q (§7.1)", kind)
	}
}

func lowerFreeAssertion(anchor Path, line int, a questionAssertion) (Transform, error) {
	path, err := resolveAnnotationPath(anchor, a.path)
	if err != nil {
		return Transform{}, err
	}
	switch a.kind {
	case "absent":
		return Transform{Op: OpTest, Path: path, Absent: true, PatchLine: line}, nil
	case "expect":
		var n yaml.Node
		if err := yaml.Unmarshal([]byte(a.value), &n); err != nil || len(n.Content) == 0 {
			return Transform{}, parseErr(line, path.String(), "? expect: malformed value %q", a.value)
		}
		return Transform{Op: OpTest, Path: path, Value: canonicalValue(n.Content[0]), PatchLine: line}, nil
	case "count":
		i, err := strconv.Atoi(a.value)
		if err != nil {
			return Transform{}, parseErr(line, path.String(), "? count: expected an integer, got %q", a.value)
		}
		return Transform{Op: OpTest, Path: path, Count: &i, PatchLine: line}, nil
	case "kind":
		k := NodeKind(a.value)
		if !k.Valid() {
			return Transform{}, parseErr(line, path.String(), "? kind: unknown kind %q", a.value)
		}
		return Transform{Op: OpTest, Path: path, NodeKind: &k, PatchLine: line}, nil
	}
	return Transform{}, fmt.Errorf("unreachable")
}

// resolveAnnotationPath resolves a "?"-line path, which may be relative to
// the enclosing hunk's anchor (§4.6).
func resolveAnnotationPath(anchor Path, raw string) (Path, error) {
	p, err := ParseAuthoredPath(raw)
	if err != nil {
		return Path{}, err
	}
	if p.IsRelative() {
		return anchor.Append(p.Segments()...), nil
	}
	return p, nil
}
