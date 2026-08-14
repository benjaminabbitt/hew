package hew

import (
	"strings"
	"testing"

	"github.com/benjaminabbitt/hew/internal/hewerr"
)

func mustVal(t *testing.T, x any) Value {
	t.Helper()
	v, err := ValueOf(x)
	if err != nil {
		t.Fatalf("ValueOf(%v): %v", x, err)
	}
	return v
}

func TestMarshalUnmarshalRoundTrip(t *testing.T) {
	tl := TransformList{
		Target: "config.yaml",
		Format: FormatYAML,
		Transform: []Transform{
			{Op: OpTest, Path: MustParsePath("/server/timeout"), Value: mustVal(t, 30)},
			{Op: OpReplace, Path: MustParsePath("/server/timeout"), Value: mustVal(t, 60)},
			{Op: OpAdd, Path: MustParsePath("/mcpServers/name=ctxloom"), After: MustParsePath("/mcpServers/name=github"),
				Value: mustVal(t, map[string]any{"name": "ctxloom", "command": "ctxloom"})},
		},
	}
	out, err := MarshalTransforms(tl)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got, err := UnmarshalTransforms(out)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !tl.Equal(got) {
		t.Fatalf("round trip mismatch:\nwant %+v\ngot  %+v", tl, got)
	}
	// hew-transforms must be the first key.
	if !strings.HasPrefix(string(out), "hew-transforms: 1\n") {
		t.Fatalf("hew-transforms must be first key, got:\n%s", out)
	}
}

func TestUnmarshalEmptyTransformsIsError(t *testing.T) {
	_, err := UnmarshalTransforms([]byte("hew-transforms: 1\ntarget: t\ntransforms: []\n"))
	if err == nil {
		t.Fatal("empty transforms sequence must be HEW001 (§9.6, §10.2)")
	}
	he, ok := hewerr.As(err)
	if !ok || he.Code != hewerr.CodeParse {
		t.Fatalf("want HEW001, got %v", err)
	}
}

func TestUnmarshalUnknownDocKeyIsError(t *testing.T) {
	_, err := UnmarshalTransforms([]byte("hew-transforms: 1\ntarget: t\nbogus: 1\ntransforms:\n- {op: test, path: /a, absent: true}\n"))
	if err == nil {
		t.Fatal("unknown document key must be HEW001")
	}
}

func TestUnmarshalVersionMustBeFirstKey(t *testing.T) {
	_, err := UnmarshalTransforms([]byte("target: t\nhew-transforms: 1\ntransforms:\n- {op: test, path: /a, absent: true}\n"))
	if err == nil {
		t.Fatal("hew-transforms must be the first key (§9.6)")
	}
}

func TestMoveNormalizedToCopyPlusRemove(t *testing.T) {
	src := []byte("hew-transforms: 1\ntarget: config.yaml\ntransforms:\n" +
		"- op: test\n  path: /server/host\n  value: localhost\n" +
		"- op: move\n  from: /server/host\n  path: /network/host\n")
	tl, err := UnmarshalTransforms(src)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(tl.Transform) != 3 {
		t.Fatalf("want 3 transforms (test, copy, remove), got %d: %+v", len(tl.Transform), tl.Transform)
	}
	if tl.Transform[1].Op != OpCopy || tl.Transform[2].Op != OpRemove {
		t.Fatalf("move must desugar to copy then remove (§11.10 reduction 1), got %s then %s",
			tl.Transform[1].Op, tl.Transform[2].Op)
	}
	if tl.Transform[1].From.String() != "/server/host" || tl.Transform[1].Path.String() != "/network/host" {
		t.Fatalf("copy addressing wrong: %+v", tl.Transform[1])
	}
	if tl.Transform[2].Path.String() != "/server/host" {
		t.Fatalf("remove addressing wrong: %+v", tl.Transform[2])
	}
	// Re-marshaling must never emit "move" (json/ir-move-normalized).
	out, err := MarshalTransforms(tl)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(out), "op: move") {
		t.Fatalf("move must never be re-emitted:\n%s", out)
	}
}

func TestExhaustiveRequiresCount(t *testing.T) {
	tr := Transform{Op: OpTest, Path: MustParsePath("/permissions"), Exhaustive: true}
	if err := tr.Validate(); err == nil {
		t.Fatal("exhaustive without count must fail Validate")
	}
	n := 2
	tr.Count = &n
	if err := tr.Validate(); err != nil {
		t.Fatalf("exhaustive+count should validate: %v", err)
	}
}

func TestExhaustiveRoundTrips(t *testing.T) {
	n := 2
	tl := TransformList{
		Target: "t.json", Format: FormatJSON,
		Transform: []Transform{{Op: OpTest, Path: MustParsePath("/permissions"), Exhaustive: true, Count: &n}},
	}
	out, err := MarshalTransforms(tl)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(out), "exhaustive: true") {
		t.Fatalf("expected exhaustive: true in output:\n%s", out)
	}
	got, err := UnmarshalTransforms(out)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !tl.Equal(got) {
		t.Fatalf("round trip mismatch: %+v != %+v", tl, got)
	}
}

func TestValidateRejectsMeaninglessFieldCombinations(t *testing.T) {
	cases := []Transform{
		{Op: OpAdd},                             // missing value
		{Op: OpCopy, Path: MustParsePath("/a")}, // missing from
		{Op: OpRemove, Path: MustParsePath("/a"), Value: mustValNoT(1)}, // value not valid on remove
		{Op: OpTest, Path: MustParsePath("/a")},                         // no assertion mode
		{Op: OpTest, Path: MustParsePath("/a"), Value: mustValNoT(1),
			Absent: true}, // two assertion modes
		{Op: OpAdd, Path: MustParsePath("/a"), Value: mustValNoT(1),
			Before: MustParsePath("/b"), After: MustParsePath("/c")}, // before+after mutually exclusive
	}
	for i, tr := range cases {
		if err := tr.Validate(); err == nil {
			t.Errorf("case %d: expected Validate error, got nil (%+v)", i, tr)
		}
	}
}

func mustValNoT(x any) Value {
	v, err := ValueOf(x)
	if err != nil {
		panic(err)
	}
	return v
}
