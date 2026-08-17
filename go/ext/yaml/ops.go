package yaml

import (
	"strconv"
	"strings"

	"github.com/benjaminabbitt/hew/go"
	"github.com/benjaminabbitt/hew/go/internal/hewerr"
	"gopkg.in/yaml.v3"
)

// alreadyApplied is the §10.6 diagnostic: the after-image holds in full, so
// this is "already applied", not "drifted". It names both remedies because
// the two corpus cases that pin it — yaml/reapply-not-idempotent and
// yaml/pragma-strict-override — read the message for different halves of the
// same sentence.
const alreadyApplied = "already applied: the after-image already holds here, and this hunk is strict; " +
	"add \"! idempotent\" (or the file-level \"idempotent: true\" pragma) if re-applying is expected (§7.5, §10.6)"

// --- test -------------------------------------------------------------------

// evalTest evaluates one OpTest, raising the code the failing MODE maps to.
// A plain value test is the before-image assert every context and "-" line
// compiles into (§9.0), so its failure is the characteristic drift error
// HEW010 — unless the after-image already holds, which §10.6 requires be
// distinguished before the code is chosen.
func (r *run) evalTest(t hew.Transform) error {
	switch {
	case t.Absent:
		_, he, final := r.resolve(t.Path, t.Anchor, t.PatchLine)
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
		rf, he, _ := r.resolve(t.Path, t.Anchor, t.PatchLine)
		if he != nil {
			return he
		}
		got := r.d.nodeKind(rf.node)
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
	rf, he, _ := r.resolve(t.Path, t.Anchor, t.PatchLine)
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
	rf, he, final := r.resolve(t.Path, t.Anchor, t.PatchLine)
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
	if r.d.matches(rf.node, t.Value.Node()) {
		return nil
	}
	if w, ok := r.pairedWrite(t.Path); ok && w.Op != hew.OpRemove && r.d.equals(rf.node, w.Value.Node()) {
		return r.converge(t, w)
	}
	e := r.err(hewerr.CodeStaleTarget, t.Path.String(), t.PatchLine, "")
	e.Want, e.Got = t.Value.String(), r.d.describe(rf.node)
	// §10.3's `found` line names WHERE in the target, not just what: the node
	// is in hand here and its span is the only place that fact exists.
	e.TargetLine = r.d.lineOf(rf.node)
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
// add/replace/remove at the same path in the same list. It is how the applier
// evaluates the after-image (§10.6) without ever seeing the .hew text.
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
// after-image already holds (§7.5, §10.6). Tolerated, it records the path so
// the paired write knows not to re-run — and, when the write is strict while
// the assert was tolerated by a file pragma, so the write can report the
// refusal against its own line (yaml/pragma-strict-override).
func (r *run) converge(t hew.Transform, w hew.Transform) error {
	if t.Idempotent || w.Idempotent {
		r.converged[t.Path.String()] = true
		return nil
	}
	return r.err(hewerr.CodeAssertionFailed, t.Path.String(), t.PatchLine, alreadyApplied)
}

// --- add --------------------------------------------------------------------

// payload is a value rendered for insertion: block lines for a block
// container, and the one-line form a flow container's siblings use.
type payload struct {
	lines []string
	flow  string
}

func (r *run) memberPayload(key string, v *yaml.Node) payload {
	return payload{lines: r.d.emitMember(key, v, false), flow: r.d.emitMember(key, v, true)[0]}
}

func (r *run) elemPayload(v *yaml.Node) payload {
	return payload{lines: r.d.emitElement(v, false), flow: r.d.flowText(v)}
}

// planAdd computes the edits for one OpAdd, or none if the add is satisfied
// with zero ops (`! default` over an existing key, or `! idempotent` over an
// equal one — §7.5, §7.7).
func (r *run) planAdd(t hew.Transform) ([]edit, error) {
	rf, he, final := r.resolve(t.Path, t.Anchor, t.PatchLine)
	if he == nil {
		// A path that resolves to a SEQUENCE is the sequence-style add: the
		// container itself is addressed and the value is a new element. Any
		// other resolved node is a map-style add whose key already exists.
		//
		// That reading is ONLY for the plain OP-11 append — an OP-03
		// `! upsert` or OP-04 `! default` targeting a path that happens to
		// hold a sequence still means the whole node, not its container:
		// `! upsert` replaces regardless of current value (§7.7), and
		// appending into the old sequence on a replace corrupts the target.
		// ConflictKeep and ConflictReplace both name an existing node
		// directly, so they route to conflict, which already implements
		// both correctly.
		if rf.node != nil && rf.node.kind == nSeq &&
			t.OnConflict != hew.ConflictReplace && t.OnConflict != hew.ConflictKeep {
			return r.insert(rf.node, r.elemPayload(t.Value.Node()), t.Before, t.After, t)
		}
		return r.conflict(t, rf)
	}
	if final {
		return nil, he
	}
	parentPath, ok := t.Path.Parent()
	if !ok {
		return nil, r.err(hewerr.CodeNoMatch, t.Path.String(), t.PatchLine, "add: no container to insert into")
	}
	prf, phe, _ := r.resolve(parentPath, t.Anchor, t.PatchLine)
	if phe != nil {
		return nil, phe
	}
	if prf.node == nil {
		return nil, r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine, "add: parent is not a container")
	}
	last := t.Path.Segment(t.Path.Len() - 1)
	switch {
	case last.Kind == hew.SegComment:
		text, ok := commentText(t.Value)
		if !ok {
			return nil, r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine,
				"add: a comment address takes a {comment: \"…\"} value (§4.5b)")
		}
		return r.insert(prf.node, payload{lines: []string{"# " + text}}, t.Before, t.After, t)
	case prf.node.kind == nMap && last.Kind == hew.SegKey:
		return r.insert(prf.node, r.memberPayload(last.Name, t.Value.Node()), t.Before, t.After, t)
	case prf.node.kind == nSeq:
		return r.insert(prf.node, r.elemPayload(t.Value.Node()), t.Before, t.After, t)
	}
	return nil, r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine, "add: unsupported address shape")
}

// conflict implements the add-semantics variants (OP-02/03/04, §7.7) for a
// node that already exists at the add's path.
func (r *run) conflict(t hew.Transform, rf *ref) ([]edit, error) {
	switch t.OnConflict {
	case hew.ConflictKeep:
		return nil, nil // `! default`: the user's value wins, zero ops
	case hew.ConflictReplace:
		return r.write(rf, t)
	}
	if t.Idempotent && r.d.equals(rf.node, t.Value.Node()) {
		return nil, nil
	}
	if r.converged[t.Path.String()] {
		return nil, r.err(hewerr.CodeAssertionFailed, t.Path.String(), t.PatchLine, alreadyApplied)
	}
	return nil, r.err(hewerr.CodeAlreadyExists, t.Path.String(), t.PatchLine,
		"add: node already exists; use \"! idempotent\" to re-apply, \"! upsert\" to overwrite, or \"! default\" to keep (§7.7)")
}

// --- remove -----------------------------------------------------------------

func (r *run) planRemove(t hew.Transform) ([]edit, error) {
	rf, he, final := r.resolve(t.Path, t.Anchor, t.PatchLine)
	if he != nil {
		if final {
			return nil, he
		}
		if t.Optional || t.Idempotent {
			return nil, nil // §7.6, §7.5: nothing to remove is success here
		}
		he.Detail = removeMissDetail(t.Path, he)
		return nil, he
	}
	switch {
	case rf.comment != nil:
		return []edit{{start: rf.comment.lineStart, end: r.d.blockEndOf(rf.comment.lineStart)}}, nil
	case rf.inherited:
		return nil, r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine,
			"remove: an inherited key cannot be forked away, only shadowed (§8.3)")
	case rf.entry != nil:
		return []edit{{start: rf.entry.blockStart, end: rf.entry.blockEnd}}, nil
	case rf.elem != nil:
		return []edit{{start: rf.elem.blockStart, end: rf.elem.blockEnd}}, nil
	}
	return nil, r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine, "remove: cannot remove the document root")
}

// removeMissDetail is what a failed remove says. "node does not exist" is the
// right answer for a missing key — the reader can see that from the address —
// but where the LAST segment is a key-match the resolver has already worked out
// something the address cannot show (O46's near miss, §10.3), and throwing that
// away to say "does not exist" is exactly the diagnostic the ruling forbids.
func removeMissDetail(p hew.Path, he *hewerr.Error) string {
	if p.Len() > 0 && p.Segment(p.Len()-1).Kind == hew.SegMatch && he.Path == p.String() {
		return "remove: " + he.Detail
	}
	return "remove: node does not exist"
}

// --- replace ----------------------------------------------------------------

func (r *run) planReplace(t hew.Transform) ([]edit, error) {
	rf, he, final := r.resolve(t.Path, t.Anchor, t.PatchLine)
	if he != nil {
		if !final && he.Code == hewerr.CodeNoMatch {
			he.Detail = "replace requires the node to exist (OP-26); use add for an unconditional write"
		}
		return nil, he
	}
	if rf.comment == nil && !rf.inherited && r.d.equals(rf.node, t.Value.Node()) {
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

// write puts a transform's value at an already-resolved address.
func (r *run) write(rf *ref, t hew.Transform) ([]edit, error) {
	if rf.comment != nil {
		text, ok := commentText(t.Value)
		if !ok {
			return nil, r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine,
				"a comment address takes a {comment: \"…\"} value (§4.5b)")
		}
		return []edit{{start: rf.comment.textStart, end: rf.comment.textEnd, text: text}}, nil
	}
	if rf.inherited {
		// `! anchor fork` (OP-41): materialize the inherited key at THIS site.
		// The merge key stays and the new member shadows it (§8.3).
		return r.insert(rf.site, r.memberPayload(rf.siteKey, t.Value.Node()), hew.Path{}, hew.Path{}, t)
	}
	v := t.Value.Node()
	if v == nil || v.Kind == yaml.ScalarNode || isEmptyCollection(v) || rf.node.flow || rf.entry == nil {
		text := r.d.flowText(v)
		if rf.node.start == rf.node.end && rf.entry != nil {
			// An empty value ("key:"): write after the colon, with the space.
			return []edit{{start: rf.entry.colon + 1, end: rf.node.end, text: " " + text}}, nil
		}
		return []edit{{start: rf.node.start, end: rf.node.end, text: text}}, nil
	}
	// A block collection replacing a member's value starts on its own line.
	var body []string
	if v.Kind == yaml.MappingNode {
		body = r.d.emitMapBody(v)
	} else {
		body = r.d.emitSeqBody(v)
	}
	return []edit{{start: rf.entry.colon + 1, end: rf.node.end,
		text: "\n" + indentBlock(body, rf.entry.indent+r.d.indent)}}, nil
}

// --- copy -------------------------------------------------------------------

// planCopy takes its value by reference (§9.6) and splices the referenced
// member's RAW source bytes, its leading comment included — which is what
// makes a rename (copy + remove, Appendix C.1) carry the comment along
// instead of orphaning it.
func (r *run) planCopy(t hew.Transform) ([]edit, error) {
	frf, he, _ := r.resolve(t.From, t.Anchor, t.PatchLine)
	if he != nil {
		return nil, he
	}
	parentPath, ok := t.Path.Parent()
	if !ok {
		return nil, r.err(hewerr.CodeNoMatch, t.Path.String(), t.PatchLine, "copy: no container to insert into")
	}
	prf, phe, _ := r.resolve(parentPath, t.Anchor, t.PatchLine)
	if phe != nil {
		return nil, phe
	}
	last := t.Path.Segment(t.Path.Len() - 1)
	if prf.node == nil || prf.node.kind != nMap || last.Kind != hew.SegKey {
		return nil, r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine,
			"copy: only a mapping member destination is supported (§9.6)")
	}
	if frf.entry == nil {
		return nil, r.err(hewerr.CodeInexpressible, t.From.String(), t.PatchLine,
			"copy: only a mapping member source is supported (§9.6)")
	}
	return r.insert(prf.node, payload{lines: r.copyMember(frf.entry, last.Name)}, t.Before, t.After, t)
}

// copyMember renders a member's own source lines — leading comments, key,
// value — dedented to column zero and re-keyed. Everything but the key token
// is the source's own bytes, which is how a copy preserves the value's
// formatting and how a rename keeps the comment attached to what it renames.
func (r *run) copyMember(e *entry, newKey string) []string {
	raw := strings.TrimRight(string(r.d.src[e.blockStart:e.blockEnd]), "\n")
	lines := strings.Split(raw, "\n")
	for i := range lines {
		lines[i] = cutIndent(lines[i], e.indent)
	}
	// The key's own line is the one after the leading comment block, and the
	// key token starts that line: swapping it for the new key leaves the
	// colon, the spacing, and the value exactly as authored.
	keyLine := strings.Count(string(r.d.src[e.blockStart:e.lineStart]), "\n")
	lines[keyLine] = yamlKey(newKey) + lines[keyLine][e.keyEnd-e.keyStart:]
	return lines
}

func cutIndent(line string, n int) string {
	cut := 0
	for cut < n && cut < len(line) && line[cut] == ' ' {
		cut++
	}
	return line[cut:]
}

// --- insertion --------------------------------------------------------------

// child is a container's direct child as an insertion neighbour: the node it
// holds and the line region it occupies.
type child struct {
	node   *ynode
	start  int
	end    int
	indent int
}

func children(n *ynode) []child {
	var out []child
	for _, e := range n.entries {
		out = append(out, child{node: e.val, start: e.blockStart, end: e.blockEnd, indent: e.indent})
	}
	for _, el := range n.elems {
		out = append(out, child{node: el.val, start: el.blockStart, end: el.blockEnd, indent: el.indent})
	}
	return out
}

// insert splices a rendered payload into a container at the position §6.2
// derived from the surrounding context.
func (r *run) insert(container *ynode, p payload, before, after hew.Path, t hew.Transform) ([]edit, error) {
	kids := children(container)
	if container.flow {
		return r.insertFlow(container, p, kids, t)
	}
	if len(kids) == 0 {
		return nil, r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine, "add: container has no children to place against")
	}
	pos := kids[len(kids)-1].end
	if !after.IsZero() {
		i, err := r.findChild(kids, after, t)
		switch {
		case err == nil:
			pos = kids[i].end
		default:
			// The sibling may be one this same patch is adding: §9.1 step 5
			// chains a run of `+` lines, each placed after the one above it,
			// and corpus jsonc/add-with-leading-comment pins that shape. It
			// is not in the parsed document, so resolution fails; it IS in
			// this run's pending inserts, and landing at the same offset puts
			// this add immediately after it, because equal-offset edits keep
			// their list order.
			p, ok := r.pendingAt(container, after)
			if !ok {
				return nil, err
			}
			pos = p
		}
	} else if !before.IsZero() {
		i, err := r.findChild(kids, before, t)
		if err != nil {
			return nil, err
		}
		pos = kids[i].start
	}
	text := indentBlock(p.lines, kids[0].indent)
	r.pending = append(r.pending, pendingAdd{container: container, path: t.Path, value: t.Value, pos: pos})
	if pos > 0 && r.d.src[pos-1] != '\n' {
		// The container's last line has no newline of its own: open one.
		return []edit{{start: pos, end: pos, text: "\n" + text}}, nil
	}
	return []edit{{start: pos, end: pos, text: text + "\n"}}, nil
}

// pendingAdd is one insertion this run has already planned: where it lands,
// and enough about what it inserts to recognize a later placement naming it.
type pendingAdd struct {
	container *ynode
	path      hew.Path // the add's own address: a member/comment path, or the container for a sequence element
	value     hew.Value
	pos       int
}

// pendingAt reports the insertion offset of a pending add that the placement
// path names. The most recent match wins, so a chain of three `+` lines walks
// forward rather than collapsing onto the first.
func (r *run) pendingAt(container *ynode, p hew.Path) (int, bool) {
	seg := p.Segment(p.Len() - 1)
	for i := len(r.pending) - 1; i >= 0; i-- {
		pa := r.pending[i]
		if pa.container != container {
			continue
		}
		if pa.path.Equal(p) || segNamesValue(seg, pa.value) {
			return pa.pos, true
		}
	}
	return 0, false
}

// segNamesValue applies §4.2's key-match comparison to a value in hand, for a
// node that is not in the document yet and so has no *ynode to compare.
func segNamesValue(seg hew.Segment, v hew.Value) bool {
	n := v.Node()
	if n == nil || seg.Kind != hew.SegMatch {
		return false
	}
	want := scalarNode(seg.Value)
	if seg.Name == "" {
		return n.Kind == yaml.ScalarNode && scalarEq(n, want)
	}
	if n.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == seg.Name {
			return n.Content[i+1].Kind == yaml.ScalarNode && scalarEq(n.Content[i+1], want)
		}
	}
	return false
}

// insertFlow adds to a flow collection. An EMPTY one has no sibling style to
// adopt, so the child is written in block style under its key — the only form
// that can carry a nested value at all (cli/apply-transforms pins it).
func (r *run) insertFlow(container *ynode, p payload, kids []child, t hew.Transform) ([]edit, error) {
	if len(kids) == 0 {
		if container.owner == nil {
			return nil, r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine,
				"add: cannot expand an empty flow collection that is not a mapping value")
		}
		indent := container.owner.indent + r.d.indent
		return []edit{{start: container.owner.colon + 1, end: container.end,
			text: "\n" + indentBlock(p.lines, indent)}}, nil
	}
	if p.flow == "" {
		return nil, r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine,
			"add: this value has no flow rendering for a flow container")
	}
	last := kids[len(kids)-1].node.end
	return []edit{{start: last, end: last, text: ", " + p.flow}}, nil
}

// findChild locates the placement sibling a before:/after: path names.
func (r *run) findChild(kids []child, p hew.Path, t hew.Transform) (int, error) {
	rf, he, _ := r.resolve(p, t.Anchor, t.PatchLine)
	if he != nil {
		return 0, he
	}
	for i, k := range kids {
		if k.node == rf.node {
			return i, nil
		}
	}
	return 0, r.err(hewerr.CodeNoMatch, p.String(), t.PatchLine, "placement sibling is not a child of the target container")
}
