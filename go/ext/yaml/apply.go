package yaml

import (
	"fmt"
	"sort"
	"strings"

	"github.com/benjaminabbitt/hew/go"
	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

// Apply is the YAML binding's apply half (§8.3, Appendix A.4's Applier.Apply
// for the "yaml" format). It resolves every path against the document it just
// parsed, evaluates every OpTest before performing any mutation, and returns
// the re-serialized bytes — or, on any error, nil bytes and that error
// (§10.5's all-or-nothing).
func Apply(target []byte, tl hew.TransformList) ([]byte, error) {
	d, err := parseDoc(target)
	if err != nil {
		return nil, &hewerr.Error{Code: hewerr.CodeTargetParse, Component: hewerr.ComponentApplier,
			Target: tl.Target, Detail: "target does not parse as YAML: " + err.Error()}
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
	// pending records the inserts planned so far, so a placement naming a
	// sibling this same patch adds can still find it (§9.1 step 5's chain).
	pending []pendingAdd
}

// unsupported refuses a transform carrying a qualifier this binding cannot
// honour: a TOML surface directive, or an ordinal selector, which would
// otherwise silently resolve to the first matching sibling — the exact
// failure §9.3 forbids.
func (r *run) unsupported(t hew.Transform) error {
	if t.Surface != "" {
		return r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine,
			"surface is a TOML placement directive and has no YAML meaning (§8.4)")
	}
	if t.Path.HasOrdinal() || t.From.HasOrdinal() {
		return r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine,
			"ordinal selectors are not implemented by the YAML binding; address the node by key or value (§4.2, §7.2)")
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
// to edit it — the container it sits in, the member or element that holds it,
// or the comment node it names.
type ref struct {
	node    *ynode
	parent  *ynode
	entry   *entry
	elem    *elem
	comment *commentNode

	// inherited marks a key reached through a merge key under "! anchor
	// fork": the value read is the anchor's, and a write shadows it by
	// creating an explicit member in site (§8.3).
	inherited  bool
	site       *ynode
	siteKey    string
	anchorName string
}

// resolveErr classifies a step failure. final marks a code the caller must
// not reinterpret: HEW012, HEW040 and the merge-key HEW013 are decided at the
// step that raised them, not by the transform that asked.
type resolveErr struct {
	code   hewerr.Code
	detail string
	final  bool
}

func (e *resolveErr) Error() string { return e.detail }

func noMatch(format string, args ...any) *resolveErr {
	return &resolveErr{code: hewerr.CodeNoMatch, detail: fmt.Sprintf(format, args...)}
}

// resolve walks a whole path from the document root, translating a step
// failure into a hewerr.Error naming the prefix that failed. The third result
// is the step's finality: a final error was decided by the step that raised
// it (HEW012, HEW040, the merge-key HEW013) and the asking transform must not
// reinterpret it as its own drift error.
func (r *run) resolve(p hew.Path, mode hew.AnchorMode, line int) (*ref, *hewerr.Error, bool) {
	cur := &ref{node: r.d.root}
	for i, seg := range p.Segments() {
		next, err := r.step(cur, seg, mode)
		if err != nil {
			re := err.(*resolveErr)
			failed := hew.NewPath(p.Segments()[:i+1]...)
			return nil, r.err(re.code, failed.String(), line, re.detail), re.final
		}
		cur = next
	}
	return cur, nil, false
}

// step resolves one segment against the current node.
func (r *run) step(cur *ref, seg hew.Segment, mode hew.AnchorMode) (*ref, error) {
	if seg.Kind == hew.SegComment {
		return r.stepComment(cur, seg)
	}
	n := cur.node
	if n == nil {
		return nil, noMatch("cannot descend into a comment node")
	}
	if n.kind == nAlias {
		followed, err := r.followAlias(n, mode)
		if err != nil {
			return nil, err
		}
		n = followed
	}
	switch seg.Kind {
	case hew.SegKey:
		if n.kind != nMap {
			return nil, noMatch("%q: not a mapping", seg.Name)
		}
		if e := n.lookup(seg.Name); e != nil {
			return &ref{node: e.val, parent: n, entry: e}, nil
		}
		return r.stepMerged(n, seg, mode)
	case hew.SegIndex:
		if n.kind != nSeq {
			return nil, noMatch("not a sequence")
		}
		if seg.Index < 0 || seg.Index >= len(n.elems) {
			return nil, noMatch("index %d out of range", seg.Index)
		}
		el := n.elems[seg.Index]
		return &ref{node: el.val, parent: n, elem: el}, nil
	case hew.SegMatch:
		if n.kind != nSeq {
			return nil, noMatch("not a sequence")
		}
		var found *elem
		var cands []hew.Value
		count := 0
		for _, el := range n.elems {
			v, has := r.d.comparedValue(el.val, seg)
			if !has {
				continue
			}
			cands = append(cands, v)
			if scalarEq(v.Node(), scalarNode(seg.Value)) {
				found = el
				count++
			}
		}
		switch count {
		case 0:
			// O46: name the near miss and its type (§10.3), in the core's
			// wording so every binding says it the same way.
			return nil, noMatch("%s", hew.NoMatchDetail(seg, cands))
		case 1:
			return &ref{node: found.val, parent: n, elem: found}, nil
		}
		return nil, &resolveErr{code: hewerr.CodeAmbiguousMatch, final: true,
			detail: fmt.Sprintf("%d elements match %s; hew will not pick one (§6.4.2)", count, seg.String())}
	}
	return nil, noMatch("segment kind %v has no YAML representation (§8.3)", seg.Kind)
}

// stepMerged handles a key a mapping has only via "<<:" (§8.3). Reading or
// writing it needs an explicit policy, because rewriting the anchor changes
// every use site and forking changes none of the others.
func (r *run) stepMerged(n *ynode, seg hew.Segment, mode hew.AnchorMode) (*ref, error) {
	e, holder, anchor := r.d.mergedLookup(n, seg.Name)
	if e == nil {
		return nil, noMatch("no key %q", seg.Name)
	}
	switch mode {
	case hew.AnchorRewrite:
		return &ref{node: e.val, parent: holder, entry: e}, nil
	case hew.AnchorFork:
		return &ref{node: e.val, parent: holder, entry: e, inherited: true,
			site: n, siteKey: seg.Name, anchorName: anchor}, nil
	}
	// No directive. With more than one alias site the edit is genuinely
	// ambiguous (§8.3, HEW040); with a single site there is nothing to
	// disambiguate and the merge-key rule stands on its own: the key is not
	// present here, which is HEW013 naming the anchor it came from. The
	// deciding pair is yaml/alias-ambiguous (two sites) and
	// yaml/merge-key-remove (one).
	if r.d.aliases[anchor] > 1 {
		return nil, &resolveErr{code: hewerr.CodeAnchorAmbiguity, final: true,
			detail: fmt.Sprintf("%q here comes from the anchor &%s, aliased at %d sites; "+
				"add \"! anchor rewrite\" to edit the anchor definition or \"! anchor fork\" to materialize this site (§8.3)",
				seg.Name, anchor, r.d.aliases[anchor])}
	}
	return nil, &resolveErr{code: hewerr.CodeNoMatch, final: true,
		detail: fmt.Sprintf("%q is present here only via the merge key \"<<: *%s\"; an inherited key is not present at this site "+
			"and cannot be removed, only shadowed (§8.3)", seg.Name, anchor)}
}

// followAlias resolves an alias node to the node it names, under the
// transform's anchor policy.
func (r *run) followAlias(n *ynode, mode hew.AnchorMode) (*ynode, error) {
	name := n.y.Value
	switch mode {
	case hew.AnchorRewrite:
		target, ok := r.d.anchors[name]
		if !ok {
			return nil, noMatch("alias *%s names no anchor in this document", name)
		}
		return target, nil
	case hew.AnchorFork:
		return nil, &resolveErr{code: hewerr.CodeInexpressible, final: true,
			detail: fmt.Sprintf("forking the whole aliased node *%s is not expressible in this binding; "+
				"fork applies to a merge-inherited key (§8.3)", name)}
	}
	return nil, &resolveErr{code: hewerr.CodeAnchorAmbiguity, final: true,
		detail: fmt.Sprintf("the path resolves at the alias *%s; add \"! anchor rewrite\" or \"! anchor fork\" (§8.3)", name)}
}

// stepComment resolves a comment address (§4.5b): "#n" selects the n'th
// standalone comment of the current container, "#t" the trailing comment on
// the current node.
func (r *run) stepComment(cur *ref, seg hew.Segment) (*ref, error) {
	if cur.node == nil {
		return nil, noMatch("no node to attach a comment address to")
	}
	if seg.Trailing {
		c := r.d.trailingComment(cur.node)
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
