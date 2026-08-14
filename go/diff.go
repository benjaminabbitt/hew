package hew

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/hew-format/hew/internal/hewerr"
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

// DefaultKeyFields is §9.4-R4's candidate identity-field list, tried in order.
// It is binding DATA, not a rule (ruling O18): the rule is "present on every
// element, scalar, unique", and `--key-fields` overrides the list.
var DefaultKeyFields = []string{"name", "id", "key"}

// DiffOptions configures the differ (Appendix A.5).
type DiffOptions struct {
	// KeyFields are the candidate identity fields for keyed-array addressing,
	// tried in order (§9.4-R4). Empty means DefaultKeyFields.
	KeyFields []string

	// Context is the sibling radius (§9.4-R2). Zero means ContextDefault;
	// ContextNone and ContextAll spell the two ends.
	Context int

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

func (o DiffOptions) keyFields() []string {
	if len(o.KeyFields) == 0 {
		return DefaultKeyFields
	}
	return o.KeyFields
}

func diffErr(code hewerr.Code, target, path, format string, args ...any) error {
	return &hewerr.Error{
		Code:      code,
		Component: hewerr.ComponentDiffer,
		Target:    target,
		Path:      path,
		Detail:    fmt.Sprintf(format, args...),
	}
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
	if err := d.root(old, new); err != nil {
		return TransformList{}, err
	}
	return TransformList{Target: opt.Target, Format: format, Transform: d.out}, nil
}

type differ struct {
	opt    DiffOptions
	fields []string
	radius int
	all    bool
	out    []Transform
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
	oldCmt := commentOrdinals(old.Children)
	newCmt := commentOrdinals(new.Children)

	var out []slot
	for _, step := range myers(oldIDs, newIDs) {
		switch step.Kind {
		case editDelete:
			c := &old.Children[step.A]
			ref := addr.childPath(path, *c, step.A, oldCmt[step.A])
			out = append(out, slot{state: slotRemoved, old: c, ref: ref, addPath: ref})
		case editInsert:
			c := &new.Children[step.B]
			ref := addr.childPath(path, *c, step.B, newCmt[step.B])
			add := ref
			if addr.seq && !c.Comment {
				add = path
			}
			out = append(out, slot{state: slotAdded, new: c, ref: ref, addPath: add})
		default:
			o, n := &old.Children[step.A], &new.Children[step.B]
			ref := addr.childPath(path, *o, step.A, oldCmt[step.A])
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

// commentOrdinals numbers a container's comment children, which is what the
// `#<n>` address counts (§4.5b: kind-scoped within the container).
func commentOrdinals(children []DiffChild) []int {
	out := make([]int, len(children))
	n := 0
	for i, c := range children {
		if c.Comment {
			out[i] = n
			n++
		} else {
			out[i] = -1
		}
	}
	return out
}

// --- context radius and emission --------------------------------------------

// emit writes one container's hunk: every assertion first, in body order, then
// every mutation, in body order — the order §9.1's lowering produces, inverted,
// so that a differ-produced list and a parser-produced list of the same patch
// are the same list.
func (d *differ) emit(addr addressing, slots []slot) {
	show := d.window(slots)
	for i := range slots {
		if !show[i] {
			continue
		}
		d.out = append(d.out, addr.tests(&slots[i])...)
	}
	for i := range slots {
		switch slots[i].state {
		case slotRemoved:
			d.out = append(d.out, Transform{Op: OpRemove, Path: slots[i].ref})
		case slotReplaced:
			d.out = append(d.out, Transform{Op: OpReplace, Path: slots[i].ref, Value: addr.valueOf(slots[i].new)})
		case slotAdded:
			t := Transform{Op: OpAdd, Path: slots[i].addPath, Value: addr.valueOf(slots[i].new)}
			if ref, ok := placement(slots, i); ok {
				if ref.before {
					t.Before = ref.path
				} else {
					t.After = ref.path
				}
			}
			d.out = append(d.out, t)
		}
	}
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
func (d *differ) window(slots []slot) []bool {
	show := make([]bool, len(slots))
	for i := range slots {
		if d.all {
			show[i] = true
			continue
		}
		if !slots[i].state.changed() {
			continue
		}
		for j := i - d.radius; j <= i+d.radius; j++ {
			if j >= 0 && j < len(slots) {
				show[j] = true
			}
		}
	}
	return show
}

type placementRef struct {
	path   Path
	before bool
}

// placement derives §9.1 step 5's relative position for an added slot: the
// nearest surviving sibling before it, or failing that the nearest after it.
// Never an index — the patch must stay target-independent (§9.2).
func placement(slots []slot, i int) (placementRef, bool) {
	for j := i - 1; j >= 0; j-- {
		if slots[j].state.survives() {
			return placementRef{path: slots[j].ref}, true
		}
	}
	for j := i + 1; j < len(slots); j++ {
		if slots[j].state.survives() {
			return placementRef{path: slots[j].ref, before: true}, true
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
	if allOfKind(oldE, KindScalar) && allOfKind(newE, KindScalar) &&
		uniqueValues(oldE) && uniqueValues(newE) {
		a.byValue = true
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
func (a addressing) childPath(path Path, c DiffChild, index, commentIndex int) Path {
	switch {
	case c.Comment:
		return path.Append(Segment{Kind: SegComment, Index: commentIndex})
	case !a.seq:
		return path.Append(Segment{Kind: SegKey, Name: c.Key})
	case a.field != "":
		if v, ok := c.Node.member(a.field); ok {
			return path.Append(Segment{Kind: SegMatch, Name: a.field, Value: valueScalar(v.Value)})
		}
		return path.Append(Segment{Kind: SegIndex, Index: index})
	case a.byValue:
		return path.Append(Segment{Kind: SegMatch, Value: valueScalar(c.Node.Value)})
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
	return Scalar{Kind: ScalarString, Text: n.Value, Quoted: ambiguousAsPathText(n.Value)}
}

func ambiguousAsPathText(s string) bool {
	switch s {
	case "", "true", "false", "null":
		return true
	}
	return isNumber(s) || strings.HasPrefix(s, `"`)
}
