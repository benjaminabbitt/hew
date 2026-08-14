package hew

import (
	"strings"
	"testing"

	"github.com/ctxloom/hew/internal/hewerr"
)

func val(t *testing.T, x any) Value { return mustValue(t, x) }

func TestTransformValidateAccepts(t *testing.T) {
	kind := KindSeq
	n := 2
	ok := []struct {
		name string
		tr   Transform
	}{
		{"test value", Transform{Op: OpTest, Path: MustParsePath("/a"), Value: val(t, 1)}},
		{"test absent", Transform{Op: OpTest, Path: MustParsePath("/a"), Absent: true}},
		{"test count", Transform{Op: OpTest, Path: MustParsePath("/a"), Count: &n}},
		{"test count zero", Transform{Op: OpTest, Path: MustParsePath("/a"), Count: ptr(0)}},
		{"test kind", Transform{Op: OpTest, Path: MustParsePath("/a"), NodeKind: &kind}},
		{"test optional", Transform{Op: OpTest, Path: MustParsePath("/a"), Absent: true, Optional: true}},
		{"test on root", Transform{Op: OpTest, Path: RootPath(), Count: &n}},
		{"add", Transform{Op: OpAdd, Path: MustParsePath("/a"), Value: val(t, 1)}},
		{"add after", Transform{Op: OpAdd, Path: MustParsePath("/a"), Value: val(t, 1), After: MustParsePath("/b")}},
		{"add before", Transform{Op: OpAdd, Path: MustParsePath("/a"), Value: val(t, 1), Before: MustParsePath("/b")}},
		{"add on_conflict", Transform{Op: OpAdd, Path: MustParsePath("/a"), Value: val(t, 1), OnConflict: ConflictKeep}},
		{"add surface", Transform{Op: OpAdd, Path: MustParsePath("/a"), Value: val(t, 1), Surface: SurfaceDotted}},
		{"add idempotent", Transform{Op: OpAdd, Path: MustParsePath("/a"), Value: val(t, 1), Idempotent: true}},
		{"add anchor", Transform{Op: OpAdd, Path: MustParsePath("/a"), Value: val(t, 1), Anchor: AnchorRewrite}},
		{"remove", Transform{Op: OpRemove, Path: MustParsePath("/a")}},
		{"remove optional", Transform{Op: OpRemove, Path: MustParsePath("/a"), Optional: true}},
		{"remove idempotent", Transform{Op: OpRemove, Path: MustParsePath("/a"), Idempotent: true}},
		{"replace", Transform{Op: OpReplace, Path: MustParsePath("/a"), Value: val(t, 1)}},
		{"replace idempotent", Transform{Op: OpReplace, Path: MustParsePath("/a"), Value: val(t, 1), Idempotent: true}},
		{"copy", Transform{Op: OpCopy, Path: MustParsePath("/a"), From: MustParsePath("/b")}},
		{"copy before", Transform{Op: OpCopy, Path: MustParsePath("/a"), From: MustParsePath("/b"), Before: MustParsePath("/c")}},
		{"copy anchor", Transform{Op: OpCopy, Path: MustParsePath("/a"), From: MustParsePath("/b"), Anchor: AnchorFork}},
	}
	for _, tc := range ok {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.tr.Validate(); err != nil {
				t.Errorf("Validate rejected a legal record: %v", err)
			}
		})
	}
}

func TestTransformValidateRejects(t *testing.T) {
	kind := KindMap
	badKind := NodeKind("array")
	n := 1
	bad := []struct {
		name   string
		tr     Transform
		detail string
	}{
		{"no op", Transform{Path: MustParsePath("/a")}, "missing op"},
		{"unknown op", Transform{Op: "frobnicate", Path: MustParsePath("/a")}, `unknown op "frobnicate"`},
		{"move is not an op", Transform{Op: opMove, Path: MustParsePath("/a"), From: MustParsePath("/b")}, `unknown op "move"`},
		{"no path", Transform{Op: OpRemove}, "missing path"},
		{"copy without from", Transform{Op: OpCopy, Path: MustParsePath("/a")}, "op copy: missing from"},
		{"from on add", Transform{Op: OpAdd, Path: MustParsePath("/a"), Value: val(t, 1), From: MustParsePath("/b")},
			"from is valid only on copy"},
		{"from on remove", Transform{Op: OpRemove, Path: MustParsePath("/a"), From: MustParsePath("/b")},
			"from is valid only on copy"},
		{"from on test", Transform{Op: OpTest, Path: MustParsePath("/a"), Absent: true, From: MustParsePath("/b")},
			"from is valid only on copy"},
		{"add without value", Transform{Op: OpAdd, Path: MustParsePath("/a")}, "op add: missing value"},
		{"replace without value", Transform{Op: OpReplace, Path: MustParsePath("/a")}, "op replace: missing value"},
		{"value on remove", Transform{Op: OpRemove, Path: MustParsePath("/a"), Value: val(t, 1)},
			"value is valid only on add, replace and test"},
		{"value on copy", Transform{Op: OpCopy, Path: MustParsePath("/a"), From: MustParsePath("/b"), Value: val(t, 1)},
			"value is valid only on add, replace and test"},
		{"before and after", Transform{Op: OpAdd, Path: MustParsePath("/a"), Value: val(t, 1),
			Before: MustParsePath("/b"), After: MustParsePath("/c")}, "mutually exclusive"},
		{"placement on remove", Transform{Op: OpRemove, Path: MustParsePath("/a"), After: MustParsePath("/b")},
			"before/after are valid only on add and copy"},
		{"placement on replace", Transform{Op: OpReplace, Path: MustParsePath("/a"), Value: val(t, 1), Before: MustParsePath("/b")},
			"before/after are valid only on add and copy"},
		{"placement on test", Transform{Op: OpTest, Path: MustParsePath("/a"), Absent: true, Before: MustParsePath("/b")},
			"before/after are valid only on add and copy"},
		{"test with no assert mode", Transform{Op: OpTest, Path: MustParsePath("/a")},
			"exactly one of value, absent, count and kind, got 0"},
		{"test with two assert modes", Transform{Op: OpTest, Path: MustParsePath("/a"), Value: val(t, 1), Absent: true},
			"exactly one of value, absent, count and kind, got 2"},
		{"test with count and kind", Transform{Op: OpTest, Path: MustParsePath("/a"), Count: &n, NodeKind: &kind},
			"exactly one of value, absent, count and kind, got 2"},
		{"test with all four", Transform{Op: OpTest, Path: MustParsePath("/a"), Value: val(t, 1), Absent: true, Count: &n, NodeKind: &kind},
			"exactly one of value, absent, count and kind, got 4"},
		{"absent on add", Transform{Op: OpAdd, Path: MustParsePath("/a"), Value: val(t, 1), Absent: true},
			"absent is valid only on test"},
		{"count on remove", Transform{Op: OpRemove, Path: MustParsePath("/a"), Count: &n},
			"count is valid only on test"},
		{"kind on replace", Transform{Op: OpReplace, Path: MustParsePath("/a"), Value: val(t, 1), NodeKind: &kind},
			"kind is valid only on test"},
		{"negative count", Transform{Op: OpTest, Path: MustParsePath("/a"), Count: ptr(-1)}, "must not be negative"},
		{"unknown kind", Transform{Op: OpTest, Path: MustParsePath("/a"), NodeKind: &badKind}, `unknown kind "array"`},
		{"on_conflict on replace", Transform{Op: OpReplace, Path: MustParsePath("/a"), Value: val(t, 1), OnConflict: ConflictFail},
			"on_conflict is valid only on add"},
		{"unknown on_conflict", Transform{Op: OpAdd, Path: MustParsePath("/a"), Value: val(t, 1), OnConflict: "merge"},
			`unknown on_conflict "merge"`},
		{"surface on remove", Transform{Op: OpRemove, Path: MustParsePath("/a"), Surface: SurfaceTable},
			"surface is valid only on add"},
		{"unknown surface", Transform{Op: OpAdd, Path: MustParsePath("/a"), Value: val(t, 1), Surface: "inline"},
			`unknown surface "inline"`},
		{"unknown anchor", Transform{Op: OpRemove, Path: MustParsePath("/a"), Anchor: "merge"}, `unknown anchor "merge"`},
		{"optional on add", Transform{Op: OpAdd, Path: MustParsePath("/a"), Value: val(t, 1), Optional: true},
			"optional is valid only on remove and test"},
		{"optional on replace", Transform{Op: OpReplace, Path: MustParsePath("/a"), Value: val(t, 1), Optional: true},
			"optional is valid only on remove and test"},
		{"optional on copy", Transform{Op: OpCopy, Path: MustParsePath("/a"), From: MustParsePath("/b"), Optional: true},
			"optional is valid only on remove and test"},
		{"idempotent on test", Transform{Op: OpTest, Path: MustParsePath("/a"), Absent: true, Idempotent: true},
			"idempotent is valid only on add, remove and replace"},
		{"idempotent on copy", Transform{Op: OpCopy, Path: MustParsePath("/a"), From: MustParsePath("/b"), Idempotent: true},
			"idempotent is valid only on add, remove and replace"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.tr.Validate()
			if err == nil {
				t.Fatalf("Validate accepted %+v", tc.tr)
			}
			he, ok := hewerr.As(err)
			if !ok {
				t.Fatalf("error is not a *hewerr.Error: %T", err)
			}
			if he.Code != hewerr.CodeParse {
				t.Errorf("code = %s, want HEW001", he.Code)
			}
			if he.Component != hewerr.ComponentParser {
				t.Errorf("component = %s, want parser", he.Component)
			}
			if !strings.Contains(he.Detail, tc.detail) {
				t.Errorf("detail %q does not contain %q", he.Detail, tc.detail)
			}
		})
	}
}

func TestTransformListValidate(t *testing.T) {
	good := TransformList{Target: "t.json", Transform: []Transform{{Op: OpRemove, Path: MustParsePath("/a")}}}
	if err := good.Validate(); err != nil {
		t.Errorf("Validate rejected a legal list: %v", err)
	}

	if err := (TransformList{Transform: good.Transform}).Validate(); err == nil {
		t.Error("a list with no target was accepted")
	}
	if err := (TransformList{Target: "t.json"}).Validate(); err == nil {
		t.Error("an empty list was accepted")
	} else if he, _ := hewerr.As(err); !strings.Contains(he.Detail, "empty") {
		t.Errorf("detail = %q", he.Detail)
	}
	for _, f := range []FormatID{FormatJSON, FormatJSONC, FormatYAML, FormatTOML, FormatHCL, FormatMarkdown, ""} {
		tl := good
		tl.Format = f
		if err := tl.Validate(); err != nil {
			t.Errorf("format %q rejected: %v", f, err)
		}
	}
	tl := good
	tl.Format = "xml"
	err := tl.Validate()
	he, ok := hewerr.As(err)
	if !ok || he.Code != hewerr.CodeUnsupportedFormat {
		t.Errorf("unknown format = %v, want HEW021", err)
	} else if he.Target != "t.json" {
		t.Errorf("Target = %q, want the declared target", he.Target)
	}

	// A bad record is reported with its index and the list's target.
	broken := TransformList{Target: "t.json", Transform: []Transform{
		{Op: OpRemove, Path: MustParsePath("/a")},
		{Op: OpTest, Path: MustParsePath("/b")},
	}}
	err = broken.Validate()
	he, ok = hewerr.As(err)
	if !ok {
		t.Fatalf("want a *hewerr.Error, got %v", err)
	}
	if !strings.Contains(he.Detail, "transform 1:") {
		t.Errorf("detail %q does not name the failing record's index", he.Detail)
	}
	if he.Target != "t.json" {
		t.Errorf("Target = %q, want t.json", he.Target)
	}
	if he.Path != "/b" {
		t.Errorf("Path = %q, want /b", he.Path)
	}
}

func TestMarshalValidates(t *testing.T) {
	// The codec never writes an invalid list.
	if _, err := MarshalTransforms(TransformList{Target: "t.json"}); err == nil {
		t.Error("MarshalTransforms wrote an empty list")
	}
	if _, err := MarshalTransforms(TransformList{Target: "t.json", Transform: []Transform{
		{Op: OpTest, Path: MustParsePath("/a")},
	}}); err == nil {
		t.Error("MarshalTransforms wrote a test with no assertion mode")
	}
}

func TestOpKindValid(t *testing.T) {
	for _, op := range []OpKind{OpTest, OpAdd, OpRemove, OpReplace, OpCopy} {
		if !op.Valid() {
			t.Errorf("%s is not Valid", op)
		}
	}
	for _, op := range []OpKind{"", "move", "MOVE", "test "} {
		if op.Valid() {
			t.Errorf("%q reports Valid", op)
		}
	}
}

func TestNodeKindValid(t *testing.T) {
	for _, k := range []NodeKind{KindMap, KindSeq, KindScalar, KindBlock, KindSection} {
		if !k.Valid() {
			t.Errorf("%s is not Valid", k)
		}
	}
	for _, k := range []NodeKind{"", "object", "array", "string"} {
		if k.Valid() {
			t.Errorf("%q reports Valid", k)
		}
	}
}

func TestFormatIDValid(t *testing.T) {
	for _, f := range []FormatID{FormatJSON, FormatJSONC, FormatYAML, FormatTOML, FormatHCL, FormatMarkdown} {
		if !f.Valid() {
			t.Errorf("%s is not Valid", f)
		}
	}
	for _, f := range []FormatID{"", "xml", "ini", "JSON", "yml"} {
		if f.Valid() {
			t.Errorf("%q reports Valid", f)
		}
	}
}

func TestTransformEqual(t *testing.T) {
	base := Transform{Op: OpAdd, Path: MustParsePath("/a"), Value: val(t, 1), After: MustParsePath("/b")}
	same := base
	if !base.Equal(same) {
		t.Error("identical records compared unequal")
	}
	// PatchLine is provenance, not content.
	same.PatchLine = 42
	if !base.Equal(same) {
		t.Error("PatchLine participated in equality")
	}
	kind := KindMap
	mutations := []func(*Transform){
		func(x *Transform) { x.Op = OpReplace },
		func(x *Transform) { x.Path = MustParsePath("/z") },
		func(x *Transform) { x.From = MustParsePath("/z") },
		func(x *Transform) { x.Value = val(t, 2) },
		func(x *Transform) { x.Value = Value{} },
		func(x *Transform) { x.Before = MustParsePath("/z") },
		func(x *Transform) { x.After = Path{} },
		func(x *Transform) { x.Absent = true },
		func(x *Transform) { x.Count = ptr(1) },
		func(x *Transform) { x.NodeKind = &kind },
		func(x *Transform) { x.OnConflict = ConflictKeep },
		func(x *Transform) { x.Anchor = AnchorFork },
		func(x *Transform) { x.Surface = SurfaceTable },
		func(x *Transform) { x.Optional = true },
		func(x *Transform) { x.Idempotent = true },
	}
	for i, mutate := range mutations {
		other := base
		mutate(&other)
		if base.Equal(other) || other.Equal(base) {
			t.Errorf("mutation %d did not break equality: %+v", i, other)
		}
	}
	// Count and NodeKind compare by value, not by pointer.
	a := Transform{Op: OpTest, Path: MustParsePath("/a"), Count: ptr(3)}
	b := Transform{Op: OpTest, Path: MustParsePath("/a"), Count: ptr(3)}
	if !a.Equal(b) {
		t.Error("equal counts compared unequal")
	}
	if a.Equal(Transform{Op: OpTest, Path: MustParsePath("/a"), Count: ptr(4)}) {
		t.Error("different counts compared equal")
	}
	k1, k2, k3 := KindMap, KindMap, KindSeq
	c := Transform{Op: OpTest, Path: MustParsePath("/a"), NodeKind: &k1}
	if !c.Equal(Transform{Op: OpTest, Path: MustParsePath("/a"), NodeKind: &k2}) {
		t.Error("equal kinds compared unequal")
	}
	if c.Equal(Transform{Op: OpTest, Path: MustParsePath("/a"), NodeKind: &k3}) {
		t.Error("different kinds compared equal")
	}
	if c.Equal(Transform{Op: OpTest, Path: MustParsePath("/a")}) {
		t.Error("a kind compared equal to no kind")
	}
}

func TestTransformListEqual(t *testing.T) {
	base := TransformList{Target: "t.json", Format: FormatJSON, Transform: []Transform{
		{Op: OpRemove, Path: MustParsePath("/a")},
	}}
	if !base.Equal(base) {
		t.Error("a list is not equal to itself")
	}
	for i, mutate := range []func(*TransformList){
		func(x *TransformList) { x.Target = "u.json" },
		func(x *TransformList) { x.Format = FormatYAML },
		func(x *TransformList) { x.Transform = nil },
		func(x *TransformList) {
			x.Transform = []Transform{{Op: OpRemove, Path: MustParsePath("/b")}}
		},
		func(x *TransformList) {
			x.Transform = append(append([]Transform(nil), x.Transform...), Transform{Op: OpRemove, Path: MustParsePath("/b")})
		},
	} {
		other := base
		mutate(&other)
		if base.Equal(other) || other.Equal(base) {
			t.Errorf("mutation %d did not break list equality", i)
		}
	}
}
