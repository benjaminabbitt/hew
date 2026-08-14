package hewfs

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/benjaminabbitt/hew/go"
	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

// RecordVersion is the `hew-record` document version this package reads and
// writes (§9.7).
const RecordVersion = 1

// Record is the application record (§9.7): what was executed, against which
// bytes. It is the input a future `hew revert` inverts, and the shape of the
// ownership record a host project's config writer otherwise lacks.
type Record struct {
	Version   int
	AppliedAt time.Time
	Patch     RecordPatch
	Targets   []RecordTarget
}

// RecordPatch names the patch a record came from. Source is informational;
// Digest ties the record to the exact bytes that produced it.
type RecordPatch struct{ Source, Digest string }

// RecordTarget is one target's row. Transforms is the RESOLVED list (§9.2) —
// indices concrete, key-matches collapsed — because the record states what
// happened to THIS file, not what the patch said in general.
type RecordTarget struct {
	Target     string
	Format     hew.FormatID
	Before     string // "sha256:..." of the bytes as read
	After      string // "sha256:..." of the bytes as written
	Committed  bool
	Transforms []hew.ResolvedOp
}

// Digest is the `sha256:`-prefixed spelling §9.7's before/after/patch.digest
// fields use.
func Digest(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// MarshalRecord writes a record as its §9.7 YAML document. Deterministic: the
// same record always produces the same bytes, which is the whole point of
// O37's pinnable applied_at — a record built twice from the same inputs and the
// same pin is the same file.
//
// applied_at is normalized to RFC 3339 UTC regardless of the location the
// caller's time.Time carried, because §9.7 says UTC and a record that spelled
// the same instant two ways would not be byte-reproducible across machines.
func MarshalRecord(r Record) ([]byte, error) {
	if r.Version != 0 && r.Version != RecordVersion {
		return nil, recordErr("unsupported hew-record version %d, want %d", r.Version, RecordVersion)
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	root := mapNode()
	put(root, "hew-record", intNode(RecordVersion))
	put(root, "applied_at", strNode(r.AppliedAt.UTC().Format(time.RFC3339)))

	patch := mapNode()
	put(patch, "source", strNode(r.Patch.Source))
	if r.Patch.Digest != "" {
		put(patch, "digest", strNode(r.Patch.Digest))
	}
	put(root, "patch", patch)

	targets := seqNode()
	for _, t := range r.Targets {
		tn := mapNode()
		put(tn, "target", strNode(t.Target))
		put(tn, "format", strNode(string(t.Format)))
		put(tn, "before", strNode(t.Before))
		put(tn, "after", strNode(t.After))
		put(tn, "committed", boolNode(t.Committed))
		ops := seqNode()
		for _, op := range t.Transforms {
			ops.Content = append(ops.Content, resolvedOpNode(op))
		}
		put(tn, "transforms", ops)
		targets.Content = append(targets.Content, tn)
	}
	put(root, "targets", targets)

	if err := enc.Encode(root); err != nil {
		return nil, recordErr("encoding the application record: %v", err)
	}
	if err := enc.Close(); err != nil {
		return nil, recordErr("encoding the application record: %v", err)
	}
	return buf.Bytes(), nil
}

// UnmarshalRecord reads a §9.7 application record. Reading is strict: an
// unknown key, a missing or non-1 version, or a malformed timestamp is HEW001.
// It is MarshalRecord's inverse, which is what lets a later tool — a verifier
// checking whether a file still holds what hew left, or the `hew revert` §9.7
// defers — read a record back without reimplementing the schema.
func UnmarshalRecord(src []byte) (Record, error) {
	var doc recordDoc
	dec := yaml.NewDecoder(bytes.NewReader(src))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		return Record{}, recordErr("malformed application record: %v", err)
	}
	if doc.Version != RecordVersion {
		return Record{}, recordErr("unsupported hew-record version %d, want %d (§9.7)", doc.Version, RecordVersion)
	}
	ts, err := time.Parse(time.RFC3339, doc.AppliedAt)
	if err != nil {
		return Record{}, recordErr("applied_at %q is not an RFC 3339 timestamp (§9.7)", doc.AppliedAt)
	}
	rec := Record{Version: doc.Version, AppliedAt: ts.UTC(), Patch: doc.Patch}
	for _, t := range doc.Targets {
		rt := RecordTarget{
			Target: t.Target, Format: hew.FormatID(t.Format),
			Before: t.Before, After: t.After, Committed: t.Committed,
		}
		for _, o := range t.Transforms {
			op, oerr := o.resolved()
			if oerr != nil {
				return Record{}, oerr
			}
			rt.Transforms = append(rt.Transforms, op)
		}
		rec.Targets = append(rec.Targets, rt)
	}
	return rec, nil
}

// recordDoc mirrors the §9.7 document for strict decoding. It exists because
// Record itself holds a time.Time and a hew.Value, neither of which is the
// document's own spelling.
type recordDoc struct {
	Version   int               `yaml:"hew-record"`
	AppliedAt string            `yaml:"applied_at"`
	Patch     RecordPatch       `yaml:"patch"`
	Targets   []recordTargetDoc `yaml:"targets"`
}

type recordTargetDoc struct {
	Target     string        `yaml:"target"`
	Format     string        `yaml:"format"`
	Before     string        `yaml:"before"`
	After      string        `yaml:"after"`
	Committed  bool          `yaml:"committed"`
	Transforms []recordOpDoc `yaml:"transforms"`
}

type recordOpDoc struct {
	Op         string
	From       string
	Path       string
	Absent     bool
	Count      *int
	Kind       *string
	Exhaustive bool
	Value      *yaml.Node
}

// UnmarshalYAML walks the transform mapping by hand rather than letting the
// strict struct decoder do it. It has to: `value` is an arbitrary YAML node,
// and a decoder in KnownFields mode recurses INTO yaml.Node's own struct
// fields, so `value: ctxloom` becomes "cannot unmarshal !!str into yaml.Node".
// Walking the keys keeps both properties — an unknown field is refused, and a
// value is kept exactly as authored (§9.6's "the value, in YAML").
func (o *recordOpDoc) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return recordErr("a record transform must be a mapping (§9.6)")
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, val := n.Content[i].Value, n.Content[i+1]
		var err error
		switch key {
		case "op":
			err = val.Decode(&o.Op)
		case "from":
			err = val.Decode(&o.From)
		case "path":
			err = val.Decode(&o.Path)
		case "absent":
			err = val.Decode(&o.Absent)
		case "count":
			err = val.Decode(&o.Count)
		case "kind":
			err = val.Decode(&o.Kind)
		case "exhaustive":
			err = val.Decode(&o.Exhaustive)
		case "value":
			o.Value = val
		default:
			return recordErr("unknown transform field %q in a record (§9.6)", key)
		}
		if err != nil {
			return recordErr("transform field %q: %v", key, err)
		}
	}
	return nil
}

func (o recordOpDoc) resolved() (hew.ResolvedOp, error) {
	op := hew.ResolvedOp{
		Op: hew.OpKind(o.Op), Path: o.Path, From: o.From,
		Absent: o.Absent, Count: o.Count, Exhaustive: o.Exhaustive,
	}
	if !op.Op.Valid() {
		return hew.ResolvedOp{}, recordErr("unknown op %q in a record's transforms (§9.6)", o.Op)
	}
	if o.Kind != nil {
		k := hew.NodeKind(*o.Kind)
		if !k.Valid() {
			return hew.ResolvedOp{}, recordErr("unknown kind %q in a record's transforms (§9.6)", *o.Kind)
		}
		op.NodeKind = &k
	}
	if o.Value != nil {
		op.Value = hew.NodeValue(o.Value)
	}
	return op, nil
}

// resolvedOpNode serializes one resolved op in the .hewt transform-record
// field order (§9.6), minus the qualifiers resolution consumed.
func resolvedOpNode(op hew.ResolvedOp) *yaml.Node {
	m := mapNode()
	put(m, "op", strNode(string(op.Op)))
	if op.From != "" {
		put(m, "from", strNode(op.From))
	}
	put(m, "path", strNode(op.Path))
	if op.Absent {
		put(m, "absent", boolNode(true))
	}
	if op.Count != nil {
		put(m, "count", intNode(*op.Count))
	}
	if op.NodeKind != nil {
		put(m, "kind", strNode(string(*op.NodeKind)))
	}
	if op.Exhaustive {
		put(m, "exhaustive", boolNode(true))
	}
	if !op.Value.IsZero() {
		put(m, "value", op.Value.Node())
	}
	return m
}

func mapNode() *yaml.Node { return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"} }
func seqNode() *yaml.Node { return &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"} }

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

func recordErr(format string, args ...any) error {
	return &hewerr.Error{
		Code:      hewerr.CodeParse,
		Component: hewerr.ComponentCLI,
		Detail:    fmt.Sprintf(format, args...),
	}
}
