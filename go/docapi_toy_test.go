package hew

import (
	"fmt"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

// The document API's unit suite (O50 rule b, Appendix A.0) runs against a TOY
// format registered from the test binary.
//
// It has to: the core cannot import ext/json without a cycle, and stubext_test
// already claims the six v0 ids with halfless bindings. That constraint is the
// same fact A.6 states from the other side — a format exists for a build
// because something registered it — so a suite that exercises the document API
// through a format the core has never heard of is testing exactly the
// property the registry exists to provide.
//
// The toy is YAML on the wire, because Value already holds yaml nodes and a
// second serialization would test the fixture rather than the API. Its applier
// is deliberately partial (no relative placement, no comments): placement is
// pinned in the IR, where it belongs, not in a fake applier's output.

const formatToy FormatID = "toy"

// toyBinding is the toy's registration. It owns the "anchor" qualifier and not
// "surface", so the qualifier-ownership checks have both answers available.
func toyBinding() Binding {
	return Binding{
		Applier:       toyApply,
		Differ:        toyDiff,
		Document:      toyDocument,
		EmptyDocument: []byte(""),
		Detect:        DetectRule{Extensions: []string{".toy"}, WellKnownNames: []string{"toyrc"}},
		Qualifiers:    []string{"anchor"},
	}
}

// toyOnly swaps in a registry holding just the toy, for the duration of one
// test. isolate (registry_test.go) restores the real one afterwards.
func toyOnly(t *testing.T) {
	t.Helper()
	isolate(t)
	Register(formatToy, toyBinding())
}

// --- the toy's read-only Document view (A.4) ---------------------------------

func toyDocument(name string, src []byte) (Document, error) {
	root, err := toyParse(name, src)
	if err != nil {
		return nil, err
	}
	return toyDoc{root: root}, nil
}

func toyParse(name string, src []byte) (*yaml.Node, error) {
	var file yaml.Node
	if err := yaml.Unmarshal(src, &file); err != nil {
		return nil, &hewerr.Error{Code: hewerr.CodeTargetParse, Component: hewerr.ComponentApplier,
			Target: name, Detail: "toy: " + err.Error()}
	}
	if len(file.Content) == 0 {
		return nil, &hewerr.Error{Code: hewerr.CodeTargetParse, Component: hewerr.ComponentApplier,
			Target: name, Detail: "toy: empty document"}
	}
	return file.Content[0], nil
}

type toyDoc struct{ root *yaml.Node }

func (d toyDoc) Root() Node { return toyNode{d.root} }

type toyNode struct{ n *yaml.Node }

func (t toyNode) Kind() NodeKind {
	switch t.n.Kind {
	case yaml.MappingNode:
		return KindMap
	case yaml.SequenceNode:
		return KindSeq
	}
	return KindScalar
}

func (t toyNode) Member(name string) (Node, bool) {
	if t.n.Kind != yaml.MappingNode {
		return nil, false
	}
	for i := 0; i+1 < len(t.n.Content); i += 2 {
		if t.n.Content[i].Value == name {
			return toyNode{t.n.Content[i+1]}, true
		}
	}
	return nil, false
}

func (t toyNode) Len() int {
	switch t.n.Kind {
	case yaml.MappingNode:
		return len(t.n.Content) / 2
	case yaml.SequenceNode:
		return len(t.n.Content)
	}
	return 0
}

func (t toyNode) Elem(i int) (Node, bool) {
	if t.n.Kind != yaml.SequenceNode || i < 0 || i >= len(t.n.Content) {
		return nil, false
	}
	return toyNode{t.n.Content[i]}, true
}

func (t toyNode) Value() Value { return NodeValue(t.n) }

// --- the toy's differ half (A.5) ---------------------------------------------

func toyDiff(src []byte) (*DiffNode, error) {
	root, err := toyParse("", src)
	if err != nil {
		return nil, err
	}
	return toyTree(root), nil
}

func toyTree(n *yaml.Node) *DiffNode {
	// Every node carries its whole value, containers included: §9.4-R5 has an
	// added node render from the new document's own bytes, so the differ needs
	// more than the scalars.
	switch n.Kind {
	case yaml.MappingNode:
		d := &DiffNode{Kind: KindMap, Value: NodeValue(n)}
		for i := 0; i+1 < len(n.Content); i += 2 {
			d.Children = append(d.Children, DiffChild{Key: n.Content[i].Value, Node: toyTree(n.Content[i+1])})
		}
		return d
	case yaml.SequenceNode:
		d := &DiffNode{Kind: KindSeq, Value: NodeValue(n)}
		for _, c := range n.Content {
			d.Children = append(d.Children, DiffChild{Node: toyTree(c)})
		}
		return d
	}
	return &DiffNode{Kind: KindScalar, Value: NodeValue(n)}
}

// --- the toy's applier half (A.4) --------------------------------------------

func toyApply(target []byte, tl TransformList) ([]byte, error) {
	root, err := toyParse(tl.Target, target)
	if err != nil {
		return nil, err
	}
	// Two phases, A.4's rule: every test is evaluated before anything mutates.
	for _, t := range tl.Transform {
		if t.Op == OpTest {
			if err := toyTest(root, t); err != nil {
				return nil, err
			}
		}
	}
	for _, t := range tl.Transform {
		if t.Op == OpTest {
			continue
		}
		if err := toyMutate(root, t); err != nil {
			return nil, err
		}
	}
	out, err := yaml.Marshal(root)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// toyFind walks a path, returning the addressed node, its container, and the
// container index the node occupies (the KEY's index in a mapping).
func toyFind(root *yaml.Node, p Path) (node, parent *yaml.Node, at int, ok bool) {
	node, at = root, -1
	for _, seg := range p.Segments() {
		parent = node
		switch seg.Kind {
		case SegKey:
			node, at = nil, -1
			if parent.Kind != yaml.MappingNode {
				return nil, parent, -1, false
			}
			for i := 0; i+1 < len(parent.Content); i += 2 {
				if parent.Content[i].Value == seg.Name {
					node, at = parent.Content[i+1], i
					break
				}
			}
		case SegIndex:
			if parent.Kind != yaml.SequenceNode || seg.Index >= len(parent.Content) {
				return nil, parent, -1, false
			}
			node, at = parent.Content[seg.Index], seg.Index
		case SegMatch:
			node, at = nil, -1
			if parent.Kind != yaml.SequenceNode {
				return nil, parent, -1, false
			}
			for i, e := range parent.Content {
				if m, found := (toyNode{e}).Member(seg.Name); found && m.Value().Equal(seg.Value.Value()) {
					node, at = e, i
					break
				}
			}
		default:
			return nil, parent, -1, false
		}
		if node == nil {
			return nil, parent, -1, false
		}
	}
	return node, parent, at, true
}

func toyErr(code hewerr.Code, t Transform, detail string) error {
	return &hewerr.Error{Code: code, Component: hewerr.ComponentApplier,
		Path: t.Path.String(), Detail: "toy: " + detail}
}

func toyTest(root *yaml.Node, t Transform) error {
	node, _, _, ok := toyFind(root, t.Path)
	if t.Absent {
		if ok {
			return toyErr(hewerr.CodeAssertionFailed, t, "expected absent")
		}
		return nil
	}
	if !ok {
		if t.Optional {
			return nil
		}
		return toyErr(hewerr.CodeNoMatch, t, "no such node")
	}
	switch {
	case t.Count != nil:
		if got := (toyNode{node}).Len(); got != *t.Count {
			return toyErr(hewerr.CodeAssertionFailed, t, fmt.Sprintf("count %d, want %d", got, *t.Count))
		}
	case t.NodeKind != nil:
		if got := (toyNode{node}).Kind(); got != *t.NodeKind {
			return toyErr(hewerr.CodeAssertionFailed, t, fmt.Sprintf("kind %s, want %s", got, *t.NodeKind))
		}
	case !t.Value.IsZero():
		if !NodeValue(node).Equal(t.Value) && !t.Idempotent {
			return toyErr(hewerr.CodeStaleTarget, t, "before-image mismatch")
		}
	}
	return nil
}

func toyMutate(root *yaml.Node, t Transform) error {
	switch t.Op {
	case OpReplace:
		node, _, _, ok := toyFind(root, t.Path)
		if !ok {
			return toyErr(hewerr.CodeNoMatch, t, "no such node")
		}
		*node = *cloneValue(t.Value)
		return nil
	case OpRemove:
		node, parent, at, ok := toyFind(root, t.Path)
		if !ok {
			if t.Optional {
				return nil
			}
			return toyErr(hewerr.CodeNoMatch, t, "no such node")
		}
		_ = node
		if parent.Kind == yaml.MappingNode {
			parent.Content = append(parent.Content[:at], parent.Content[at+2:]...)
		} else {
			parent.Content = append(parent.Content[:at], parent.Content[at+1:]...)
		}
		return nil
	case OpAdd:
		return toyAdd(root, t)
	}
	return toyErr(hewerr.CodeInexpressible, t, "unsupported op "+string(t.Op))
}

func toyAdd(root *yaml.Node, t Transform) error {
	if node, _, _, ok := toyFind(root, t.Path); ok && node.Kind == yaml.SequenceNode {
		node.Content = append(node.Content, cloneValue(t.Value))
		return nil
	}
	segs := t.Path.Segments()
	last := segs[len(segs)-1]
	container := root
	if len(segs) > 1 {
		var ok bool
		container, _, _, ok = toyFind(root, RootPath().Append(segs[:len(segs)-1]...))
		if !ok {
			return toyErr(hewerr.CodeNoMatch, t, "no container")
		}
	}
	if last.Kind == SegAppend {
		container.Content = append(container.Content, cloneValue(t.Value))
		return nil
	}
	for i := 0; i+1 < len(container.Content); i += 2 {
		if container.Content[i].Value != last.Name {
			continue
		}
		switch t.OnConflict {
		case ConflictKeep:
			return nil
		case ConflictReplace:
			*container.Content[i+1] = *cloneValue(t.Value)
			return nil
		default:
			if t.Idempotent && NodeValue(container.Content[i+1]).Equal(t.Value) {
				return nil
			}
			return toyErr(hewerr.CodeAlreadyExists, t, "key exists")
		}
	}
	container.Content = append(container.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: last.Name}, cloneValue(t.Value))
	return nil
}

func cloneValue(v Value) *yaml.Node {
	n := *v.Node()
	n.Line, n.Column, n.Style = 0, 0, 0
	return &n
}
