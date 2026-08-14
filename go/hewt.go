package hew

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/benjaminabbitt/hew/go/internal/hewerr"
	"gopkg.in/yaml.v3"
)

// The canonical .hewt key order (§9.6). `hew-transforms` MUST be the first
// document key; the record order below is the one the corpus fixtures use —
// `op`, then `from` before `path` on a copy, the assertion qualifiers, the
// format directives, the placement, and `value` last.
const (
	keyVersion    = "hew-transforms"
	keyTarget     = "target"
	keyFormat     = "format"
	keyTransforms = "transforms"

	keyOp         = "op"
	keyFrom       = "from"
	keyPath       = "path"
	keyAbsent     = "absent"
	keyCount      = "count"
	keyKind       = "kind"
	keyExhaustive = "exhaustive"
	keyOnConflict = "on_conflict"
	keyAnchor     = "anchor"
	keySurface    = "surface"
	keyOptional   = "optional"
	keyIdempotent = "idempotent"
	keyBefore     = "before"
	keyAfter      = "after"
	keyValue      = "value"
	keyLine       = "line"
)

// MarshalTransforms writes the canonical .hewt serialization of one transform
// list (§9.6). Output is deterministic: the same list always produces the same
// bytes.
//
// `line` is not emitted. §9.6 describes it as emitted and ignored on input,
// but no corpus fixture carries it and the parse seam compares canonicalized
// fixtures byte-for-byte against parser output, so the corpus governs.
func MarshalTransforms(tl TransformList) ([]byte, error) {
	return MarshalTransformStream([]TransformList{tl})
}

// MarshalTransformStream writes a multi-target transform list as a
// multi-document YAML stream, one document per target, applied in order
// (§9.6).
func MarshalTransformStream(tls []TransformList) ([]byte, error) {
	if len(tls) == 0 {
		return nil, irErr("", "transform stream is empty; an empty patch is refused (§10.2)")
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	for i := range tls {
		if err := tls[i].Validate(); err != nil {
			return nil, err
		}
		// Every path in this document is about to become text; refuse the ones
		// v0 cannot spell before writing an address that means something else.
		// MarshalTransforms funnels through here, so it is guarded too.
		if err := tls[i].checkSpellable(hewerr.ComponentRenderer); err != nil {
			return nil, err
		}
		if err := enc.Encode(documentNode(tls[i])); err != nil {
			return nil, irErr(tls[i].Target, "encoding .hewt: %v", err)
		}
	}
	if err := enc.Close(); err != nil {
		return nil, irErr("", "encoding .hewt: %v", err)
	}
	return buf.Bytes(), nil
}

func documentNode(tl TransformList) *yaml.Node {
	doc := mapNode()
	put(doc, keyVersion, intNode(TransformsVersion))
	put(doc, keyTarget, strNode(tl.Target))
	if tl.Format != "" {
		put(doc, keyFormat, strNode(string(tl.Format)))
	}
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for i := range tl.Transform {
		seq.Content = append(seq.Content, transformNode(tl.Transform[i]))
	}
	put(doc, keyTransforms, seq)
	return doc
}

func transformNode(t Transform) *yaml.Node {
	m := mapNode()
	put(m, keyOp, strNode(string(t.Op)))
	if !t.From.IsZero() {
		put(m, keyFrom, strNode(t.From.String()))
	}
	put(m, keyPath, strNode(t.Path.String()))
	if t.Absent {
		put(m, keyAbsent, boolNode(true))
	}
	if t.Count != nil {
		put(m, keyCount, intNode(*t.Count))
	}
	if t.NodeKind != nil {
		put(m, keyKind, strNode(string(*t.NodeKind)))
	}
	if t.Exhaustive {
		put(m, keyExhaustive, boolNode(true))
	}
	if t.OnConflict != "" {
		put(m, keyOnConflict, strNode(string(t.OnConflict)))
	}
	if t.Anchor != "" {
		put(m, keyAnchor, strNode(string(t.Anchor)))
	}
	if t.Surface != "" {
		put(m, keySurface, strNode(string(t.Surface)))
	}
	if t.Optional {
		put(m, keyOptional, boolNode(true))
	}
	if t.Idempotent {
		put(m, keyIdempotent, boolNode(true))
	}
	if !t.Before.IsZero() {
		put(m, keyBefore, strNode(t.Before.String()))
	}
	if !t.After.IsZero() {
		put(m, keyAfter, strNode(t.After.String()))
	}
	if !t.Value.IsZero() {
		put(m, keyValue, cloneNode(t.Value.Node()))
	}
	return m
}

func mapNode() *yaml.Node { return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"} }

func put(m *yaml.Node, key string, val *yaml.Node) {
	m.Content = append(m.Content, strNode(key), val)
}

func strNode(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
}

func intNode(i int) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(i)}
}

func boolNode(b bool) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(b)}
}

// UnmarshalTransforms reads a single-document .hewt file. Reading is strict:
// an unknown document key, an unknown transform field, a missing or non-1
// version, a missing target, or an empty transform sequence is HEW001. `op:
// move` is accepted as input sugar and normalized to copy+remove on the way
// in (§11.10 reduction 1).
func UnmarshalTransforms(src []byte) (TransformList, error) {
	tls, err := UnmarshalTransformStream(src)
	if err != nil {
		return TransformList{}, err
	}
	if len(tls) > 1 {
		return TransformList{}, irErr("", "%d documents in a single-target .hewt read; use UnmarshalTransformStream", len(tls))
	}
	return tls[0], nil
}

// UnmarshalTransformStream reads a multi-document .hewt stream (§9.6), one
// transform list per document.
func UnmarshalTransformStream(src []byte) ([]TransformList, error) {
	dec := yaml.NewDecoder(bytes.NewReader(src))
	var out []TransformList
	for {
		var doc yaml.Node
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, irErr("", "malformed .hewt YAML: %v", err)
		}
		tl, derr := decodeDocument(&doc)
		if derr != nil {
			return nil, derr
		}
		out = append(out, tl)
	}
	if len(out) == 0 {
		return nil, irErr("", "no .hewt document found; an empty patch is refused (§10.2)")
	}
	return out, nil
}

func atLine(err error, line int) error {
	if he, ok := hewerr.As(err); ok && he.PatchLine == 0 {
		he.PatchLine = line
	}
	return err
}

func decodeDocument(doc *yaml.Node) (TransformList, error) {
	var tl TransformList
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 {
		return tl, irErr("", "malformed .hewt document")
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return tl, atLine(irErr("", "a .hewt document must be a mapping"), root.Line)
	}
	if len(root.Content) == 0 {
		return tl, atLine(irErr("", "a .hewt document must be a mapping"), root.Line)
	}

	seen := map[string]bool{}
	var haveTransforms bool
	for i := 0; i < len(root.Content); i += 2 {
		k, v := root.Content[i], root.Content[i+1]
		key, err := scalarKey(k)
		if err != nil {
			return tl, atLine(err, k.Line)
		}
		if seen[key] {
			return tl, atLine(irErr("", "duplicate document key %q", key), k.Line)
		}
		seen[key] = true
		if i == 0 && key != keyVersion {
			return tl, atLine(irErr("", "%q must be the first key of a .hewt document (§9.6)", keyVersion), k.Line)
		}
		switch key {
		case keyVersion:
			n, err := intValue(v)
			if err != nil {
				return tl, atLine(irErr("", "%s: %v", keyVersion, err), v.Line)
			}
			if n != TransformsVersion {
				return tl, atLine(irErr("", "unsupported %s version %d, want %d", keyVersion, n, TransformsVersion), v.Line)
			}
		case keyTarget:
			s, err := stringValue(v)
			if err != nil {
				return tl, atLine(irErr("", "%s: %v", keyTarget, err), v.Line)
			}
			tl.Target = s
		case keyFormat:
			s, err := stringValue(v)
			if err != nil {
				return tl, atLine(irErr("", "%s: %v", keyFormat, err), v.Line)
			}
			tl.Format = FormatID(s)
		case keyTransforms:
			haveTransforms = true
			if v.Kind != yaml.SequenceNode {
				return tl, atLine(irErr("", "%s must be a sequence", keyTransforms), v.Line)
			}
			for _, item := range v.Content {
				ts, err := decodeTransform(item)
				if err != nil {
					return tl, err
				}
				tl.Transform = append(tl.Transform, ts...)
			}
		default:
			return tl, atLine(irErr("", "unknown document key %q", key), k.Line)
		}
	}
	if !seen[keyVersion] {
		return tl, irErr("", "missing %q key (§9.6)", keyVersion)
	}
	if !haveTransforms {
		return tl, irErr("", "missing %q key (§9.6)", keyTransforms)
	}
	if err := tl.Validate(); err != nil {
		return TransformList{}, atLine(err, root.Line)
	}
	return tl, nil
}

func decodeTransform(m *yaml.Node) ([]Transform, error) {
	if m.Kind != yaml.MappingNode {
		return nil, atLine(irErr("", "a transform must be a mapping"), m.Line)
	}
	var t Transform
	seen := map[string]bool{}
	for i := 0; i < len(m.Content); i += 2 {
		k, v := m.Content[i], m.Content[i+1]
		key, err := scalarKey(k)
		if err != nil {
			return nil, atLine(err, k.Line)
		}
		if seen[key] {
			return nil, atLine(irErr("", "duplicate transform field %q", key), k.Line)
		}
		seen[key] = true
		if err := assignField(&t, key, v); err != nil {
			return nil, atLine(err, v.Line)
		}
	}
	if t.Op == opMove {
		return normalizeMove(t, m.Line)
	}
	if err := t.Validate(); err != nil {
		return nil, atLine(err, m.Line)
	}
	return []Transform{t}, nil
}

func assignField(t *Transform, key string, v *yaml.Node) error {
	str := func(dst *string) error {
		s, err := stringValue(v)
		if err != nil {
			return irErr("", "%s: %v", key, err)
		}
		*dst = s
		return nil
	}
	path := func(dst *Path) error {
		s, err := stringValue(v)
		if err != nil {
			return irErr("", "%s: %v", key, err)
		}
		p, perr := ParsePath(s)
		if perr != nil {
			return perr
		}
		*dst = p
		return nil
	}
	flag := func(dst *bool) error {
		b, err := boolValue(v)
		if err != nil {
			return irErr("", "%s: %v", key, err)
		}
		if !b {
			return irErr("", "%s: false is meaningless; omit the field", key)
		}
		*dst = true
		return nil
	}

	switch key {
	case keyOp:
		var s string
		if err := str(&s); err != nil {
			return err
		}
		t.Op = OpKind(s)
		if !t.Op.Valid() && t.Op != opMove {
			return irErr("", "unknown op %q", s)
		}
	case keyPath:
		return path(&t.Path)
	case keyFrom:
		return path(&t.From)
	case keyBefore:
		return path(&t.Before)
	case keyAfter:
		return path(&t.After)
	case keyValue:
		t.Value = NodeValue(cloneNode(v))
	case keyAbsent:
		return flag(&t.Absent)
	case keyOptional:
		return flag(&t.Optional)
	case keyIdempotent:
		return flag(&t.Idempotent)
	case keyCount:
		n, err := intValue(v)
		if err != nil {
			return irErr("", "%s: %v", key, err)
		}
		t.Count = &n
	case keyKind:
		var s string
		if err := str(&s); err != nil {
			return err
		}
		k := NodeKind(s)
		t.NodeKind = &k
	case keyExhaustive:
		return flag(&t.Exhaustive)
	case keyOnConflict:
		var s string
		if err := str(&s); err != nil {
			return err
		}
		t.OnConflict = OnConflict(s)
	case keyAnchor:
		var s string
		if err := str(&s); err != nil {
			return err
		}
		t.Anchor = AnchorMode(s)
	case keySurface:
		var s string
		if err := str(&s); err != nil {
			return err
		}
		t.Surface = Surface(s)
	case keyLine:
		// §9.6: provenance, emitted and ignored on input. Accepting and
		// discarding it keeps Unmarshal∘Marshal the identity on the IR.
		if _, err := intValue(v); err != nil {
			return irErr("", "%s: %v", key, err)
		}
	default:
		return irErr("", "unknown transform field %q", key)
	}
	return nil
}

// normalizeMove expands the `op: move` input sugar into the two core records
// it desugars to (§11.10 reduction 1). The pair is emitted in copy-then-remove
// order, matching §9.6's worked example; `move` never reaches a Transform and
// is never written back out, which corpus/json/ir-move-normalized pins.
func normalizeMove(t Transform, line int) ([]Transform, error) {
	bad := func(what string) error {
		return atLine(irErr(t.Path.String(), "op move: %s is not valid on move", what), line)
	}
	switch {
	case t.Path.IsZero():
		return nil, atLine(irErr("", "op move: missing path"), line)
	case t.From.IsZero():
		return nil, atLine(irErr(t.Path.String(), "op move: missing from"), line)
	case !t.Value.IsZero():
		return nil, bad("value")
	case t.Absent:
		return nil, bad("absent")
	case t.Count != nil:
		return nil, bad("count")
	case t.NodeKind != nil:
		return nil, bad("kind")
	case t.Exhaustive:
		return nil, bad("exhaustive")
	case t.OnConflict != "":
		return nil, bad("on_conflict")
	case t.Surface != "":
		return nil, bad("surface")
	case t.Optional:
		return nil, bad("optional")
	case t.Idempotent:
		return nil, bad("idempotent")
	}
	cp := Transform{
		Op:        OpCopy,
		Path:      t.Path,
		From:      t.From,
		Before:    t.Before,
		After:     t.After,
		Anchor:    t.Anchor,
		PatchLine: t.PatchLine,
	}
	rm := Transform{Op: OpRemove, Path: t.From, PatchLine: t.PatchLine}
	if err := cp.Validate(); err != nil {
		return nil, atLine(err, line)
	}
	if err := rm.Validate(); err != nil {
		return nil, atLine(err, line)
	}
	return []Transform{cp, rm}, nil
}

func scalarKey(n *yaml.Node) (string, error) {
	if n.Kind != yaml.ScalarNode {
		return "", irErr("", "non-scalar mapping key")
	}
	return n.Value, nil
}

func stringValue(n *yaml.Node) (string, error) {
	if n.Kind != yaml.ScalarNode || n.ShortTag() != "!!str" {
		return "", fmt.Errorf("expected a string")
	}
	return n.Value, nil
}

func intValue(n *yaml.Node) (int, error) {
	if n.Kind != yaml.ScalarNode || n.ShortTag() != "!!int" {
		return 0, fmt.Errorf("expected an integer")
	}
	i, err := strconv.Atoi(n.Value)
	if err != nil {
		return 0, fmt.Errorf("expected an integer")
	}
	return i, nil
}

func boolValue(n *yaml.Node) (bool, error) {
	if n.Kind != yaml.ScalarNode || n.ShortTag() != "!!bool" {
		return false, fmt.Errorf("expected a boolean")
	}
	var b bool
	if err := n.Decode(&b); err != nil {
		return false, fmt.Errorf("expected a boolean")
	}
	return b, nil
}

// CanonicalizeTransforms re-canonicalizes .hewt bytes: read them, then write
// them back in canonical form. It is the corpus harness's CanonHewt hook —
// hand-authored fixtures compare against generated output at the IR level
// while the comparison itself stays byte-exact.
func CanonicalizeTransforms(src []byte) ([]byte, error) {
	tls, err := UnmarshalTransformStream(src)
	if err != nil {
		return nil, err
	}
	return MarshalTransformStream(tls)
}
