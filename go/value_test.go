package hew

import "testing"

func TestValueIsZero(t *testing.T) {
	var v Value
	if !v.IsZero() {
		t.Fatal("zero Value must report IsZero")
	}
	nv, err := ValueOf(nil)
	if err != nil {
		t.Fatalf("ValueOf(nil): %v", err)
	}
	if nv.IsZero() {
		t.Fatal("an explicit null must not be the zero (absent) value")
	}
}

func TestValueEqual(t *testing.T) {
	a, _ := ValueOf(8080)
	b, _ := ValueOf(8080)
	c, _ := ValueOf("8080")
	if !a.Equal(b) {
		t.Fatal("8080 should equal 8080")
	}
	if a.Equal(c) {
		t.Fatal("the number 8080 must not equal the string \"8080\" (§4.2)")
	}
}

func TestValueEqualIgnoresPresentation(t *testing.T) {
	src1, err := UnmarshalTransforms([]byte("hew-transforms: 1\ntarget: t\ntransforms:\n- {op: test, path: /a, value: {x: 1, y: 2}}\n"))
	if err != nil {
		t.Fatalf("unmarshal 1: %v", err)
	}
	src2, err := UnmarshalTransforms([]byte("hew-transforms: 1\ntarget: t\ntransforms:\n- op: test\n  path: /a\n  value:\n    x: 1\n    y: 2\n"))
	if err != nil {
		t.Fatalf("unmarshal 2: %v", err)
	}
	if !src1.Transform[0].Value.Equal(src2.Transform[0].Value) {
		t.Fatal("flow vs block style must compare equal (§6.3)")
	}
}

func TestValueDecode(t *testing.T) {
	v, err := ValueOf(map[string]any{"a": 1})
	if err != nil {
		t.Fatalf("ValueOf: %v", err)
	}
	var out map[string]int
	if err := v.Decode(&out); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if out["a"] != 1 {
		t.Fatalf("Decode: got %v", out)
	}
}
