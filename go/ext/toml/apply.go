package toml

import (
	"fmt"
	"sort"
	"strings"

	"github.com/benjaminabbitt/hew/go"
	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

// Apply is the TOML binding's apply half (§8.4, Appendix A.4's Applier.Apply
// for the "toml" format). It resolves every path against the document it just
// parsed, evaluates every OpTest before performing any mutation, and returns
// the re-serialized bytes — or, on any error, nil bytes and that error
// (§10.5's all-or-nothing).
func Apply(target []byte, tl hew.TransformList) ([]byte, error) {
	d, err := parseDoc(target)
	if err != nil {
		return nil, &hewerr.Error{Code: hewerr.CodeTargetParse, Component: hewerr.ComponentApplier,
			Target: tl.Target, Detail: "target does not parse as TOML: " + err.Error()}
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
			return nil, err
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
	return applyEdits(target, edits)
}

// run is one Apply invocation's state: the parsed target, the whole transform
// list (the after-image checks of §10.6 need to see a test's paired write),
// and the set of paths whose before-image assert was tolerated as "already
// applied" (§7.5).
type run struct {
	d         *doc
	target    string
	all       []hew.Transform
	converged map[string]bool

	// pending is the insertions this run has already planned, which is what
	// lets a `+` line be placed after another `+` line of the same hunk (§9.1
	// step 5) even though that sibling is in no parse of the target.
	pending []pendingAdd
}

// unsupported refuses a transform carrying a qualifier this binding cannot
// honour: a YAML anchor policy, or an ordinal selector, which would otherwise
// silently resolve to the first matching sibling — the exact failure §9.3
// forbids.
func (r *run) unsupported(t hew.Transform) error {
	if t.Anchor != "" {
		return r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine,
			"anchor is a YAML alias directive and has no TOML meaning (§8.3)")
	}
	if t.Path.HasOrdinal() || t.From.HasOrdinal() {
		return r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine,
			"ordinal selectors are not implemented by the TOML binding; address the node by key or value (§4.2, §7.2)")
	}
	return nil
}

func (r *run) err(code hewerr.Code, path string, line int, detail string) *hewerr.Error {
	return &hewerr.Error{Code: code, Component: hewerr.ComponentApplier, Target: r.target,
		Path: path, PatchLine: line, Detail: detail}
}

// edit is one byte-range splice against the ORIGINAL source: replace
// [start,end) with text. An insertion is start==end.
type edit struct {
	start, end int
	text       string
}

func applyEdits(src []byte, edits []edit) ([]byte, error) {
	sort.SliceStable(edits, func(i, j int) bool { return edits[i].start < edits[j].start })
	for i := 1; i < len(edits); i++ {
		if edits[i].start < edits[i-1].end {
			return nil, &hewerr.Error{Code: hewerr.CodeConflict, Component: hewerr.ComponentApplier,
				Detail: "two transforms touch overlapping regions of the target (§10 HEW030)"}
		}
	}
	var b strings.Builder
	pos := 0
	for _, e := range edits {
		b.Write(src[pos:e.start])
		b.WriteString(e.text)
		pos = e.end
	}
	b.Write(src[pos:])
	return []byte(b.String()), nil
}

// --- path resolution --------------------------------------------------------

// ref is a resolved address: the node itself plus enough of its surroundings
// to edit it — the table it sits in, the member or element that holds it, or
// the comment node it names.
type ref struct {
	node    *tnode
	parent  *tnode
	entry   *entry
	elem    *elem
	comment *commentNode
}

// resolveErr classifies a step failure. final marks a code the caller must not
// reinterpret: HEW012 and HEW041 are decided at the step that raised them, not
// by the transform that asked. It is deliberately not an `error`: every step
// returns it concretely, so no caller can lose the finality flag by widening
// it to the interface.
type resolveErr struct {
	code   hewerr.Code
	detail string
	final  bool
}

func noMatch(format string, args ...any) *resolveErr {
	return &resolveErr{code: hewerr.CodeNoMatch, detail: fmt.Sprintf(format, args...)}
}

// resolve walks a whole path from the document root, translating a step
// failure into a hewerr.Error naming the prefix that failed. The third result
// is the step's finality: a final error was decided by the step that raised it
// and the asking transform must not reinterpret it as its own drift error.
func (r *run) resolve(p hew.Path, line int) (*ref, *hewerr.Error, bool) {
	cur := &ref{node: r.d.root}
	for i, seg := range p.Segments() {
		next, re := r.step(cur, seg)
		if re != nil {
			failed := hew.RootPath().Append(p.Segments()[:i+1]...)
			return nil, r.err(re.code, failed.String(), line, re.detail), re.final
		}
		cur = next
	}
	return cur, nil, false
}

// step resolves one segment against the current node. A resolved node defined
// at two surfaces is §8.4 rule 2's refusal: hew will not pick one, because
// picking one silently orphans the other.
func (r *run) step(cur *ref, seg hew.Segment) (*ref, *resolveErr) {
	next, re := r.stepRaw(cur, seg)
	if re != nil {
		return nil, re
	}
	if next.node != nil && next.node.defs > 1 {
		return nil, &resolveErr{code: hewerr.CodeSurfaceAmbiguity, final: true,
			detail: fmt.Sprintf("%q is defined at %d surfaces in this document (a dotted key and a table header "+
				"denote the same node); hew refuses to pick one, because picking one orphans the other (§8.4 rule 2)",
				dottedKey(next.node.path), next.node.defs)}
	}
	return next, nil
}

func (r *run) stepRaw(cur *ref, seg hew.Segment) (*ref, *resolveErr) {
	if seg.Kind == hew.SegComment {
		return r.stepComment(cur, seg)
	}
	n := cur.node
	if n == nil {
		return nil, noMatch("cannot descend into a comment node")
	}
	switch seg.Kind {
	case hew.SegKey:
		if n.kind != nTable {
			return nil, noMatch("%q: not a table", seg.Name)
		}
		e := n.lookup(seg.Name)
		if e == nil {
			return nil, noMatch("no key %q", seg.Name)
		}
		return &ref{node: e.val, parent: n, entry: e}, nil
	case hew.SegIndex:
		if n.kind != nSeq {
			return nil, noMatch("not an array")
		}
		if seg.Index < 0 || seg.Index >= len(n.elems) {
			return nil, noMatch("index %d out of range", seg.Index)
		}
		el := n.elems[seg.Index]
		return &ref{node: el.val, parent: n, elem: el}, nil
	case hew.SegMatch:
		if n.kind != nSeq {
			return nil, noMatch("not an array")
		}
		var found *elem
		count := 0
		for _, el := range n.elems {
			if matchesSeg(el.val, seg) {
				found = el
				count++
			}
		}
		switch count {
		case 0:
			return nil, noMatch("no element matches %s", seg.String())
		case 1:
			return &ref{node: found.val, parent: n, elem: found}, nil
		}
		return nil, &resolveErr{code: hewerr.CodeAmbiguousMatch, final: true,
			detail: fmt.Sprintf("%d elements match %s; hew will not pick one (§6.4.2)", count, seg.String())}
	}
	return nil, noMatch("segment kind %v has no TOML representation (§8.4)", seg.Kind)
}

// stepComment resolves a comment address (§4.5b): "#n" selects the n'th
// standalone comment of the current table, "#t" the trailing comment on the
// current node's line.
func (r *run) stepComment(cur *ref, seg hew.Segment) (*ref, *resolveErr) {
	if cur.node == nil {
		return nil, noMatch("no node to attach a comment address to")
	}
	if seg.Trailing {
		from, ok := r.commentAnchor(cur)
		if !ok {
			return nil, noMatch("this node has no line of its own to carry a trailing comment")
		}
		c := r.d.trailingComment(from)
		if c == nil {
			return nil, noMatch("no trailing comment here")
		}
		return &ref{comment: c, parent: cur.parent}, nil
	}
	comments := r.d.commentChildren(cur.node)
	if seg.Index < 0 || seg.Index >= len(comments) {
		return nil, noMatch("no comment #%d in this container (found %d)", seg.Index, len(comments))
	}
	return &ref{comment: comments[seg.Index], parent: cur.node}, nil
}

// commentAnchor is the offset a "#t" address scans forward from: the end of
// the node's own value text, or — for a table opened by a header — the header
// line itself. A node with neither carries no trailing comment.
func (r *run) commentAnchor(cur *ref) (int, bool) {
	if cur.node.end > cur.node.start {
		return cur.node.end, true
	}
	if cur.node.physical && cur.node.regionStart > 0 {
		return r.d.lineStartOf(cur.node.regionStart - 1), true
	}
	return 0, false
}
