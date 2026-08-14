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
}

type fieldTest struct {
	seg   Segment
	value Value
}

// lowerHunk implements §9.1's lowering algorithm for one hunk, plus the
// free-standing `?` assertions (§7.1) and `? exhaustive` (§9.1 step 3).
//
// Scope, flagged in the P2 report: the algorithm addresses ONE level of the
// anchor's direct children per hunk (map fields, or sequence elements,
// classified per line per §6.1/§6.4.2). Every JSON-family corpus case, and
// the spec's own worked examples, write deeper edits via a deeper anchor
// rather than nested body indentation, so recursive multi-level fragment
// parsing within a single hunk body is not implemented; a body line whose
// value is itself a nested object (e.g. `tls: {enabled: true}`) is asserted
// by exact value equality rather than recursive subset matching.
func lowerHunk(anchor Path, headerLine int, body []bodyLine, filePragmaIdempotent bool) ([]Transform, error) {
	idempotent := filePragmaIdempotent
	commentOrdinal := 0
	var members []member
	var freeAsserts []Transform
	exhaustive := false
	exhaustiveLine := headerLine

	for idx, bl := range body {
		switch bl.margin {
		case '!':
			d, err := parseBangDirective(bl.text)
			if err != nil {
				return nil, parseErr(bl.line, "", "%s", err.Error())
			}
			switch d.kind {
			case "idempotent":
				if idx == 0 {
					idempotent = true
				}
				// A line-scoped "! idempotent" (not the hunk's first line) is
				// accepted syntactically; per-line override is not exercised
				// by any case in P2's scope and is not implemented further.
			case "strict":
				idempotent = false
			case "match":
				// Ordinal selection (§7.2): accepted syntactically. Applying
				// it against a target is out of scope for the JSON binding
				// this slice ships — no JSON corpus case needs it.
			case "optional", "upsert", "default", "anchor", "surface":
				// Accepted syntactically; per-line semantics beyond the JSON
				// backend's needs are not implemented in P2.
			default:
				return nil, parseErr(bl.line, "", "unknown directive %q (§7)", d.kind)
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
			freeAsserts = append(freeAsserts, tr)
			continue
		}

		m, err := classifyMember(bl, &commentOrdinal)
		if err != nil {
			return nil, err
		}
		members = append(members, m)
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
				out = append(out, Transform{Op: OpTest, Path: anchor.Append(m.seg, f.seg), Value: f.value, PatchLine: m.bl.line})
			}
		} else {
			out = append(out, Transform{Op: OpTest, Path: anchor.Append(m.seg), Value: m.value, PatchLine: m.bl.line})
		}
	}
	out = append(out, freeAsserts...)
	if exhaustive {
		n := listedCount
		out = append(out, Transform{Op: OpTest, Path: anchor, Exhaustive: true, Count: &n, PatchLine: exhaustiveLine})
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
		pairOf := make([]int, r.end-r.start)
		for i := range pairOf {
			pairOf[i] = -1
		}
		for i := r.start; i < r.end; i++ {
			if members[i].bl.margin != '-' {
				continue
			}
			rmKey := members[i].seg.String()
			for j := r.start; j < r.end; j++ {
				if members[j].bl.margin != '+' || matched[j-r.start] {
					continue
				}
				if members[j].seg.String() == rmKey {
					matched[j-r.start] = true
					pairOf[i-r.start] = j
					break
				}
			}
		}
		for i := r.start; i < r.end; i++ {
			if members[i].bl.margin != '-' {
				continue
			}
			if pair := pairOf[i-r.start]; pair >= 0 {
				add := members[pair]
				out = append(out, Transform{
					Op: OpReplace, Path: anchor.Append(members[i].seg), Value: add.value,
					PatchLine: add.bl.line, Idempotent: idempotent,
				})
				continue
			}
			if leadsRemovedMember(members, i, r.end, pairOf, r.start) {
				continue
			}
			out = append(out, Transform{
				Op: OpRemove, Path: anchor.Append(members[i].seg), PatchLine: members[i].bl.line,
			})
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
			tr := Transform{Op: OpAdd, Path: path, Value: add.value, PatchLine: add.bl.line, Idempotent: idempotent}
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

// leadsRemovedMember reports whether the "-" comment at members[idx] is the
// LEADING comment of a member the same run removes (§8.2: "immediately
// preceding, with no blank line"). It is, when the very next body line is an
// unpaired "-" member — and then the comment needs no removal record of its
// own, because §8.2 makes removing that member remove its leading comment
// too. corpus/jsonc/delete-key-with-comment is this case; emitting a second
// remove would instead address a comment ORDINAL that counts only the
// comments the patch happened to list, not the container's.
func leadsRemovedMember(members []member, idx, runEnd int, pairOf []int, runStart int) bool {
	if members[idx].seg.Kind != SegComment {
		return false
	}
	next := idx + 1
	if next >= runEnd {
		return false
	}
	return members[next].bl.margin == '-' &&
		members[next].seg.Kind != SegComment &&
		pairOf[next-runStart] < 0
}

// placement finds the sibling an unmatched add at members[idx] is placed
// relative to (§6.2). after=true means "after"; after=false means "before".
// ok=false means append at the end of the container.
func placement(members []member, idx int) (sib Segment, after bool, ok bool) {
	// A "+" comment line immediately above a "+" member is that member's
	// leading comment (§8.2), so the member is placed after the comment the
	// same hunk is adding rather than after the context line above them both.
	// corpus/jsonc/add-with-leading-comment pins the resulting `after: /#0`.
	if idx > 0 && members[idx-1].bl.margin == '+' && members[idx-1].seg.Kind == SegComment {
		return members[idx-1].seg, true, true
	}
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

// targetCommentText recognizes a body line that spells a comment IN THE
// TARGET (§3: "target comments are ordinary body text") and returns its
// content with the marker and one leading space stripped, which is the form
// §6.1's comment-match row compares. All three v0 comment syntaxes are
// accepted regardless of the section's format, because no format's value
// grammar can produce a body line starting with "#", "//" or "/*" — so the
// spelling is unambiguous without the parser knowing which backend will read
// it.
func targetCommentText(text string) (string, bool) {
	switch {
	case strings.HasPrefix(text, "//"):
		return strings.TrimPrefix(strings.TrimPrefix(text, "//"), " "), true
	case strings.HasPrefix(text, "/*") && strings.HasSuffix(text, "*/") && len(text) >= 4:
		return strings.TrimSpace(text[2 : len(text)-2]), true
	case strings.HasPrefix(text, "#"):
		return strings.TrimPrefix(strings.TrimPrefix(text, "#"), " "), true
	}
	return "", false
}

// commentValue is a comment node's value in the IR: the one-key mapping
// `{comment: <text>}` (§11.10 reduction 3), which is why an `add` at a comment
// address needs no `comment:` qualifier of its own. corpus/yaml/set-scalar and
// corpus/jsonc/add-with-leading-comment both pin this shape.
func commentValue(text string) Value {
	return NodeValue(&yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "comment"},
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: text},
	}})
}

// commentTextOf reads a comment node's text back out of a value, reporting
// false for a value that is not a comment node. A bare scalar is accepted as
// the same thing, so a hand-authored .hewt fixture may spell a comment either
// way.
func commentTextOf(v Value) (string, bool) {
	n := v.Node()
	if n == nil {
		return "", false
	}
	if n.Kind == yaml.ScalarNode {
		return n.Value, true
	}
	if n.Kind == yaml.MappingNode && len(n.Content) == 2 &&
		n.Content[0].Value == "comment" && n.Content[1].Kind == yaml.ScalarNode {
		return n.Content[1].Value, true
	}
	return "", false
}

// classifyMember turns one context/"-"/"+" body line into a member: either a
// map-anchor field ("key: value"), a comment node ("# text"), or a
// sequence-anchor element (a bare scalar or a flow-style value, addressed by
// content per §4.2's empty-field and named-field forms).
func classifyMember(bl bodyLine, commentOrdinal *int) (member, error) {
	text := strings.TrimSpace(bl.text)
	if text == "" {
		return member{}, parseErr(bl.line, "", "empty body line")
	}
	// A sequence element may be spelled in YAML's own block-sequence style
	// ("- beta", per the spec's own §5 worked example for /tags) rather than
	// as a bare value; strip the marker before classifying the element
	// itself. A multi-line block-style element (its fields continued on
	// FOLLOWING body lines rather than one flow-style line) is out of scope:
	// every JSON-family fixture, and the single-line YAML cases this
	// implementation targets, write nested element content in flow style.
	dashElement := strings.HasPrefix(text, "- ")
	if dashElement {
		text = strings.TrimSpace(text[2:])
	}
	if txt, ok := targetCommentText(text); ok {
		seg := Segment{Kind: SegComment, Index: *commentOrdinal}
		*commentOrdinal++
		return member{bl: bl, seg: seg, value: commentValue(txt), isField: true}, nil
	}

	var n yaml.Node
	if err := yaml.Unmarshal([]byte(text), &n); err != nil {
		return member{}, parseErr(bl.line, "", "malformed body value %q: %v", text, err)
	}
	if len(n.Content) == 0 {
		return member{}, parseErr(bl.line, "", "empty body value %q", text)
	}
	val := n.Content[0]

	// A dash-prefixed line is always a sequence element, even when its
	// remaining text happens to parse as a single "key: value" pair — that
	// shape means a keyed element subset-matched by its one listed field
	// (routed below), not a map field of the anchor itself.
	isMapEntry := !dashElement && val.Kind == yaml.MappingNode && len(val.Content) == 2 && !strings.HasPrefix(text, "{")
	if isMapEntry {
		key := val.Content[0].Value
		v := canonicalValue(val.Content[1])
		return member{bl: bl, seg: Segment{Kind: SegKey, Name: key}, value: v, isField: true}, nil
	}

	if val.Kind == yaml.MappingNode && len(val.Content) >= 2 {
		// A flow-style object: one sequence element, subset-matched by its
		// listed fields (§5.1, §6.4.2).
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

	// A bare scalar (or other opaque value) sequence element, addressed by
	// content — the "=value" empty-field form (§4.2).
	sc, err := scalarOfNode(val)
	if err != nil {
		return member{}, parseErr(bl.line, "", "%v", err)
	}
	seg := Segment{Kind: SegMatch, Value: sc}
	return member{bl: bl, seg: seg, value: canonicalValue(val), isField: false}, nil
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

// bangDirective is a parsed "!" line.
type bangDirective struct{ kind string }

func parseBangDirective(text string) (bangDirective, error) {
	text = strings.TrimSpace(text)
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return bangDirective{}, fmt.Errorf("empty directive")
	}
	return bangDirective{kind: fields[0]}, nil
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
