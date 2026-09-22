package hew

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

// The three spellings of §9.4-R2's sibling radius that are not a plain count.
//
// The zero value of DiffOptions has to mean "the default radius of 1", not
// "no context": a differ that quietly produced the weakest patch it can for a
// caller who filled in no options would be exactly the strictness loss R2's
// own note warns against. So "no context" gets a name of its own, and 0 stays
// available as the unset marker.
const (
	// ContextDefault is the radius §9.4-R2 defaults to: one unchanged sibling
	// before and one after each changed run.
	ContextDefault = 1
	// ContextAll emits every sibling of every touched container (`-U all`).
	ContextAll = -1
	// ContextNone emits margins only (`-U 0`).
	ContextNone = -2
)

// HintContextDefault is the neighbour radius of the non-asserting hint channel:
// three untouched neighbours either side of each changed run.
//
// It is WIDER than ContextDefault on purpose, and the asymmetry is the whole
// reason the two are separate constants. A hint costs the patch nothing in
// strictness — it asserts nothing, so an extra one can never make the patch
// refuse — while it buys the locator another piece of evidence for placing a
// hunk whose address no longer resolves on its own. Three either side is the
// point where a repeated value's neighbourhood distinguishes it: at 1 a
// duplicate-bearing array offers the locator two neighbours to weigh, at 3 it
// offers six.
const HintContextDefault = 3

// DefaultKeyFields is §9.4-R4's candidate identity-field list, tried in order.
// It is binding DATA, not a rule (ruling O18): the rule is "present on every
// element, scalar, unique", and `--key-fields` overrides the list.
var DefaultKeyFields = []string{"name", "id", "key"}

// DiffOptions configures the differ (Appendix A.5).
type DiffOptions struct {
	// KeyFields are the candidate identity fields for keyed-array addressing,
	// tried in order (§9.4-R4). Empty means DefaultKeyFields.
	KeyFields []string

	// Context is the sibling radius (§9.4-R2) for the ASSERTING channel: the
	// value-carrying `test` records a keyed or index sequence's untouched
	// neighbours ride. Zero means ContextDefault; ContextNone and ContextAll
	// spell the two ends.
	Context int

	// HintContext is the neighbour radius for the non-asserting HINT channel:
	// the `~` lines a mapping's and a by-value set's untouched neighbours ride.
	//
	// It needs a knob of its own because the same number has OPPOSITE effects on
	// the two channels. Widening ASSERTING context makes a patch more BRITTLE —
	// every extra neighbour is one more unrelated edit it can refuse over.
	// Widening HINT context makes it more ROBUST — every extra neighbour is more
	// evidence the locator can place the hunk with, and a hint that no longer
	// matches costs nothing, because it asserts nothing. One shared knob would
	// silently trade one property for the other in whichever direction it moved.
	//
	// Zero means HintContextDefault; ContextNone and ContextAll spell the two
	// ends. Spelling either of those on Context alone carries it here too.
	HintContext int

	// Target is stamped into the produced TransformList; it is a label, not a
	// path the differ reads. The differ performs no I/O of any kind.
	Target string

	// Note receives the human-readable remarks §9.4-R4 requires the differ to
	// emit when it falls back to index addressing. The CLI turns them into the
	// patch's leading `#` comment; a nil Note discards them.
	Note func(string)
}

func (o DiffOptions) radius() (n int, all bool) {
	switch {
	case o.Context == 0:
		return ContextDefault, false
	case o.Context == ContextNone:
		return 0, false
	case o.Context < 0: // ContextAll, and any other negative spelling of it
		return 0, true
	}
	return o.Context, false
}

// hintRadius is the radius governing the hint channel.
//
// An unset knob takes HintContextDefault rather than Context's count: the two
// channels want opposite numbers, so a plain count set on one must not drag the
// other with it. The two SENTINELS do carry across, because ContextNone and
// ContextAll are body-wide requests — "no context at all", "every sibling" —
// and a user who spells one means it for the whole hunk body, not for one
// channel of it.
func (o DiffOptions) hintRadius() (n int, all bool) {
	if o.HintContext == 0 {
		switch {
		case o.Context == ContextNone:
			return 0, false
		case o.Context < 0: // ContextAll, and any other negative spelling of it
			return 0, true
		}
		return HintContextDefault, false
	}
	switch {
	case o.HintContext == ContextNone:
		return 0, false
	case o.HintContext < 0: // ContextAll, and any other negative spelling of it
		return 0, true
	}
	return o.HintContext, false
}

func (o DiffOptions) keyFields() []string {
	if len(o.KeyFields) == 0 {
		return DefaultKeyFields
	}
	return o.KeyFields
}

// One constructor per COMPONENT, and that is the point. The component is fixed
// here so no call site can pass the wrong one or forget it; a single shared
// helper taking it as a parameter would turn a compile-time fact into an
// argument, which is the mistake this shape exists to prevent.
// reprise:ignore
func diffErr(code hewerr.Code, target, path, format string, args ...any) error {
	return &hewerr.Error{
		Code:      code,
		Component: hewerr.ComponentDiffer,
		Target:    target,
		Path:      path,
		Detail:    fmt.Sprintf(format, args...),
	}
}

// Invert returns the transform list that UNDOES an application which turned
// before into after: applying it to the after-image yields the before-image.
//
// The inverse of an application is a hew question, not a caller's, which is
// why this exists rather than leaving every consumer to assemble it. Derived
// by hand it is a rule per op shape — an add that replaced a value inverts to
// an add carrying the old one, an add that created a key inverts to a remove,
// a remove inverts to an add of what was there — and each rule is somewhere to
// be quietly wrong. Diffing the other way round answers all of them at once.
//
// The DIRECTION is the whole point of the function. `Diff(before, after)` and
// `Diff(after, before)` are both well-formed calls that both return a valid
// transform list, and only one of them undoes anything; a caller that swaps
// them gets the FORWARD list back and finds out when its pointers fail to
// resolve, if it is lucky, and silently reapplies its own change if it is not.
// Naming the direction here means it is decided once.
//
// APPLY THE RESULT TO THE AFTER-IMAGE. The addresses it carries name positions
// in the document as it stands after the application, not as it stood before.
// Resolving it against the before-image is the other easy mistake and this
// function cannot prevent it — it returns the abstract list, as DiffTrees does.
//
// An inverse carries values: restoring what an op replaced means holding the
// replaced content. It copies only what an op actually touched, which is why
// a record can store this instead of a copy of the whole pre-image.
func Invert(format FormatID, before, after []byte, opt DiffOptions) (TransformList, error) {
	b, ok := Lookup(format)
	if !ok || b.Differ == nil {
		return TransformList{}, diffErr(hewerr.CodeUnsupportedFormat, opt.Target, "",
			"this build cannot diff %q documents, so an application to one cannot be inverted", string(format))
	}
	afterTree, err := b.Differ(after)
	if err != nil {
		return TransformList{}, err
	}
	beforeTree, err := b.Differ(before)
	if err != nil {
		return TransformList{}, err
	}
	return DiffTrees(afterTree, beforeTree, format, opt)
}

// DiffTrees computes the structural difference between two parsed documents
// and returns the abstract transform list (§9.2) that turns old into new.
//
// It is the format-agnostic half of the differ: a format binding parses its
// own bytes into a DiffNode tree and hands both trees here, so the traversal,
// the sequence diff, the addressing preference and the context radius are
// implemented exactly once and cannot drift between formats.
//
// Deterministic (§9.4-R1): the same (old, new, opt) triple yields the same
// list, because the traversal is key-order-preserving, the sequence diff is
// Myers with a pinned tie-break, and every fallback (identity-field choice,
// index addressing) is decided from sorted, total information.
func DiffTrees(old, new *DiffNode, format FormatID, opt DiffOptions) (TransformList, error) {
	if old == nil || new == nil {
		return TransformList{}, diffErr(hewerr.CodeTargetParse, opt.Target, "", "differ: missing document tree")
	}
	d := &differ{opt: opt, fields: opt.keyFields()}
	d.radius, d.all = opt.radius()
	d.hint, d.hintAll = opt.hintRadius()
	if err := d.root(old, new); err != nil {
		return TransformList{}, err
	}
	// The differ is the one producer that builds paths from RAW DOCUMENT KEYS,
	// which is why it used to run the spellability guard here and refuse a
	// package.json with a "@scope/pkg" dependency outright. O41 removed the
	// reason: the canonical-rendering rule spells every key, so the differ
	// emits `/dependencies/"@scope/pkg"` and the address means what it says.
	// json/diff-scoped-key pins that from producer to consumer, and what is
	// left of the guard lives at the emitting seams (transform.go).
	return TransformList{Target: opt.Target, Format: format, Transform: d.out}, nil
}

type differ struct {
	opt     DiffOptions
	fields  []string
	radius  int
	all     bool
	hint    int
	hintAll bool
	out     []Transform
}

func (d *differ) note(format string, args ...any) {
	if d.opt.Note != nil {
		d.opt.Note(fmt.Sprintf(format, args...))
	}
}

// root diffs the two documents' root nodes. A root whose KIND changed, or a
// scalar root that changed, is §9.4-R6's inexpressible case: the mirror
// grammar addresses members and elements, and has no line shape for "the
// document is now something else". R6 forbids degrading it silently.
func (d *differ) root(old, new *DiffNode) error {
	if sameNode(old, new) {
		return nil
	}
	if old.Kind != new.Kind || old.Kind == KindScalar {
		return diffErr(hewerr.CodeInexpressible, d.opt.Target, "/",
			"the document root changed from %s to %s; the mirror grammar can express edits to a root container's children, not a replacement of the root itself (Appendix C, §9.4-R6)",
			old.Kind, new.Kind)
	}
	return d.container(RootPath(), old, new)
}

// --- slots ------------------------------------------------------------------

type slotState uint8

const (
	slotSame     slotState = iota // unchanged: eligible as context (§9.4-R2)
	slotNested                    // matched, differs, both containers: its own hunk (§9.4-R3)
	slotRemoved                   // in old only
	slotAdded                     // in new only
	slotReplaced                  // matched, differs, not both containers
)

func (s slotState) changed() bool {
	return s == slotRemoved || s == slotAdded || s == slotReplaced
}

// survives reports whether the slot still exists in the new document, which is
// what a relative placement (`after:` / `before:`) may name.
func (s slotState) survives() bool { return s != slotRemoved }

// slot is one position in the merged child order of a container — the same
// merge unified diff walks, with old-only, new-only, and matched positions
// interleaved.
type slot struct {
	state slotState
	old   *DiffChild
	new   *DiffChild

	// ref addresses the child itself: /server/timeout, /tags/=beta,
	// /mcpServers/name=github, /server/#0.
	ref Path
	// addPath is where an OpAdd for this slot writes. It is ref for a map
	// member and for a comment, and the CONTAINER for a sequence element,
	// because a sequence element has no name to be created under (§9.1 step 5).
	addPath Path
}

// --- the walk ---------------------------------------------------------------

// container emits the hunk for one container's own changed children, then
// descends into the children that changed but kept their identity. Depth-first
// in source order, so the hunks come out in document order and R3's "deepest
// container containing every changed node" is decided structurally: a
// container with no changed child of its own emits no hunk at all, which is why
// a nested edit anchors at /server rather than at /.
func (d *differ) container(path Path, old, new *DiffNode) error {
	addr := d.addressing(path, old, new)
	slots := d.match(path, addr, old, new)

	changed := false
	for i := range slots {
		if slots[i].state.changed() {
			changed = true
			break
		}
	}
	if changed {
		d.emit(addr, slots)
	}
	for i := range slots {
		if slots[i].state != slotNested {
			continue
		}
		if err := d.container(slots[i].ref, slots[i].old.Node, slots[i].new.Node); err != nil {
			return err
		}
	}
	return nil
}

// match runs the sequence diff over the two child lists and classifies every
// merged position.
func (d *differ) match(path Path, addr addressing, old, new *DiffNode) []slot {
	oldIDs := addr.identities(old.Children)
	newIDs := addr.identities(new.Children)

	var out []slot
	for _, step := range myers(oldIDs, newIDs) {
		switch step.Kind {
		case editDelete:
			c := &old.Children[step.A]
			ref := addr.childPath(path, *c, step.A)
			out = append(out, slot{state: slotRemoved, old: c, ref: ref, addPath: ref})
		case editInsert:
			c := &new.Children[step.B]
			ref := addr.childPath(path, *c, step.B)
			add := ref
			// A sequence element and a COMMENT are both keyless, so an add
			// names the CONTAINER and the slot's placement carries the
			// position (§9.1 step 5). A comment is not gated on addr.seq: it
			// is keyless in a MAP too, and its `#hew:comment=<hex>` address
			// identifies a comment that is already in the document — which is
			// exactly what an add does not have.
			if c.Comment || addr.seq {
				add = path
			}
			out = append(out, slot{state: slotAdded, new: c, ref: ref, addPath: add})
		default:
			o, n := &old.Children[step.A], &new.Children[step.B]
			ref := addr.childPath(path, *o, step.A)
			out = append(out, slot{state: pairState(o, n), old: o, new: n, ref: ref, addPath: ref})
		}
	}
	return out
}

func pairState(o, n *DiffChild) slotState {
	if sameNode(o.Node, n.Node) && o.Text == n.Text {
		return slotSame
	}
	if o.Node != nil && n.Node != nil && o.Node.Kind == n.Node.Kind &&
		(o.Node.Kind == KindMap || o.Node.Kind == KindSeq) {
		return slotNested
	}
	return slotReplaced
}

// --- context radius and emission --------------------------------------------

// emit writes one container's hunk: every assertion first, in body order, then
// every mutation, in body order — the order §9.1's lowering produces, inverted,
// so that a differ-produced list and a parser-produced list of the same patch
// are the same list.
func (d *differ) emit(addr addressing, slots []slot) {
	show := d.window(addr, slots)
	pos, needsPos := positions(addr, slots)
	for i := range slots {
		if !show[i] {
			continue
		}
		// A pure context sibling rides the non-asserting hint channel
		// (satisfied-recoil): a positioned OpHint instead of a value-carrying
		// OpTest. Asserting an untouched neighbour made the patch brittle — an edit
		// to it refused the whole patch — and copied its value verbatim, which is
		// how a neighbour's `Authorization: Bearer …` reached an audit record. The
		// hint asserts nothing and carries no value; being positioned in body
		// order, it still anchors an add's placement.
		//
		//   - a MAPPING neighbour rides its KEY path      -> `~ key`
		//   - a SET (by-value scalar) neighbour rides its content HASH
		//                                                 -> `~ #hew:sha256=<hex>`
		//   - a COMMENT rides the hash of its own TEXT, in every container kind
		//                                                 -> `~ #hew:comment=<hex>`
		//
		// Keyed and index sequences keep their current emission for now: a keyed
		// element's context line is already just its identity field (an address),
		// and an index carries no value in the address.
		if addr.hinted(&slots[i]) {
			h := Transform{Op: OpHint, Path: slots[i].ref}
			// A HINT always carries its position in a duplicate-bearing
			// collection, even when its own value is unique. The dup[] gate that
			// applies to mutations does not apply here: a neighbour's position is
			// what makes it ORDERABLE evidence, and the most discriminating
			// neighbours are precisely the unique ones. Without it the
			// neighbourhood is a bag of unplaced tokens and cannot be compared
			// against a candidate's surroundings at all.
			if p, ok := pos[i]; ok {
				// Without this two context duplicates are byte-identical records:
				// indistinguishable in the IR, collapsed into one body line by the
				// renderer, and useless as the anchor an add is placed against.
				h.At, h.Length = intPtr(p.at), intPtr(p.length)
			}
			d.out = append(d.out, h)
			continue
		}
		tests := addr.tests(&slots[i])
		if p, ok := pos[i]; ok && needsPos[i] {
			for j := range tests {
				tests[j].At, tests[j].Length = intPtr(p.at), intPtr(p.length)
			}
		}
		d.out = append(d.out, tests...)
	}
	for i := range slots {
		var t Transform
		// Which slot's advisory this transform carries. Ordinarily its own: the
		// address it resolves is its own element. An add is the exception (§4.5c)
		// — it has no before-image position, so the address that must resolve is
		// its ANCHOR's, and the anchor's coordinates are what it carries.
		advisorySlot := i
		switch slots[i].state {
		case slotRemoved:
			t = Transform{Op: OpRemove, Path: slots[i].ref}
		case slotReplaced:
			t = Transform{Op: OpReplace, Path: slots[i].ref, Value: addr.valueOf(slots[i].new)}
		case slotAdded:
			t = Transform{Op: OpAdd, Path: slots[i].addPath, Value: addr.valueOf(slots[i].new)}
			advisorySlot = -1
			if ref, ok := placement(slots, i); ok {
				if ref.before {
					t.Before = ref.path
				} else {
					t.After = ref.path
				}
				advisorySlot = ref.slot
			}
		default:
			continue
		}
		// Written where it DECIDES something: on an element that cannot resolve
		// by its own content alone — because its value repeats (the digest
		// doesn't say which element is meant) or because it has no usable
		// identity field at all (§6.4.2; positionsNoIdentity) — and ALWAYS on an
		// add, whose anchor index is what later transforms in this collection
		// migrate their own coordinates against (§4.5e) even when that anchor is
		// itself unique.
		if p, ok := pos[advisorySlot]; ok && (needsPos[advisorySlot] || slots[i].state == slotAdded) {
			t.At, t.Length = intPtr(p.at), intPtr(p.length)
		}
		d.out = append(d.out, t)
	}
}

// hinted reports whether a shown slot rides the non-asserting hint channel
// rather than carrying an assertion. It is asked in two places that must agree:
// window, deciding which RADIUS governs the slot, and emit, deciding which
// RECORD to write for it. Were they to drift, a slot could be admitted by one
// channel's radius and then emitted on the other's.
func (a addressing) hinted(s *slot) bool {
	if s.state != slotSame || s.old == nil {
		return false
	}
	// A COMMENT rides the hint channel in EVERY container kind, so the
	// container's addressing decides nothing here. childPath addresses a
	// comment by the digest of its own text BEFORE it splits on sequence vs
	// mapping (§4.5b), which is exactly the property the hint channel asks
	// for: an address that identifies the neighbour without carrying it.
	//
	// A comment is also the neighbour whose value most wants withholding —
	// prose a human wrote, up to and including a credential pasted into a
	// remark — and the one most likely to be reworded by someone who has not
	// touched the code, which as an assertion refuses the whole patch.
	if s.old.Comment {
		return true
	}
	return !a.seq || a.byValue
}

type elemPos struct{ at, length int }

func intPtr(n int) *int { return &n }

// positions computes the per-slot position advisory for an addressing that
// cannot resolve every element from its own content alone: a DUPLICATE
// by-value scalar array, where several elements share a digest, or a sequence
// with no usable §6.4.2 identity field at all, where no element has one.
// Both write position into the patch TEXT (`~hew:at=N ~hew:length=M`,
// render.go's withAdvice) because the parser never reads a target to count
// against (§9.1's lowering is purely textual) and so cannot recover a
// position that was left unwritten. Nil for every other addressing — an
// addressing that already resolves by key-match, hash, or unique value needs
// no position.
func positions(addr addressing, slots []slot) (map[int]elemPos, map[int]bool) {
	switch {
	case addr.byValue && addr.dups:
		return positionsByValueDup(slots)
	case addr.seq && !addr.byValue && addr.field == "":
		return positionsNoIdentity(slots)
	default:
		return nil, nil
	}
}

// positionsByValueDup computes positions for a by-value scalar array with
// repeated values: a removed/replaced element carries its index among the OLD
// elements, an added element its index among the NEW ones, each with that
// side's length, so a value that collides on its digest stays resolvable.
func positionsByValueDup(slots []slot) (map[int]elemPos, map[int]bool) {
	// Only a value that actually REPEATS needs a position. A unique digest
	// already identifies its element on its own (the advisory would change no
	// decision the locator makes), so putting one on every element of a
	// collection that merely CONTAINS a duplicate is noise in the patch.
	repeats := map[string]int{}
	oldLen := 0
	for i := range slots {
		if slots[i].state != slotAdded { // present in old
			oldLen++
			if c := slots[i].old; c != nil && !c.Comment && c.Node != nil {
				repeats[scalarToken(c.Node.Value)]++
			}
		}
	}
	out := map[int]elemPos{}
	oldIdx := 0
	for i := range slots {
		switch slots[i].state {
		case slotRemoved, slotReplaced:
			out[i] = elemPos{oldIdx, oldLen}
		case slotSame:
			// An UNTOUCHED element still needs to say which duplicate it is,
			// because it is a placement ANCHOR: an add writes its position
			// relative to a sibling, and "after the element hashing to X" names
			// two different places when X appears twice. Its index is the OLD
			// one, since that is the document the anchor is resolved against.
			out[i] = elemPos{oldIdx, oldLen}
		}
		if slots[i].state != slotAdded {
			oldIdx++
		}
	}
	needsPos := map[int]bool{}
	for i := range slots {
		if c := slots[i].old; c != nil && !c.Comment && c.Node != nil && repeats[scalarToken(c.Node.Value)] > 1 {
			needsPos[i] = true
		}
	}
	return out, needsPos
}

// positionsNoIdentity computes positions for a sequence whose elements carry
// no usable §6.4.2 identity field at all — addressing already fell back to
// indexing them (childPath's default case) because usableFields found zero
// candidates. Unlike the by-value-dup case, EVERY removed/replaced/anchor
// element needs its own position written, not just the ones that collide:
// none of them has any content-derived address for the parser to reconstruct.
func positionsNoIdentity(slots []slot) (map[int]elemPos, map[int]bool) {
	oldLen := 0
	for i := range slots {
		if slots[i].state != slotAdded {
			oldLen++
		}
	}
	out := map[int]elemPos{}
	needsPos := map[int]bool{}
	oldIdx := 0
	for i := range slots {
		switch slots[i].state {
		case slotRemoved, slotReplaced, slotSame:
			out[i] = elemPos{oldIdx, oldLen}
			needsPos[i] = true
		}
		if slots[i].state != slotAdded {
			oldIdx++
		}
	}
	return out, needsPos
}

// window marks which slots the hunk body shows: every changed slot, and every
// sibling within the radius of a changed run.
//
// Overlapping and abutting windows need no coalescing pass: marking slots in a
// single shared array IS the coalesce, exactly as unified diff's is.
//
// §9.4-R2's other clause — "identity lines are exempt from the radius and are
// always emitted" — needs no code here, because this differ never renders an
// identity as a suppressible context line in the first place. A keyed element
// that is added or removed carries its identity on its own `+`/`-` line, and a
// keyed element with an INNER change anchors a hunk of its own
// (`@@ /mcpServers/name=github @@`), where the identity is the anchor. The
// case R2 guards against — a radius small enough to leave a hunk unaddressable
// — cannot arise.
// Each neighbour is admitted by the radius of the CHANNEL IT WILL RIDE, not by
// one shared number: a slot destined for a `~` hint answers to HintContext, one
// destined for a value-carrying `test` to Context. The scan therefore reaches as
// far as the WIDER of the two and filters per slot, which is what lets the hint
// channel run at 3 while assertions stay at 1 in the very same hunk.
func (d *differ) window(addr addressing, slots []slot) []bool {
	show := make([]bool, len(slots))
	reach := d.radius
	if d.hint > reach {
		reach = d.hint
	}
	for i := range slots {
		if d.channelAll(addr, &slots[i]) {
			show[i] = true
		}
	}
	for i := range slots {
		if !slots[i].state.changed() {
			continue
		}
		for j := i - reach; j <= i+reach; j++ {
			if j < 0 || j >= len(slots) {
				continue
			}
			dist := j - i
			if dist < 0 {
				dist = -dist
			}
			if dist <= d.channelRadius(addr, &slots[j]) {
				show[j] = true
			}
		}
	}
	return show
}

// channelRadius and channelAll read the knob governing one slot's channel.
func (d *differ) channelRadius(addr addressing, s *slot) int {
	if addr.hinted(s) {
		return d.hint
	}
	return d.radius
}

func (d *differ) channelAll(addr addressing, s *slot) bool {
	if addr.hinted(s) {
		return d.hintAll
	}
	return d.all
}

type placementRef struct {
	path   Path
	before bool
	// slot is the anchor's slot index. An add has no before-image position of
	// its own (§4.5c), so it carries the ANCHOR's — the coordinates its
	// Before/After address has to be resolved against.
	slot int
}

// placement derives §9.1 step 5's relative position for an added slot: the
// nearest surviving sibling before it, or failing that the nearest one after
// it. Never an index — the patch must stay target-independent (§9.2).
//
// The two scans are not symmetric, and the asymmetry is the point. Looking
// BACKWARD, an already-added sibling is a legitimate anchor: mutations execute
// in slot order, so it is in the document by the time this add runs — that is
// what lets a member land after the comment that documents it. Looking
// FORWARD, an added sibling is not there yet, so naming it would hand the
// applier a path that cannot resolve; the scan skips ahead to a sibling the
// old document already has.
func placement(slots []slot, i int) (placementRef, bool) {
	for j := i - 1; j >= 0; j-- {
		if slots[j].state.survives() {
			return placementRef{path: slots[j].ref, slot: j}, true
		}
	}
	for j := i + 1; j < len(slots); j++ {
		if slots[j].state.survives() && slots[j].state != slotAdded {
			return placementRef{path: slots[j].ref, before: true, slot: j}, true
		}
	}
	return placementRef{}, false
}

// --- addressing (§9.4-R4) ----------------------------------------------------

// addressing is how one container's children are named. For a mapping there is
// only one answer; for a sequence it is R4's preference order, decided once per
// container so that every path into it agrees.
type addressing struct {
	seq bool
	// field is the identity field of a keyed sequence ("" = not keyed).
	field string
	// byValue addresses scalar elements by their own value (/tags/=beta).
	byValue bool
	// dups is set when a by-value scalar array has repeated values, so its
	// elements collide on their digest and need a position advisory to be
	// resolvable (satisfied-recoil).
	dups bool
}

// addressing picks a sequence's addressing per §9.4-R4: a key-match segment
// wherever the sequence has a usable identity field, because a positional
// address drifts the moment the user reorders the list.
func (d *differ) addressing(path Path, old, new *DiffNode) addressing {
	if old.Kind != KindSeq {
		return addressing{}
	}
	a := addressing{seq: true}
	oldE, newE := elements(old), elements(new)
	if len(oldE) == 0 && len(newE) == 0 {
		return a
	}
	if allOfKind(oldE, KindMap) && allOfKind(newE, KindMap) {
		usable := usableFields(oldE, newE)
		for _, cand := range d.fields {
			if contains(usable, cand) {
				a.field = cand
				return a
			}
		}
		switch len(usable) {
		case 0:
		case 1:
			a.field = usable[0]
		default:
			d.note("%s: %d fields are usable as an identity (%s) and none is a candidate (%s); addressing this sequence by index (§9.4-R4)",
				pathLabel(path), len(usable), strings.Join(usable, ", "), strings.Join(d.fields, ", "))
		}
		return a
	}
	if bothOfKind(oldE, newE, KindScalar) {
		// A scalar array is addressed by content hash (satisfied-recoil). When a
		// value repeats, the digests collide, so its elements also carry a
		// position advisory (`~hew:at=`/`~hew:length=`) to stay resolvable.
		a.byValue = true
		a.dups = !uniqueValues(oldE) || !uniqueValues(newE)
	}
	return a
}

func pathLabel(p Path) string {
	if s := p.String(); s != "" {
		return s
	}
	return "/"
}

// identities returns the token each child is matched on. Matching a mapping on
// its key rather than on its whole value is what makes "the value under this
// key changed" a replace instead of a delete plus an add.
func (a addressing) identities(children []DiffChild) []string {
	out := make([]string, len(children))
	for i, c := range children {
		switch {
		case c.Comment:
			out[i] = "#" + c.Text
		case !a.seq:
			out[i] = "k" + c.Key
		case a.field != "":
			out[i] = "f" + identityToken(c.Node, a.field)
		case a.byValue:
			out[i] = "v" + scalarToken(c.Node.Value)
		default:
			out[i] = "n" + c.Node.canonical()
		}
	}
	return out
}

func identityToken(n *DiffNode, field string) string {
	if v, ok := n.member(field); ok {
		return scalarToken(v.Value)
	}
	return "\x00missing"
}

// childPath addresses one child of the container at path.
func (a addressing) childPath(path Path, c DiffChild, index int) Path {
	switch {
	case c.Comment:
		return path.Append(commentSegment(c.Text))
	case !a.seq:
		return path.Append(Segment{Kind: SegKey, Name: c.Key})
	case a.field != "":
		if v, ok := c.Node.member(a.field); ok {
			return path.Append(Segment{Kind: SegMatch, Name: a.field, Value: valueScalar(v.Value)})
		}
		return path.Append(Segment{Kind: SegIndex, Index: index})
	case a.byValue:
		// A set member is addressed by a hash of its value, never by the value
		// itself (satisfied-recoil): `/tags/#hew:sha256=<hex>`, not `/tags/=beta`.
		return path.Append(hashSegment(c.Node.Value))
	default:
		return path.Append(Segment{Kind: SegIndex, Index: index})
	}
}

func (a addressing) valueOf(c *DiffChild) Value {
	if c.Comment {
		return CommentValue(c.Text)
	}
	return c.Node.Value
}

// tests renders one slot's before-image assertions (§9.0: a context line and a
// "-" line are the same assertion, differing only in what happens next).
//
// A keyed element shows only its identity field when it is context — the line
// is an address, and asserting the rest of an untouched neighbour would make
// the patch brittle for no gain — but shows every field when it is being
// removed, because the "-" line has to say what is going away.
func (a addressing) tests(s *slot) []Transform {
	c := s.old
	if c == nil {
		return nil // an added slot has no before-image
	}
	switch {
	case c.Comment:
		return []Transform{{Op: OpTest, Path: s.ref, Value: CommentValue(c.Text)}}
	case a.seq && a.field != "":
		fields := []string{a.field}
		if s.state == slotRemoved {
			fields = memberNames(c.Node)
		}
		var out []Transform
		for _, f := range fields {
			v, ok := c.Node.member(f)
			if !ok {
				continue
			}
			out = append(out, Transform{Op: OpTest,
				Path:  s.ref.Append(Segment{Kind: SegKey, Name: f}),
				Value: v.Value})
		}
		return out
	default:
		return []Transform{{Op: OpTest, Path: s.ref, Value: c.Node.Value}}
	}
}

func memberNames(n *DiffNode) []string {
	var out []string
	for _, c := range n.Children {
		if !c.Comment {
			out = append(out, c.Key)
		}
	}
	return out
}

// --- identity-field qualification -------------------------------------------

func elements(n *DiffNode) []*DiffNode {
	var out []*DiffNode
	for _, c := range n.Children {
		if !c.Comment {
			out = append(out, c.Node)
		}
	}
	return out
}

func allOfKind(ns []*DiffNode, k NodeKind) bool {
	for _, n := range ns {
		if n == nil || n.Kind != k {
			return false
		}
	}
	return len(ns) > 0
}

// bothOfKind asks the ADDRESSING question, which is not allOfKind's: can these
// elements be addressed by kind k, given a before side and an after side. An
// EMPTY side agrees vacuously, because a sequence emptied out is still made of
// whatever the side that HAS elements is made of, and the addresses being
// written name elements on that side. allOfKind answers the narrower question
// "do these nodes agree on a kind", where nothing cannot agree — asking it of an
// absent side made an emptied scalar array fall back to INDEX addressing while
// the notation still wrote its elements as values, so the differ and a re-parse
// of its own patch disagreed about how the elements were addressed.
func bothOfKind(oldE, newE []*DiffNode, k NodeKind) bool {
	switch {
	case len(oldE) == 0:
		return allOfKind(newE, k)
	case len(newE) == 0:
		return allOfKind(oldE, k)
	}
	return allOfKind(oldE, k) && allOfKind(newE, k)
}

func uniqueValues(ns []*DiffNode) bool {
	seen := make(map[string]bool, len(ns))
	for _, n := range ns {
		tok := scalarToken(n.Value)
		if seen[tok] {
			return false
		}
		seen[tok] = true
	}
	return true
}

// usableFields is §9.4-R4's condition, applied to both sides: present on every
// element, scalar, and unique across the sequence. Both sides must qualify,
// because the same segment has to address the old document (where the `test`
// is evaluated) and name the new element it came from.
func usableFields(old, new []*DiffNode) []string {
	usable := qualifying(old)
	keep := qualifying(new)
	var out []string
	for _, f := range usable {
		if contains(keep, f) {
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}

func qualifying(ns []*DiffNode) []string {
	if len(ns) == 0 {
		return nil
	}
	var out []string
	for _, c := range ns[0].Children {
		if c.Comment {
			continue
		}
		if fieldQualifies(ns, c.Key) {
			out = append(out, c.Key)
		}
	}
	return out
}

func fieldQualifies(ns []*DiffNode, field string) bool {
	seen := make(map[string]bool, len(ns))
	for _, n := range ns {
		v, ok := n.member(field)
		if !ok || v.Kind != KindScalar {
			return false
		}
		tok := scalarToken(v.Value)
		if seen[tok] {
			return false
		}
		seen[tok] = true
	}
	return true
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// valueScalar converts a decoded scalar back into the identity Scalar a
// key-match segment carries (§4.2), the inverse of Scalar.Value. A string that
// would read back as a number, a boolean or null is spelled quoted, so that
// `port="8080"` survives as the string it is.
func valueScalar(v Value) Scalar {
	n := v.Node()
	if n == nil || n.Kind != yaml.ScalarNode {
		return Scalar{Kind: ScalarString, Text: v.String(), Quoted: true}
	}
	switch n.ShortTag() {
	case "!!bool":
		return Scalar{Kind: ScalarBool, Text: n.Value}
	case "!!null":
		return Scalar{Kind: ScalarNull, Text: "null"}
	case "!!int", "!!float":
		return Scalar{Kind: ScalarNumber, Text: n.Value}
	}
	// No Quoted here, and that is the point: the differ used to carry its own
	// copy of "which strings would re-read as something else", and O42 made
	// that the RENDERER's rule — pathString force-quotes exactly this set, and
	// a little more (a value ending `?`). One copy of the rule cannot
	// drift from itself.
	return Scalar{Kind: ScalarString, Text: n.Value}
}
