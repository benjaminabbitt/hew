package yaml

import (
	"fmt"
	"github.com/benjaminabbitt/hew/go/internal/hewsplice"

	"github.com/benjaminabbitt/hew/go"
	"github.com/benjaminabbitt/hew/go/internal/hewcomment"
	"github.com/benjaminabbitt/hew/go/internal/hewerr"
	"github.com/benjaminabbitt/hew/go/internal/hewmatch"
	"github.com/benjaminabbitt/hew/go/internal/hewresolve"
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
	// §4.5e: compensate the patch's own earlier edits before reading advisories.
	migrated := hew.Migrate(tl.Transform)
	for i, t := range tl.Transform {
		d, err := parseDoc(cur)
		if err != nil {
			return nil, &hewerr.Error{Code: hewerr.CodeTargetParse, Component: hewerr.ComponentApplier,
				Target: tl.Target, Detail: "target does not parse as YAML: " + err.Error()}
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
			re := err
			failed := hew.RootPath().Append(p.Segments()[:i+1]...)
			return nil, r.err(re.Code, failed.String(), line, re.Detail), re.Final
		}
		cur = next
	}
	return cur, nil, false
}

// step resolves one segment against the current node.
func (r *run) step(cur *ref, seg hew.Segment, mode hew.AnchorMode) (*ref, *hewresolve.Err) {
	if seg.Kind == hew.SegComment {
		return r.stepComment(cur, seg)
	}
	n := cur.node
	if n == nil {
		return nil, hewresolve.NoMatch("cannot descend into a comment node")
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
			return nil, hewresolve.NoMatch("%q: not a mapping", seg.Name)
		}
		if e := n.lookup(seg.Name); e != nil {
			return &ref{node: e.val, parent: n, entry: e}, nil
		}
		return r.stepMerged(n, seg, mode)
	case hew.SegIndex:
		if n.kind != nSeq {
			return nil, hewresolve.NoMatch("not a sequence")
		}
		if seg.Index < 0 || seg.Index >= len(n.elems) {
			return nil, hewresolve.NoMatch("index %d out of range", seg.Index)
		}
		el := n.elems[seg.Index]
		return &ref{node: el.val, parent: n, elem: el}, nil
	case hew.SegMatch:
		if n.kind != nSeq {
			return nil, hewresolve.NoMatch("not a sequence")
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
			if scalarEq(v.Node(), hewmatch.ScalarNode(seg.Value)) {
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
			return nil, hewresolve.NoMatch("not a sequence")
		}
		tokenAt := func(k int) (string, bool) {
			v, has := r.d.comparedValue(n.elems[k].val, hew.Segment{})
			if !has {
				return "", false
			}
			return hew.MemberToken(v), true
		}
		radius := hew.NeighbourRadius(r.adv)
		var cands []hew.Candidate
		for i, el := range n.elems {
			if v, has := r.d.comparedValue(el.val, hew.Segment{}); has && seg.MatchesHash(v) {
				cands = append(cands, hew.Candidate{Index: i,
					Line:       r.d.lineOf(el.val),
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
	return nil, hewresolve.NoMatch("segment kind %v has no YAML representation (§8.3)", seg.Kind)
}

// stepMerged handles a key a mapping has only via "<<:" (§8.3). Reading or
// writing it needs an explicit policy, because rewriting the anchor changes
// every use site and forking changes none of the others.
func (r *run) stepMerged(n *ynode, seg hew.Segment, mode hew.AnchorMode) (*ref, *hewresolve.Err) {
	e, holder, anchor := r.d.mergedLookup(n, seg.Name)
	if e == nil {
		return nil, hewresolve.NoMatch("no key %q", seg.Name)
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
		return nil, &hewresolve.Err{Code: hewerr.CodeAnchorAmbiguity, Final: true,
			Detail: fmt.Sprintf("%q here comes from the anchor &%s, aliased at %d sites; "+
				"add \"! anchor rewrite\" to edit the anchor definition or \"! anchor fork\" to materialize this site (§8.3)",
				seg.Name, anchor, r.d.aliases[anchor])}
	}
	return nil, &hewresolve.Err{Code: hewerr.CodeNoMatch, Final: true,
		Detail: fmt.Sprintf("%q is present here only via the merge key \"<<: *%s\"; an inherited key is not present at this site "+
			"and cannot be removed, only shadowed (§8.3)", seg.Name, anchor)}
}

// followAlias resolves an alias node to the node it names, under the
// transform's anchor policy.
func (r *run) followAlias(n *ynode, mode hew.AnchorMode) (*ynode, *hewresolve.Err) {
	name := n.y.Value
	switch mode {
	case hew.AnchorRewrite:
		target, ok := r.d.anchors[name]
		if !ok {
			return nil, hewresolve.NoMatch("alias *%s names no anchor in this document", name)
		}
		return target, nil
	case hew.AnchorFork:
		return nil, &hewresolve.Err{Code: hewerr.CodeInexpressible, Final: true,
			Detail: fmt.Sprintf("forking the whole aliased node *%s is not expressible in this binding; "+
				"fork applies to a merge-inherited key (§8.3)", name)}
	}
	return nil, &hewresolve.Err{Code: hewerr.CodeAnchorAmbiguity, Final: true,
		Detail: fmt.Sprintf("the path resolves at the alias *%s; add \"! anchor rewrite\" or \"! anchor fork\" (§8.3)", name)}
}

// stepComment resolves a comment address (§4.5b): a `#hew:comment=<hex>`
// fragment selects the standalone comment of the current container whose text
// hashes to that digest, "#t" the trailing comment on the current node.
func (r *run) stepComment(cur *ref, seg hew.Segment) (*ref, *hewresolve.Err) {
	if cur.node == nil {
		return nil, hewresolve.NoMatch("no node to attach a comment address to")
	}
	if seg.Trailing {
		c := r.d.trailingComment(cur.node)
		if c == nil {
			return nil, hewresolve.NoMatch("no trailing comment here")
		}
		return &ref{comment: c, parent: cur.parent}, nil
	}
	// Which comment a digest names is not a format question — the digest is
	// over the marker-stripped text, so the same comment hashes alike in every
	// format — so the decision, collisions included, is made once.
	comments := r.d.commentChildren(cur.node)
	texts := make([]string, len(comments))
	for i, c := range comments {
		texts[i] = c.text
	}
	i, err := hewcomment.Pick(seg, texts, r.adv)
	if err != nil {
		return nil, err
	}
	return &ref{comment: comments[i], parent: cur.node}, nil
}
