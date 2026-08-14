package hew

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/benjaminabbitt/hew/go/internal/hewerr"
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
	// own note warns against). The radius is DiffOptions.Context's to choose,
	// because choosing it needs both documents, which the renderer never has.
	Context int
	// Preamble controls whether "hew: 1" is emitted. Convergence is never
	// written as the file-level `idempotent:` pragma (§2.1, ruling O3): the
	// pragma governs every hunk, while Idempotent is a per-transform
	// qualifier, so it goes back out as the `! idempotent` line it came from.
	Preamble bool
	// Comment, if non-empty, is written as a leading "# " comment line before
	// the preamble.
	Comment string
	// Fragment selects the syntax the hunk bodies' fragments are written in.
	// §5 defines both images as parsed "by the target format's fragment
	// parser", so a JSON patch may spell its keys `"port"` and a JSONC patch
	// may spell a comment `// …`; the zero value keeps the format-neutral
	// spelling this renderer has always emitted, which every format's fragment
	// parser also accepts.
	Fragment FragmentStyle
}

// FragmentStyle is RenderOptions.Fragment's vocabulary.
type FragmentStyle string

const (
	// FragmentNeutral writes body lines in one format-neutral, YAML-shaped
	// spelling. It round-trips through the parser for every format.
	FragmentNeutral FragmentStyle = ""
	// FragmentNative writes each body line in the list's own Format: JSON and
	// JSONC keys and strings quoted, JSONC comments as `//`, YAML sequences
	// with `- ` markers and block-style values. This is what the differ emits,
	// so that a patch reads as a diff of the file it patches (§9.4-R5).
	FragmentNative FragmentStyle = "native"
)

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
	// The hunk headers below are spelled from these paths; refuse the ones v0
	// cannot spell before writing one down (§9.3).
	//
	// Deliberately conservative: a mutation's LAST segment often goes out as a
	// body fragment line instead ("@scope/pkg": …), a spelling the fragment
	// parser reads back correctly, so a few of these lists would have rendered
	// cleanly. Which ones is a function of the change's shape — scalar or
	// container — and the same list emitted as .hewt is corrupt either way. A
	// guard whose verdict depended on the shape would be a guard nobody could
	// reason about, so the whole class is refused until the quoted-key form
	// (PR #1) makes the question moot.
	if err := tl.checkSpellable(hewerr.ComponentRenderer); err != nil {
		return nil, err
	}
	groups, order, err := groupByAnchor(tl.Transform)
	if err != nil {
		return nil, err
	}
	dial := dialectFor(opt.Fragment, tl.Format)

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
		lines, err := renderGroup(anchor, groups[anchor.String()], dial)
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
	bare := RootPath().Append(segs...)
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

// anchorFor computes the hunk anchor a transform renders under. owned is the
// set of anchors some MUTATION in the same list already claims; it decides the
// one ambiguous case, see testAnchor.
func anchorFor(t Transform, owned map[string]bool) Path {
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
		return testAnchor(t.Path, owned)
	default: // remove, replace, copy(rejected before this is called on it)
		if p, ok := t.Path.Parent(); ok {
			return p
		}
		return t.Path
	}
}

// testAnchor computes a plain test's hunk anchor. Normally that is the test's
// own parent (a map field, or a scalar sequence element).
//
// A path two segments below a sequence — .../name=github/command — is
// genuinely ambiguous, and both readings are things the notation writes:
//
//	@@ /mcpServers @@                  a subset-matched context line for the
//	  - name: github                   element, §9.1 step 2's "one test per
//	                                   listed field"
//	@@ /mcpServers/name=github @@      a body line of the element's OWN hunk,
//	  command: npx                     which is where an inner edit anchors
//
// owned settles it: if some mutation in the same list already anchors at the
// element, the element has a hunk and the test belongs in it; otherwise the
// test is a subset line and hoists to the sequence, so that every field test
// for one element lands in the same rendered hunk.
func testAnchor(path Path, owned map[string]bool) Path {
	parent, ok := path.Parent()
	if !ok {
		return path
	}
	if n := path.Len(); n >= 2 && path.Segment(n-2).Kind == SegMatch && !owned[parent.String()] {
		if gp, ok2 := parent.Parent(); ok2 {
			return gp
		}
	}
	return parent
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
func hostAnchor(ts []Transform, i int, owned map[string]bool) Path {
	for j := i - 1; j >= 0; j-- {
		if !freeAssert(ts[j]) {
			return anchorFor(ts[j], owned)
		}
	}
	for j := i + 1; j < len(ts); j++ {
		if !freeAssert(ts[j]) {
			return anchorFor(ts[j], owned)
		}
	}
	return anchorFor(ts[i], owned)
}

// ownedAnchors collects the anchors some mutation — or a container-scoped
// assertion — already claims, which is what testAnchor needs to settle a
// keyed-element path's two readings.
func ownedAnchors(ts []Transform) map[string]bool {
	owned := map[string]bool{}
	for i := range ts {
		if ts[i].Op == OpTest && !ts[i].Exhaustive && !freeAssert(ts[i]) {
			continue
		}
		owned[anchorFor(ts[i], nil).String()] = true
	}
	return owned
}

// groupByAnchor buckets transforms by their hunk anchor, preserving
// first-seen anchor order (deterministic, §9.4-R1 applies to render too).
func groupByAnchor(ts []Transform) (map[string][]Transform, []Path, error) {
	groups := map[string][]Transform{}
	var order []Path
	seen := map[string]bool{}
	owned := ownedAnchors(ts)
	for i, t := range ts {
		if t.Op == OpCopy {
			return nil, nil, ErrInexpressible
		}
		a := anchorFor(t, owned)
		if freeAssert(t) {
			a = hostAnchor(ts, i, owned)
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
func renderGroup(anchor Path, ts []Transform, dial dialect) ([]string, error) {
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
		if err := checkLabelBody(anchor, *t); err != nil {
			return nil, err
		}
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
			var key string
			var seg Segment
			if rel, ok := relSegs(anchor, t.Path); ok && len(rel) == 1 {
				seg, key = rel[0], rel[0].String()
			} else {
				seg = Segment{Kind: syntheticKind, Index: syntheticN}
				key = fmt.Sprintf("+%d", syntheticN)
				syntheticN++
			}
			// A placement names a sibling by ADDRESS; the body knows it by slot
			// key, and a sequence element added by this same hunk has no
			// address of its own to be keyed by (its transform addresses the
			// container). slotFor bridges the two.
			slotFor := func(p Path) (string, bool) {
				rel, ok := relSegs(anchor, p)
				if !ok || len(rel) != 1 {
					return "", false
				}
				return resolveSlot(order, bySeg, rel[0]), true
			}
			e := &contentEntry{seg: seg, add: t}
			bySeg[key] = e
			switch target, ok := slotFor(t.After); {
			case !t.After.IsZero() && ok:
				dest := target
				if chained, has := chainAfter[target]; has {
					dest = chained
				}
				order = insertAfter(order, dest, key)
				chainAfter[target] = key
			default:
				target, ok := slotFor(t.Before)
				if t.Before.IsZero() || !ok {
					order = append(order, key)
					continue
				}
				// The second add sharing one `before:` sibling goes AFTER the
				// first, not ahead of it: both land in front of the named
				// sibling, in the order the list writes them, which is the
				// order the applier will produce.
				if chained, has := chainBefore[target]; has {
					order = insertAfter(order, chained, key)
				} else {
					order = insertBefore(order, target, key)
				}
				chainBefore[target] = key
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
				v, err = subsetValueFromFields(e.fieldTests, dial)
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
			lines = append(lines, dial.memberLines('-', e.seg, v)...)
			lines = append(lines, qualLines(e.replace)...)
			lines = append(lines, dial.memberLines('+', e.seg, e.replace.Value)...)
		case e.remove != nil:
			var v Value
			switch {
			case e.plainTest != nil:
				v = e.plainTest.Value
			case len(e.fieldTests) > 0:
				var err error
				v, err = subsetValueFromFields(e.fieldTests, dial)
				if err != nil {
					return nil, err
				}
			default:
				return nil, fmt.Errorf("%w: remove at %s/%s has no accompanying test to supply the removed value", ErrInexpressible, anchor, e.seg)
			}
			// One `-` line carries both the test and the removal, so it takes
			// the union of their qualifiers.
			lines = append(lines, qualLines(e.test(), e.remove)...)
			lines = append(lines, dial.memberLines('-', e.seg, v)...)
		case e.add != nil:
			lines = append(lines, qualLines(e.add)...)
			lines = append(lines, dial.memberLines('+', e.seg, e.add.Value)...)
		case len(e.fieldTests) > 0:
			v, err := subsetValueFromFields(e.fieldTests, dial)
			if err != nil {
				return nil, err
			}
			lines = append(lines, qualLines(e.test())...)
			lines = append(lines, dial.memberLines(' ', e.seg, v)...)
		case e.plainTest != nil:
			lines = append(lines, qualLines(e.plainTest)...)
			lines = append(lines, dial.memberLines(' ', e.seg, e.plainTest.Value)...)
		}
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("hew: render: hunk at %s has no body lines", anchor)
	}
	return dial.alignLines(lines), nil
}

// checkLabelBody refuses a transform whose first step below the anchor is an
// HCL label. A label is half of a block's `(type, labels)` address (§4.3), and
// the mirror grammar writes a block as `type "label" { … }` in the container
// that holds it — there is no body line that spells a label on its own. Saying
// so is Appendix C's rule: what the notation cannot express is refused, never
// approximated by a line that would read as something else (§9.4-R6).
func checkLabelBody(anchor Path, t Transform) error {
	rel, ok := relSegs(anchor, t.Path)
	if !ok || len(rel) == 0 || rel[0].Kind != SegLabel {
		return nil
	}
	return fmt.Errorf("%w: %s addresses an HCL block by its label, which the mirror grammar writes as a `type \"label\" { … }` body in %s's own container, not as a body line of %s (§8.5, Appendix C)",
		ErrInexpressible, t.Path, anchor, anchor)
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

// resolveSlot maps a placement's sibling segment to the body slot that holds
// it. Ordinarily the segment IS the key. The exception is a sequence element
// this same hunk adds: its slot is synthetic, because its transform addresses
// the container rather than the element, so a later sibling naming it by
// key-match is matched against the added VALUE — the same §4.2 comparison the
// applier will make, and the same one the parser makes when it re-derives
// placements from the rendered body.
func resolveSlot(order []string, bySeg map[string]*contentEntry, seg Segment) string {
	key := seg.String()
	if _, ok := bySeg[key]; ok || seg.Kind != SegMatch {
		return key
	}
	for _, k := range order {
		e := bySeg[k]
		if e != nil && e.add != nil && e.seg.Kind == syntheticKind && valueMatchesSegment(e.add.Value, seg) {
			return k
		}
	}
	return key
}

// valueMatchesSegment applies §4.2's key-match comparison to a value in hand,
// as resolve.go's matchesSegment applies it to a parsed target node.
func valueMatchesSegment(v Value, seg Segment) bool {
	n := v.Node()
	if n == nil {
		return false
	}
	want := seg.Value.Value()
	if seg.Name == "" {
		return NodeValue(n).Equal(want)
	}
	if n.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == seg.Name {
			return NodeValue(n.Content[i+1]).Equal(want)
		}
	}
	return false
}

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

// subsetValueFromFields reconstructs a keyed element's subset-matched value
// from its individually-tested fields, for a "-" line's display or a member's
// context rendering. The style follows the dialect: a JSON body line spells the
// subset as a flow object, a YAML one as a block mapping under its `- ` marker.
func subsetValueFromFields(fields []*Transform, dial dialect) (Value, error) {
	m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Style: yaml.FlowStyle}
	if dial.block {
		m.Style = 0
	}
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

// dialect is the body-line spelling one rendered patch uses. §5 hands both
// hunk images to the target format's own fragment parser, so the notation may
// wear that format's syntax; the neutral dialect is the one spelling every
// fragment parser accepts, and is what a caller who names no format gets.
type dialect struct {
	native bool
	json   bool   // JSON/JSONC: quoted keys, quoted strings, flow collections
	block  bool   // YAML: "- " sequence markers and block-style values
	eq     bool   // TOML/HCL: members are `key = value` (§8.4, §8.5)
	quote  bool   // TOML/HCL: a string is always written quoted
	align  bool   // HCL: `=` lines up within a run of member lines (§8.5)
	marker string // standalone comment marker, trailing space included
}

func dialectFor(style FragmentStyle, format FormatID) dialect {
	if style != FragmentNative {
		return dialect{marker: "# "}
	}
	switch format {
	case FormatJSON, FormatJSONC:
		return dialect{native: true, json: true, marker: "// "}
	case FormatYAML:
		return dialect{native: true, block: true, marker: "# "}
	case FormatTOML:
		return dialect{native: true, eq: true, quote: true, marker: "# "}
	case FormatHCL:
		return dialect{native: true, eq: true, quote: true, align: true, marker: "# "}
	default:
		return dialect{native: true, marker: "# "}
	}
}

// alignMark is the placeholder an aligning dialect writes where a run of
// member lines' `=` signs must line up. alignLines replaces every one of
// them, so it never reaches the output.
const alignMark = "\x00"

// sep is the member separator: TOML and HCL write `key = value`, everything
// else `key: value`. An aligning dialect defers the padding to alignLines,
// which is the only place a line's width can be compared with its neighbours'.
func (d dialect) sep() string {
	switch {
	case d.align:
		return alignMark + "= "
	case d.eq:
		return " = "
	}
	return ": "
}

// alignLines reproduces hclfmt's rule inside a rendered hunk: within a run of
// consecutive member lines the `=` signs line up one column past the widest
// name, and any other line — a comment, an annotation, a block header — breaks
// the run (§8.5). Doing it here rather than per line is the point: the padding
// is a property of the run, not of the member, which is why hcl/roundtrip-basic
// widens an ADDED `region` to sit under the `=` of the context line above it.
func (d dialect) alignLines(lines []string) []string {
	if !d.align {
		return lines
	}
	for i := 0; i < len(lines); {
		end, width := i, -1
		for end < len(lines) {
			at := strings.Index(lines[end], alignMark)
			if at < 0 {
				break
			}
			if at > width {
				width = at
			}
			end++
		}
		if end == i {
			i++
			continue
		}
		for j := i; j < end; j++ {
			at := strings.Index(lines[j], alignMark)
			lines[j] = lines[j][:at] + strings.Repeat(" ", width-at+1) + lines[j][at+len(alignMark):]
		}
		i = end
	}
	return lines
}

// memberLines renders one context/"-"/"+" body entry for a direct-child
// segment and its value. It returns several lines when the value is a block
// collection: an added node keeps the new document's own shape (§9.4-R5), and
// every continuation line carries the same margin.
func (d dialect) memberLines(margin byte, seg Segment, v Value) []string {
	switch seg.Kind {
	case SegKey:
		body := d.valueLines(v)
		if !d.isBlock(v) {
			return d.marginate(margin, []string{d.key(seg.Name) + d.sep() + body[0]})
		}
		// A block collection lives under its key, not after it, even when it
		// happens to be one line long — `env: GITHUB_TOKEN: x` is not YAML.
		out := []string{d.key(seg.Name) + ":"}
		for _, l := range body {
			out = append(out, "  "+l)
		}
		return d.marginate(margin, out)
	case SegComment:
		// A comment node's value is `{comment: <text>}` (§4.5b, §11.10
		// reduction 3); the body line shows the text, not the wrapper.
		body, ok := commentTextOf(v)
		if !ok {
			body = valueText(v)
		}
		return d.marginate(margin, []string{strings.TrimRight(d.marker+body, " ")})
	default:
		// Sequence element, and the synthetic container-add marker.
		body := d.valueLines(v)
		if !d.block {
			return d.marginate(margin, body)
		}
		out := make([]string, 0, len(body))
		for i, l := range body {
			if i == 0 {
				out = append(out, "- "+l)
				continue
			}
			out = append(out, "  "+l)
		}
		return d.marginate(margin, out)
	}
}

func (d dialect) marginate(margin byte, lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = string(margin) + " " + l
	}
	return out
}

// key spells a member name on a body line. The neutral dialect keeps the
// path-shaped spelling it has always emitted; a native one writes the name the
// way the target format writes it.
func (d dialect) key(name string) string {
	switch {
	case d.json:
		return strconv.Quote(name)
	case d.native && needsQuote(name):
		return strconv.Quote(name)
	}
	return escapeKey(name)
}

// isBlock reports whether the value renders as YAML block lines rather than
// inline. An empty collection has no block form — yaml writes it `{}` — so it
// is inline whatever its style says.
func (d dialect) isBlock(v Value) bool {
	n := v.Node()
	return d.block && n != nil && len(n.Content) > 0 &&
		(n.Kind == yaml.MappingNode || n.Kind == yaml.SequenceNode) &&
		n.Style&yaml.FlowStyle == 0
}

// valueLines renders a value as one or more body lines, never empty.
func (d dialect) valueLines(v Value) []string {
	n := v.Node()
	switch {
	case n == nil:
		return []string{"null"}
	case d.json:
		return []string{jsonText(n)}
	case d.quote && n.Kind == yaml.ScalarNode:
		// TOML and HCL have no bare-string spelling: `project = old-project`
		// is not a string in either grammar, so a string is written quoted
		// whether or not the neutral dialect would need to.
		if n.ShortTag() == "!!str" {
			return []string{strconv.Quote(n.Value)}
		}
		return []string{n.Value}
	case d.isBlock(v):
		return blockLines(n)
	case d.block && n.Kind == yaml.ScalarNode:
		return []string{yamlScalarText(n)}
	}
	return []string{renderValueText(v)}
}

// blockLines renders a collection in YAML block style, which is what makes an
// added mapping come back out with the shape it had in the new document
// (§9.4-R5) instead of collapsing into one flow line.
func blockLines(n *yaml.Node) []string {
	c := cloneNode(n)
	stripComments(c)
	var buf strings.Builder
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(c); err != nil {
		return []string{renderValueText(NodeValue(n))}
	}
	_ = enc.Close()
	return strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
}

func stripComments(n *yaml.Node) {
	if n == nil {
		return
	}
	n.HeadComment, n.LineComment, n.FootComment = "", "", ""
	for _, c := range n.Content {
		stripComments(c)
	}
}

// yamlScalarText keeps an explicitly double-quoted source scalar quoted, so a
// string that would otherwise read back as a number or a boolean survives the
// round trip as the string the author wrote.
func yamlScalarText(n *yaml.Node) string {
	if n.Style&yaml.DoubleQuotedStyle != 0 && n.ShortTag() == "!!str" {
		return strconv.Quote(n.Value)
	}
	return scalarLiteral(n)
}

// jsonText renders a value as JSON. JSONC's own comments never reach here: a
// comment is a body line of its own, not a value.
func jsonText(n *yaml.Node) string {
	switch n.Kind {
	case yaml.MappingNode:
		parts := make([]string, 0, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			parts = append(parts, strconv.Quote(n.Content[i].Value)+": "+jsonText(n.Content[i+1]))
		}
		if len(parts) == 0 {
			return "{}"
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case yaml.SequenceNode:
		parts := make([]string, 0, len(n.Content))
		for _, c := range n.Content {
			parts = append(parts, jsonText(c))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case yaml.AliasNode:
		return jsonText(n.Alias)
	}
	switch n.ShortTag() {
	case "!!bool", "!!int", "!!float":
		return n.Value
	case "!!null":
		return "null"
	}
	return strconv.Quote(n.Value)
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
