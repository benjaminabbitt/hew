package hew

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// RenderOptions controls rendering (Appendix A.3).
type RenderOptions struct {
	// Context is the sibling radius a differ would use (§9.4-R2); the
	// renderer itself renders every transform it is given; a transform list
	// with fewer context `test` records than a human would write simply
	// renders a tighter patch. Default 1, per O19 — kept here for interface
	// parity with Appendix A.3 though this implementation does not read it:
	// the renderer never drops or synthesizes assertions the caller didn't
	// hand it (that would silently change strictness, exactly what §9.4-R2's
	// own note warns against).
	Context int
	// Preamble controls whether "hew: 1" is emitted. Convergence is never
	// written as the file-level `idempotent:` pragma (§2.1, ruling O3): the
	// pragma governs every hunk, while Idempotent is a per-transform
	// qualifier, so it goes back out as the `! idempotent` line it came from.
	Preamble bool
	// Comment, if non-empty, is written as a leading "# " comment line before
	// the preamble.
	Comment string
}

// ErrInexpressible reports a transform the mirror grammar cannot express
// (Appendix C): op copy (C.2), or a remove with no accompanying test to
// supply the value the "-" line must show.
var ErrInexpressible = errors.New("hew: transform not expressible in mirror grammar")

// Render writes a transform list back out as .hew mirror-grammar text
// (Appendix A.3). Deterministic: the same input produces the same bytes.
//
// Scope, flagged in the P2 report: Render targets round-trip identity
// (RT2: parse(render(ir)) == ir) rather than reproducing any particular
// human-authored layout — the renderer regroups transforms into hunks by
// address rather than preserving whatever hunk boundaries a hand-authored
// .hew happened to use (the abstract IR does not retain that boundary
// information at all; nothing in Appendix A.1's Transform records which
// hunk it came from).
func Render(tl TransformList, opt RenderOptions) ([]byte, error) {
	if err := tl.Validate(); err != nil {
		return nil, err
	}
	groups, order, err := groupByAnchor(tl.Transform)
	if err != nil {
		return nil, err
	}

	var b strings.Builder
	if opt.Comment != "" {
		for _, line := range strings.Split(opt.Comment, "\n") {
			b.WriteString("# ")
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	if opt.Preamble {
		fmt.Fprintf(&b, "hew: %d\n", Version)
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "--- %s", targetToken(tl.Target))
	if tl.Format != "" {
		fmt.Fprintf(&b, " format=%s", tl.Format)
	}
	b.WriteByte('\n')

	for _, anchor := range order {
		header, ord, err := authoredAnchor(anchor)
		if err != nil {
			return nil, err
		}
		lines, err := renderGroup(anchor, groups[anchor.String()])
		if err != nil {
			return nil, err
		}
		if ord != nil {
			// An ordinal is an addressing mode in the IR and an annotation in
			// the notation: it goes back to `! match ord=` on the hunk's first
			// body line, the hunk-anchored form of §7.2.
			lines = append([]string{fmt.Sprintf("! match ord=%d", *ord)}, lines...)
		}
		b.WriteByte('\n')
		fmt.Fprintf(&b, "@@ %s @@\n", header)
		for _, l := range lines {
			b.WriteString(l)
			b.WriteByte('\n')
		}
	}
	return []byte(b.String()), nil
}

// authoredAnchor splits a hunk anchor into the address the notation can spell
// and the ordinal selector that has to become a `! match ord=` annotation:
// ParseAuthoredPath refuses an `[n]` selector, so rendering one into a hunk
// header would emit notation this very parser rejects (§7.2, §9.6).
func authoredAnchor(p Path) (string, *int, error) {
	segs := p.Segments()
	for i, s := range segs {
		if s.Ordinal == nil || i == len(segs)-1 {
			continue
		}
		return "", nil, fmt.Errorf("%w: an ordinal selector on %s is not the anchor's last segment, and only the last one is writable as `! match ord=` (§7.2)",
			ErrInexpressible, p)
	}
	if len(segs) == 0 || segs[len(segs)-1].Ordinal == nil {
		return p.String(), nil, nil
	}
	ord := *segs[len(segs)-1].Ordinal
	segs[len(segs)-1].Ordinal = nil
	bare := NewPath(segs...)
	if p.IsRelative() {
		bare = NewRelativePath(segs...)
	}
	return bare.String(), &ord, nil
}

func targetToken(s string) string {
	if strings.ContainsRune(s, ' ') {
		return `"` + s + `"`
	}
	return s
}

// anchorFor computes the hunk anchor a transform renders under.
func anchorFor(t Transform) Path {
	switch t.Op {
	case OpAdd:
		if !t.After.IsZero() {
			if p, ok := t.After.Parent(); ok {
				return p
			}
		}
		if !t.Before.IsZero() {
			if p, ok := t.Before.Parent(); ok {
				return p
			}
		}
		if p, ok := t.Path.Parent(); ok {
			return p
		}
		return t.Path
	case OpTest:
		// `? exhaustive` governs the container it is written in, so its path
		// IS the anchor. Every other free-standing assertion carries its own
		// path and can live in any hunk; hostAnchor puts it in the one it was
		// written in (§7.1, §4.6).
		if t.Exhaustive || freeAssert(t) {
			return t.Path
		}
		return testAnchor(t.Path)
	default: // remove, replace, copy(rejected before this is called on it)
		if p, ok := t.Path.Parent(); ok {
			return p
		}
		return t.Path
	}
}

// testAnchor computes a plain test's hunk anchor. Normally that is the
// test's own parent (a map field, or a scalar sequence element). But a
// keyed-element field test (§9.1 step 2's "one test per listed field") is
// TWO segments deeper than the sequence's own container — path
// .../name=github/name — so its parent is the ELEMENT (.../name=github),
// not the sequence; the anchor needs to go one level further up so every
// field test for the same element lands in the same rendered hunk.
func testAnchor(path Path) Path {
	if n := path.Len(); n >= 2 && path.Segment(n-2).Kind == SegMatch {
		if p, ok := path.Parent(); ok {
			if gp, ok2 := p.Parent(); ok2 {
				return gp
			}
		}
	}
	if p, ok := path.Parent(); ok {
		return p
	}
	return path
}

// freeAssert reports a `?` assertion that carries its own path rather than
// addressing a child of the hunk it stands in (§7.1). `? exhaustive` is not
// one: it governs the container, and is handled with the anchor.
func freeAssert(t Transform) bool {
	return t.Op == OpTest && !t.Exhaustive && (t.Absent || t.Count != nil || t.NodeKind != nil)
}

// hostAnchor picks the hunk a free-standing assertion is written in: the one
// its neighbours belong to, so the assertion keeps its position in body order
// instead of being exiled to a hunk of its own (which would reorder the list
// and break RT2). It falls back to its own path when the list is nothing but
// assertions — an assert-only hunk (§7.4).
func hostAnchor(ts []Transform, i int) Path {
	for j := i - 1; j >= 0; j-- {
		if !freeAssert(ts[j]) {
			return anchorFor(ts[j])
		}
	}
	for j := i + 1; j < len(ts); j++ {
		if !freeAssert(ts[j]) {
			return anchorFor(ts[j])
		}
	}
	return anchorFor(ts[i])
}

// groupByAnchor buckets transforms by their hunk anchor, preserving
// first-seen anchor order (deterministic, §9.4-R1 applies to render too).
func groupByAnchor(ts []Transform) (map[string][]Transform, []Path, error) {
	groups := map[string][]Transform{}
	var order []Path
	seen := map[string]bool{}
	for i, t := range ts {
		if t.Op == OpCopy {
			return nil, nil, ErrInexpressible
		}
		a := anchorFor(t)
		if freeAssert(t) {
			a = hostAnchor(ts, i)
		}
		key := a.String()
		if !seen[key] {
			seen[key] = true
			order = append(order, a)
		}
		groups[key] = append(groups[key], t)
	}
	return groups, order, nil
}

// relSegs returns path's segments beyond anchor's, or ok=false if path does
// not descend from anchor.
func relSegs(anchor, path Path) (segs []Segment, ok bool) {
	aSegs := anchor.Segments()
	pSegs := path.Segments()
	if len(pSegs) < len(aSegs) {
		return nil, false
	}
	for i := range aSegs {
		if !pSegs[i].Equal(aSegs[i]) {
			return nil, false
		}
	}
	return pSegs[len(aSegs):], true
}

type contentEntry struct {
	seg        Segment
	plainTest  *Transform
	fieldTests []*Transform // tests one level deeper than seg (a keyed element's listed fields)
	remove     *Transform
	replace    *Transform
	add        *Transform

	// A free-standing assertion (§7.1) occupies a body slot of its own,
	// keeping the position it holds in the transform stream: the parser
	// emits `?` lines in body order, interleaved with the container's
	// tests, so rendering them all at the top of the hunk would move them
	// and break RT2 for any list whose asserts do not come first.
	assert *Transform
}

// renderGroup renders one hunk's body lines for the transforms sharing one
// anchor (§9.1's lowering, inverted).
func renderGroup(anchor Path, ts []Transform) ([]string, error) {
	var order []string
	bySeg := map[string]*contentEntry{}
	// chainAfter/chainBefore let a second add that names the same sibling
	// insert next to the FIRST add rather than back at the sibling itself,
	// so several unmatched adds sharing one After/Before keep their
	// original relative order (jsonc/roundtrip-basic needs this: two adds
	// both placed "after timeout").
	chainAfter := map[string]string{}
	chainBefore := map[string]string{}
	getEntry := func(seg Segment) *contentEntry {
		key := seg.String()
		e, ok := bySeg[key]
		if !ok {
			e = &contentEntry{seg: seg}
			bySeg[key] = e
			order = append(order, key)
		}
		return e
	}
	syntheticN := 0
	assertN := 0
	// slot appends a body slot that is not addressed by a child segment: a
	// free-standing assertion, which keeps its place in body order.
	slot := func(t *Transform) {
		key := fmt.Sprintf("?%d", assertN)
		assertN++
		bySeg[key] = &contentEntry{assert: t}
		order = append(order, key)
	}

	for i := range ts {
		t := &ts[i]
		switch t.Op {
		case OpTest:
			if t.Exhaustive || t.Absent || t.Count != nil || t.NodeKind != nil {
				slot(t)
				continue
			}
			rel, ok := relSegs(anchor, t.Path)
			if !ok || len(rel) == 0 {
				return nil, fmt.Errorf("hew: render: test at %s does not descend from anchor %s", t.Path, anchor)
			}
			if len(rel) == 1 {
				getEntry(rel[0]).plainTest = t
			} else {
				e := getEntry(rel[0])
				e.fieldTests = append(e.fieldTests, t)
			}
		case OpRemove:
			rel, ok := relSegs(anchor, t.Path)
			if !ok || len(rel) != 1 {
				return nil, fmt.Errorf("hew: render: remove at %s does not address a direct child of %s", t.Path, anchor)
			}
			getEntry(rel[0]).remove = t
		case OpReplace:
			rel, ok := relSegs(anchor, t.Path)
			if !ok || len(rel) != 1 {
				return nil, fmt.Errorf("hew: render: replace at %s does not address a direct child of %s", t.Path, anchor)
			}
			getEntry(rel[0]).replace = t
		case OpAdd:
			var seg Segment
			if rel, ok := relSegs(anchor, t.Path); ok && len(rel) == 1 {
				seg = rel[0]
			} else {
				seg = Segment{Kind: syntheticKind, Index: syntheticN}
				syntheticN++
			}
			var after, before *Segment
			if !t.After.IsZero() {
				if rel, ok := relSegs(anchor, t.After); ok && len(rel) == 1 {
					after = &rel[0]
				}
			}
			if before == nil && !t.Before.IsZero() {
				if rel, ok := relSegs(anchor, t.Before); ok && len(rel) == 1 {
					before = &rel[0]
				}
			}
			key := seg.String()
			e := &contentEntry{seg: seg, add: t}
			bySeg[key] = e
			switch {
			case after != nil:
				target := after.String()
				if chained, ok := chainAfter[target]; ok {
					target = chained
				}
				order = insertAfter(order, target, key)
				chainAfter[after.String()] = key
			case before != nil:
				target := before.String()
				if chained, ok := chainBefore[target]; ok {
					target = chained
				}
				order = insertBefore(order, target, key)
				chainBefore[before.String()] = key
			default:
				order = append(order, key)
			}
		default:
			return nil, ErrInexpressible
		}
	}

	var lines []string
	// `! surface` is container-scoped (§7, §8.4 rule 4): it governs every
	// creation in the hunk, so it is written once, at the top.
	surface, err := groupSurface(ts)
	if err != nil {
		return nil, err
	}
	if surface != "" {
		lines = append(lines, "! surface "+string(surface))
	}
	for _, key := range order {
		e := bySeg[key]
		if e == nil {
			continue
		}
		switch {
		case e.assert != nil:
			l, err := renderFreeAssertion(*e.assert)
			if err != nil {
				return nil, err
			}
			lines = append(lines, l)
		case e.replace != nil:
			var v Value
			switch {
			case e.plainTest != nil:
				v = e.plainTest.Value
			case len(e.fieldTests) > 0:
				var err error
				v, err = flowValueFromFields(e.seg, e.fieldTests)
				if err != nil {
					return nil, err
				}
			default:
				return nil, fmt.Errorf("%w: replace at %s/%s has no accompanying test to supply the removed value", ErrInexpressible, anchor, e.seg)
			}
			// A line-scoped directive governs the line that FOLLOWS it (§7),
			// and the parser reads a `-`/`+` pair as two entries: the test
			// carries the `-` line's qualifiers, the replace the `+` line's.
			lines = append(lines, qualLines(e.test())...)
			lines = append(lines, renderMemberLine('-', e.seg, v))
			lines = append(lines, qualLines(e.replace)...)
			lines = append(lines, renderMemberLine('+', e.seg, e.replace.Value))
		case e.remove != nil:
			var v Value
			switch {
			case e.plainTest != nil:
				v = e.plainTest.Value
			case len(e.fieldTests) > 0:
				var err error
				v, err = flowValueFromFields(e.seg, e.fieldTests)
				if err != nil {
					return nil, err
				}
			default:
				return nil, fmt.Errorf("%w: remove at %s/%s has no accompanying test to supply the removed value", ErrInexpressible, anchor, e.seg)
			}
			// One `-` line carries both the test and the removal, so it takes
			// the union of their qualifiers.
			lines = append(lines, qualLines(e.test(), e.remove)...)
			lines = append(lines, renderMemberLine('-', e.seg, v))
		case e.add != nil:
			lines = append(lines, qualLines(e.add)...)
			lines = append(lines, renderMemberLine('+', e.seg, e.add.Value))
		case len(e.fieldTests) > 0:
			v, err := flowValueFromFields(e.seg, e.fieldTests)
			if err != nil {
				return nil, err
			}
			lines = append(lines, qualLines(e.test())...)
			lines = append(lines, renderMemberLine(' ', e.seg, v))
		case e.plainTest != nil:
			lines = append(lines, qualLines(e.plainTest)...)
			lines = append(lines, renderMemberLine(' ', e.seg, e.plainTest.Value))
		}
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("hew: render: hunk at %s has no body lines", anchor)
	}
	return lines, nil
}

// groupSurface reads the one TOML surface directive a hunk may carry. The
// notation attaches `! surface` to a container, not to a line, so a hunk
// whose adds disagree about their surface cannot be written at all — that is
// two hunks' worth of intent, and saying so is better than picking one.
func groupSurface(ts []Transform) (Surface, error) {
	var surface Surface
	seen := false
	for i := range ts {
		if ts[i].Op != OpAdd {
			continue
		}
		if seen && ts[i].Surface != surface {
			return "", fmt.Errorf("%w: adds under one anchor disagree about `! surface` (%q vs %q), which is container-scoped (§8.4 rule 4)",
				ErrInexpressible, surface, ts[i].Surface)
		}
		surface, seen = ts[i].Surface, true
	}
	return surface, nil
}

// test returns the entry's before-image test, whichever shape it took: a
// whole-node test, or the first of a keyed element's field tests (which all
// carry the same qualifiers, having come from the same body line).
func (e *contentEntry) test() *Transform {
	if e.plainTest != nil {
		return e.plainTest
	}
	if len(e.fieldTests) > 0 {
		return e.fieldTests[0]
	}
	return nil
}

// qualLines renders the `!` directive lines that put a transform's qualifiers
// back on the body line they ride (§9.1 step 6, inverted). Order is fixed so
// rendering stays deterministic; `on_conflict: fail` has no spelling because
// it IS the default add semantics (§7.7), and writing nothing round-trips it
// as the same behavior.
func qualLines(ts ...*Transform) []string {
	var out []string
	var anchor AnchorMode
	var onConflict OnConflict
	optional, idempotent := false, false
	for _, t := range ts {
		if t == nil {
			continue
		}
		if t.Anchor != "" {
			anchor = t.Anchor
		}
		if t.OnConflict != "" {
			onConflict = t.OnConflict
		}
		optional = optional || t.Optional
		idempotent = idempotent || t.Idempotent
	}
	if anchor != "" {
		out = append(out, "! anchor "+string(anchor))
	}
	switch onConflict {
	case ConflictReplace:
		out = append(out, "! upsert")
	case ConflictKeep:
		out = append(out, "! default")
	}
	if optional {
		out = append(out, "! optional")
	}
	if idempotent {
		out = append(out, "! idempotent")
	}
	return out
}

// syntheticKind marks an add whose element has no identity yet (a
// sequence-container add): the Segment exists only to hold this add's slot
// in the rendered body order, and is never itself rendered as a key.
const syntheticKind SegmentKind = 250

func insertAfter(order []string, target, key string) []string {
	for i, k := range order {
		if k == target {
			out := make([]string, 0, len(order)+1)
			out = append(out, order[:i+1]...)
			out = append(out, key)
			out = append(out, order[i+1:]...)
			return out
		}
	}
	return append(order, key)
}

func insertBefore(order []string, target, key string) []string {
	for i, k := range order {
		if k == target {
			out := make([]string, 0, len(order)+1)
			out = append(out, order[:i]...)
			out = append(out, key)
			out = append(out, order[i:]...)
			return out
		}
	}
	return append(order, key)
}

// flowValueFromFields reconstructs a keyed element's whole value from its
// individually-tested fields, for a "-" line's display or a member's context
// rendering.
func flowValueFromFields(elem Segment, fields []*Transform) (Value, error) {
	m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Style: yaml.FlowStyle}
	for _, f := range fields {
		// The field's own name is its Path's last segment.
		last := f.Path.Segment(f.Path.Len() - 1)
		if last.Kind != SegKey {
			return Value{}, fmt.Errorf("hew: render: field test at %s is not a key segment", f.Path)
		}
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: last.Name}, cloneNode(f.Value.Node()))
	}
	return NodeValue(m), nil
}

// renderMemberLine renders one context/"-"/"+" line for a direct-child
// segment and its value.
func renderMemberLine(margin byte, seg Segment, v Value) string {
	var text string
	switch seg.Kind {
	case SegKey:
		text = escapeKey(seg.Name) + ": " + renderValueText(v)
	case SegComment:
		// A comment node's value is `{comment: <text>}` (§4.5b, §11.10
		// reduction 3); the body line shows the text, not the wrapper.
		body, ok := commentTextOf(v)
		if !ok {
			body = valueText(v)
		}
		text = strings.TrimRight("# "+body, " ")
	default:
		// Sequence element (scalar or keyed flow object) and the synthetic
		// container-add marker both render as a bare value line.
		text = renderValueText(v)
	}
	return string(margin) + " " + text
}

func renderFreeAssertion(t Transform) (string, error) {
	if t.Exhaustive {
		return "? exhaustive", nil
	}
	if t.Path.HasOrdinal() {
		return "", fmt.Errorf("%w: assertion at %s carries an ordinal selector, which only a hunk anchor can spell as `! match ord=` (§7.2)",
			ErrInexpressible, t.Path)
	}
	path := t.Path.String()
	switch {
	case t.Absent:
		return "? absent " + path, nil
	case t.Count != nil:
		return fmt.Sprintf("? count %s = %d", path, *t.Count), nil
	case t.NodeKind != nil:
		return fmt.Sprintf("? kind %s = %s", path, string(*t.NodeKind)), nil
	}
	return "", fmt.Errorf("hew: render: test at %s has no recognized assertion mode", t.Path)
}

func valueText(v Value) string {
	n := v.Node()
	if n == nil {
		return ""
	}
	if n.Kind == yaml.ScalarNode {
		return n.Value
	}
	return renderValueText(v)
}

// renderValueText renders a Value as it appears after a ":" or on its own
// scalar/flow-object body line: scalars print as their own literal text
// (quoted only when it was itself string-quoted or is one of the reserved
// words), everything else prints as compact flow YAML — which is also valid
// JSON, so a JSON target's fragment reader (§8.1) accepts it unchanged.
func renderValueText(v Value) string {
	n := v.Node()
	if n == nil {
		return "null"
	}
	if n.Kind == yaml.ScalarNode {
		return scalarLiteral(n)
	}
	flow := flowCopy(n)
	var buf strings.Builder
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	_ = enc.Encode(flow)
	_ = enc.Close()
	return strings.TrimRight(buf.String(), "\n")
}

func scalarLiteral(n *yaml.Node) string {
	switch n.ShortTag() {
	case "!!str":
		if needsQuote(n.Value) {
			return strconv.Quote(n.Value)
		}
		return n.Value
	default:
		return n.Value
	}
}

func needsQuote(s string) bool {
	if s == "" || s == "true" || s == "false" || s == "null" {
		return true
	}
	if isNumber(s) {
		return true
	}
	return strings.ContainsAny(s, ":#{}[]&*!|>'\"%@`\n\t") || strings.HasPrefix(s, " ") || strings.HasSuffix(s, " ")
}
