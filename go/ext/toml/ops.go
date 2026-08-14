package toml

import (
	"errors"
	"strconv"
	"strings"

	"github.com/benjaminabbitt/hew/go"
	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

// alreadyApplied is the §10.6 diagnostic: the after-image holds in full, so
// this is "already applied", not "drifted".
const alreadyApplied = "already applied: the after-image already holds here, and this hunk is strict; " +
	"add \"! idempotent\" (or the file-level \"idempotent: true\" pragma) if re-applying is expected (§7.5, §10.6)"

// --- test -------------------------------------------------------------------

// evalTest evaluates one OpTest, raising the code the failing MODE maps to. A
// plain value test is the before-image assert every context and "-" line
// compiles into (§9.0), so its failure is the characteristic drift error
// HEW010 — unless the after-image already holds, which §10.6 requires be
// distinguished before the code is chosen.
func (r *run) evalTest(t hew.Transform) error {
	switch {
	case t.Absent:
		_, he, final := r.resolve(t.Path, t.PatchLine)
		if he != nil {
			if final {
				return he
			}
			return nil // genuinely absent: satisfied
		}
		return r.err(hewerr.CodeAssertionFailed, t.Path.String(), t.PatchLine,
			"expected absent (? absent), but the node exists")
	case t.Exhaustive:
		return r.evalCount(t, "? exhaustive: the listed children are not the complete child set")
	case t.Count != nil:
		return r.evalCount(t, "? count: mismatch")
	case t.NodeKind != nil:
		rf, he, _ := r.resolve(t.Path, t.PatchLine)
		if he != nil {
			return he
		}
		got := nodeKind(rf.node)
		if got != *t.NodeKind {
			e := r.err(hewerr.CodeAssertionFailed, t.Path.String(), t.PatchLine, "? kind: mismatch")
			e.Want, e.Got = string(*t.NodeKind), string(got)
			return e
		}
		return nil
	}
	return r.evalValue(t)
}

func (r *run) evalCount(t hew.Transform, detail string) error {
	rf, he, _ := r.resolve(t.Path, t.PatchLine)
	if he != nil {
		return he
	}
	got, ok := childCount(rf.node)
	if !ok {
		return r.err(hewerr.CodeAssertionFailed, t.Path.String(), t.PatchLine, detail+": not a container")
	}
	if got != *t.Count {
		e := r.err(hewerr.CodeAssertionFailed, t.Path.String(), t.PatchLine, detail)
		e.Want, e.Got = strconv.Itoa(*t.Count), strconv.Itoa(got)
		return e
	}
	return nil
}

func (r *run) evalValue(t hew.Transform) error {
	rf, he, final := r.resolve(t.Path, t.PatchLine)
	if he != nil {
		if final {
			return he
		}
		if t.Optional {
			return nil // §7.6: satisfied whether or not the node exists
		}
		// The node is gone. If a remove at this path is what the patch wanted,
		// the after-image holds and this is "already applied" (§10.6).
		if w, ok := r.pairedWrite(t.Path); ok && w.Op == hew.OpRemove {
			return r.converge(t, w)
		}
		he.Code = hewerr.CodeStaleTarget
		he.Want = t.Value.String()
		return he
	}
	if rf.comment != nil {
		return r.testComment(t, rf)
	}
	if matches(rf.node, t.Value.Node()) {
		return nil
	}
	if w, ok := r.pairedWrite(t.Path); ok && w.Op != hew.OpRemove && equals(rf.node, w.Value.Node()) {
		return r.converge(t, w)
	}
	e := r.err(hewerr.CodeStaleTarget, t.Path.String(), t.PatchLine, "")
	e.Want, e.Got = t.Value.String(), r.d.describe(rf.node)
	return e
}

func (r *run) testComment(t hew.Transform, rf *ref) error {
	want, ok := commentText(t.Value)
	if !ok {
		return r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine,
			"a comment address takes a {comment: \"…\"} value (§4.5b)")
	}
	if want == rf.comment.text {
		return nil
	}
	e := r.err(hewerr.CodeStaleTarget, t.Path.String(), t.PatchLine, "comment text differs")
	e.Want, e.Got = want, rf.comment.text
	return e
}

// pairedWrite finds the write this test's before-image belongs to: the
// add/replace/remove at the same path in the same list.
func (r *run) pairedWrite(p hew.Path) (hew.Transform, bool) {
	for _, u := range r.all {
		if u.Op == hew.OpTest || !u.Path.Equal(p) {
			continue
		}
		switch u.Op {
		case hew.OpAdd, hew.OpReplace, hew.OpRemove:
			return u, true
		}
	}
	return hew.Transform{}, false
}

// converge decides what a failing before-image assert means when the
// after-image already holds (§7.5, §10.6).
func (r *run) converge(t hew.Transform, w hew.Transform) error {
	if t.Idempotent || w.Idempotent {
		r.converged[t.Path.String()] = true
		return nil
	}
	return r.err(hewerr.CodeAssertionFailed, t.Path.String(), t.PatchLine, alreadyApplied)
}

// --- add --------------------------------------------------------------------

// planAdd computes the edits for one OpAdd, or none if the add is satisfied
// with zero ops (`! default` over an existing key, or `! idempotent` over an
// equal one — §7.5, §7.7).
func (r *run) planAdd(t hew.Transform) ([]edit, error) {
	rf, he, final := r.resolve(t.Path, t.PatchLine)
	if he == nil {
		// A path that resolves to an ARRAY is the sequence-style add: the
		// container itself is addressed and the value is a new element. Any
		// other resolved node is a map-style add whose key already exists.
		if rf.node != nil && rf.node.kind == nSeq {
			return r.insertElement(rf.node, t)
		}
		return r.conflict(t, rf)
	}
	if final {
		return nil, he
	}
	return r.planCreate(t)
}

// conflict implements the add-semantics variants (OP-02/03/04, §7.7) for a
// node that already exists at the add's path.
func (r *run) conflict(t hew.Transform, rf *ref) ([]edit, error) {
	switch t.OnConflict {
	case hew.ConflictKeep:
		return nil, nil // `! default`: the user's value wins, zero ops
	case hew.ConflictReplace:
		// `! upsert` writes over a node that is already there, so it writes at
		// the surface that node already has. Honouring a `! surface` directive
		// would mean migrating one, which v0 does not do — and quietly not
		// honouring it is the silence §9.3 forbids.
		if t.Surface != "" {
			return nil, r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine,
				"\"! surface\" chooses where a CREATION goes; this path already exists, and rewriting an "+
					"existing path's surface is not a v0 operation (§8.4 rule 4, O10)")
		}
		return r.write(rf, t)
	}
	if t.Idempotent && equals(rf.node, t.Value.Node()) {
		return nil, nil
	}
	if r.converged[t.Path.String()] {
		return nil, r.err(hewerr.CodeAssertionFailed, t.Path.String(), t.PatchLine, alreadyApplied)
	}
	return nil, r.err(hewerr.CodeAlreadyExists, t.Path.String(), t.PatchLine,
		"add: node already exists; use \"! idempotent\" to re-apply, \"! upsert\" to overwrite, or \"! default\" to keep (§7.7)")
}

// planCreate writes a node that does not exist yet, choosing the SURFACE §8.4
// rules 3 and 4 prescribe: a creation adopts the surface of its nearest
// existing ancestor, unless a `! surface` directive overrides.
func (r *run) planCreate(t hew.Transform) ([]edit, error) {
	segs := t.Path.Segments()
	if len(segs) == 0 {
		return nil, r.err(hewerr.CodeNoMatch, t.Path.String(), t.PatchLine, "add: no container to insert into")
	}
	if segs[len(segs)-1].Kind == hew.SegComment {
		return r.addComment(t)
	}
	anc, rest, he := r.nearestAncestor(t.Path, t.PatchLine)
	if he != nil {
		return nil, he
	}
	names := make([]string, len(rest))
	for i, s := range rest {
		if s.Kind != hew.SegKey {
			return nil, r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine,
				"add: "+s.String()+" is not a key, and a TOML table's children are addressed by key (§8.4)")
		}
		names[i] = s.Name
	}
	if anc.node == nil || anc.node.kind != nTable {
		return nil, r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine,
			"add: the nearest existing ancestor is not a table")
	}
	full := append(append([]string{}, anc.node.path...), names...)

	switch {
	case t.Surface == hew.SurfaceTable:
		// §8.4 rule 4 overriding rule 3. The header belongs to the ADDED CHILD
		// — §2.3's exception, which toml/surface-directive-table pins — so the
		// whole path gets a block of its own.
		body, verr := tomlPairLines(t.Value.Node())
		if errors.Is(verr, errNotATable) {
			return nil, r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine,
				"add: \"! surface table\" asks for a ["+dottedKey(full)+"] header, which only a table value can fill (§8.4 rule 4)")
		}
		if verr != nil {
			return nil, r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine,
				"add: "+t.Value.String()+" has no TOML spelling (§8.4)")
		}
		return r.insertBlock(anc.node, "["+dottedKey(full)+"]", body, t, len(r.d.src))
	case anc.node.inline:
		if len(rest) != 1 {
			return nil, r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine,
				"add: an inline table cannot gain an intermediate table; write the whole value at "+dottedKey(anc.node.path))
		}
		return r.insertInline(anc.node, names[0], t)
	case t.Surface == hew.SurfaceDotted || (anc.node.holder != nil && len(rest) == 1):
		if anc.node.holder == nil {
			return nil, r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine,
				"add: \"! surface dotted\" needs an ancestor with a body to write the dotted key into (§8.4 rule 3)")
		}
		line, verr := r.assignLine(append(append([]string{}, anc.node.prefix...), names...), t)
		if verr != nil {
			return nil, verr
		}
		return r.insertLines(anc.node.holder, []string{line}, t)
	}
	// Rule 3's last clause: the parent has no body of its own, so it gets a
	// table header at the end of the document and the new key goes in it.
	line, verr := r.assignLine(names[len(names)-1:], t)
	if verr != nil {
		return nil, verr
	}
	return r.insertBlock(anc.node, "["+dottedKey(full[:len(full)-1])+"]", []string{line}, t, len(r.d.src))
}

// assignLine renders one `key = value` assignment for a creation.
func (r *run) assignLine(keys []string, t hew.Transform) (string, *hewerr.Error) {
	text, err := tomlValue(t.Value.Node())
	if err != nil {
		return "", r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine,
			"add: "+t.Value.String()+" has no TOML spelling (§8.4)")
	}
	return dottedKey(keys) + " = " + text, nil
}

// nearestAncestor walks up from a path until a prefix resolves, and returns
// that node together with the segments still to be created under it — §8.4
// rule 3's "the nearest existing ancestor".
func (r *run) nearestAncestor(p hew.Path, line int) (*ref, []hew.Segment, *hewerr.Error) {
	segs := p.Segments()
	for j := len(segs) - 1; j > 0; j-- {
		rf, he, final := r.resolve(hew.RootPath().Append(segs[:j]...), line)
		if he == nil {
			return rf, segs[j:], nil
		}
		if final {
			return nil, nil, he
		}
	}
	return &ref{node: r.d.root}, segs, nil
}

// addComment inserts a standalone comment line (OP-30) into the table its
// address names.
func (r *run) addComment(t hew.Transform) ([]edit, error) {
	parentPath, ok := t.Path.Parent()
	if !ok {
		return nil, r.err(hewerr.CodeNoMatch, t.Path.String(), t.PatchLine, "add: no container to insert into")
	}
	prf, phe, _ := r.resolve(parentPath, t.PatchLine)
	if phe != nil {
		return nil, phe
	}
	if prf.node == nil || !prf.node.physical {
		return nil, r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine,
			"add: a comment line needs a table with a body of its own to live in (§4.5b)")
	}
	text, ok := commentText(t.Value)
	if !ok {
		return nil, r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine,
			"add: a comment address takes a {comment: \"…\"} value (§4.5b)")
	}
	return r.insertLines(prf.node, []string{"# " + text}, t)
}

// insertElement adds one element to an array. An array-of-tables gains a
// `[[x]]` block; an inline array gains an item between its brackets.
func (r *run) insertElement(seq *tnode, t hew.Transform) ([]edit, error) {
	if !seq.aot {
		return r.insertItem(seq, t)
	}
	body, err := tomlPairLines(t.Value.Node())
	if errors.Is(err, errNotATable) {
		return nil, r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine,
			"add: an array-of-tables element is a table, and "+t.Value.String()+" is not (§8.4)")
	}
	if err != nil {
		return nil, r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine,
			"add: "+t.Value.String()+" has no TOML spelling (§8.4)")
	}
	// A new element goes after the last one, not at the end of the document:
	// tables belonging to some other path may well follow it.
	pos := len(r.d.src)
	if n := len(seq.elems); n > 0 {
		pos = seq.elems[n-1].blockEnd
	}
	return r.insertBlock(seq, "[["+dottedKey(seq.path)+"]]", body, t, pos)
}

// insertItem adds one item to an inline array, adopting the separator its
// siblings use.
func (r *run) insertItem(seq *tnode, t hew.Transform) ([]edit, error) {
	text, err := tomlValue(t.Value.Node())
	if err != nil {
		return nil, r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine,
			"add: "+t.Value.String()+" has no TOML spelling (§8.4)")
	}
	if len(seq.elems) == 0 {
		pos := seq.end - 1
		return []edit{{start: pos, end: pos, text: text}}, nil
	}
	i, err2 := r.placeAmong(elemSpans(seq), t)
	if err2 != nil {
		pos, ok := r.pendingAt(seq, t)
		if !ok {
			return nil, err2
		}
		r.remember(seq, t, pos)
		return []edit{{start: pos, end: pos, text: ", " + text}}, nil
	}
	if i < 0 {
		pos := seq.elems[0].blockStart
		r.remember(seq, t, pos)
		return []edit{{start: pos, end: pos, text: text + ", "}}, nil
	}
	pos := seq.elems[i].blockEnd
	r.remember(seq, t, pos)
	return []edit{{start: pos, end: pos, text: ", " + text}}, nil
}

// pendingAdd is one insertion this run has already planned: the container it
// lands in, what it creates, and where. §9.1 step 5 chains a run of `+` lines,
// each placed after the one above it, so a placement may name a sibling that is
// not in the parsed document at all but IS in this run's pending inserts.
type pendingAdd struct {
	holder *tnode
	path   hew.Path // the add's own address, or the container for an array item
	value  hew.Value
	pos    int
}

func (r *run) remember(holder *tnode, t hew.Transform, pos int) {
	r.pending = append(r.pending, pendingAdd{holder: holder, path: t.Path, value: t.Value, pos: pos})
}

// pendingAt reports the offset of an add this run has already planned that the
// transform's `after:` placement names. Landing at the SAME offset puts this
// add immediately behind it, because equal-offset edits keep their list order.
// Only `after:` is consulted: a forward placement never names an added
// sibling, because the sibling would not be there yet (§9.1 step 5).
func (r *run) pendingAt(holder *tnode, t hew.Transform) (int, bool) {
	// A zero path and the root both have no last segment to match on.
	p := t.After
	if p.Len() == 0 {
		return 0, false
	}
	seg := p.Segment(p.Len() - 1)
	// The most recent match wins, so a chain of three `+` lines walks forward
	// rather than collapsing onto the first.
	for i := len(r.pending) - 1; i >= 0; i-- {
		pa := r.pending[i]
		if pa.holder != holder {
			continue
		}
		if pa.path.Equal(p) || segNamesValue(seg, pa.value) {
			return pa.pos, true
		}
	}
	return 0, false
}

// placeAmong turns a before:/after: placement into the index of the sibling to
// insert AFTER, or -1 for "before them all". Without a placement the answer is
// the last sibling, which is §6.2's "insert at the end of the container".
func (r *run) placeAmong(spans []span, t hew.Transform) (int, *hewerr.Error) {
	p, after := t.After, true
	if p.IsZero() {
		p, after = t.Before, false
	}
	if p.IsZero() {
		return len(spans) - 1, nil
	}
	rf, he, _ := r.resolve(p, t.PatchLine)
	if he != nil {
		return 0, he
	}
	for i, s := range spans {
		if s.node != rf.node {
			continue
		}
		if after {
			return i, nil
		}
		return i - 1, nil
	}
	return 0, r.err(hewerr.CodeNoMatch, p.String(), t.PatchLine,
		"placement sibling is not a child of the container this add writes into")
}

// span is one sibling's insertion geometry: the node it holds and the source
// range it occupies.
type span struct {
	node       *tnode
	start, end int
}

func elemSpans(seq *tnode) []span {
	out := make([]span, len(seq.elems))
	for i, el := range seq.elems {
		out[i] = span{node: el.val, start: el.blockStart, end: el.blockEnd}
	}
	return out
}

// lineSpans lists the assignment lines physically written in a table's own
// body — the siblings an inserted line can be placed against. A dotted key
// counts here even though its member hangs off some deeper table, which is why
// `tool.ctxloom.retries` lands under `tool.ctxloom.timeout` rather than under
// whatever happens to sit at the root (§8.4 rule 1).
func lineSpans(holder *tnode) []span {
	out := make([]span, len(holder.lines))
	for i, e := range holder.lines {
		out[i] = span{node: e.val, start: e.blockStart, end: e.blockEnd}
	}
	return out
}

// insertLines splices whole lines into a physical table's body at the position
// §6.2 derives from the surrounding context.
func (r *run) insertLines(holder *tnode, lines []string, t hew.Transform) ([]edit, error) {
	pos, err := r.linePos(holder, t)
	if err != nil {
		return nil, err
	}
	text := strings.Join(lines, "\n") + "\n"
	if pos > 0 && r.d.src[pos-1] != '\n' {
		text = "\n" + text
	}
	r.remember(holder, t, pos)
	return []edit{{start: pos, end: pos, text: text}}, nil
}

// linePos is the offset a new line lands at in a table's body.
func (r *run) linePos(holder *tnode, t hew.Transform) (int, *hewerr.Error) {
	spans := lineSpans(holder)
	i, err := r.placeAmong(spans, t)
	if err != nil {
		if pos, ok := r.pendingAt(holder, t); ok {
			return pos, nil
		}
		return 0, err
	}
	switch {
	case i >= 0:
		return spans[i].end, nil
	case len(spans) > 0:
		return spans[0].start, nil
	}
	return holder.regionStart, nil
}

// insertBlock splices a `[a.b]` or `[[a.b]]` block, header line included. A
// table block is separated from its neighbours by a blank line, which is what
// toml/array-of-tables-add and toml/surface-directive-table both pin; the
// separator goes on whichever side the insertion did not already have one.
func (r *run) insertBlock(holder *tnode, header string, body []string, t hew.Transform, def int) ([]edit, error) {
	pos, before, err := r.blockPos(holder, t, def)
	if err != nil {
		return nil, err
	}
	r.remember(holder, t, pos)
	src := r.d.src
	text := strings.Join(append([]string{header}, body...), "\n") + "\n"
	switch {
	case pos == 0:
	case src[pos-1] != '\n':
		text = "\n\n" + text // the line before is unterminated: end it first
	case pos >= 2 && src[pos-2] == '\n':
		// already preceded by a blank line
	default:
		text = "\n" + text
	}
	if before && pos < len(src) && src[pos] != '\n' {
		text += "\n"
	}
	return []edit{{start: pos, end: pos, text: text}}, nil
}

// blockPos resolves a block insertion's before:/after: placement, defaulting
// to def when the transform carries none. The second result reports that the
// block is going in AHEAD of a sibling, which is what decides the side its
// blank-line separator lands on.
func (r *run) blockPos(holder *tnode, t hew.Transform, def int) (int, bool, *hewerr.Error) {
	p, after := t.After, true
	if p.IsZero() {
		p, after = t.Before, false
	}
	if p.IsZero() {
		return def, false, nil
	}
	rf, he, _ := r.resolve(p, t.PatchLine)
	if he != nil {
		if pos, ok := r.pendingAt(holder, t); ok {
			return pos, false, nil
		}
		return 0, false, he
	}
	start, end, ok := blockSpan(rf)
	if !ok {
		return 0, false, r.err(hewerr.CodeNoMatch, p.String(), t.PatchLine,
			"placement sibling occupies no block of its own to place a table against")
	}
	if after {
		return end, false, nil
	}
	return start, true, nil
}

// blockSpan is the source range a placement sibling occupies as a whole block:
// an array-of-tables element, a `[a.b]` table, or an assignment line.
func blockSpan(rf *ref) (int, int, bool) {
	switch {
	case rf.elem != nil:
		return rf.elem.blockStart, rf.elem.blockEnd, true
	case rf.node != nil && rf.node.physical && rf.node.blockEnd > rf.node.blockStart:
		return rf.node.blockStart, rf.node.blockEnd, true
	case rf.entry != nil && rf.entry.assign:
		return rf.entry.blockStart, rf.entry.blockEnd, true
	}
	return 0, 0, false
}

// insertInline adds a member to an inline table, which is §8.4 rule 3's "where
// only a.b = {} exists (inline table) it edits the inline table".
func (r *run) insertInline(n *tnode, name string, t hew.Transform) ([]edit, error) {
	text, err := tomlValue(t.Value.Node())
	if err != nil {
		return nil, r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine,
			"add: "+t.Value.String()+" has no TOML spelling (§8.4)")
	}
	pair := tomlKey(name) + " = " + text
	if len(n.entries) == 0 {
		pos := n.end - 1
		return []edit{{start: pos, end: pos, text: pair}}, nil
	}
	pos := n.entries[len(n.entries)-1].blockEnd
	return []edit{{start: pos, end: pos, text: ", " + pair}}, nil
}

// --- remove -----------------------------------------------------------------

func (r *run) planRemove(t hew.Transform) ([]edit, error) {
	rf, he, final := r.resolve(t.Path, t.PatchLine)
	if he != nil {
		if final {
			return nil, he
		}
		if t.Optional || t.Idempotent {
			return nil, nil // §7.6, §7.5: nothing to remove is success here
		}
		he.Detail = "remove: node does not exist"
		return nil, he
	}
	switch {
	case rf.comment != nil:
		return []edit{{start: rf.comment.lineStart, end: r.d.blockEndOf(rf.comment.lineStart)}}, nil
	case rf.elem != nil && rf.parent.inline:
		return []edit{cut(elemSpans(rf.parent), indexOf(elemSpans(rf.parent), rf.node))}, nil
	case rf.entry != nil && rf.parent.inline:
		return []edit{cut(memberSpans(rf.parent), indexOf(memberSpans(rf.parent), rf.node))}, nil
	}
	if rf.node == r.d.root {
		return nil, r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine,
			"remove: cannot remove the document root")
	}
	start, end, ok := blockSpan(rf)
	if !ok {
		return nil, r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine,
			"remove: this node is only implied by the keys around it and occupies no region of its own; "+
				"remove the assignments that name it instead (§8.4)")
	}
	return []edit{{start: start, end: end}}, nil
}

func memberSpans(n *tnode) []span {
	out := make([]span, len(n.entries))
	for i, e := range n.entries {
		out[i] = span{node: e.val, start: e.blockStart, end: e.blockEnd}
	}
	return out
}

func indexOf(spans []span, n *tnode) int {
	for i, s := range spans {
		if s.node == n {
			return i
		}
	}
	return 0
}

// cut deletes one member of an inline collection, taking the separator that
// joined it to its neighbours with it so the survivors stay well-formed.
func cut(spans []span, i int) edit {
	switch {
	case i > 0:
		return edit{start: spans[i-1].end, end: spans[i].end}
	case len(spans) > 1:
		return edit{start: spans[0].start, end: spans[1].start}
	}
	return edit{start: spans[i].start, end: spans[i].end}
}

// --- replace ----------------------------------------------------------------

func (r *run) planReplace(t hew.Transform) ([]edit, error) {
	rf, he, final := r.resolve(t.Path, t.PatchLine)
	if he != nil {
		if !final && he.Code == hewerr.CodeNoMatch {
			he.Detail = "replace requires the node to exist (OP-26); use add for an unconditional write"
		}
		return nil, he
	}
	if rf.comment == nil && equals(rf.node, t.Value.Node()) {
		if t.Idempotent {
			return nil, nil // converged; the file is already what the patch asks for
		}
		if r.converged[t.Path.String()] {
			return nil, r.err(hewerr.CodeAssertionFailed, t.Path.String(), t.PatchLine, alreadyApplied)
		}
		return nil, nil
	}
	return r.write(rf, t)
}

// write puts a transform's value at an already-resolved address. The address
// carries its own surface: a dotted key's value text and a table member's
// value text are both just spans, so the write lands wherever the target
// already spells the node (§8.4 rule 1) without this code choosing anything.
func (r *run) write(rf *ref, t hew.Transform) ([]edit, error) {
	if rf.comment != nil {
		text, ok := commentText(t.Value)
		if !ok {
			return nil, r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine,
				"a comment address takes a {comment: \"…\"} value (§4.5b)")
		}
		return []edit{{start: rf.comment.textStart, end: rf.comment.textEnd, text: text}}, nil
	}
	n := rf.node
	if n == nil || n.end <= n.start {
		return nil, r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine,
			"write: a table opened by a [a.b] header has no value text to overwrite; "+
				"rewriting an existing path's surface is not a v0 operation (§8.4 rule 4)")
	}
	text, err := tomlValue(t.Value.Node())
	if err != nil {
		return nil, r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine,
			"write: "+t.Value.String()+" has no TOML spelling (§8.4)")
	}
	return []edit{{start: n.start, end: n.end, text: text}}, nil
}

// --- copy -------------------------------------------------------------------

// planCopy takes its value by reference (§9.6) and splices the referenced
// member's RAW source bytes, its leading comment included — which is what
// makes a rename (copy + remove, Appendix C.1) carry the comment along instead
// of orphaning it.
func (r *run) planCopy(t hew.Transform) ([]edit, error) {
	frf, he, _ := r.resolve(t.From, t.PatchLine)
	if he != nil {
		return nil, he
	}
	if frf.entry == nil || !frf.entry.assign {
		return nil, r.err(hewerr.CodeInexpressible, t.From.String(), t.PatchLine,
			"copy: only a member written as a key/value assignment can be copied (§9.6)")
	}
	anc, rest, ahe := r.nearestAncestor(t.Path, t.PatchLine)
	if ahe != nil {
		return nil, ahe
	}
	if len(rest) != 1 || rest[0].Kind != hew.SegKey || anc.node.kind != nTable || anc.node.holder == nil {
		return nil, r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine,
			"copy: the destination must be a new key in a table with a body of its own (§9.6)")
	}
	key := dottedKey(append(append([]string{}, anc.node.prefix...), rest[0].Name))
	return r.insertLines(anc.node.holder, r.copyMember(frf.entry, key), t)
}

// copyMember renders a member's own source lines — leading comments, key,
// value — with the key token swapped. Everything but the key is the source's
// own bytes, which is how a copy preserves the value's formatting and how a
// rename keeps the comment attached to what it renames.
func (r *run) copyMember(e *entry, newKey string) []string {
	raw := strings.TrimRight(string(r.d.src[e.blockStart:e.blockEnd]), "\n")
	lines := strings.Split(raw, "\n")
	keyLine := strings.Count(string(r.d.src[e.blockStart:e.lineStart]), "\n")
	col := e.keyStart - e.lineStart
	lines[keyLine] = lines[keyLine][:col] + newKey + lines[keyLine][e.keyEnd-e.lineStart:]
	return lines
}
