package jsonc

import (
	"fmt"

	"github.com/benjaminabbitt/hew/go"
	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

// ref is a resolved location. Exactly one of node and cmt is set: a value node,
// or a comment node reached through a `#n` / `#t` segment (§4.5b). parent is
// the container the location sits in, and member/elem the child it belongs to,
// both of which the edit planner needs and neither of which a bare *node
// carries.
type ref struct {
	node   *node
	cmt    *comment
	parent *node
	member *member
	elem   *element
}

func (r ref) span() (int, int) {
	if r.cmt != nil {
		return r.cmt.start, r.cmt.end
	}
	return r.node.start, r.node.end
}

func appErr(code hewerr.Code, target, path string, patchLine int, detail string) *hewerr.Error {
	return &hewerr.Error{Code: code, Component: hewerr.ComponentApplier,
		Target: target, Path: path, PatchLine: patchLine, Detail: detail}
}

// resolveErr classifies a failing step: a missing node (HEW013) or an
// ambiguous one (HEW012), which the caller stamps with the path prefix that
// actually failed.
type resolveErr struct {
	ambiguous bool
	detail    string
}

func (e *resolveErr) Error() string { return e.detail }

func (d *doc) walk(segs []hew.Segment) (ref, int, error) {
	cur := ref{node: d.root}
	for i, seg := range segs {
		next, err := d.step(cur, seg)
		if err != nil {
			return ref{}, i, err
		}
		cur = next
	}
	return cur, -1, nil
}

func (d *doc) step(cur ref, seg hew.Segment) (ref, error) {
	if seg.Kind == hew.SegComment {
		return d.stepComment(cur, seg)
	}
	if cur.node == nil {
		return ref{}, &resolveErr{detail: "a comment node has no children"}
	}
	switch seg.Kind {
	case hew.SegKey:
		if cur.node.kind != kObj {
			return ref{}, &resolveErr{detail: fmt.Sprintf("%q: not an object", seg.Name)}
		}
		if m := cur.node.memberNamed(seg.Name); m != nil {
			return ref{node: m.value, parent: cur.node, member: m}, nil
		}
		return ref{}, &resolveErr{detail: fmt.Sprintf("no key %q", seg.Name)}
	case hew.SegIndex:
		if cur.node.kind != kArr {
			return ref{}, &resolveErr{detail: "not an array"}
		}
		if seg.Index < 0 || seg.Index >= len(cur.node.elems) {
			return ref{}, &resolveErr{detail: fmt.Sprintf("index %d out of range", seg.Index)}
		}
		e := cur.node.elems[seg.Index]
		return ref{node: e.value, parent: cur.node, elem: e}, nil
	case hew.SegMatch:
		if cur.node.kind != kArr {
			return ref{}, &resolveErr{detail: "not an array"}
		}
		var found *element
		var cands []hew.Value
		count := 0
		for _, e := range cur.node.elems {
			v, has := d.comparedValue(e.value, seg)
			if !has {
				continue
			}
			cands = append(cands, v)
			if v.Equal(scalarToValue(seg.Value)) {
				found = e
				count++
			}
		}
		switch {
		case count == 0:
			return ref{}, &resolveErr{detail: hew.NoMatchDetail(seg, cands)}
		case count > 1:
			return ref{}, &resolveErr{ambiguous: true, detail: fmt.Sprintf("%d elements match %s", count, seg.String())}
		}
		return ref{node: found.value, parent: cur.node, elem: found}, nil
	default:
		return ref{}, &resolveErr{detail: fmt.Sprintf("segment kind %v has no JSONC representation (§8.2)", seg.Kind)}
	}
}

// stepComment resolves a §4.5b comment address: `#t` is the trailing comment
// of the member or element just stepped through, `#n` the n'th standalone
// comment node of the container just stepped to.
func (d *doc) stepComment(cur ref, seg hew.Segment) (ref, error) {
	if seg.Trailing {
		switch {
		case cur.member != nil && cur.member.trailing != nil:
			return ref{cmt: cur.member.trailing, parent: cur.parent, member: cur.member}, nil
		case cur.elem != nil && cur.elem.trailing != nil:
			return ref{cmt: cur.elem.trailing, parent: cur.parent, elem: cur.elem}, nil
		}
		return ref{}, &resolveErr{detail: "no trailing comment here"}
	}
	if cur.node == nil || !cur.node.container() {
		return ref{}, &resolveErr{detail: "comment ordinals address a container's comments"}
	}
	all := cur.node.standalone()
	if seg.Index < 0 || seg.Index >= len(all) {
		return ref{}, &resolveErr{detail: fmt.Sprintf("no comment node #%d in this container", seg.Index)}
	}
	return ref{cmt: all[seg.Index], parent: cur.node}, nil
}

// comparedValue is the value a key-match compares this element against (§4.2).
// It is separate from the test so that a failed match can report the NEAR MISS
// it found (§10.3, O46) rather than only that nothing matched.
func (d *doc) comparedValue(n *node, seg hew.Segment) (hew.Value, bool) {
	if seg.Name == "" {
		v, err := n.hewValue()
		return v, err == nil
	}
	if n.kind != kObj {
		return hew.Value{}, false
	}
	m := n.memberNamed(seg.Name)
	if m == nil {
		return hew.Value{}, false
	}
	v, err := m.value.hewValue()
	return v, err == nil
}

func (d *doc) matchesSegMatch(n *node, seg hew.Segment) bool {
	v, ok := d.comparedValue(n, seg)
	return ok && v.Equal(scalarToValue(seg.Value))
}

// resolveFull resolves a whole path against the document root, translating a
// step failure into the HEW code and the path prefix that failed.
func (d *doc) resolveFull(target string, path hew.Path, line int) (ref, error) {
	r, failedAt, err := d.walk(path.Segments())
	if err == nil {
		return r, nil
	}
	re := err.(*resolveErr)
	code := hewerr.CodeNoMatch
	if re.ambiguous {
		code = hewerr.CodeAmbiguousMatch
	}
	failPath := path
	if failedAt >= 0 {
		failPath = hew.RootPath().Append(path.Segments()[:failedAt+1]...)
	}
	return ref{}, appErr(code, target, failPath.String(), line, re.Error())
}
