package hewhcl

import (
	"strconv"
	"strings"

	"github.com/hew-format/hew"
	"github.com/hew-format/hew/internal/hewerr"
	"gopkg.in/yaml.v3"
)

// alreadyApplied is the §10.6 diagnostic: the after-image holds in full, so
// this is "already applied", not "drifted".
const alreadyApplied = "already applied: the after-image already holds here, and this hunk is strict; " +
	"add \"! idempotent\" (or the file-level \"idempotent: true\" pragma) if re-applying is expected (§7.5, §10.6)"

// --- test -------------------------------------------------------------------

func (r *run) evalTest(t hew.Transform) error {
	switch {
	case t.Absent:
		_, he, final := r.resolve(t.Path, t)
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
		rf, he, _ := r.resolve(t.Path, t)
		if he != nil {
			return he
		}
		got, err := r.d.nodeKind(rf)
		if err != nil {
			return r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine, err.Error())
		}
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
	rf, he, _ := r.resolve(t.Path, t)
	if he != nil {
		return he
	}
	got, ok := r.d.childCount(rf)
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

// evalValue is the before-image assert every context and `-` line compiles
// into (§9.0), so its failure is the characteristic drift error HEW010 —
// unless the after-image already holds, which §10.6 requires be distinguished
// before the code is chosen.
func (r *run) evalValue(t hew.Transform) error {
	rf, he, final := r.resolve(t.Path, t)
	if he != nil {
		if final {
			return he
		}
		if t.Optional {
			return nil // §7.6: satisfied whether or not the node exists
		}
		if w, ok := r.pairedWrite(t.Path); ok && w.Op == hew.OpRemove {
			return r.converge(t, w)
		}
		he.Code = hewerr.CodeStaleTarget
		he.Want = t.Value.String()
		return he
	}
	got, err := r.d.refValue(rf)
	if err != nil {
		return r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine, err.Error())
	}
	if got.Equal(t.Value) {
		return nil
	}
	if w, ok := r.pairedWrite(t.Path); ok && w.Op != hew.OpRemove && got.Equal(w.Value) {
		return r.converge(t, w)
	}
	e := r.err(hewerr.CodeStaleTarget, t.Path.String(), t.PatchLine, "")
	e.Want, e.Got = t.Value.String(), got.String()
	return e
}

// pairedWrite finds the write this test's before-image belongs to: the
// add/replace/remove at the same path in the same list (§10.6).
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

func (r *run) converge(t hew.Transform, w hew.Transform) error {
	if t.Idempotent || w.Idempotent {
		r.converged[t.Path.String()] = true
		return nil
	}
	return r.err(hewerr.CodeAssertionFailed, t.Path.String(), t.PatchLine, alreadyApplied)
}

// ordinalDrift widens the diagnostic for an assert reached through an ordinal
// selector.
//
// §6.4.3 rule 2 makes a distinguishing assert mandatory beside `! match ord=`
// precisely so that an inserted earlier sibling fails loudly. When it does
// fail, the failure is not about one attribute: the whole block the ordinal
// selected is the wrong block, and every assert the hunk made about it is
// stale. The diagnostic therefore names the FIRST assert that caught the shift
// — §6.4.3's mechanism, the one the reviewer must read — and reports the LAST
// line of the stale before-image, so the patch region to re-check is bracketed
// rather than reduced to its first line. hcl/ordinal-shifted-target pins both
// halves.
func (r *run) ordinalDrift(t hew.Transform, err error) error {
	he, ok := hewerr.As(err)
	if !ok || he.Code != hewerr.CodeStaleTarget {
		return err
	}
	prefix, ok := ordinalPrefix(t.Path)
	if !ok {
		return err
	}
	last := he.PatchLine
	for _, u := range r.all {
		if u.Op != hew.OpTest || u.PatchLine <= last || !underPrefix(u.Path, prefix) {
			continue
		}
		if r.evalTest(u) != nil {
			last = u.PatchLine
		}
	}
	he.PatchLine = last
	if he.Detail != "" {
		he.Detail += "; "
	}
	he.Detail += "the block " + prefix.String() + " selects is not the one this hunk asserts; " +
		"an earlier same-label sibling was inserted since the patch was written (§6.4.3, §6.4.5)"
	return he
}

// ordinalPrefix is the address step whose `[n]` selector chose the node: the
// shortest prefix of p ending at an ordinal-bearing segment.
func ordinalPrefix(p hew.Path) (hew.Path, bool) {
	segs := p.Segments()
	for i, s := range segs {
		if s.Ordinal != nil {
			return hew.NewPath(segs[:i+1]...), true
		}
	}
	return hew.Path{}, false
}

func underPrefix(p, prefix hew.Path) bool {
	segs, pre := p.Segments(), prefix.Segments()
	if len(segs) < len(pre) {
		return false
	}
	for i := range pre {
		if !segs[i].Equal(pre[i]) {
			return false
		}
	}
	return true
}

// --- add --------------------------------------------------------------------

func (r *run) planAdd(t hew.Transform) ([]edit, error) {
	rf, he, final := r.resolve(t.Path, t)
	if he == nil {
		return r.conflict(t, rf)
	}
	if final {
		return nil, he
	}
	body, name, labels, cerr := r.container(t, t.Path)
	if cerr != nil {
		return nil, cerr
	}
	text, isBlock, terr := r.render(t, name, labels, t.Value, body.indent)
	if terr != nil {
		return nil, terr
	}
	return r.insert(body, text, isBlock, t)
}

// conflict implements the add-semantics variants (OP-02/03/04, §7.7) for a
// node that already exists at the add's path.
func (r *run) conflict(t hew.Transform, rf *ref) ([]edit, error) {
	switch t.OnConflict {
	case hew.ConflictKeep:
		return nil, nil // `! default`: the user's value wins, zero ops
	case hew.ConflictReplace:
		return r.write(t, rf)
	}
	if t.Idempotent {
		got, err := r.d.refValue(rf)
		if err == nil && got.Equal(t.Value) {
			return nil, nil
		}
	}
	if r.converged[t.Path.String()] {
		return nil, r.err(hewerr.CodeAssertionFailed, t.Path.String(), t.PatchLine, alreadyApplied)
	}
	return nil, r.err(hewerr.CodeAlreadyExists, t.Path.String(), t.PatchLine,
		"add: node already exists; use \"! idempotent\" to re-apply, \"! upsert\" to overwrite, or \"! default\" to keep (§7.7)")
}

// render writes the lines a new child occupies. A value that is a mapping —
// or an address that carries labels — is a BLOCK; anything else is an
// attribute. hcl/add-block pins the depth rule that follows from it: the added
// node is a block, and the mappings INSIDE its value are object constructors
// (`aws = {`), because a block body's members are attributes and an
// attribute's value is an expression.
func (r *run) render(t hew.Transform, name string, labels []string, v hew.Value, indent int) (string, bool, error) {
	if len(labels) > 0 || isMapping(v) {
		text, err := blockText(name, labels, v.Node(), indent)
		if err != nil {
			return "", true, r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine, err.Error())
		}
		return text, true, nil
	}
	expr, err := exprText(v.Node(), indent)
	if err != nil {
		return "", false, r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine, err.Error())
	}
	return strings.Repeat(" ", indent) + name + " = " + expr, false, nil
}

// container resolves the body a path's last address step lives in, and returns
// that step's name and labels. `/provider/"aws-legacy"` lives at the ROOT: an
// HCL address step is a `(type, labels)` tuple, so dropping one segment is not
// dropping one step (§4.3).
func (r *run) container(t hew.Transform, p hew.Path) (*bodyNode, string, []string, *hewerr.Error) {
	segs := p.Segments()
	k := -1
	for i, s := range segs {
		if s.Kind == hew.SegKey {
			k = i
		}
	}
	if k < 0 {
		return nil, "", nil, r.err(hewerr.CodeInexpressible, p.String(), t.PatchLine,
			"address has no HCL name step (§8.5)")
	}
	var labels []string
	for _, s := range segs[k+1:] {
		if s.Kind != hew.SegLabel {
			return nil, "", nil, r.err(hewerr.CodeInexpressible, p.String(), t.PatchLine,
				"address mixes a label with a segment HCL cannot express (§8.5)")
		}
		labels = append(labels, s.Name)
	}
	rf, he, _ := r.resolve(hew.NewPath(segs[:k]...), t)
	if he != nil {
		return nil, "", nil, he
	}
	body := rf.body()
	if body == nil {
		return nil, "", nil, r.err(hewerr.CodeInexpressible, p.String(), t.PatchLine,
			"an attribute is not a body; nothing can be added inside it (§8.5)")
	}
	return body, segs[k].Name, labels, nil
}

// insert splices rendered lines into a body at the position §6.2 derives from
// the surrounding context.
func (r *run) insert(b *bodyNode, text string, isBlock bool, t hew.Transform) ([]edit, error) {
	r.touch(b)
	// A top-level block is separated from its neighbours by a blank line; a
	// member of a block body is not (hcl/add-block writes none).
	blank := isBlock && b.open == rootBody

	if !t.After.IsZero() || !t.Before.IsZero() {
		p := t.After
		if p.IsZero() {
			p = t.Before
		}
		rf, he, _ := r.resolve(p, t)
		if he != nil {
			return nil, he
		}
		if rf.item == nil || rf.parent != b {
			return nil, r.err(hewerr.CodeNoMatch, p.String(), t.PatchLine,
				"placement sibling is not a child of the target body")
		}
		pos := rf.item.end
		if t.After.IsZero() {
			pos = rf.item.start
		}
		return []edit{{start: pos, end: pos, text: text + "\n", blank: blank}}, nil
	}

	if len(b.items) > 0 {
		pos := b.items[len(b.items)-1].end
		return []edit{{start: pos, end: pos, text: text + "\n", blank: blank}}, nil
	}
	// An empty body. `features {}` has no line to append after, so the braces
	// are opened out into a block body.
	if nl := strings.IndexByte(string(r.d.src[b.innerStart:b.innerEnd]), '\n'); nl >= 0 {
		pos := b.innerStart + nl + 1
		return []edit{{start: pos, end: pos, text: text + "\n", blank: blank}}, nil
	}
	closeIndent := 0
	if b.owner != nil {
		closeIndent = b.owner.indent
	}
	return []edit{{start: b.innerStart, end: b.innerEnd,
		text: "\n" + text + "\n" + strings.Repeat(" ", closeIndent)}}, nil
}

// --- remove -----------------------------------------------------------------

func (r *run) planRemove(t hew.Transform) ([]edit, error) {
	rf, he, final := r.resolve(t.Path, t)
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
	if rf.item == nil {
		return nil, r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine,
			"remove: cannot remove the document root")
	}
	r.touch(rf.parent)
	start, end := r.d.removalSpan(rf)
	return []edit{{start: start, end: end}}, nil
}

// removalSpan is the item's lines plus the blank line that separated it from
// its neighbours: removing a top-level block must not leave the gap it used to
// fill (hcl/remove-block).
func (d *doc) removalSpan(rf *ref) (int, int) {
	b, it := rf.parent, rf.item
	lo, hi := b.innerStart, b.innerEnd
	for i, sib := range b.items {
		if sib != it {
			continue
		}
		if i > 0 {
			lo = b.items[i-1].end
		}
		if i+1 < len(b.items) {
			hi = b.items[i+1].start
		}
	}
	if s := d.blankRunBefore(it.start, lo); s < it.start {
		return s, it.end
	}
	return it.start, d.blankRunAfter(it.end, hi)
}

// --- replace ----------------------------------------------------------------

func (r *run) planReplace(t hew.Transform) ([]edit, error) {
	rf, he, final := r.resolve(t.Path, t)
	if he != nil {
		if !final && he.Code == hewerr.CodeNoMatch {
			he.Detail = "replace requires the node to exist (OP-26); use add for an unconditional write"
		}
		return nil, he
	}
	if got, err := r.d.refValue(rf); err == nil && got.Equal(t.Value) {
		if t.Idempotent {
			return nil, nil // converged; the file is already what the patch asks for
		}
		if r.converged[t.Path.String()] {
			return nil, r.err(hewerr.CodeAssertionFailed, t.Path.String(), t.PatchLine, alreadyApplied)
		}
		return nil, nil
	}
	return r.write(t, rf)
}

// write puts a transform's value at an already-resolved address.
func (r *run) write(t hew.Transform, rf *ref) ([]edit, error) {
	it := rf.item
	if it == nil {
		return nil, r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine,
			"cannot write over the document root")
	}
	r.touch(rf.parent)
	if it.kind == itemAttr {
		expr, err := exprText(t.Value.Node(), it.indent)
		if err != nil {
			return nil, r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine, err.Error())
		}
		return []edit{{start: it.exprStart, end: it.exprEnd, text: expr}}, nil
	}
	n := unwrapDoc(t.Value.Node())
	if n == nil || n.Kind != yaml.MappingNode {
		return nil, r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine,
			"a block's value must be a mapping (§8.5)")
	}
	body, err := bodyText(n, it.indent+bodyIndent)
	if err != nil {
		return nil, r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine, err.Error())
	}
	if body == "" {
		return []edit{{start: it.body.innerStart, end: it.body.innerEnd}}, nil
	}
	return []edit{{start: it.body.innerStart, end: it.body.innerEnd,
		text: "\n" + body + strings.Repeat(" ", it.indent)}}, nil
}

// --- copy -------------------------------------------------------------------

// planCopy splices the source node's RAW bytes under a new address. A relabel
// is a copy plus a remove (OP-36, Appendix C.1), and copying the source's own
// text is what makes the relabelled block keep its body verbatim.
func (r *run) planCopy(t hew.Transform) ([]edit, error) {
	frf, he, _ := r.resolve(t.From, t)
	if he != nil {
		return nil, he
	}
	if frf.item == nil {
		return nil, r.err(hewerr.CodeInexpressible, t.From.String(), t.PatchLine,
			"copy: the document root is not a node that can be copied")
	}
	if _, dhe, _ := r.resolve(t.Path, t); dhe == nil {
		return nil, r.err(hewerr.CodeAlreadyExists, t.Path.String(), t.PatchLine,
			"copy: a node already exists at the destination")
	}
	body, name, labels, cerr := r.container(t, t.Path)
	if cerr != nil {
		return nil, cerr
	}
	text, isBlock, terr := r.copyText(t, frf.item, name, labels, body.indent)
	if terr != nil {
		return nil, terr
	}
	return r.insert(body, text, isBlock, t)
}

func (r *run) copyText(t hew.Transform, src *itemNode, name string, labels []string, indent int) (string, bool, *hewerr.Error) {
	pad := strings.Repeat(" ", indent)
	if src.kind == itemAttr {
		if len(labels) > 0 {
			return "", false, r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine,
				"copy: an attribute destination cannot carry block labels (§8.5)")
		}
		return pad + name + " = " + string(r.d.src[src.exprStart:src.exprEnd]), false, nil
	}
	inner := string(r.d.src[src.body.innerStart:src.body.innerEnd])
	return pad + blockHeader(name, labels) + shiftIndent(inner, indent-src.indent) + "}", true, nil
}

// shiftIndent re-indents every line but the first by delta columns.
func shiftIndent(s string, delta int) string {
	if delta == 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	for i := 1; i < len(lines); i++ {
		switch {
		case lines[i] == "":
		case delta > 0:
			lines[i] = strings.Repeat(" ", delta) + lines[i]
		default:
			cut := 0
			for cut < -delta && cut < len(lines[i]) && lines[i][cut] == ' ' {
				cut++
			}
			lines[i] = lines[i][cut:]
		}
	}
	return strings.Join(lines, "\n")
}

// --- node inspection --------------------------------------------------------

func (d *doc) refValue(rf *ref) (hew.Value, error) {
	if rf.item == nil {
		return d.bodyValue(d.root)
	}
	return d.itemValue(rf.item)
}

func (d *doc) nodeKind(rf *ref) (hew.NodeKind, error) {
	if rf.item == nil {
		return hew.KindMap, nil
	}
	if rf.item.kind == itemBlock {
		return hew.KindBlock, nil
	}
	n, err := d.exprNode(rf.item.expr)
	if err != nil {
		return "", err
	}
	switch n.Kind {
	case yaml.MappingNode:
		return hew.KindMap, nil
	case yaml.SequenceNode:
		return hew.KindSeq, nil
	}
	return hew.KindScalar, nil
}

func (d *doc) childCount(rf *ref) (int, bool) {
	if b := rf.body(); b != nil {
		return len(b.items), true
	}
	n, err := d.exprNode(rf.item.expr)
	if err != nil {
		return 0, false
	}
	switch n.Kind {
	case yaml.MappingNode:
		return len(n.Content) / 2, true
	case yaml.SequenceNode:
		return len(n.Content), true
	}
	return 0, false
}
