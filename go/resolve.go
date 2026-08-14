package hew

import (
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/benjaminabbitt/hew/internal/hewerr"
)

// Document is the read-only view of a parsed target that Resolve projects
// against (§9.2). A format binding implements it over whatever tree its
// applier already parses. Nothing in this interface can mutate a document:
// resolution is a projection, not an application, and Resolve deliberately
// evaluates no assertion and performs no edit.
type Document interface {
	// Root returns the document's root node.
	Root() Node
}

// Node is one node of a Document. Only the accessors its Kind documents are
// meaningful: Member on a map, Len and Elem on a sequence, Value wherever a
// key-match segment (§4.2) has to compare.
type Node interface {
	// Kind reports the node's kind. Only KindMap, KindSeq and KindScalar are
	// meaningful here; the Markdown kinds have no RFC 6901 projection at all.
	Kind() NodeKind
	// Member returns the named member of a map.
	Member(name string) (Node, bool)
	// Len is the number of children: elements of a sequence, members of a
	// map, zero for a scalar.
	Len() int
	// Elem returns the i'th element of a sequence.
	Elem(i int) (Node, bool)
	// Value is the node's value, for comparison against a key-match
	// segment's decoded scalar.
	Value() Value
}

// ResolvedOp is one record of the RESOLVED transform list (§9.2): an RFC 6901
// pointer against one specific document, with every key-match segment already
// collapsed to an index and every relative placement already a concrete
// position.
//
// It is a DERIVED ARTIFACT, not a serialization of the patch. `hew apply
// --ops` prints it and `--record` embeds it (§9.7); hew cannot read it back,
// and v0 defines no way to author one. The qualifiers with no RFC 6902
// representation — anchor, surface, on_conflict, optional, idempotent, the
// ordinal selectors, and the Markdown/HCL/comment addressing forms — are
// consumed during resolution, which is the lossy direction §9.2 names.
//
// Two fields extend Appendix A.1's ResolvedOp, for the same reason
// Transform.Exhaustive extends its Transform: dropping an assertion the
// applier really evaluated would make `--record` under-report what happened
// to the file, and §9.7 requires the record to state what happened. Exhaustive
// is Appendix A.1's own field; NodeKind is the symmetric one it omits.
type ResolvedOp struct {
	Op    OpKind
	Path  string // RFC 6901 pointer
	From  string // OpCopy only; RFC 6901 pointer
	Value Value

	// The assertion modes of OpTest, carried through unchanged.
	Absent     bool
	Count      *int
	NodeKind   *NodeKind
	Exhaustive bool
}

// Resolve projects the abstract transform list onto the RFC 6901 form (§9.2)
// against a specific document: key-match segments become indices, relative
// placements become indices, format-specific qualifiers are consumed.
//
// Resolution is address-only. Resolve does not evaluate a single `test`, does
// not apply an `on_conflict` policy, and therefore reports positions, never
// outcomes: an `add` whose OP-04 defaulting would make it a no-op still
// resolves to the address it would have written. Deterministic — the same
// (list, document) pair always yields the same ops in the same order.
func Resolve(tl TransformList, doc Document) ([]ResolvedOp, error) {
	if doc == nil {
		return nil, resolveErr(hewerr.CodeTargetParse, tl.Target, "", 0, "resolve: no document to resolve against")
	}
	root := doc.Root()
	if root == nil {
		return nil, resolveErr(hewerr.CodeTargetParse, tl.Target, "", 0, "resolve: document has no root node")
	}
	r := &resolver{target: tl.Target, root: root}
	out := make([]ResolvedOp, 0, len(tl.Transform))
	for i := range tl.Transform {
		op, err := r.transform(tl.Transform[i])
		if err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	return out, nil
}

type resolver struct {
	target string
	root   Node
}

func resolveErr(code hewerr.Code, target, path string, line int, format string, args ...any) error {
	return &hewerr.Error{
		Code:      code,
		Component: hewerr.ComponentApplier,
		Target:    target,
		Path:      path,
		PatchLine: line,
		Detail:    fmt.Sprintf(format, args...),
	}
}

func (r *resolver) err(code hewerr.Code, path string, line int, format string, args ...any) error {
	return resolveErr(code, r.target, path, line, format, args...)
}

// transform projects one record. The op decides how its addresses resolve:
// an existing node for the reading ops, a position for the creating ones.
func (r *resolver) transform(t Transform) (ResolvedOp, error) {
	op := ResolvedOp{
		Op:         t.Op,
		Value:      t.Value,
		Absent:     t.Absent,
		Count:      t.Count,
		NodeKind:   t.NodeKind,
		Exhaustive: t.Exhaustive,
	}
	var err error
	switch t.Op {
	case OpAdd:
		op.Path, err = r.insertPointer(t.Path, t.Before, t.After, t.PatchLine)
	case OpCopy:
		op.From, err = r.pointer(t.From, t.PatchLine)
		if err != nil {
			return ResolvedOp{}, err
		}
		op.Path, err = r.insertPointer(t.Path, t.Before, t.After, t.PatchLine)
	case OpTest:
		// `? absent` addresses a node that must NOT exist, so it resolves
		// like a creation position rather than like an existing node.
		if t.Absent {
			op.Path, err = r.createPointer(t.Path, t.PatchLine)
		} else {
			op.Path, err = r.pointer(t.Path, t.PatchLine)
		}
	case OpRemove, OpReplace:
		op.Path, err = r.pointer(t.Path, t.PatchLine)
	default:
		return ResolvedOp{}, r.err(hewerr.CodeInexpressible, t.Path.String(), t.PatchLine,
			"resolve: unknown op %q", string(t.Op))
	}
	if err != nil {
		return ResolvedOp{}, err
	}
	return op, nil
}

// pointer resolves an address that must already exist.
func (r *resolver) pointer(p Path, line int) (string, error) {
	ptr, _, err := r.walk(p, line)
	return ptr, err
}

// insertPointer resolves an add's or a copy's destination.
//
// Disambiguating a sequence-style add (path IS the container) from a
// map-style one (path is container + the new key) uses exactly the signal
// hewjson's planInsert uses — whether path resolves to an existing SEQUENCE —
// so that the resolved list describes the insertion the applier would really
// perform rather than a second, independent guess.
func (r *resolver) insertPointer(path, before, after Path, line int) (string, error) {
	if err := r.checkPath(path, line); err != nil {
		return "", err
	}
	ptr, node, err := r.walk(path, line)
	if err == nil {
		if node != nil && node.Kind() == KindSeq {
			idx, ierr := r.placementIndex(ptr, node, before, after, line)
			if ierr != nil {
				return "", ierr
			}
			return ptr + "/" + strconv.Itoa(idx), nil
		}
		// Either a map-style add onto a node that already exists (OP-03's
		// upsert, OP-04's defaulting, `! idempotent` re-application), or a
		// path that ends in "-" and so already IS a position rather than a
		// node. Both are the pointer walk produced; placement has no RFC 6901
		// meaning inside an object, whose members are unordered there, and is
		// consumed.
		return ptr, nil
	}
	// The address does not exist yet: the ordinary map-style add, whose
	// resolved pointer is the parent's pointer plus the new key.
	return r.createPointer(path, line)
}

// placementIndex turns a relative placement (§6.2) into a concrete index in
// container. It mirrors hewjson.insertArrayElement, sibling lookup and
// fallback included: a placement path that resolves but is not a direct child
// of the container leaves the insertion at the end, because that is what the
// applier does, and a resolved list that disagreed with the applier would be
// describing an edit that never happened.
func (r *resolver) placementIndex(containerPtr string, container Node, before, after Path, line int) (int, error) {
	afterIdx, beforeIdx := -1, -1
	if !after.IsZero() {
		i, err := r.childIndex(containerPtr, after, line)
		if err != nil {
			return 0, err
		}
		afterIdx = i
	}
	if afterIdx < 0 && !before.IsZero() {
		i, err := r.childIndex(containerPtr, before, line)
		if err != nil {
			return 0, err
		}
		beforeIdx = i
	}
	switch {
	case afterIdx >= 0:
		return afterIdx + 1, nil
	case beforeIdx >= 0:
		return beforeIdx, nil
	default:
		return container.Len(), nil
	}
}

// childIndex resolves a placement sibling and returns its index within the
// container, or -1 if it is not a direct element of it.
func (r *resolver) childIndex(containerPtr string, sibling Path, line int) (int, error) {
	ptr, _, err := r.walk(sibling, line)
	if err != nil {
		return 0, err
	}
	rest, ok := strings.CutPrefix(ptr, containerPtr+"/")
	if !ok {
		return -1, nil
	}
	i, aerr := strconv.Atoi(rest)
	if aerr != nil {
		return -1, nil
	}
	return i, nil
}

// createPointer resolves an address whose LAST segment names a node that need
// not exist yet: an add's new key, or an `? absent` assertion's subject.
func (r *resolver) createPointer(path Path, line int) (string, error) {
	if err := r.checkPath(path, line); err != nil {
		return "", err
	}
	parent, ok := path.Parent()
	if !ok {
		return "", r.err(hewerr.CodeInexpressible, path.String(), line,
			"resolve: the document root is not a node that can be created or asserted absent")
	}
	parentPtr, parentNode, err := r.walk(parent, line)
	if err != nil {
		return "", err
	}
	last := path.Segment(path.Len() - 1)
	if last.Ordinal != nil {
		return "", r.err(hewerr.CodeInexpressible, path.String(), line,
			"resolve: an ordinal selector cannot address a node that does not exist")
	}
	switch last.Kind {
	case SegKey:
		return parentPtr + "/" + escapePointer(last.Name), nil
	case SegIndex:
		return parentPtr + "/" + strconv.Itoa(last.Index), nil
	case SegAppend:
		if parentNode.Kind() != KindSeq {
			return "", r.err(hewerr.CodeNoMatch, parent.String(), line,
				`resolve: "-" addresses the end of a sequence, but this node is a %s`, parentNode.Kind())
		}
		return parentPtr + "/" + strconv.Itoa(parentNode.Len()), nil
	case SegMatch:
		return "", r.err(hewerr.CodeNoMatch, path.String(), line,
			"resolve: %s matches no element, and RFC 6901 has no index for a node that does not exist (§9.2)", last.String())
	default:
		return "", r.err(hewerr.CodeInexpressible, path.String(), line,
			"resolve: a %s segment has no RFC 6901 representation (§9.2)", last.Kind)
	}
}

// checkPath rejects the two path forms that cannot reach a document at all.
func (r *resolver) checkPath(p Path, line int) error {
	if p.IsZero() {
		return r.err(hewerr.CodeInexpressible, "", line, "resolve: missing path")
	}
	if p.IsRelative() {
		return r.err(hewerr.CodeInexpressible, p.String(), line,
			"resolve: a relative path (§4.6) must be resolved against its hunk anchor before it reaches Resolve")
	}
	return nil
}

// walk resolves every segment of p against the document, building the RFC
// 6901 pointer as it goes. The root path yields "", RFC 6901's whole-document
// pointer.
func (r *resolver) walk(p Path, line int) (string, Node, error) {
	if err := r.checkPath(p, line); err != nil {
		return "", nil, err
	}
	segs := p.Segments()
	cur := r.root
	var b strings.Builder
	for i, seg := range segs {
		if cur == nil {
			return "", nil, r.err(hewerr.CodeNoMatch, NewPath(segs[:i]...).String(), line,
				"resolve: this address has no children to descend into")
		}
		tok, next, err := r.step(cur, seg)
		if err != nil {
			code := hewerr.CodeNoMatch
			if err.ambiguous {
				code = hewerr.CodeAmbiguousMatch
			}
			if err.inexpressible {
				code = hewerr.CodeInexpressible
			}
			return "", nil, r.err(code, NewPath(segs[:i+1]...).String(), line, "resolve: %s", err.detail)
		}
		b.WriteByte('/')
		b.WriteString(tok)
		cur = next
	}
	return b.String(), cur, nil
}

// stepErr classifies a single failed addressing step, so walk can attach the
// right HEW code and the path prefix that failed.
type stepErr struct {
	ambiguous     bool
	inexpressible bool
	detail        string
}

// step resolves one segment, returning the RFC 6901 token it projects to and
// the node it selects. A nil node means the segment addresses a position
// rather than a node ("-"), which only the last segment may do.
func (r *resolver) step(n Node, seg Segment) (string, Node, *stepErr) {
	if seg.Ordinal != nil && seg.Kind != SegMatch {
		return "", nil, &stepErr{inexpressible: true,
			detail: fmt.Sprintf("an ordinal selector on a %s segment has no RFC 6901 projection (§9.2)", seg.Kind)}
	}
	switch seg.Kind {
	case SegKey:
		if n.Kind() != KindMap {
			return "", nil, &stepErr{detail: fmt.Sprintf("%q: not a map", seg.Name)}
		}
		child, ok := n.Member(seg.Name)
		if !ok {
			return "", nil, &stepErr{detail: fmt.Sprintf("no key %q", seg.Name)}
		}
		return escapePointer(seg.Name), child, nil
	case SegIndex:
		if n.Kind() != KindSeq {
			return "", nil, &stepErr{detail: "not a sequence"}
		}
		child, ok := n.Elem(seg.Index)
		if !ok {
			return "", nil, &stepErr{detail: fmt.Sprintf("index %d out of range", seg.Index)}
		}
		return strconv.Itoa(seg.Index), child, nil
	case SegAppend:
		if n.Kind() != KindSeq {
			return "", nil, &stepErr{detail: `"-" addresses the end of a sequence, but this node is not one`}
		}
		// RFC 6902 spells the append position "-"; the resolved form is
		// concrete by definition (§9.2), so it becomes the index one past
		// the last element.
		return strconv.Itoa(n.Len()), nil, nil
	case SegMatch:
		return r.stepMatch(n, seg)
	default:
		// Labels, headings, blocks, markers and comment addresses: §9.2's
		// "no RFC 6902 representation at all".
		return "", nil, &stepErr{inexpressible: true,
			detail: fmt.Sprintf("a %s segment has no RFC 6901 representation (§9.2)", seg.Kind)}
	}
}

func (r *resolver) stepMatch(n Node, seg Segment) (string, Node, *stepErr) {
	if n.Kind() != KindSeq {
		return "", nil, &stepErr{detail: "not a sequence"}
	}
	var hits []matched
	for i := 0; i < n.Len(); i++ {
		e, ok := n.Elem(i)
		if ok && matchesSegment(e, seg) {
			hits = append(hits, matched{index: i, node: e})
		}
	}
	if seg.Ordinal != nil {
		// §7.2: ord is 0-based and counts same-shape siblings in source
		// order. Selecting the ordinal is the whole point of the selector,
		// so several matches are not ambiguous here.
		o := *seg.Ordinal
		if o < 0 || o >= len(hits) {
			return "", nil, &stepErr{detail: fmt.Sprintf("ordinal %d selects nothing among %d elements matching %s", o, len(hits), seg.String())}
		}
		return strconv.Itoa(hits[o].index), hits[o].node, nil
	}
	switch len(hits) {
	case 0:
		return "", nil, &stepErr{detail: "no element matches " + seg.String()}
	case 1:
		return strconv.Itoa(hits[0].index), hits[0].node, nil
	default:
		return "", nil, &stepErr{ambiguous: true,
			detail: fmt.Sprintf("%d elements match %s", len(hits), seg.String())}
	}
}

// matched is one element a key-match segment selected, with the index that
// becomes its RFC 6901 token.
type matched struct {
	index int
	node  Node
}

// matchesSegment reports whether node satisfies a key-match segment (§4.2):
// the `=value` form compares the element itself, the `field=value` form
// compares that member.
func matchesSegment(node Node, seg Segment) bool {
	want := seg.Value.Value()
	if seg.Name == "" {
		return node.Value().Equal(want)
	}
	if node.Kind() != KindMap {
		return false
	}
	m, ok := node.Member(seg.Name)
	if !ok {
		return false
	}
	return m.Value().Equal(want)
}

// Value converts a key-match segment's decoded scalar (§4.2) into a Value, so
// that a path's identity token compares against a target node under the same
// equality the rest of the IR uses.
func (s Scalar) Value() Value {
	tag := "!!str"
	switch s.Kind {
	case ScalarBool:
		tag = "!!bool"
	case ScalarNull:
		tag = "!!null"
	case ScalarNumber:
		if strings.ContainsAny(s.Text, ".eE") {
			tag = "!!float"
		} else {
			tag = "!!int"
		}
	}
	return NodeValue(&yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: s.Text})
}

// escapePointer encodes one RFC 6901 reference token: "~" is "~0" and "/" is
// "~1". Hew's own "~2" escape is a PATH spelling, not a pointer one, and does
// not survive the projection — an "=" in a key is literal in a pointer.
func escapePointer(s string) string {
	if !strings.ContainsAny(s, "~/") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '~':
			b.WriteString("~0")
		case '/':
			b.WriteString("~1")
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// MarshalResolvedOps writes the resolved op list in the JSON shape `hew apply
// --ops` prints and the corpus pins in expected-ops.json (§9.2, Appendix
// B.1): a JSON array, one op per line, two-space indented, fields in the same
// order the canonical .hewt record uses.
//
// Unlike MarshalTransforms this returns no error: every ResolvedOp came out
// of Resolve, which already refused everything JSON cannot spell, so there is
// nothing left here that can fail.
func MarshalResolvedOps(ops []ResolvedOp) []byte {
	var b strings.Builder
	b.WriteString("[\n")
	for i := range ops {
		b.WriteString("  ")
		b.WriteString(resolvedOpJSON(ops[i]))
		if i != len(ops)-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString("]\n")
	return []byte(b.String())
}

func resolvedOpJSON(op ResolvedOp) string {
	var fields []string
	add := func(key, val string) { fields = append(fields, strconv.Quote(key)+": "+val) }
	add("op", strconv.Quote(string(op.Op)))
	if op.From != "" {
		add("from", strconv.Quote(op.From))
	}
	add("path", strconv.Quote(op.Path))
	if op.Absent {
		add("absent", "true")
	}
	if op.Count != nil {
		add("count", strconv.Itoa(*op.Count))
	}
	if op.NodeKind != nil {
		add("kind", strconv.Quote(string(*op.NodeKind)))
	}
	if op.Exhaustive {
		add("exhaustive", "true")
	}
	if !op.Value.IsZero() {
		add("value", jsonLiteral(op.Value.Node()))
	}
	return "{ " + strings.Join(fields, ", ") + " }"
}

// jsonLiteral renders a value node as compact JSON, preserving mapping key
// order (a Go map would not) so that the printed op list is deterministic.
func jsonLiteral(n *yaml.Node) string {
	if n == nil {
		return "null"
	}
	switch n.Kind {
	case yaml.MappingNode:
		var parts []string
		for i := 0; i+1 < len(n.Content); i += 2 {
			parts = append(parts, strconv.Quote(n.Content[i].Value)+":"+jsonLiteral(n.Content[i+1]))
		}
		return "{" + strings.Join(parts, ",") + "}"
	case yaml.SequenceNode:
		var parts []string
		for _, c := range n.Content {
			parts = append(parts, jsonLiteral(c))
		}
		return "[" + strings.Join(parts, ",") + "]"
	case yaml.ScalarNode:
		switch n.ShortTag() {
		case "!!bool", "!!int", "!!float":
			return n.Value
		case "!!null":
			return "null"
		default:
			return strconv.Quote(n.Value)
		}
	default:
		return "null"
	}
}
