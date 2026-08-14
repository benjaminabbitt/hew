package hewjson

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hew-format/hew"
	"github.com/hew-format/hew/internal/hewerr"
	"gopkg.in/yaml.v3"
)

// Apply is the JSON binding's apply half (§8.1, Appendix A.4's Applier.Apply
// for the "json" format). It resolves every path against the document it
// just parsed, evaluates every OpTest before performing any mutation, and
// returns the re-serialized bytes — or, on any error, nil bytes and that
// error (§10.5's all-or-nothing).
//
// Byte preservation is structural, not textual: every untouched byte range
// of the source is copied verbatim into the output, and an edit's own
// replacement text is the only place new bytes appear (§6.3, §8.1 — no
// float64 round trip of numeric literals, since edits splice source text
// rather than re-encoding untouched values at all).
func Apply(target []byte, tl hew.TransformList) ([]byte, error) {
	d, err := parseDoc(target)
	if err != nil {
		return nil, &hewerr.Error{Code: hewerr.CodeTargetParse, Component: hewerr.ComponentApplier,
			Target: tl.Target, Detail: "target does not parse as JSON: " + err.Error()}
	}

	// Pass 1: evaluate every test before any mutation is even computed
	// (§9.0, §9.3).
	for _, t := range tl.Transform {
		if t.Op != hew.OpTest {
			continue
		}
		if err := d.evalTest(tl.Target, t); err != nil {
			return nil, err
		}
	}

	// Pass 2: compute edits for add/remove/replace/copy. Still no bytes
	// written — an error here also leaves the target untouched.
	var edits []edit
	for _, t := range tl.Transform {
		switch t.Op {
		case hew.OpTest:
			continue
		case hew.OpAdd:
			e, err := d.planAdd(tl.Target, t)
			if err != nil {
				return nil, err
			}
			if e != nil {
				edits = append(edits, *e)
			}
		case hew.OpRemove:
			e, err := d.planRemove(tl.Target, t)
			if err != nil {
				return nil, err
			}
			if e != nil {
				edits = append(edits, *e)
			}
		case hew.OpReplace:
			e, err := d.planReplace(tl.Target, t)
			if err != nil {
				return nil, err
			}
			edits = append(edits, *e)
		case hew.OpCopy:
			e, err := d.planCopy(tl.Target, t)
			if err != nil {
				return nil, err
			}
			edits = append(edits, *e)
		default:
			return nil, &hewerr.Error{Code: hewerr.CodeInexpressible, Component: hewerr.ComponentApplier,
				Target: tl.Target, Path: t.Path.String(), Detail: fmt.Sprintf("unsupported op %q", t.Op)}
		}
	}

	return applyEdits(target, edits)
}

// doc is a parsed JSON target: the source bytes and the tree.
type doc struct {
	src  []byte
	root *jNode
}

func parseDoc(src []byte) (*doc, error) {
	root, err := parseJSON(src)
	if err != nil {
		return nil, err
	}
	return &doc{src: src, root: root}, nil
}

// edit is one byte-range splice against the ORIGINAL source: replace
// [start,end) with text. An insertion is start==end.
type edit struct {
	start, end int
	text       string
}

func applyEdits(src []byte, edits []edit) ([]byte, error) {
	sort.Slice(edits, func(i, j int) bool { return edits[i].start < edits[j].start })
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

func appErr(code hewerr.Code, target, path string, patchLine int, detail string) *hewerr.Error {
	return &hewerr.Error{Code: code, Component: hewerr.ComponentApplier, Target: target, Path: path, PatchLine: patchLine, Detail: detail}
}

// resolveErr classifies a step failure: HEW013 no-match or HEW012
// ambiguous-match, both raised by the applier per §4.2/§4.3/§4.5.
type resolveErr struct {
	ambiguous bool
	detail    string
}

func (e *resolveErr) Error() string { return e.detail }

// walk resolves a sequence of segments against n, in order. It returns a
// *resolveErr for a missing or ambiguous step, which the caller turns into
// the right HEW0xx code and path at the point of failure.
func (d *doc) walk(n *jNode, segs []hew.Segment) (*jNode, int, error) {
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

func (d *doc) step(n *jNode, seg hew.Segment) (*jNode, error) {
	switch seg.Kind {
	case hew.SegKey:
		if n.kind != jObj {
			return nil, &resolveErr{detail: fmt.Sprintf("%q: not an object", seg.Name)}
		}
		for _, m := range n.members {
			if m.key == seg.Name {
				return m.value, nil
			}
		}
		return nil, &resolveErr{detail: fmt.Sprintf("no key %q", seg.Name)}
	case hew.SegIndex:
		if n.kind != jArr {
			return nil, &resolveErr{detail: "not an array"}
		}
		if seg.Index < 0 || seg.Index >= len(n.elems) {
			return nil, &resolveErr{detail: fmt.Sprintf("index %d out of range", seg.Index)}
		}
		return n.elems[seg.Index].value, nil
	case hew.SegMatch:
		if n.kind != jArr {
			return nil, &resolveErr{detail: "not an array"}
		}
		var found *jNode
		count := 0
		for _, e := range n.elems {
			if d.matchesSegMatch(e.value, seg) {
				found = e.value
				count++
			}
		}
		if count == 0 {
			return nil, &resolveErr{detail: "no element matches " + seg.String()}
		}
		if count > 1 {
			return nil, &resolveErr{ambiguous: true, detail: fmt.Sprintf("%d elements match %s", count, seg.String())}
		}
		return found, nil
	default:
		return nil, &resolveErr{detail: fmt.Sprintf("segment kind %v has no JSON representation (§8.1)", seg.Kind)}
	}
}

func (d *doc) matchesSegMatch(n *jNode, seg hew.Segment) bool {
	want := scalarToValue(seg.Value)
	if seg.Name == "" {
		v, err := d.nodeValue(n)
		return err == nil && v.Equal(want)
	}
	if n.kind != jObj {
		return false
	}
	for _, m := range n.members {
		if m.key == seg.Name {
			v, err := d.nodeValue(m.value)
			return err == nil && v.Equal(want)
		}
	}
	return false
}

// nodeValue decodes a parsed node's own source span into a hew.Value, for
// comparison against a transform's asserted or matched value. JSON is a YAML
// subset, so the existing YAML-based Value machinery reads it directly.
func (d *doc) nodeValue(n *jNode) (hew.Value, error) {
	var y yaml.Node
	if err := yaml.Unmarshal(d.src[n.start:n.end], &y); err != nil || len(y.Content) == 0 {
		return hew.Value{}, fmt.Errorf("hewjson: cannot decode node: %v", err)
	}
	return hew.NodeValue(y.Content[0]), nil
}

// scalarToValue converts a Hew path segment's identity Scalar into a
// hew.Value, for comparing it against a decoded target value via Equal.
func scalarToValue(s hew.Scalar) hew.Value {
	tag := "!!str"
	switch s.Kind {
	case hew.ScalarBool:
		tag = "!!bool"
	case hew.ScalarNull:
		tag = "!!null"
	case hew.ScalarNumber:
		if strings.ContainsAny(s.Text, ".eE") {
			tag = "!!float"
		} else {
			tag = "!!int"
		}
	}
	return hew.NodeValue(&yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: s.Text})
}

// resolveFull resolves a full path against the document root, translating a
// resolveErr into the corresponding hewerr.Error at the given patch line.
func (d *doc) resolveFull(target string, path hew.Path, line int) (*jNode, error) {
	n, failedAt, err := d.walk(d.root, path.Segments())
	if err == nil {
		return n, nil
	}
	re := err.(*resolveErr)
	code := hewerr.CodeNoMatch
	if re.ambiguous {
		code = hewerr.CodeAmbiguousMatch
	}
	failPath := path
	if failedAt >= 0 {
		failPath = hew.NewPath(path.Segments()[:failedAt+1]...)
	}
	return nil, appErr(code, target, failPath.String(), line, re.Error())
}
