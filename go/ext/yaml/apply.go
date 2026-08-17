package yaml

import (
	"fmt"
	"sort"
	"strings"

	"github.com/benjaminabbitt/hew/go"
	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

// Apply is the YAML binding's apply half (§8.3, Appendix A.4's Applier.Apply
// for the "yaml" format). SEQUENTIAL RESOLUTION (§9.2, §9.3, human ruling):
// every transform — `test` included — is resolved and evaluated (or applied)
// against the document AS MODIFIED BY every transform before it, one at a
// time, in list order, by reparsing the current byte buffer before each
// step. There is no longer a fixed "every test before any mutation" split; a
// `test` placed after an earlier write in the same list sees that write,
// exactly as an `add` placed after one does.
//
// Everything happens against an in-memory byte buffer that is only ever
// returned once every transform has succeeded (§10.5's all-or-nothing): an
// error at any step discards the buffer and returns nil bytes.
func Apply(target []byte, tl hew.TransformList) ([]byte, error) {
	// Pass 0: refuse qualifiers this binding does not implement. §9.3 is
	// explicit that ignoring one is non-conformant, not lenient. This is a
	// property of the transform list alone, so it stays a single static pass
	// ahead of the sequential one below.
	for _, t := range tl.Transform {
		if err := unsupported(tl.Target, t); err != nil {
			return nil, err
		}
	}

	// converged spans the whole apply (§10.6/§7.5): whether a path's
	// before-image assert was tolerated as "already applied" has to be seen
	// by that path's own write, wherever in the list it falls, so it lives
	// above the per-transform reparse rather than inside run.
	converged := map[string]bool{}
	cur := target
	for _, t := range tl.Transform {
		d, err := parseDoc(cur)
		if err != nil {
			return nil, &hewerr.Error{Code: hewerr.CodeTargetParse, Component: hewerr.ComponentApplier,
				Target: tl.Target, Detail: "target does not parse as YAML: " + err.Error()}
		}
		r := &run{d: d, target: tl.Target, all: tl.Transform, converged: converged}
		if t.Op == hew.OpTest {
			if err := r.evalTest(t); err != nil {
				return nil, err
			}
			continue
		}
		es, err := r.planOne(t)
		if err != nil {
			return nil, err
		}
		if len(es) == 0 {
			continue
		}
		cur, err = applyEdits(cur, es)
		if err != nil {
			return nil, err
		}
	}
	return cur, nil
}

// planOne computes the edits one mutating transform stands for.
func (r *run) planOne(t hew.Transform) ([]edit, error) {
	switch t.Op {
	case hew.OpAdd:
		return r.planAdd(t)
	case hew.OpRemove:
		return r.planRemove(t)
	case hew.OpReplace:
		return r.planReplace(t)
	case hew.OpCopy:
		return r.planCopy(t)
	default:
		return nil, r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine, fmt.Sprintf("unsupported op %q", t.Op))
	}
}

// run is one transform's resolution state: the document that transform
// reparsed and plans against, the whole transform list (the after-image
// checks of §10.6 need to see a test's paired write, wherever in the list it
// falls — not just the ones before this one), and the set of paths whose
// before-image assert has been tolerated as "already applied" (§7.5), shared
// across every transform's own run (Apply constructs a fresh run each step,
// but converged is the same map throughout).
type run struct {
	d         *doc
	target    string
	all       []hew.Transform
	converged map[string]bool
}

// unsupported refuses a transform carrying a qualifier this binding cannot
// honour: a TOML surface directive, which has no meaning here (§9.3). It
// needs no document, so it runs as a static pass over the whole list before
// any parsing happens.
func unsupported(target string, t hew.Transform) error {
	if t.Surface != "" {
		return &hewerr.Error{Code: hewerr.CodeInexpressible, Component: hewerr.ComponentApplier,
			Target: target, Path: t.Path.String(), PatchLine: t.PatchLine,
			Detail: "surface is a TOML placement directive and has no YAML meaning (§8.4)"}
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
			failed := hew.RootPath().Append(p.Segments()[:i+1]...)
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
