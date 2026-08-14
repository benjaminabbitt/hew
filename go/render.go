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
	// Preamble controls whether "hew: 1" (and "idempotent: true", if any
	// transform carries Idempotent) is emitted.
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
		if anyIdempotent(tl.Transform) {
			b.WriteString("idempotent: true\n")
		}
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "--- %s", targetToken(tl.Target))
	if tl.Format != "" {
		fmt.Fprintf(&b, " format=%s", tl.Format)
	}
	b.WriteByte('\n')

	for _, anchor := range order {
		lines, err := renderGroup(anchor, groups[anchor.String()])
		if err != nil {
			return nil, err
		}
		b.WriteByte('\n')
		fmt.Fprintf(&b, "@@ %s @@\n", anchor.String())
		for _, l := range lines {
			b.WriteString(l)
			b.WriteByte('\n')
		}
	}
	return []byte(b.String()), nil
}

func anyIdempotent(ts []Transform) bool {
	for _, t := range ts {
		if t.Idempotent {
			return true
		}
	}
	return false
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
		if t.Exhaustive {
			return t.Path
		}
		if t.Absent || (t.Count != nil && !t.Exhaustive) || t.NodeKind != nil {
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

// groupByAnchor buckets transforms by their hunk anchor, preserving
// first-seen anchor order (deterministic, §9.4-R1 applies to render too).
func groupByAnchor(ts []Transform) (map[string][]Transform, []Path, error) {
	groups := map[string][]Transform{}
	var order []Path
	seen := map[string]bool{}
	for _, t := range ts {
		if t.Op == OpCopy {
			return nil, nil, ErrInexpressible
		}
		a := anchorFor(t)
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
}

// renderGroup renders one hunk's body lines for the transforms sharing one
// anchor (§9.1's lowering, inverted).
func renderGroup(anchor Path, ts []Transform) ([]string, error) {
	var exhaustive *Transform
	var freeAsserts []Transform
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

	for i := range ts {
		t := &ts[i]
		switch t.Op {
		case OpTest:
			if t.Exhaustive {
				exhaustive = t
				continue
			}
			if t.Absent || (t.Count != nil) || t.NodeKind != nil {
				freeAsserts = append(freeAsserts, *t)
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
	if exhaustive != nil {
		lines = append(lines, "? exhaustive")
	}
	for _, a := range freeAsserts {
		l, err := renderFreeAssertion(anchor, a)
		if err != nil {
			return nil, err
		}
		lines = append(lines, l)
	}

	for _, key := range order {
		e := bySeg[key]
		if e == nil {
			continue
		}
		switch {
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
			lines = append(lines, renderMemberLine('-', e.seg, v))
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
			lines = append(lines, renderMemberLine('-', e.seg, v))
		case e.add != nil:
			lines = append(lines, renderMemberLine('+', e.seg, e.add.Value))
		case len(e.fieldTests) > 0:
			v, err := flowValueFromFields(e.seg, e.fieldTests)
			if err != nil {
				return nil, err
			}
			lines = append(lines, renderMemberLine(' ', e.seg, v))
		case e.plainTest != nil:
			lines = append(lines, renderMemberLine(' ', e.seg, e.plainTest.Value))
		}
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("hew: render: hunk at %s has no body lines", anchor)
	}
	return lines, nil
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
		body, ok := commentTextOf(v)
		if !ok {
			body = valueText(v)
		}
		text = "# " + body
	default:
		// Sequence element (scalar or keyed flow object) and the synthetic
		// container-add marker both render as a bare value line.
		text = renderValueText(v)
	}
	return string(margin) + " " + text
}

func renderFreeAssertion(anchor Path, t Transform) (string, error) {
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
