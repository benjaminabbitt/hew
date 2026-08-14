package hewcli

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"github.com/benjaminabbitt/hew/go"
	"gopkg.in/yaml.v3"
)

// recordTarget and applicationRecord are §9.7's application record: what hew
// did to which bytes, from which patch. targets[].transforms holds the
// RESOLVED list (§9.2) — indices concrete, key-matches collapsed — because
// the record states what happened to THIS file, not what the patch said in
// general, and because the resolved list plus both digests is precisely what
// a future `hew revert` needs to invert safely.
type recordTarget struct {
	Target     string
	Format     hew.FormatID
	Before     string
	After      string
	Committed  bool
	Transforms []hew.ResolvedOp
}

type applicationRecord struct {
	AppliedAt   time.Time
	PatchSource string
	PatchDigest string
	Targets     []recordTarget
}

// buildRecord resolves each staged section against the target as READ. The
// pre-image is the right document to resolve against: it is what the applier
// itself resolved every path through, so `/mcpServers/1` in the record is the
// position the add really took.
func buildRecord(inputs []patchInput, results []staged) (applicationRecord, error) {
	rec := applicationRecord{AppliedAt: time.Now().UTC(), PatchSource: "-"}
	if len(inputs) > 0 {
		rec.PatchSource = inputs[0].source
		rec.PatchDigest = digest(inputs[0].bytes)
	}
	for _, r := range results {
		doc, err := documentFor(r.tl.Target, r.format, r.before)
		if err != nil {
			return applicationRecord{}, err
		}
		ops, err := hew.Resolve(r.tl, doc)
		if err != nil {
			return applicationRecord{}, err
		}
		rec.Targets = append(rec.Targets, recordTarget{
			Target: r.tl.Target, Format: r.format,
			Before: digest(r.before), After: digest(r.after),
			Committed: true, Transforms: ops,
		})
	}
	return rec, nil
}

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func marshalRecord(r applicationRecord) ([]byte, error) {
	var b strings.Builder
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	put := func(key string, v *yaml.Node) {
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, v)
	}
	str := func(s string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s} }
	put("hew-record", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: "1"})
	put("applied_at", str(r.AppliedAt.Format(time.RFC3339)))
	patchNode := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	patchNode.Content = append(patchNode.Content, str("source"), str(r.PatchSource))
	if r.PatchDigest != "" {
		patchNode.Content = append(patchNode.Content, str("digest"), str(r.PatchDigest))
	}
	put("patch", patchNode)

	targets := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, t := range r.Targets {
		tn := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		add := func(k string, v *yaml.Node) { tn.Content = append(tn.Content, str(k), v) }
		add("target", str(t.Target))
		add("format", str(string(t.Format)))
		add("before", str(t.Before))
		add("after", str(t.After))
		add("committed", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(t.Committed)})
		tfSeq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, op := range t.Transforms {
			tfSeq.Content = append(tfSeq.Content, resolvedOpNode(op))
		}
		add("transforms", tfSeq)
		targets.Content = append(targets.Content, tn)
	}
	put("targets", targets)

	if err := enc.Encode(root); err != nil {
		return nil, err
	}
	_ = enc.Close()
	return []byte(b.String()), nil
}

// resolvedOpNode serializes one resolved op in the .hewt transform-record
// field order (§9.6), minus the qualifiers resolution consumed.
func resolvedOpNode(op hew.ResolvedOp) *yaml.Node {
	m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	str := func(s string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s} }
	add := func(k string, v *yaml.Node) { m.Content = append(m.Content, str(k), v) }
	add("op", str(string(op.Op)))
	if op.From != "" {
		add("from", str(op.From))
	}
	add("path", str(op.Path))
	if op.Absent {
		add("absent", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"})
	}
	if op.Count != nil {
		add("count", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(*op.Count)})
	}
	if op.NodeKind != nil {
		add("kind", str(string(*op.NodeKind)))
	}
	if op.Exhaustive {
		add("exhaustive", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"})
	}
	if !op.Value.IsZero() {
		add("value", op.Value.Node())
	}
	return m
}
