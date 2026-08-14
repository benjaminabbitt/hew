package hewcli

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"github.com/hew-format/hew"
	"gopkg.in/yaml.v3"
)

// recordTarget and record mirror Appendix A.8's Record/RecordTarget for the
// one field this slice does not have: targets[].transforms holds the
// ABSTRACT transform list, not the resolved RFC 6901 form §9.7 specifies.
// Flagged in the P2 report: Resolve (§9.2, Appendix A.1) is not implemented
// in P2, and building the resolved form is exactly what it is for.
type recordTarget struct {
	Target     string
	Format     hew.FormatID
	Before     string
	After      string
	Committed  bool
	Transforms []hew.Transform
}

type applicationRecord struct {
	AppliedAt   time.Time
	PatchSource string
	PatchDigest string
	Targets     []recordTarget
}

func buildRecord(patchFiles []string, results []staged) applicationRecord {
	source := "-"
	if len(patchFiles) > 0 {
		source = patchFiles[0]
	}
	rec := applicationRecord{AppliedAt: time.Now().UTC(), PatchSource: source}
	for _, r := range results {
		rec.Targets = append(rec.Targets, recordTarget{
			Target: r.tl.Target, Format: r.format,
			Before: digest(r.before), After: digest(r.after),
			Committed: true, Transforms: r.tl.Transform,
		})
	}
	return rec
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
		for _, tr := range t.Transforms {
			tfSeq.Content = append(tfSeq.Content, transformSummaryNode(tr))
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

func transformSummaryNode(t hew.Transform) *yaml.Node {
	m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	str := func(s string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s} }
	add := func(k string, v *yaml.Node) { m.Content = append(m.Content, str(k), v) }
	add("op", str(string(t.Op)))
	add("path", str(t.Path.String()))
	if !t.Value.IsZero() {
		add("value", t.Value.Node())
	}
	return m
}
