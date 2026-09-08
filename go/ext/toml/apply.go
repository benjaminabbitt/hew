package toml

import (
	"fmt"
	"github.com/benjaminabbitt/hew/go/internal/hewsplice"

	"github.com/benjaminabbitt/hew/go"
	"github.com/benjaminabbitt/hew/go/internal/hewerr"
	"github.com/benjaminabbitt/hew/go/internal/hewresolve"
)

// Apply is the TOML binding's apply half (§8.4, Appendix A.4's Applier.Apply
// for the "toml" format). SEQUENTIAL RESOLUTION (§9.2, §9.3, human ruling):
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
	// §4.5e: compensate the patch's own earlier edits before reading advisories.
	migrated := hew.Migrate(tl.Transform)
	for i, t := range tl.Transform {
		d, err := parseDoc(cur)
		if err != nil {
			return nil, &hewerr.Error{Code: hewerr.CodeTargetParse, Component: hewerr.ComponentApplier,
				Target: tl.Target, Detail: "target does not parse as TOML: " + err.Error()}
		}
		r := &run{d: d, target: tl.Target, all: tl.Transform, converged: converged, adv: migrated[i]}
		if t.Op == hew.OpTest {
			if err := r.evalTest(t); err != nil {
				return nil, err
			}
			continue
		}
		if t.Op == hew.OpHint {
			continue // the non-asserting hint channel (satisfied-recoil): no edit, no assertion
		}
		es, err := r.planOne(t)
		if err != nil {
			return nil, err
		}
		if len(es) == 0 {
			continue
		}
		cur, err = hewsplice.Apply(cur, es)
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
	// adv: the current transform's position advisory (satisfied-recoil),
	// already migrated into the frame this transform meets (§4.5e).
	adv hew.Advisory
}

// unsupported refuses a transform carrying a qualifier this binding cannot
// honour: a YAML anchor policy, which has no meaning here (§9.3). It needs
// no document, so it runs as a static pass over the whole list before any
// parsing happens.
func unsupported(target string, t hew.Transform) error {
	if t.Anchor != "" {
		return &hewerr.Error{Code: hewerr.CodeInexpressible, Component: hewerr.ComponentApplier,
			Target: target, Path: t.Path.String(), PatchLine: t.PatchLine,
			Detail: "anchor is a YAML alias directive and has no TOML meaning (§8.3)"}
	}
	return nil
}

// One constructor per COMPONENT, and that is the point. The component is fixed
// here so no call site can pass the wrong one or forget it; a single shared
// helper taking it as a parameter would turn a compile-time fact into an
// argument, which is the mistake this shape exists to prevent.
// reprise:ignore
func (r *run) err(code hewerr.Code, path string, line int, detail string) *hewerr.Error {
	return &hewerr.Error{Code: code, Component: hewerr.ComponentApplier, Target: r.target,
		Path: path, PatchLine: line, Detail: detail}
}

// edit is one byte-range splice against the source the doc was parsed from:
// replace [Start,End) with Text. An insertion is Start==End.
//
// The splice is not a per-format concern — every binding resolves transforms
// to byte ranges and then splices them all at once — so the type and the
// algorithm are shared (hewsplice.Apply). The alias keeps this package's own
// spelling at its call sites.
type edit = hewsplice.Edit

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
			return nil, r.err(re.Code, failed.String(), line, re.Detail), re.Final
		}
		cur = next
	}
	return cur, nil, false
}

// step resolves one segment against the current node. A resolved node defined
// at two surfaces is §8.4 rule 2's refusal: hew will not pick one, because
// picking one silently orphans the other.
func (r *run) step(cur *ref, seg hew.Segment) (*ref, *hewresolve.Err) {
	next, re := r.stepRaw(cur, seg)
	if re != nil {
		return nil, re
	}
	if next.node != nil && next.node.defs > 1 {
		return nil, &hewresolve.Err{Code: hewerr.CodeSurfaceAmbiguity, Final: true,
			Detail: fmt.Sprintf("%q is defined at %d surfaces in this document (a dotted key and a table header "+
				"denote the same node); hew refuses to pick one, because picking one orphans the other (§8.4 rule 2)",
				dottedKey(next.node.path), next.node.defs)}
	}
	return next, nil
}

func (r *run) stepRaw(cur *ref, seg hew.Segment) (*ref, *hewresolve.Err) {
	if seg.Kind == hew.SegComment {
		return r.stepComment(cur, seg)
	}
	n := cur.node
	if n == nil {
		return nil, hewresolve.NoMatch("cannot descend into a comment node")
	}
	switch seg.Kind {
	case hew.SegKey:
		if n.kind != nTable {
			return nil, hewresolve.NoMatch("%q: not a table", seg.Name)
		}
		e := n.lookup(seg.Name)
		if e == nil {
			return nil, hewresolve.NoMatch("no key %q", seg.Name)
		}
		return &ref{node: e.val, parent: n, entry: e}, nil
	case hew.SegIndex:
		if n.kind != nSeq {
			return nil, hewresolve.NoMatch("not an array")
		}
		if seg.Index < 0 || seg.Index >= len(n.elems) {
			return nil, hewresolve.NoMatch("index %d out of range", seg.Index)
		}
		el := n.elems[seg.Index]
		return &ref{node: el.val, parent: n, elem: el}, nil
	case hew.SegMatch:
		if n.kind != nSeq {
			return nil, hewresolve.NoMatch("not an array")
		}
		var found *elem
		var cands []hew.Value
		count := 0
		for _, el := range n.elems {
			c, has := comparedValue(el.val, seg)
			if !has {
				continue
			}
			cands = append(cands, c)
			if matchesSeg(el.val, seg) {
				found = el
				count++
			}
		}
		switch count {
		case 0:
			// O46: name the near miss and its type (§10.3), in the core's
			// wording so every binding says it the same way.
			return nil, hewresolve.NoMatch("%s", hew.NoMatchDetail(seg, cands))
		case 1:
			return &ref{node: found.val, parent: n, elem: found}, nil
		}
		return nil, &hewresolve.Err{Code: hewerr.CodeAmbiguousMatch, Final: true,
			Detail: fmt.Sprintf("%d elements match %s; hew will not pick one (§6.4.2)", count, seg.String())}
	case hew.SegHash:
		if n.kind != nSeq {
			return nil, hewresolve.NoMatch("not an array")
		}
		tokenAt := func(k int) (string, bool) {
			v, has := comparedValue(n.elems[k].val, hew.Segment{})
			if !has {
				return "", false
			}
			return hew.MemberToken(v), true
		}
		radius := hew.NeighbourRadius(r.adv)
		var cands []hew.Candidate
		for i, el := range n.elems {
			if v, has := comparedValue(el.val, hew.Segment{}); has && seg.MatchesHash(v) {
				cands = append(cands, hew.Candidate{Index: i,
					Line:       lineOf(r.d.src, el.blockStart),
					Neighbours: hew.ObservedNeighbours(i, len(n.elems), radius, tokenAt)})
			}
		}
		if len(cands) == 0 {
			return nil, hewresolve.NoMatch("no element matches %s", seg.String())
		}
		// A collision is decided by the scored locator, shared with every binding.
		pick := hew.Locate(cands, len(n.elems), r.adv)
		if !pick.OK {
			return nil, &hewresolve.Err{Code: hewerr.CodeAmbiguousMatch, Final: true, Detail: pick.Explain(seg)}
		}
		return &ref{node: n.elems[pick.Index].val, parent: n, elem: n.elems[pick.Index]}, nil
	}
	return nil, hewresolve.NoMatch("segment kind %v has no TOML representation (§8.4)", seg.Kind)
}

// stepComment resolves a comment address (§4.5b): a `#hew:comment=<hex>`
// fragment selects the standalone comment of the current table whose text
// hashes to that digest, "#t" the trailing comment on the current node's line.
func (r *run) stepComment(cur *ref, seg hew.Segment) (*ref, *hewresolve.Err) {
	if cur.node == nil {
		return nil, hewresolve.NoMatch("no node to attach a comment address to")
	}
	if seg.Trailing {
		from, ok := r.commentAnchor(cur)
		if !ok {
			return nil, hewresolve.NoMatch("this node has no line of its own to carry a trailing comment")
		}
		c := r.d.trailingComment(from)
		if c == nil {
			return nil, hewresolve.NoMatch("no trailing comment here")
		}
		return &ref{comment: c, parent: cur.parent}, nil
	}
	if seg.Hash == "" {
		// The ordinal is gone (§4.5b) and the parser refuses it, so this is
		// only reachable from a Segment built in code. Refusing beats falling
		// back to Index, which would resolve position 0 for every such segment.
		return nil, hewresolve.NoMatch("a comment is addressed by the digest of its text, `#hew:comment=<hex>`")
	}
	// Addressed by the digest of its TEXT. Identical comments collide just as
	// identical set members do, and are resolved by the same scored locator
	// rather than by a private rule.
	comments := r.d.commentChildren(cur.node)
	var cands []hew.Candidate
	for i, c := range comments {
		if seg.MatchesComment(c.text) {
			cands = append(cands, hew.Candidate{Index: i})
		}
	}
	if len(cands) == 0 {
		return nil, hewresolve.NoMatch("no comment matches %s", seg.String())
	}
	pick := hew.Locate(cands, len(comments), r.adv)
	if !pick.OK {
		return nil, &hewresolve.Err{Code: hewerr.CodeAmbiguousMatch, Final: true, Detail: pick.Explain(seg)}
	}
	return &ref{comment: comments[pick.Index], parent: cur.node}, nil
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
