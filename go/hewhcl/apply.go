// Package hewhcl is hew's HCL binding (spec §8.5): attributes, blocks, label
// addressing, and the ordinal selector that repeated `(type, labels)` tuples
// force.
//
// Byte preservation is structural, not textual. hclsyntax parses the target,
// this package records where every node's bytes are, and every edit is a
// splice against the original source. Nothing is re-serialized — which is how
// §8.5's mixed rule is met: a body hew MODIFIED adopts hclfmt's `=` alignment,
// and a body it did not touch stays byte-identical, down to a hand-written
// misalignment nobody asked hew to fix.
package hewhcl

import (
	"fmt"
	"sort"

	"github.com/benjaminabbitt/hew/go"
	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

// Apply is the HCL binding's apply half (§8.5, Appendix A.4's Applier.Apply
// for the "hcl" format). It evaluates every OpTest before performing any
// mutation and returns the spliced bytes — or, on any error, nil bytes and
// that error (§10.5's all-or-nothing).
func Apply(target []byte, tl hew.TransformList) ([]byte, error) {
	d, err := parseDoc(target)
	if err != nil {
		return nil, &hewerr.Error{Code: hewerr.CodeTargetParse, Component: hewerr.ComponentApplier,
			Target: tl.Target, Detail: "target does not parse as HCL: " + err.Error()}
	}
	r := &run{d: d, target: tl.Target, all: tl.Transform, converged: map[string]bool{}}

	// Pass 0: refuse qualifiers this binding does not implement. §9.3 is
	// explicit that ignoring one is non-conformant, not lenient.
	for _, t := range tl.Transform {
		if err := r.unsupported(t); err != nil {
			return nil, err
		}
	}

	// Pass 1: every test, before any mutation is even computed (§9.0, §9.3).
	for _, t := range tl.Transform {
		if t.Op != hew.OpTest {
			continue
		}
		if err := r.evalTest(t); err != nil {
			return nil, r.ordinalDrift(t, err)
		}
	}

	// Pass 2: compute edits. Still no bytes written — an error here also
	// leaves the target untouched.
	var edits []edit
	for _, t := range tl.Transform {
		var es []edit
		var perr error
		switch t.Op {
		case hew.OpTest:
			continue
		case hew.OpAdd:
			es, perr = r.planAdd(t)
		case hew.OpRemove:
			es, perr = r.planRemove(t)
		case hew.OpReplace:
			es, perr = r.planReplace(t)
		case hew.OpCopy:
			es, perr = r.planCopy(t)
		default:
			perr = r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine, fmt.Sprintf("unsupported op %q", t.Op))
		}
		if perr != nil {
			return nil, perr
		}
		edits = append(edits, es...)
	}

	out, marks, err := applyEdits(target, edits)
	if err != nil {
		return nil, err
	}
	return r.realign(out, marks)
}

// run is one Apply invocation's state: the parsed target, the whole transform
// list (the after-image checks of §10.6 need to see a test's paired write),
// the paths whose before-image assert was tolerated as "already applied"
// (§7.5), and the bodies hew modified, which are the bodies that adopt the
// backend's alignment (§8.5).
type run struct {
	d         *doc
	target    string
	all       []hew.Transform
	converged map[string]bool
	touched   []int

	// pending is the insertions this run has already planned, which is what
	// lets a `+` line be placed after another `+` line of the same hunk (§9.1
	// step 5) even though that sibling is in no parse of the target.
	pending []pendingAdd
}

// touch records that a body was modified, by the byte offset of its opening
// brace in the ORIGINAL source (rootBody for the document root).
func (r *run) touch(b *bodyNode) {
	for _, o := range r.touched {
		if o == b.open {
			return
		}
	}
	r.touched = append(r.touched, b.open)
}

// unsupported refuses a transform carrying a qualifier this binding cannot
// honour.
func (r *run) unsupported(t hew.Transform) error {
	if t.Surface != "" {
		return r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine,
			"surface is a TOML placement directive and has no HCL meaning (§8.4)")
	}
	if t.Anchor != "" {
		return r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine,
			"anchor is a YAML alias policy and has no HCL meaning (§8.3)")
	}
	return nil
}

func (r *run) err(code hewerr.Code, path string, line int, detail string) *hewerr.Error {
	return &hewerr.Error{Code: code, Component: hewerr.ComponentApplier, Target: r.target,
		Path: path, PatchLine: line, Detail: detail}
}

// --- edits ------------------------------------------------------------------

// edit is one byte-range splice against the ORIGINAL source: replace
// [start,end) with text. An insertion is start==end.
type edit struct {
	start, end int
	text       string

	// blank asks for one blank line between what precedes the insertion IN THE
	// OUTPUT and this text. It cannot be decided while planning, because a
	// removal earlier in the same run may be what ends up preceding it —
	// hcl/ir-relabel-block is exactly that: the copy lands at the end of a file
	// whose only block the paired remove deletes, and the result must not open
	// with a blank line.
	blank bool
}

// mark records, at one point in the output, how far the output has drifted
// from the source. It is what lets the realignment pass find a body it
// identified before the splice.
type mark struct {
	src   int
	delta int
}

func applyEdits(src []byte, edits []edit) ([]byte, []mark, error) {
	sort.SliceStable(edits, func(i, j int) bool { return edits[i].start < edits[j].start })
	for i := 1; i < len(edits); i++ {
		if edits[i].start < edits[i-1].end {
			return nil, nil, &hewerr.Error{Code: hewerr.CodeConflict, Component: hewerr.ComponentApplier,
				Detail: "two transforms touch overlapping regions of the target (§10 HEW030)"}
		}
	}
	out := make([]byte, 0, len(src))
	marks := []mark{{src: 0, delta: 0}}
	pos := 0
	for _, e := range edits {
		out = append(out, src[pos:e.start]...)
		if e.blank {
			out = appendBlankSeparator(out)
		}
		out = append(out, e.text...)
		pos = e.end
		marks = append(marks, mark{src: pos, delta: len(out) - pos})
	}
	out = append(out, src[pos:]...)
	return out, marks, nil
}

// appendBlankSeparator brings the output to "ends with a blank line", unless
// there is nothing before it at all.
func appendBlankSeparator(out []byte) []byte {
	switch {
	case len(out) == 0:
		return out
	case len(out) >= 2 && out[len(out)-1] == '\n' && out[len(out)-2] == '\n':
		return out
	case out[len(out)-1] == '\n':
		return append(out, '\n')
	}
	return append(out, '\n', '\n')
}

// shift maps an original offset to its offset in the spliced output.
func shift(marks []mark, off int) int {
	delta := 0
	for _, m := range marks {
		if m.src > off {
			break
		}
		delta = m.delta
	}
	return off + delta
}

// --- path resolution --------------------------------------------------------

// ref is a resolved address: the item and the body that holds it. The document
// root resolves to a ref with no item.
type ref struct {
	item   *itemNode
	parent *bodyNode
}

func (rf *ref) body() *bodyNode {
	if rf.item == nil {
		return rf.parent
	}
	return rf.item.body
}

// resolveErr classifies a step failure. final marks a code the caller must not
// reinterpret: HEW012 is decided at the step that raised it, not by the
// transform that asked.
type resolveErr struct {
	code   hewerr.Code
	detail string
	final  bool
}

func (e *resolveErr) Error() string { return e.detail }

func noMatch(format string, args ...any) *resolveErr {
	return &resolveErr{code: hewerr.CodeNoMatch, detail: fmt.Sprintf(format, args...)}
}

// resolve walks a whole path from the document root. A path step in HCL
// consumes a NAME segment and the label segments that follow it, because
// `/provider/"aws"` is one address — a `(type, labels)` tuple — not two (§4.3).
func (r *run) resolve(p hew.Path, t hew.Transform) (*ref, *hewerr.Error, bool) {
	segs := p.Segments()
	cur := &ref{parent: r.d.root}
	for i := 0; i < len(segs); {
		next, used, err := r.step(cur, segs[i:])
		if err != nil {
			re := err.(*resolveErr)
			failed := hew.NewPath(segs[:i+used]...)
			return nil, r.err(re.code, failed.String(), r.lineFor(t, i+used), re.detail), re.final
		}
		cur = next
		i += used
	}
	return cur, nil, false
}

// lineFor picks the patch line a resolution failure is reported against. A
// failure INSIDE the hunk's anchor is the anchor's failure and is reported at
// the `@@` line — the address the reviewer has to fix is written there, not on
// the body line that happened to ask first. hcl/repeated-label-ambiguous pins
// it (HEW012 at the anchor's line 5, not at the context line's 6).
func (r *run) lineFor(t hew.Transform, depth int) int {
	if t.AnchorLine > 0 && depth <= t.AnchorPath.Len() {
		return t.AnchorLine
	}
	return t.PatchLine
}

// step resolves one address step: a name and the labels that qualify it. It
// returns the number of path segments it consumed.
func (r *run) step(cur *ref, segs []hew.Segment) (*ref, int, error) {
	seg := segs[0]
	if seg.Kind != hew.SegKey {
		return nil, 1, &resolveErr{code: hewerr.CodeInexpressible, final: true,
			detail: fmt.Sprintf("segment kind %v has no HCL representation (§8.5)", seg.Kind)}
	}
	b := cur.body()
	if b == nil {
		return nil, 1, noMatch("%q: an attribute has no body to descend into", seg.Name)
	}

	// Collect the label segments qualifying this name, and the ordinal, which
	// the IR carries on whichever segment closed the tuple (§9.6, §11.10).
	used := 1
	var labels []string
	for used < len(segs) && segs[used].Kind == hew.SegLabel {
		labels = append(labels, segs[used].Name)
		used++
	}
	ord, err := tupleOrdinal(segs[:used])
	if err != nil {
		return nil, used, err
	}

	var cands []*itemNode
	for _, it := range b.items {
		if it.name != seg.Name || !labelsMatch(it, labels) {
			continue
		}
		cands = append(cands, it)
	}
	switch {
	case len(cands) == 0:
		return nil, used, noMatch("no %s here", tupleString(seg.Name, labels))
	case ord != nil:
		if *ord >= len(cands) {
			return nil, used, noMatch("ord=%d selects the %s of only %d here",
				*ord, ordinalWord(*ord), len(cands))
		}
		return &ref{item: cands[*ord], parent: b}, used, nil
	case len(cands) > 1:
		return nil, used, &resolveErr{code: hewerr.CodeAmbiguousMatch, final: true,
			detail: fmt.Sprintf("%d blocks match %s; hew will not pick one — add \"! match ord=\" to select one (§7.2, §8.5)",
				len(cands), tupleString(seg.Name, labels))}
	}
	return &ref{item: cands[0], parent: b}, used, nil
}

// labelsMatch reports whether an item answers to the label segments given. No
// labels at all matches any block of that type, which is what makes
// `/provider` name every provider block and therefore HEW012.
func labelsMatch(it *itemNode, labels []string) bool {
	if len(labels) == 0 {
		return true
	}
	if it.kind != itemBlock || len(it.labels) < len(labels) {
		return false
	}
	for i, l := range labels {
		if it.labels[i] != l {
			return false
		}
	}
	return true
}

// tupleOrdinal reads the one `[n]` selector an address step may carry.
func tupleOrdinal(segs []hew.Segment) (*int, error) {
	var ord *int
	for _, s := range segs {
		if s.Ordinal == nil {
			continue
		}
		if ord != nil {
			return nil, &resolveErr{code: hewerr.CodeInexpressible, final: true,
				detail: "an address step carries more than one ordinal selector (§7.2)"}
		}
		ord = s.Ordinal
	}
	return ord, nil
}

func tupleString(name string, labels []string) string {
	s := name
	for _, l := range labels {
		s += " " + quoteHCL(l)
	}
	return s
}

func ordinalWord(n int) string {
	switch n {
	case 0:
		return "1st"
	case 1:
		return "2nd"
	case 2:
		return "3rd"
	}
	return fmt.Sprintf("%dth", n+1)
}
