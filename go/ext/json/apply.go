package json

import (
	"fmt"
	"github.com/benjaminabbitt/hew/go/internal/hewsplice"

	"github.com/benjaminabbitt/hew/go"
	"github.com/benjaminabbitt/hew/go/internal/hewapply"
	"github.com/benjaminabbitt/hew/go/internal/hewerr"
	"github.com/benjaminabbitt/hew/go/internal/hewresolve"
	"gopkg.in/yaml.v3"
)

// binding is this format's variant of the shared apply loop (hewapply): the
// loop owns the SEQUENCE — reparse per transform, hints neither assert nor
// edit, an empty plan is a no-op — and everything below is what JSON itself
// answers.
type binding struct{}

func (binding) FormatName() string { return "JSON" }

// Unsupported has nothing to refuse: this binding implements every qualifier
// §9.3 defines for it, so pass 0 passes.
func (binding) Unsupported(string, hew.Transform) error { return nil }

func (binding) NewRun(src []byte, ctx hewapply.RunContext) (hewapply.Run, error) {
	d, err := parseDoc(src)
	if err != nil {
		return nil, err
	}
	d.adv = ctx.Adv
	return docRun{d: d, target: ctx.Target}, nil
}

// docRun carries the target alongside the document because this binding's
// evaluators take it per call rather than holding it.
type docRun struct {
	d      *doc
	target string
}

func (r docRun) EvalTest(t hew.Transform) error { return r.d.evalTest(r.target, t) }

func (r docRun) PlanOne(t hew.Transform) ([]hewsplice.Edit, error) {
	e, err := r.d.planOne(r.target, t)
	if err != nil || e == nil {
		return nil, err
	}
	return []hewsplice.Edit{*e}, nil
}

func Apply(target []byte, tl hew.TransformList) ([]byte, error) {
	return hewapply.Apply(target, tl, binding{})
}

// planOne computes the single edit one mutating transform stands for, or nil
// if it is satisfied with no change at all (`! default` over an existing
// key, `! idempotent` over an equal value — §7.5, §7.7).
func (d *doc) planOne(target string, t hew.Transform) (*edit, error) {
	switch t.Op {
	case hew.OpAdd:
		return d.planAdd(target, t)
	case hew.OpRemove:
		return d.planRemove(target, t)
	case hew.OpReplace:
		return d.planReplace(target, t)
	case hew.OpCopy:
		return d.planCopy(target, t)
	default:
		return nil, &hewerr.Error{Code: hewerr.CodeInexpressible, Component: hewerr.ComponentApplier,
			Target: target, Path: t.Path.String(), Detail: fmt.Sprintf("unsupported op %q", t.Op)}
	}
}

// doc is a parsed JSON target: the source bytes and the tree. One doc is
// built per transform (Apply reparses the buffer each step), so it never
// needs to track insertions still pending against a stale parse — the next
// step's parse already contains them for real.
type doc struct {
	src  []byte
	root *jNode
	// adv is the current transform's position advisory (satisfied-recoil),
	// already migrated into the frame this transform meets:
	// which of several hash-colliding elements it means. Set per transform.
	adv hew.Advisory
}

func parseDoc(src []byte) (*doc, error) {
	root, err := parseJSON(src)
	if err != nil {
		return nil, err
	}
	return &doc{src: src, root: root}, nil
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

// One constructor per COMPONENT, and that is the point. The component is fixed
// here so no call site can pass the wrong one or forget it; a single shared
// helper taking it as a parameter would turn a compile-time fact into an
// argument, which is the mistake this shape exists to prevent.
// reprise:ignore
func appErr(code hewerr.Code, target, path string, patchLine int, detail string) *hewerr.Error {
	return &hewerr.Error{Code: code, Component: hewerr.ComponentApplier, Target: target, Path: path, PatchLine: patchLine, Detail: detail}
}

// resolveErr classifies a step failure: HEW013 no-match or HEW012
// ambiguous-match, both raised by the applier per §4.2/§4.3/§4.5.
// walk resolves a sequence of segments against n, in order. It returns a
// *hewresolve.Err for a missing or ambiguous step, which the caller turns into
// the right HEW0xx code and path at the point of failure.
func (d *doc) walk(n *jNode, segs []hew.Segment) (*jNode, int, *hewresolve.Err) {
	cur := n
	for i, seg := range segs {
		next, err := d.step(cur, seg)
		if err != nil {
			return nil, i, err
		}
		cur = next
	}
	return cur, -1, nil
}

func (d *doc) step(n *jNode, seg hew.Segment) (*jNode, *hewresolve.Err) {
	switch seg.Kind {
	case hew.SegKey:
		if n.kind != jObj {
			return nil, &hewresolve.Err{Detail: fmt.Sprintf("%q: not an object", seg.Name)}
		}
		for _, m := range n.members {
			if m.key == seg.Name {
				return m.value, nil
			}
		}
		return nil, &hewresolve.Err{Detail: fmt.Sprintf("no key %q", seg.Name)}
	case hew.SegIndex:
		if n.kind != jArr {
			return nil, &hewresolve.Err{Detail: "not an array"}
		}
		if seg.Index < 0 || seg.Index >= len(n.elems) {
			return nil, &hewresolve.Err{Detail: fmt.Sprintf("index %d out of range", seg.Index)}
		}
		return n.elems[seg.Index].value, nil
	case hew.SegMatch:
		if n.kind != jArr {
			return nil, &hewresolve.Err{Detail: "not an array"}
		}
		var found *jNode
		var cands []hew.Value
		count := 0
		for _, e := range n.elems {
			v, has := d.comparedValue(e.value, seg)
			if !has {
				continue
			}
			cands = append(cands, v)
			if v.Equal(seg.Value.Value()) {
				found = e.value
				count++
			}
		}
		if count == 0 {
			// O46: the near miss, named with its type (§10.3). The wording is
			// the core's, so every binding says it the same way.
			return nil, &hewresolve.Err{Detail: hew.NoMatchDetail(seg, cands)}
		}
		if count > 1 {
			return nil, &hewresolve.Err{Code: hewerr.CodeAmbiguousMatch, Detail: fmt.Sprintf("%d elements match %s", count, seg.String())}
		}
		return found, nil
	case hew.SegHash:
		if n.kind != jArr {
			return nil, &hewresolve.Err{Detail: "not an array"}
		}
		tokenAt := func(k int) (string, bool) {
			v, err := d.nodeValue(n.elems[k].value)
			if err != nil {
				return "", false
			}
			return hew.MemberToken(v), true
		}
		radius := hew.NeighbourRadius(d.adv)
		var cands []hew.Candidate
		for i, e := range n.elems {
			if v, err := d.nodeValue(e.value); err == nil && seg.MatchesHash(v) {
				cands = append(cands, hew.Candidate{Index: i,
					Line:       lineOf(d.src, e.value.start),
					Neighbours: hew.ObservedNeighbours(i, len(n.elems), radius, tokenAt)})
			}
		}
		if len(cands) == 0 {
			return nil, &hewresolve.Err{Detail: "no element matches " + seg.String()}
		}
		// A collision is decided by the scored locator, shared with every binding.
		pick := hew.Locate(cands, len(n.elems), d.adv)
		if !pick.OK {
			return nil, &hewresolve.Err{Code: hewerr.CodeAmbiguousMatch, Detail: pick.Explain(seg)}
		}
		return n.elems[pick.Index].value, nil
	default:
		return nil, &hewresolve.Err{Detail: fmt.Sprintf("segment kind %v has no JSON representation (§8.1)", seg.Kind)}
	}
}

// comparedValue is the value a key-match compares this element against (§4.2):
// the element itself for `=value`, the named member for `field=value`. It is
// separate from the test so that a failed match can report the NEAR MISS it
// found (§10.3, O46) instead of only reporting that nothing matched.
func (d *doc) comparedValue(n *jNode, seg hew.Segment) (hew.Value, bool) {
	if seg.Name == "" {
		v, err := d.nodeValue(n)
		return v, err == nil
	}
	if n.kind != jObj {
		return hew.Value{}, false
	}
	for _, m := range n.members {
		if m.key == seg.Name {
			v, err := d.nodeValue(m.value)
			return v, err == nil
		}
	}
	return hew.Value{}, false
}

func (d *doc) matchesSegMatch(n *jNode, seg hew.Segment) bool {
	v, ok := d.comparedValue(n, seg)
	return ok && v.Equal(scalarToValue(seg.Value))
}

// nodeValue decodes a parsed node's own source span into a hew.Value, for
// comparison against a transform's asserted or matched value. JSON is a YAML
// subset, so the existing YAML-based Value machinery reads it directly.
func (d *doc) nodeValue(n *jNode) (hew.Value, error) {
	var y yaml.Node
	if err := yaml.Unmarshal(d.src[n.start:n.end], &y); err != nil || len(y.Content) == 0 {
		return hew.Value{}, fmt.Errorf("ext/json: cannot decode node: %v", err)
	}
	return hew.NodeValue(y.Content[0]), nil
}

// scalarToValue converts a Hew path segment's identity Scalar into a
// hew.Value, for comparing it against a decoded target value via Equal. The
// conversion is the core's (hew.Scalar.Value), so that this binding's
// matching and hew.Resolve's key-match projection cannot drift apart.
func scalarToValue(s hew.Scalar) hew.Value { return s.Value() }

// resolveFull resolves a full path against the document root, translating a
// resolveErr into the corresponding hewerr.Error at the given patch line.
func (d *doc) resolveFull(target string, path hew.Path, line int) (*jNode, error) {
	n, failedAt, err := d.walk(d.root, path.Segments())
	if err == nil {
		return n, nil
	}
	re := err
	code := hewerr.CodeNoMatch
	if re.Code == hewerr.CodeAmbiguousMatch {
		code = hewerr.CodeAmbiguousMatch
	}
	failPath := path
	if failedAt >= 0 {
		failPath = hew.RootPath().Append(path.Segments()[:failedAt+1]...)
	}
	return nil, appErr(code, target, failPath.String(), line, re.Error())
}
