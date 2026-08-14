package hew

import (
	"strings"

	"github.com/ctxloom/hew/internal/hewerr"
	"gopkg.in/yaml.v3"
)

// The §9.1 lowering algorithm: mirror fragment in, transform list out. It is
// purely textual — no target is read — which is what lets the parser
// establish loud staleness at parse time (§9.0): every context line and every
// `-` line becomes a test transform here, and nothing about drift detection
// lives in the applier.

// quals are the qualifiers an annotation rides onto the transforms it governs
// (§9.1 step 6). Each is applied only where §9.6 admits it, so an annotation
// governing a mixed run of transforms cannot produce an invalid record.
type quals struct {
	anchor     AnchorMode
	surface    Surface
	onConflict OnConflict
	optional   bool
	idempotent bool
	strict     bool // `! strict`: convergence off, overriding a file pragma (§7.5)
}

// over folds the more specific qualifier set q onto dst. Precedence is
// §7.5's: line beats hunk beats the file pragma beats the strict default.
func (q quals) over(dst *quals) {
	if q.strict {
		dst.idempotent = false
	}
	if q.idempotent {
		dst.idempotent = true
	}
	if q.optional {
		dst.optional = true
	}
	if q.anchor != "" {
		dst.anchor = q.anchor
	}
	if q.surface != "" {
		dst.surface = q.surface
	}
	if q.onConflict != "" {
		dst.onConflict = q.onConflict
	}
}

func (q quals) applyTo(t *Transform) {
	if q.anchor != "" {
		t.Anchor = q.anchor
	}
	if t.Op == OpAdd {
		if q.surface != "" {
			t.Surface = q.surface
		}
		if q.onConflict != "" {
			t.OnConflict = q.onConflict
		}
	}
	if q.optional && (t.Op == OpRemove || t.Op == OpTest) {
		t.Optional = true
	}
	if q.idempotent && (t.Op == OpAdd || t.Op == OpRemove || t.Op == OpReplace) {
		t.Idempotent = true
	}
}

// ordAnnot is a parsed `! match [label=[…]] ord=<n>` (§7.2).
type ordAnnot struct {
	ord       int
	labels    []string
	hasLabels bool
	line      int
}

// ordUse records where an ordinal selector landed, so the
// distinguishing-assert rule (§6.4.3 rule 2) can be checked once the hunk's
// transforms exist.
type ordUse struct {
	path Path   // the selected path, ordinal included
	show string // the path as authored, without the selector — what the error names
	line int
}

type lowerer struct {
	anchor Path
	out    []Transform
	ords   []ordUse
}

// lower compiles one hunk body into transforms (§9.1). filePragma is the
// preamble's `idempotent:` (§2.1, ruling O3); a hunk's own `! strict` or
// `! idempotent` overrides it.
func (h *Hunk) lower(body []bodyLine, filePragma bool) error {
	for _, bl := range body {
		if bl.margin == marginAdd || bl.margin == marginRemove {
			h.assertOnly = false
			break
		}
		h.assertOnly = true
	}
	r := &bodyReader{lines: body}
	entries, err := r.entries(body[0].indent)
	if err != nil {
		return err
	}
	if r.i < len(r.lines) {
		bl := r.lines[r.i]
		return parseErr(bl.num, "", "unexpected indentation: %d spaces where %d were expected (§2.3)", bl.indent, body[0].indent)
	}

	nodes, surface, hunkQ, hunkOrd, err := attach(entries, true)
	if err != nil {
		return err
	}
	q := quals{idempotent: filePragma}
	hunkQ.over(&q)

	l := &lowerer{}
	if hunkOrd != nil {
		if err := checkLabels(hunkOrd, anchorLabels(h.anchor), h.anchor.String()); err != nil {
			return err
		}
		show := h.anchor.String()
		h.anchor, err = withOrdinal(h.anchor, hunkOrd.ord, show)
		if err != nil {
			return err
		}
		l.ords = append(l.ords, ordUse{path: h.anchor, show: show, line: hunkOrd.line})
	}
	l.anchor = h.anchor

	if err := l.emit(h.anchor, nodes, surface, q); err != nil {
		return err
	}
	if err := l.checkOrdinals(); err != nil {
		return err
	}
	h.transforms = l.out
	return nil
}

// attach resolves §7's three attachment classes over one container's entry
// list. Free-standing assertions stay in the list, in place, because their
// transforms are emitted where they were written; container-scoped `! surface`
// is lifted out; line-scoped directives are folded onto the entry they
// govern. When top is set, a line-scoped directive written as the first body
// line governs the anchor instead.
func attach(entries []*mirrorEntry, top bool) (nodes []*mirrorEntry, surface Surface, hunkQ quals, hunkOrd *ordAnnot, err error) {
	fail := func(e error) ([]*mirrorEntry, Surface, quals, *ordAnnot, error) {
		return nil, "", quals{}, nil, e
	}
	var pending quals
	var pendOrd *ordAnnot
	pendLine := 0
	for i, e := range entries {
		if e.node() {
			e.q, e.ord = pending, pendOrd
			pending, pendOrd, pendLine = quals{}, nil, 0
			nodes = append(nodes, e)
			continue
		}
		cls, cerr := classifyAnnot(e.annot)
		if cerr != nil {
			return fail(cerr)
		}
		switch cls {
		case annotFree:
			nodes = append(nodes, e)
		case annotContainer:
			if e.annot.verb == verbSurface {
				s, serr := parseSurface(e.annot)
				if serr != nil {
					return fail(serr)
				}
				surface = s
				continue
			}
			nodes = append(nodes, e) // `? exhaustive`, emitted in place
		case annotLine:
			toAnchor, aerr := anchorScoped(entries, i, top)
			if aerr != nil {
				return fail(aerr)
			}
			target, ordSlot := &pending, &pendOrd
			if toAnchor {
				target, ordSlot = &hunkQ, &hunkOrd
			} else {
				pendLine = e.annot.line
			}
			if derr := applyDirective(e.annot, target, ordSlot); derr != nil {
				return fail(derr)
			}
		}
	}
	if pendLine != 0 {
		return fail(parseErr(pendLine, "", "line-scoped annotation is not followed by a body line (§7)"))
	}
	return nodes, surface, hunkQ, hunkOrd, nil
}

// anchorScoped decides whether a line-scoped directive governs the hunk's
// anchor rather than the body line after it. §7's rule is positional — the
// first body line of a hunk governs the anchor — with one refinement §7.2
// spells out and the corpus pins: `! match` is written "in place, beside the
// block it selects", so an ordinal followed by a block header selects THAT
// block even when it stands first (hcl/repeated-label-ordinal, whose anchor
// is the document root). Only when no block line follows it is the ordinal
// the hunk-anchored form (hcl/ordinal-shifted-target).
func anchorScoped(entries []*mirrorEntry, i int, top bool) (bool, error) {
	first := top && i == 0
	if entries[i].annot.verb != verbMatch {
		return first, nil
	}
	for j := i + 1; j < len(entries); j++ {
		if !entries[j].node() {
			continue
		}
		if entries[j].kind == mBlock {
			return false, nil
		}
		break
	}
	if first {
		return true, nil
	}
	return false, parseErr(entries[i].annot.line, "",
		"`! match` must precede the block it selects, or stand as the hunk's first body line (§7.2)")
}

// emit lowers one container's entries. Order is §9.1's: the container's
// assertions and before-image tests first, in body order, then its mutations
// in body order. A context container is processed in full where it stands, so
// a nested block's tests and edits stay together — which is what
// hcl/repeated-label-ordinal pins.
func (l *lowerer) emit(path Path, nodes []*mirrorEntry, surface Surface, q quals) error {
	assignCommentIndices(nodes)
	q.surface = pickSurface(surface, q.surface)

	for _, e := range nodes {
		var err error
		switch {
		case !e.node():
			err = l.freeAssert(path, e)
		case e.inBefore():
			err = l.tests(path, e, q)
		}
		if err != nil {
			return err
		}
	}

	paired, err := pairings(path, nodes)
	if err != nil {
		return err
	}
	for i, e := range nodes {
		if !e.node() || e.margin == marginContext {
			continue
		}
		eq := q
		e.q.over(&eq)
		switch e.margin {
		case marginRemove:
			if paired[i] >= 0 {
				continue // the `+` at the same position emits the replace
			}
			p, perr := l.entryPath(path, e, true)
			if perr != nil {
				return perr
			}
			l.push(Transform{Op: OpRemove, Path: p}, e, eq)
		case marginAdd:
			if err := l.emitAdd(path, nodes, i, paired[i] >= 0, eq); err != nil {
				return err
			}
		}
	}
	return nil
}

func pickSurface(container, inherited Surface) Surface {
	if container != "" {
		return container
	}
	return inherited
}

func (l *lowerer) emitAdd(path Path, nodes []*mirrorEntry, i int, replace bool, q quals) error {
	e := nodes[i]
	target, err := l.entryPath(path, e, false)
	if err != nil {
		return err
	}
	val, err := entryValue(e)
	if err != nil {
		return err
	}
	if replace {
		l.push(Transform{Op: OpReplace, Path: target, Value: val}, e, q)
		return nil
	}
	t := Transform{Op: OpAdd, Value: val, Path: target}
	if positional(e) {
		// A sequence element is added to its container; the address of the
		// element it lands beside carries the position (§9.6 placement).
		t.Path = path
	}
	before, after, err := l.placement(path, nodes, i)
	if err != nil {
		return err
	}
	t.Before, t.After = before, after
	l.push(t, e, q)
	return nil
}

// placement derives an added node's position from the context around it
// (§6.2), as a relative placement and never a numeric index (§9.1 step 5):
// after the nearest preceding after-image sibling, else before the nearest
// following one, else nothing, meaning the end of the container.
func (l *lowerer) placement(path Path, nodes []*mirrorEntry, i int) (before, after Path, err error) {
	for j := i - 1; j >= 0; j-- {
		if !nodes[j].node() || !nodes[j].inAfter() {
			continue
		}
		p, perr := l.entryPath(path, nodes[j], false)
		return Path{}, p, perr
	}
	for j := i + 1; j < len(nodes); j++ {
		if !nodes[j].node() || !nodes[j].inAfter() {
			continue
		}
		p, perr := l.entryPath(path, nodes[j], false)
		return p, Path{}, perr
	}
	return Path{}, Path{}, nil
}

// tests emits the before-image assertions for one entry (§9.1 step 2). A
// context container is lowered in full — its own edits included — where it
// stands; a removed container contributes assertions only, because the
// container itself is what gets removed.
func (l *lowerer) tests(path Path, e *mirrorEntry, q quals) error {
	eq := q
	e.q.over(&eq)
	p, err := l.entryPath(path, e, true)
	if err != nil {
		return err
	}
	switch {
	case e.kind == mComment:
		l.push(Transform{Op: OpTest, Path: p, Value: commentValue(e.comment)}, e, eq)
		return nil
	case e.container():
		if e.margin == marginContext {
			return l.container(p, e.children, eq)
		}
		return l.testsOnly(p, e.children, eq)
	case positional(e) && e.inline.mapping():
		// A keyed element written inline is matched as a subset: one test
		// per listed field, never one for the element (§5.1, §9.1 step 2).
		return l.fieldTests(p, e, eq)
	default:
		l.push(Transform{Op: OpTest, Path: p, Value: e.inline}, e, eq)
		return nil
	}
}

func (l *lowerer) fieldTests(elem Path, e *mirrorEntry, q quals) error {
	n := e.inline.Node()
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, err := mappingKey(n.Content[i], e.line)
		if err != nil {
			return err
		}
		l.push(Transform{
			Op:    OpTest,
			Path:  elem.Append(Segment{Kind: SegKey, Name: key}),
			Value: NodeValue(cloneNode(n.Content[i+1])),
		}, e, q)
	}
	return nil
}

// container lowers a nested container: its own annotations are attached
// within it, and its transforms are emitted where the container line stands.
func (l *lowerer) container(path Path, entries []*mirrorEntry, q quals) error {
	nodes, surface, _, _, err := attach(entries, false)
	if err != nil {
		return err
	}
	return l.emit(path, nodes, surface, q)
}

// testsOnly emits a removed subtree's assertions without its mutations: the
// children of a removed container are asserted, but only the container itself
// is removed (§9.1 steps 2 and 4).
func (l *lowerer) testsOnly(path Path, entries []*mirrorEntry, q quals) error {
	nodes, _, _, _, err := attach(entries, false)
	if err != nil {
		return err
	}
	assignCommentIndices(nodes)
	for _, e := range nodes {
		var err error
		switch {
		case !e.node():
			err = l.freeAssert(path, e)
		case e.inBefore():
			err = l.tests(path, e, q)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// freeAssert emits a `?` annotation's transform (§9.1 step 3, §7.1).
func (l *lowerer) freeAssert(path Path, e *mirrorEntry) error {
	a := e.annot
	t := Transform{Op: OpTest, PatchLine: a.line}
	switch a.verb {
	case verbExhaustive:
		n := 0
		for _, sib := range e.siblings {
			if sib.node() && sib.inBefore() {
				n++
			}
		}
		t.Path, t.Count = path, &n
	case verbExpect, verbCount, verbKind:
		target, val, err := splitAssertion(a)
		if err != nil {
			return err
		}
		if t.Path, err = l.resolve(target, a.line); err != nil {
			return err
		}
		switch a.verb {
		case verbExpect:
			v, verr := parseMirrorValue(val, a.line)
			if verr != nil {
				return verr
			}
			t.Value = v
		case verbCount:
			n, cerr := parseCount(val, a.line)
			if cerr != nil {
				return cerr
			}
			t.Count = &n
		case verbKind:
			k := NodeKind(val)
			if !k.Valid() {
				return parseErr(a.line, t.Path.String(), "unknown node kind %q; §7.1 defines map|seq|scalar|block|section", val)
			}
			t.NodeKind = &k
		}
	case verbAbsent:
		p, err := l.resolve(strings.TrimSpace(a.rest), a.line)
		if err != nil {
			return err
		}
		t.Path, t.Absent = p, true
	default:
		return parseErr(a.line, "", "unknown assertion %q (§7.1)", a.verb)
	}
	t.Value = topValue(t.Value)
	l.out = append(l.out, t)
	return nil
}

// resolve reads an annotation's path argument, expanding a `.`-relative path
// against the enclosing hunk's anchor (§4.6).
func (l *lowerer) resolve(text string, line int) (Path, error) {
	p, err := ParseAuthoredPath(text)
	if err != nil {
		if he, ok := hewerr.As(err); ok {
			he.PatchLine = line
		}
		return Path{}, err
	}
	if !p.IsRelative() {
		return p, nil
	}
	return l.anchor.Append(p.Segments()...), nil
}

func (l *lowerer) push(t Transform, e *mirrorEntry, q quals) {
	q.applyTo(&t)
	t.Value = topValue(t.Value)
	t.PatchLine = e.line
	l.out = append(l.out, t)
}

// entryPath is an entry's address within its container, in the requested
// image: comment addresses differ between the two projections because a
// comment is addressed by its position among the comments of the image it
// belongs to (§4.5b).
func (l *lowerer) entryPath(path Path, e *mirrorEntry, before bool) (Path, error) {
	p, err := entrySegments(path, e, before)
	if err != nil {
		return Path{}, err
	}
	if e.ord == nil {
		return p, nil
	}
	if err := checkLabels(e.ord, entryLabels(e), p.String()); err != nil {
		return Path{}, err
	}
	show := p.String()
	p, err = withOrdinal(p, e.ord.ord, show)
	if err != nil {
		return Path{}, err
	}
	l.noteOrdinal(ordUse{path: p, show: show, line: e.ord.line})
	return p, nil
}

func (l *lowerer) noteOrdinal(u ordUse) {
	for _, have := range l.ords {
		if have.path.Equal(u.path) {
			return
		}
	}
	l.ords = append(l.ords, u)
}

func entrySegments(path Path, e *mirrorEntry, before bool) (Path, error) {
	switch e.kind {
	case mKV:
		return path.Append(Segment{Kind: SegKey, Name: e.key}), nil
	case mBlock:
		segs := []Segment{{Kind: SegKey, Name: e.labels[0]}}
		for _, label := range e.labels[1:] {
			segs = append(segs, Segment{Kind: SegLabel, Name: label})
		}
		return path.Append(segs...), nil
	case mTable:
		rest := stripPathPrefix(path, e.labels)
		if len(rest) == 0 {
			return identityPath(path, e)
		}
		segs := make([]Segment, 0, len(rest))
		for _, name := range rest {
			segs = append(segs, Segment{Kind: SegKey, Name: name})
		}
		return path.Append(segs...), nil
	case mComment:
		idx := e.afterIdx
		if before {
			idx = e.beforeIdx
		}
		if idx < 0 {
			return Path{}, parseErr(e.line, path.String(), "comment node is in neither projection (§5)")
		}
		return path.Append(Segment{Kind: SegComment, Index: idx}), nil
	}
	return identityPath(path, e)
}

// identityPath addresses a sequence element by identity rather than by
// position (§4.2, §6.4.2): `=value` for a scalar element, `field=value` for a
// keyed one. The parser has no target to count against, so a positional
// address is not available to it even as a fallback.
func identityPath(path Path, e *mirrorEntry) (Path, error) {
	if v, ok := elementScalar(e); ok {
		s, serr := matchScalar(v, e.line)
		if serr != nil {
			return Path{}, serr
		}
		return path.Append(Segment{Kind: SegMatch, Value: s}), nil
	}
	name, val, ok := elementKeyField(e)
	if !ok {
		return Path{}, parseErr(e.line, path.String(),
			"sequence element has no usable identity field: a key must be present with a scalar value (§6.4.2)")
	}
	s, err := matchScalar(val, e.line)
	if err != nil {
		return Path{}, err
	}
	return path.Append(Segment{Kind: SegMatch, Name: name, Value: s}), nil
}

// stripPathPrefix drops the leading components of a TOML table header that
// merely restate the enclosing container's path: `[mcp_servers.taskloom]`
// under `@@ /mcp_servers @@` introduces the child `taskloom` (§8.4).
func stripPathPrefix(path Path, comps []string) []string {
	segs := path.Segments()
	n := 0
	for n < len(comps) && n < len(segs) {
		if segs[len(segs)-1-n].Kind != SegKey {
			break
		}
		n++
	}
	for k := n; k > 0; k-- {
		if matchesTail(segs, comps[:k]) {
			return comps[k:]
		}
	}
	return comps
}

func matchesTail(segs []Segment, comps []string) bool {
	if len(comps) > len(segs) {
		return false
	}
	tail := segs[len(segs)-len(comps):]
	for i, c := range comps {
		if tail[i].Kind != SegKey || tail[i].Name != c {
			return false
		}
	}
	return true
}

// positional reports an entry addressed by identity within a sequence rather
// than by a key of its own.
func positional(e *mirrorEntry) bool {
	switch e.kind {
	case mSeqItem, mScalar:
		return true
	case mTable:
		return e.array
	}
	return false
}

// assignCommentIndices numbers a container's comment nodes within each
// projection (§4.5b, §5).
func assignCommentIndices(nodes []*mirrorEntry) {
	for _, e := range nodes {
		e.siblings = nodes
	}
	before, after := 0, 0
	for _, e := range nodes {
		if e.kind != mComment {
			continue
		}
		if e.inBefore() {
			e.beforeIdx = before
			before++
		}
		if e.inAfter() {
			e.afterIdx = after
			after++
		}
	}
}

// pairings matches each `+` entry with the `-` entry it replaces (§9.1 steps
// 4 and 5). A keyed member pairs with the same key anywhere in the container,
// because a map has no positions worth the name (§6.4.1); a positional
// element pairs by offset within an adjacent `-`/`+` run, which is what "at
// the same position" means for a sequence.
func pairings(path Path, nodes []*mirrorEntry) ([]int, error) {
	paired := make([]int, len(nodes))
	for i := range paired {
		paired[i] = -1
	}
	keyOf := func(e *mirrorEntry, before bool) (string, bool, error) {
		if positional(e) {
			return "", false, nil
		}
		p, err := entrySegments(path, e, before)
		if err != nil {
			return "", false, err
		}
		return p.String(), true, nil
	}
	removes := map[string]int{}
	for i, e := range nodes {
		if !e.node() || e.margin != marginRemove {
			continue
		}
		k, ok, err := keyOf(e, true)
		if err != nil {
			return nil, err
		}
		if ok {
			removes[k] = i
		}
	}
	for i, e := range nodes {
		if !e.node() || e.margin != marginAdd {
			continue
		}
		k, ok, err := keyOf(e, false)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		if j, found := removes[k]; found && paired[j] < 0 {
			paired[i], paired[j] = j, i
		}
	}
	pairRuns(nodes, paired)
	return paired, nil
}

// pairRuns pairs positional entries: the k'th element of a `-` run with the
// k'th of the `+` run that immediately follows it.
func pairRuns(nodes []*mirrorEntry, paired []int) {
	var run []int
	flush := func(adds []int) {
		for k := 0; k < len(run) && k < len(adds); k++ {
			paired[run[k]], paired[adds[k]] = adds[k], run[k]
		}
		run = nil
	}
	for i := 0; i < len(nodes); i++ {
		e := nodes[i]
		if !e.node() {
			continue
		}
		switch {
		case e.margin == marginRemove && positional(e) && paired[i] < 0:
			run = append(run, i)
		case e.margin == marginAdd && positional(e) && paired[i] < 0:
			var adds []int
			for ; i < len(nodes); i++ {
				if !nodes[i].node() {
					continue
				}
				if nodes[i].margin != marginAdd || !positional(nodes[i]) || paired[i] >= 0 {
					break
				}
				adds = append(adds, i)
			}
			i--
			flush(adds)
		default:
			run = nil
		}
	}
}

// checkOrdinals enforces §6.4.3 rule 2: an ordinal-addressed hunk MUST carry
// at least one distinguishing assert, so that a shifted target fails loudly
// instead of patching the wrong block. The parser can see whether an
// assertion exists; whether it distinguishes anything is the target's
// business, and the corpus splits the two (hcl/ordinal-without-distinguishing-assert
// versus hcl/ordinal-shifted-target).
func (l *lowerer) checkOrdinals() error {
	for _, u := range l.ords {
		found := false
		for i := range l.out {
			t := &l.out[i]
			if t.Op == OpTest && strictlyUnder(u.path, t.Path) {
				found = true
				break
			}
		}
		if !found {
			return parseErr(u.line, u.show,
				"`! match ord=` carries no distinguishing assert: an ordinal-addressed hunk MUST assert a child that differs between the same-label siblings, or a shifted target would be silently patched (§6.4.3, §7.2)")
		}
	}
	return nil
}

func strictlyUnder(parent, p Path) bool {
	if p.Len() <= parent.Len() || p.IsRelative() != parent.IsRelative() {
		return false
	}
	for i := 0; i < parent.Len(); i++ {
		if !p.Segment(i).Equal(parent.Segment(i)) {
			return false
		}
	}
	return true
}

// withOrdinal puts a `! match ord=` selector on a path's last segment: an
// ordinal is an addressing mode, and belongs with the address once it is out
// of human hands (§9.6, §11.10 reduction 4).
func withOrdinal(p Path, ord int, show string) (Path, error) {
	segs := p.Segments()
	if len(segs) == 0 {
		return Path{}, parseErr(0, show, "`! match ord=` cannot select the document root (§7.2)")
	}
	n := ord
	segs[len(segs)-1].Ordinal = &n
	if p.IsRelative() {
		return NewRelativePath(segs...), nil
	}
	return NewPath(segs...), nil
}

// checkLabels performs the redundant `label=[…]` cross-check of §7.2: when
// present it is checked against the selected block's actual labels, and a
// mismatch is HEW011.
func checkLabels(o *ordAnnot, actual []string, path string) error {
	if !o.hasLabels {
		return nil
	}
	if len(o.labels) != len(actual) {
		return assertErr(o.line, path, "! match label=%v does not match the selected block's labels %v (§7.2)", o.labels, actual)
	}
	for i := range o.labels {
		if o.labels[i] != actual[i] {
			return assertErr(o.line, path, "! match label=%v does not match the selected block's labels %v (§7.2)", o.labels, actual)
		}
	}
	return nil
}

func entryLabels(e *mirrorEntry) []string {
	if e.kind == mBlock && len(e.labels) > 0 {
		return e.labels[1:]
	}
	return nil
}

func anchorLabels(p Path) []string {
	var out []string
	for i := 0; i < p.Len(); i++ {
		if s := p.Segment(i); s.Kind == SegLabel {
			out = append(out, s.Name)
		}
	}
	return out
}

// entryValue is the value an added or replaced entry writes: its inline
// value, or the document its mirrored subtree denotes.
func entryValue(e *mirrorEntry) (Value, error) {
	if e.kind == mComment {
		return commentValue(e.comment), nil
	}
	if !e.container() {
		return e.inline, nil
	}
	n, err := subtreeNode(e)
	if err != nil {
		return Value{}, err
	}
	return NodeValue(n), nil
}

// subtreeNode builds the value a container entry's children denote. Comments
// inside an added container have no address to be added at and no place in a
// value, so they are refused rather than dropped (§4.5b, §10).
func subtreeNode(e *mirrorEntry) (*yaml.Node, error) {
	seq := false
	for _, c := range e.children {
		if !c.node() {
			continue
		}
		if c.kind == mComment {
			return nil, &hewerr.Error{
				Code:      hewerr.CodeInexpressible,
				Component: hewerr.ComponentParser,
				PatchLine: c.line,
				Detail:    "a comment inside an added container has no address in the IR; write it as its own line beside the container (§4.5b, §11.10)",
			}
		}
		seq = positional(c)
		break
	}
	if seq {
		out := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, c := range e.children {
			if !c.node() {
				continue
			}
			child, err := childNode(c)
			if err != nil {
				return nil, err
			}
			out.Content = append(out.Content, child)
		}
		return out, nil
	}
	out := mapNode()
	for _, c := range e.children {
		if !c.node() {
			continue
		}
		key, err := childKey(c)
		if err != nil {
			return nil, err
		}
		child, err := childNode(c)
		if err != nil {
			return nil, err
		}
		put(out, key, child)
	}
	return out, nil
}

func childKey(c *mirrorEntry) (string, error) {
	switch c.kind {
	case mKV:
		return c.key, nil
	case mBlock, mTable:
		return strings.Join(c.labels, "."), nil
	}
	return "", parseErr(c.line, "", "cannot write this line as a member of the container being added (§5)")
}

func childNode(c *mirrorEntry) (*yaml.Node, error) {
	v, err := entryValue(c)
	if err != nil {
		return nil, err
	}
	if v.IsZero() {
		return nil, parseErr(c.line, "", "member has no value (§5)")
	}
	return cloneNode(v.Node()), nil
}

// elementScalar reports a sequence element that is a bare scalar rather than
// a keyed object.
func elementScalar(e *mirrorEntry) (*yaml.Node, bool) {
	if n := e.inline.Node(); n != nil && n.Kind == yaml.ScalarNode {
		return n, true
	}
	return nil, false
}

// elementKeyField chooses the field that identifies a keyed element. §9.4-R4
// fixes the preference order for the differ; the parser follows it so that a
// hand-written element and a generated one address the same node, falling
// back to the first field the author listed.
func elementKeyField(e *mirrorEntry) (string, *yaml.Node, bool) {
	type field struct {
		name string
		val  *yaml.Node
	}
	var fields []field
	if e.container() {
		for _, c := range e.children {
			if c.kind != mKV || c.container() {
				continue
			}
			if n := c.inline.Node(); n != nil && n.Kind == yaml.ScalarNode {
				fields = append(fields, field{c.key, n})
			}
		}
	} else if n := e.inline.Node(); n != nil && n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Kind == yaml.ScalarNode && n.Content[i+1].Kind == yaml.ScalarNode {
				fields = append(fields, field{n.Content[i].Value, n.Content[i+1]})
			}
		}
	}
	if len(fields) == 0 {
		return "", nil, false
	}
	for _, want := range []string{"name", "id", "key"} {
		for _, f := range fields {
			if f.name == want {
				return f.name, f.val, true
			}
		}
	}
	return fields[0].name, fields[0].val, true
}

func mappingKey(n *yaml.Node, line int) (string, error) {
	if n.Kind != yaml.ScalarNode {
		return "", parseErr(line, "", "object key is not a scalar (§5)")
	}
	return n.Value, nil
}

// mapping reports a value that is an inline object.
func (v Value) mapping() bool {
	n := v.Node()
	return n != nil && n.Kind == yaml.MappingNode
}

// matchScalar converts a decoded value into a key-match segment's scalar
// (§4.2). The rendered form must read back as the same scalar, so a string
// that would otherwise resolve to a number, a boolean or null is quoted.
func matchScalar(n *yaml.Node, line int) (Scalar, error) {
	switch n.ShortTag() {
	case "!!null":
		return Scalar{Kind: ScalarNull, Text: "null"}, nil
	case "!!bool":
		var b bool
		if err := n.Decode(&b); err != nil {
			return Scalar{}, parseErr(line, "", "cannot address by the boolean %q (§4.2)", n.Value)
		}
		if b {
			return Scalar{Kind: ScalarBool, Text: "true"}, nil
		}
		return Scalar{Kind: ScalarBool, Text: "false"}, nil
	case "!!int", "!!float":
		if isNumber(n.Value) {
			return Scalar{Kind: ScalarNumber, Text: n.Value}, nil
		}
	}
	return Scalar{Kind: ScalarString, Text: n.Value, Quoted: needsQuote(n.Value)}, nil
}

// needsQuote reports a string whose bare spelling would decode as something
// else — a number, a boolean, null, or a token the path grammar would split.
func needsQuote(s string) bool {
	switch s {
	case "", "true", "false", "null":
		return true
	}
	if strings.ContainsAny(s, " \t\"") {
		return true
	}
	return isNumber(s)
}
