package jsonc

import (
	"strconv"

	"github.com/benjaminabbitt/hew/go"
	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

// --- test -------------------------------------------------------------------

// evalTest evaluates one OpTest, raising the code the failing MODE maps to.
// Plain-value failures are HEW010 stale-target (the row §10 names for context
// lines, and the one a lowered `-` line is indistinguishable from); the
// dedicated assertion modes are HEW011.
func (d *doc) evalTest(target string, t hew.Transform) error {
	switch {
	case t.Absent:
		if _, err := d.resolveTestRef(target, t); err != nil {
			if he, ok := err.(*hewerr.Error); ok && he.Code == hewerr.CodeAmbiguousMatch {
				return err
			}
			return nil // genuinely absent: satisfied
		}
		return appErr(hewerr.CodeAssertionFailed, target, t.Path.String(), t.PatchLine,
			"expected absent (? absent), but the node exists")
	case t.Exhaustive:
		return d.evalCount(target, t, "? exhaustive: the listed children are not the complete child set")
	case t.Count != nil:
		return d.evalCount(target, t, "? count: mismatch")
	case t.NodeKind != nil:
		r, err := d.resolveTestRef(target, t)
		if err != nil {
			return err
		}
		if r.cmt != nil {
			return appErr(hewerr.CodeAssertionFailed, target, t.Path.String(), t.PatchLine,
				"? kind: a comment node has no assertable kind")
		}
		got := nodeKindOf(r.node)
		if got != *t.NodeKind {
			e := appErr(hewerr.CodeAssertionFailed, target, t.Path.String(), t.PatchLine, "? kind: mismatch")
			e.Want, e.Got = string(*t.NodeKind), string(got)
			return e
		}
		return nil
	default:
		return d.evalValue(target, t)
	}
}

func (d *doc) evalCount(target string, t hew.Transform, detail string) error {
	r, err := d.resolveTestRef(target, t)
	if err != nil {
		return err
	}
	if r.cmt != nil {
		return appErr(hewerr.CodeAssertionFailed, target, t.Path.String(), t.PatchLine, detail+" (not a container)")
	}
	got, kerr := containerCount(r.node)
	if kerr != nil {
		return appErr(hewerr.CodeAssertionFailed, target, t.Path.String(), t.PatchLine, detail+": "+kerr.Error())
	}
	if got != *t.Count {
		e := appErr(hewerr.CodeAssertionFailed, target, t.Path.String(), t.PatchLine, detail)
		e.Want, e.Got = strconv.Itoa(*t.Count), strconv.Itoa(got)
		return e
	}
	return nil
}

func (d *doc) evalValue(target string, t hew.Transform) error {
	r, err := d.resolveTestRef(target, t)
	if err != nil {
		if he, ok := err.(*hewerr.Error); ok {
			if he.Code == hewerr.CodeAmbiguousMatch {
				return he
			}
			he.Code = hewerr.CodeStaleTarget
			he.Want = t.Value.String()
		}
		return err
	}
	if r.cmt != nil {
		want, ok := commentTextOf(t.Value)
		if ok && want == r.cmt.text {
			return nil
		}
		e := appErr(hewerr.CodeStaleTarget, target, t.Path.String(), t.PatchLine, "")
		e.Want, e.Got = t.Value.String(), r.cmt.text
		return e
	}
	if r.node.matches(t.Value.Node()) {
		return nil
	}
	got, verr := r.node.hewValue()
	if verr != nil {
		return appErr(hewerr.CodeTargetParse, target, t.Path.String(), t.PatchLine, verr.Error())
	}
	e := appErr(hewerr.CodeStaleTarget, target, t.Path.String(), t.PatchLine, "")
	e.Want, e.Got = t.Value.String(), got.String()
	// §10.3's `found` line names WHERE in the target, not only what.
	e.TargetLine = lineOf(d.src, r.node.start)
	return e
}

// resolveTestRef resolves an assertion's path, with one tolerance a comment
// address needs and no other address does.
//
// A `#n` ordinal counts comment nodes within a container, but a hunk body is a
// PARTIAL mirror of that container (§6.1) — the ordinal a lowered patch
// carries counts only the comments the patch itself wrote down, which is a
// different sequence from the target's whenever the patch skipped one.
// corpus/jsonc/delete-key-with-comment is exactly that: it names one comment
// of a container that holds two. So an assertion that already states the
// comment's text resolves BY that text (§6.1's "Comment ... exact text" row),
// using the ordinal only to choose among repeats. Everything else, adds and
// removes included, uses the ordinal as written.
func (d *doc) resolveTestRef(target string, t hew.Transform) (ref, error) {
	segs := t.Path.Segments()
	n := len(segs)
	if n == 0 || segs[n-1].Kind != hew.SegComment || segs[n-1].Trailing {
		return d.resolveFull(target, t.Path, t.PatchLine)
	}
	want, ok := commentTextOf(t.Value)
	if !ok {
		return d.resolveFull(target, t.Path, t.PatchLine)
	}
	parentPath, _ := t.Path.Parent()
	pr, err := d.resolveFull(target, parentPath, t.PatchLine)
	if err != nil || pr.node == nil || !pr.node.container() {
		return d.resolveFull(target, t.Path, t.PatchLine)
	}
	var cands []*comment
	for _, c := range pr.node.standalone() {
		if c.text == want {
			cands = append(cands, c)
		}
	}
	if len(cands) == 0 {
		return d.resolveFull(target, t.Path, t.PatchLine)
	}
	i := segs[n-1].Index
	if i < 0 || i >= len(cands) {
		i = 0
	}
	return ref{cmt: cands[i], parent: pr.node}, nil
}

// --- add --------------------------------------------------------------------

func (d *doc) planAdd(target string, t hew.Transform) ([]edit, error) {
	return d.planInsert(target, t.Path, t.Before, t.After, t.Value, "", t.OnConflict, t.Idempotent, t.PatchLine)
}

// planCopy resolves From and inserts its RAW source bytes at Path — copy takes
// its value by reference (§9.6), and this binding preserves the referenced
// subtree's exact formatting, comments included, rather than re-encoding it.
func (d *doc) planCopy(target string, t hew.Transform) ([]edit, error) {
	from, err := d.resolveFull(target, t.From, t.PatchLine)
	if err != nil {
		return nil, err
	}
	start, end := from.span()
	if from.cmt != nil {
		return nil, appErr(hewerr.CodeInexpressible, target, t.From.String(), t.PatchLine,
			"copy: a comment node is copied by adding a comment node, not by reference")
	}
	v, verr := from.node.hewValue()
	if verr != nil {
		return nil, appErr(hewerr.CodeTargetParse, target, t.From.String(), t.PatchLine, verr.Error())
	}
	return d.planInsert(target, t.Path, t.Before, t.After, v, string(d.src[start:end]), "", false, t.PatchLine)
}

func (d *doc) planInsert(target string, path, before, after hew.Path, v hew.Value, raw string,
	onConflict hew.OnConflict, idempotent bool, line int) ([]edit, error) {
	last, hasLast := lastSegment(path)
	if hasLast && last.Kind == hew.SegComment {
		return d.planCommentAdd(target, path, before, after, v, last, line)
	}

	if existing, err := d.resolveFull(target, path, line); err == nil {
		if existing.node != nil && existing.node.kind == kArr {
			return d.planChildInsert(target, existing.node, before, after, d.insertText(v, raw), true, line)
		}
		return d.planConflict(target, path, existing, v, raw, onConflict, idempotent, line)
	}

	parentPath, ok := path.Parent()
	if !ok {
		return nil, appErr(hewerr.CodeNoMatch, target, path.String(), line, "add: no container to insert into")
	}
	parent, err := d.resolveFull(target, parentPath, line)
	if err != nil {
		return nil, err
	}
	if parent.node == nil || !parent.node.container() {
		return nil, appErr(hewerr.CodeInexpressible, target, path.String(), line, "add: parent is not a container")
	}
	switch {
	case parent.node.kind == kObj && last.Kind == hew.SegKey:
		text := jsonQuote(last.Name) + ": " + d.insertText(v, raw)
		return d.planChildInsert(target, parent.node, before, after, text, true, line)
	case parent.node.kind == kArr:
		return d.planChildInsert(target, parent.node, before, after, d.insertText(v, raw), true, line)
	default:
		return nil, appErr(hewerr.CodeInexpressible, target, path.String(), line, "add: unsupported address shape")
	}
}

func (d *doc) insertText(v hew.Value, raw string) string {
	if raw != "" {
		return raw
	}
	return jsonEncode(v)
}

func lastSegment(p hew.Path) (hew.Segment, bool) {
	if p.Len() == 0 {
		return hew.Segment{}, false
	}
	return p.Segment(p.Len() - 1), true
}

// planCommentAdd adds a comment node. A comment ordinal is positional, not an
// identity: `add /#0` means "a comment node here", placed by the transform's
// before/after, so an already-occupied ordinal is not HEW014 the way an
// already-occupied key is.
func (d *doc) planCommentAdd(target string, path, before, after hew.Path, v hew.Value, last hew.Segment, line int) ([]edit, error) {
	text, ok := commentTextOf(v)
	if !ok {
		return nil, appErr(hewerr.CodeInexpressible, target, path.String(), line,
			"add: a comment node's value must be a comment (§9.6: {comment: <text>})")
	}
	parentPath, ok2 := path.Parent()
	if !ok2 {
		return nil, appErr(hewerr.CodeNoMatch, target, path.String(), line, "add: no container for the comment")
	}
	parent, err := d.resolveFull(target, parentPath, line)
	if err != nil {
		return nil, err
	}
	if last.Trailing {
		return d.planTrailingCommentAdd(target, path, parent, text, line)
	}
	if parent.node == nil || !parent.node.container() {
		return nil, appErr(hewerr.CodeInexpressible, target, path.String(), line,
			"add: comment ordinals address a container's comments (§4.5b)")
	}
	return d.planChildInsert(target, parent.node, before, after, renderComment(text), false, line)
}

// planTrailingCommentAdd hangs a `#t` comment off the member the path stepped
// through, after its value and its comma.
func (d *doc) planTrailingCommentAdd(target string, path hew.Path, owner ref, text string, line int) ([]edit, error) {
	var pos int
	switch {
	case owner.member != nil:
		pos = trailingAnchor(owner.member.valEnd, owner.member.commaPos)
	case owner.elem != nil:
		pos = trailingAnchor(owner.elem.valEnd, owner.elem.commaPos)
	default:
		return nil, appErr(hewerr.CodeInexpressible, target, path.String(), line,
			"add: a trailing comment attaches to a member or element (§4.5b)")
	}
	return []edit{{start: pos, end: pos, text: " " + renderComment(text)}}, nil
}

func trailingAnchor(valEnd, commaPos int) int {
	if commaPos >= 0 {
		return commaPos + 1
	}
	return valEnd
}

// planChildInsert resolves the before/after siblings against the container and
// hands the geometry to insert.
func (d *doc) planChildInsert(target string, c *node, before, after hew.Path, text string, isItem bool, line int) ([]edit, error) {
	slots := c.slots()
	p := placement{idx: len(slots)}
	if len(slots) > 0 {
		p.pos = slots[len(slots)-1].insertAfterPos()
	}
	switch {
	case !after.IsZero():
		r, err := d.resolveFull(target, after, line)
		if err != nil {
			return nil, err
		}
		if j, leading, ok := slotIndexOf(slots, r); ok {
			p = afterPlacement(slots, r, j, leading)
		}
	case !before.IsZero():
		r, err := d.resolveFull(target, before, line)
		if err != nil {
			return nil, err
		}
		if j, _, ok := slotIndexOf(slots, r); ok {
			p = placement{pos: slots[j].start, idx: j, before: true}
			if r.cmt != nil {
				p.pos = r.cmt.start
			}
		}
	}
	return d.insert(c, slots, p, text, isItem), nil
}

// afterPlacement puts a new child immediately after r. Landing after a
// member's LEADING comment means landing between that comment and the member,
// so the new child takes the member's slot index rather than the next one.
func afterPlacement(slots []slot, r ref, j int, leading bool) placement {
	if r.cmt != nil {
		if leading {
			return placement{pos: r.cmt.end, idx: j}
		}
		if slots[j].comment == r.cmt {
			return placement{pos: r.cmt.end, idx: j + 1}
		}
	}
	return placement{pos: slots[j].insertAfterPos(), idx: j + 1}
}

// slotIndexOf locates the slot a resolved ref belongs to, reporting whether
// the ref is one of that slot's leading comments.
func slotIndexOf(slots []slot, r ref) (idx int, leading bool, ok bool) {
	for j, s := range slots {
		if r.cmt != nil {
			if s.comment == r.cmt {
				return j, false, true
			}
			for _, c := range s.leading {
				if c == r.cmt {
					return j, true, true
				}
			}
			if (s.member != nil && s.member.trailing == r.cmt) || (s.elem != nil && s.elem.trailing == r.cmt) {
				return j, false, true
			}
			continue
		}
		if (s.member != nil && s.member.value == r.node) || (s.elem != nil && s.elem.value == r.node) {
			return j, false, true
		}
	}
	return 0, false, false
}

// planConflict implements the add-semantics variants (OP-02/03/04, §7.7) for a
// node that already exists at path.
func (d *doc) planConflict(target string, path hew.Path, existing ref, v hew.Value, raw string,
	onConflict hew.OnConflict, idempotent bool, line int) ([]edit, error) {
	switch onConflict {
	case hew.ConflictKeep:
		return nil, nil
	case hew.ConflictReplace:
		start, end := existing.span()
		return []edit{{start: start, end: end, text: d.insertText(v, raw)}}, nil
	}
	if idempotent && existing.node != nil {
		got, verr := existing.node.hewValue()
		if verr == nil && got.Equal(v) {
			return nil, nil
		}
	}
	return nil, appErr(hewerr.CodeAlreadyExists, target, path.String(), line,
		"add: node already exists; use \"! idempotent\" if re-applying is expected")
}

// --- remove -----------------------------------------------------------------

// planRemove deletes the addressed node. A member takes its leading and
// trailing comments with it (§8.2): they are part of its slot, so no separate
// bookkeeping says so.
func (d *doc) planRemove(target string, t hew.Transform) ([]edit, error) {
	r, err := d.resolveFull(target, t.Path, t.PatchLine)
	if err != nil {
		if t.Optional {
			return nil, nil
		}
		return nil, err
	}
	if r.parent == nil {
		return nil, appErr(hewerr.CodeInexpressible, target, t.Path.String(), t.PatchLine,
			"remove: cannot remove the document root")
	}
	if r.cmt != nil {
		return d.removeComment(r), nil
	}
	slots := r.parent.slots()
	j, _, ok := slotIndexOf(slots, r)
	if !ok {
		if t.Optional {
			return nil, nil
		}
		return nil, appErr(hewerr.CodeNoMatch, target, t.Path.String(), t.PatchLine, "remove: node does not exist")
	}
	return d.remove(r.parent, slots, j), nil
}

// removeComment deletes one comment node. A standalone comment is removed with
// the line it occupies; a trailing comment is removed with the whitespace that
// separated it from the value it followed, leaving that line otherwise intact.
func (d *doc) removeComment(r ref) []edit {
	slots := r.parent.slots()
	for i, s := range slots {
		if s.comment == r.cmt {
			return d.remove(r.parent, slots, i)
		}
		if (s.member != nil && s.member.trailing == r.cmt) || (s.elem != nil && s.elem.trailing == r.cmt) {
			return []edit{{start: gapStart(d.src, r.cmt.start), end: r.cmt.end, text: ""}}
		}
	}
	// A leading comment: the member it documents stays, so only the comment's
	// own line goes.
	return []edit{{start: lineStart(d.src, r.cmt.start), end: lineEnd(d.src, r.cmt.end), text: ""}}
}

func gapStart(src []byte, pos int) int {
	i := pos
	for i > 0 && (src[i-1] == ' ' || src[i-1] == '\t') {
		i--
	}
	return i
}

// --- replace ----------------------------------------------------------------

func (d *doc) planReplace(target string, t hew.Transform) ([]edit, error) {
	r, err := d.resolveFull(target, t.Path, t.PatchLine)
	if err != nil {
		if he, ok := err.(*hewerr.Error); ok && he.Code == hewerr.CodeNoMatch {
			he.Detail = "replace requires the node to exist (O26); use add for an unconditional write"
		}
		return nil, err
	}
	start, end := r.span()
	if r.cmt != nil {
		text, ok := commentTextOf(t.Value)
		if !ok {
			return nil, appErr(hewerr.CodeInexpressible, target, t.Path.String(), t.PatchLine,
				"replace: a comment node's value must be a comment (§9.6: {comment: <text>})")
		}
		return []edit{{start: start, end: end, text: renderComment(text)}}, nil
	}
	return []edit{{start: start, end: end, text: jsonEncode(t.Value)}}, nil
}
