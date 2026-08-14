package hewfs

import (
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/benjaminabbitt/hew/go"
	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

func count(n int) *int { return &n }

func kind(k hew.NodeKind) *hew.NodeKind { return &k }

// fullRecord exercises every field of §9.7's schema at once, so the round trip
// below is a statement about the whole document and not about its easy half.
func fullRecord(t *testing.T) Record {
	t.Helper()
	v, err := hew.ValueOf(map[string]any{"command": "taskloom"})
	if err != nil {
		t.Fatal(err)
	}
	return Record{
		Version:   RecordVersion,
		AppliedAt: time.Date(2026, 8, 14, 9, 31, 7, 0, time.UTC),
		Patch:     RecordPatch{Source: "migrate.hew", Digest: "sha256:abc"},
		Targets: []RecordTarget{{
			Target: ".mcp.json", Format: hew.FormatJSON,
			Before: "sha256:1", After: "sha256:2", Committed: true,
			Transforms: []hew.ResolvedOp{
				{Op: hew.OpTest, Path: "/mcpServers/0/name", Value: mustValue(t, "ctxloom")},
				{Op: hew.OpTest, Path: "/mcpServers/9", Absent: true},
				{Op: hew.OpTest, Path: "/mcpServers", Count: count(2)},
				{Op: hew.OpTest, Path: "/mcpServers", NodeKind: kind(hew.KindSeq), Exhaustive: true},
				{Op: hew.OpCopy, From: "/a", Path: "/b"},
				{Op: hew.OpAdd, Path: "/mcpServers/1", Value: v},
				{Op: hew.OpRemove, Path: "/old"},
			},
		}, {
			Target: "settings.json", Format: hew.FormatJSONC,
			Before: "sha256:3", After: "sha256:3", Committed: false,
		}},
	}
}

func mustValue(t *testing.T, x any) hew.Value {
	t.Helper()
	v, err := hew.ValueOf(x)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestRecordRoundTrip: UnmarshalRecord is MarshalRecord's inverse, field for
// field. A record a later tool cannot read back is not an audit trail.
func TestRecordRoundTrip(t *testing.T) {
	want := fullRecord(t)
	out, err := MarshalRecord(want)
	if err != nil {
		t.Fatalf("MarshalRecord: %v", err)
	}
	got, err := UnmarshalRecord(out)
	if err != nil {
		t.Fatalf("UnmarshalRecord: %v\n%s", err, out)
	}
	if !got.AppliedAt.Equal(want.AppliedAt) {
		t.Errorf("applied_at = %s, want %s", got.AppliedAt, want.AppliedAt)
	}
	if got.Patch != want.Patch {
		t.Errorf("patch = %+v, want %+v", got.Patch, want.Patch)
	}
	if len(got.Targets) != len(want.Targets) {
		t.Fatalf("%d target rows, want %d", len(got.Targets), len(want.Targets))
	}
	for i := range want.Targets {
		w, g := want.Targets[i], got.Targets[i]
		if w.Target != g.Target || w.Format != g.Format || w.Before != g.Before ||
			w.After != g.After || w.Committed != g.Committed {
			t.Errorf("target %d = %+v, want %+v", i, g, w)
		}
		if len(g.Transforms) != len(w.Transforms) {
			t.Fatalf("target %d: %d transforms, want %d", i, len(g.Transforms), len(w.Transforms))
		}
		for j := range w.Transforms {
			assertOpEqual(t, g.Transforms[j], w.Transforms[j])
		}
	}
	// Re-marshalling the decoded record reproduces the bytes, which is the
	// stronger half of the identity and the one O37's reproducibility rests on.
	again, err := MarshalRecord(got)
	if err != nil {
		t.Fatalf("re-marshalling: %v", err)
	}
	if string(again) != string(out) {
		t.Errorf("marshal(unmarshal(x)) != x:\n%s\nvs\n%s", again, out)
	}
}

func assertOpEqual(t *testing.T, got, want hew.ResolvedOp) {
	t.Helper()
	if got.Op != want.Op || got.Path != want.Path || got.From != want.From ||
		got.Absent != want.Absent || got.Exhaustive != want.Exhaustive {
		t.Errorf("op = %+v, want %+v", got, want)
	}
	if (got.Count == nil) != (want.Count == nil) || (got.Count != nil && *got.Count != *want.Count) {
		t.Errorf("count = %v, want %v", got.Count, want.Count)
	}
	if (got.NodeKind == nil) != (want.NodeKind == nil) || (got.NodeKind != nil && *got.NodeKind != *want.NodeKind) {
		t.Errorf("kind = %v, want %v", got.NodeKind, want.NodeKind)
	}
	if !got.Value.Equal(want.Value) {
		t.Errorf("value = %v, want %v", got.Value, want.Value)
	}
}

// TestMarshalRecordShape pins the document's key order and version line: a
// record is read by other tools and by humans, and `hew-record` must be first
// for the same reason `hew: 1` is (§2.1's precedent, §9.7's table).
func TestMarshalRecordShape(t *testing.T) {
	out, err := MarshalRecord(fullRecord(t))
	if err != nil {
		t.Fatalf("MarshalRecord: %v", err)
	}
	if !strings.HasPrefix(string(out), "hew-record: 1\n") {
		t.Errorf("hew-record must be the first key, got:\n%s", out)
	}
	if !strings.Contains(string(out), `applied_at: "2026-08-14T09:31:07Z"`) {
		t.Errorf("applied_at is not RFC 3339 UTC:\n%s", out)
	}
}

// TestMarshalRecordNormalizesToUTC is §9.7's "normalized to RFC 3339 UTC and
// written byte-exactly": the same instant in two zones is one record.
func TestMarshalRecordNormalizesToUTC(t *testing.T) {
	utc := time.Date(2026, 8, 14, 9, 31, 7, 0, time.UTC)
	east := utc.In(time.FixedZone("east", 5*3600))
	a, err := MarshalRecord(Record{AppliedAt: utc})
	if err != nil {
		t.Fatal(err)
	}
	b, err := MarshalRecord(Record{AppliedAt: east})
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Errorf("the same instant produced two records:\n%s\nvs\n%s", a, b)
	}
	if !strings.Contains(string(a), "2026-08-14T09:31:07Z") {
		t.Errorf("not normalized to UTC:\n%s", a)
	}
}

func TestMarshalRecordRefusesAnUnknownVersion(t *testing.T) {
	_, err := MarshalRecord(Record{Version: 2})
	if err == nil {
		t.Fatal("want an error for hew-record version 2")
	}
	if he, ok := hewerr.As(err); !ok || he.Code != hewerr.CodeParse {
		t.Errorf("want HEW001, got %v", err)
	}
}

// TestMarshalRecordDefaultsTheVersion: the zero Version means "this one", so a
// caller building a record in memory does not have to restate the constant.
func TestMarshalRecordDefaultsTheVersion(t *testing.T) {
	out, err := MarshalRecord(Record{})
	if err != nil {
		t.Fatalf("MarshalRecord: %v", err)
	}
	if !strings.HasPrefix(string(out), "hew-record: 1\n") {
		t.Errorf("got:\n%s", out)
	}
}

func TestUnmarshalRecordIsStrict(t *testing.T) {
	const good = "hew-record: 1\napplied_at: \"2026-08-14T09:31:07Z\"\npatch:\n  source: p.hew\ntargets: []\n"
	if _, err := UnmarshalRecord([]byte(good)); err != nil {
		t.Fatalf("the control fixture must decode: %v", err)
	}
	cases := []struct {
		name string
		src  string
	}{
		{"unknown document key", "hew-record: 1\napplied_at: \"2026-08-14T09:31:07Z\"\nbogus: 1\n"},
		{"missing version", "applied_at: \"2026-08-14T09:31:07Z\"\n"},
		{"wrong version", "hew-record: 2\napplied_at: \"2026-08-14T09:31:07Z\"\n"},
		{"applied_at absent", "hew-record: 1\n"},
		{"applied_at not RFC 3339", "hew-record: 1\napplied_at: yesterday\n"},
		{"not a mapping", "- 1\n"},
		{"unknown op", "hew-record: 1\napplied_at: \"2026-08-14T09:31:07Z\"\ntargets:\n  - target: a\n    transforms:\n      - {op: frobnicate, path: /a}\n"},
		{"unknown kind", "hew-record: 1\napplied_at: \"2026-08-14T09:31:07Z\"\ntargets:\n  - target: a\n    transforms:\n      - {op: test, path: /a, kind: rhombus}\n"},
		{"unknown transform field", "hew-record: 1\napplied_at: \"2026-08-14T09:31:07Z\"\ntargets:\n  - target: a\n    transforms:\n      - {op: test, path: /a, bogus: 1}\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := UnmarshalRecord([]byte(tc.src))
			if err == nil {
				t.Fatal("want HEW001, got success")
			}
			if he, ok := hewerr.As(err); !ok || he.Code != hewerr.CodeParse {
				t.Errorf("want HEW001, got %v", err)
			}
		})
	}
}

// TestUnmarshalRecordCarriesTheValueNode: a record's `value` is YAML, and the
// decoder must hand it back as a node rather than flattening it to a string.
func TestUnmarshalRecordCarriesTheValueNode(t *testing.T) {
	src := "hew-record: 1\napplied_at: \"2026-08-14T09:31:07Z\"\ntargets:\n  - target: a\n    transforms:\n      - op: add\n        path: /s\n        value:\n          name: taskloom\n          args: [mcp]\n"
	rec, err := UnmarshalRecord([]byte(src))
	if err != nil {
		t.Fatalf("UnmarshalRecord: %v", err)
	}
	v := rec.Targets[0].Transforms[0].Value
	if v.IsZero() {
		t.Fatal("the value was dropped")
	}
	if v.Node().Kind != yaml.MappingNode {
		t.Errorf("value node kind = %v, want a mapping", v.Node().Kind)
	}
	var out struct {
		Name string   `yaml:"name"`
		Args []string `yaml:"args"`
	}
	if err := v.Decode(&out); err != nil {
		t.Fatalf("decoding the value: %v", err)
	}
	if out.Name != "taskloom" || len(out.Args) != 1 || out.Args[0] != "mcp" {
		t.Errorf("value decoded to %+v", out)
	}
}

func TestDigestIsSHA256Prefixed(t *testing.T) {
	got := Digest([]byte("abc"))
	const want = "sha256:ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if got != want {
		t.Errorf("Digest = %q, want %q", got, want)
	}
	if Digest([]byte("abc")) == Digest([]byte("abd")) {
		t.Error("different bytes must digest differently")
	}
}
