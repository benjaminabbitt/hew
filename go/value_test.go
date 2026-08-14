package hew

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func decodeValue(t *testing.T, src string) Value {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(src), &n); err != nil {
		t.Fatal(err)
	}
	return NodeValue(cloneNode(n.Content[0]))
}

func TestValueZero(t *testing.T) {
	var v Value
	if !v.IsZero() {
		t.Error("the zero Value is not IsZero")
	}
	if v.Node() != nil {
		t.Error("the zero Value has a node")
	}
	if v.String() != "" {
		t.Errorf("the zero Value prints %q", v.String())
	}
	if err := v.Decode(new(int)); err != nil {
		t.Errorf("Decode on the zero Value = %v", err)
	}
	if !v.Equal(Value{}) {
		t.Error("two zero Values compared unequal")
	}
	if v.Equal(decodeValue(t, "null")) {
		t.Error("the absent value equals an explicit null")
	}
}

func TestValueEqualIgnoresPresentation(t *testing.T) {
	same := [][2]string{
		{"[1, 2]", "- 1\n- 2\n"},         // flow vs block sequence
		{"{a: 1}", "a: 1\n"},             // flow vs block mapping
		{`"text"`, "'text'"},             // quoting style
		{"a: 1 # trailing\n", "a: 1\n"},  // comments
		{"a: 1\nb: 2\n", "a: 1\nb: 2\n"}, //
		{"key: value", "key: value\n"},   //
	}
	for _, pair := range same {
		a, b := decodeValue(t, pair[0]), decodeValue(t, pair[1])
		if !a.Equal(b) || !b.Equal(a) {
			t.Errorf("%q and %q compared unequal", pair[0], pair[1])
		}
	}
	differ := [][2]string{
		{"8080", `"8080"`},               // number vs string is the §4.2 distinction
		{"true", `"true"`},               //
		{"null", `"null"`},               //
		{"a: 1\nb: 2\n", "b: 2\na: 1\n"}, // mapping key ORDER is content here
		{"[1, 2]", "[2, 1]"},             //
		{"[1, 2]", "[1, 2, 3]"},          //
		{"{a: 1}", "{a: 2}"},             //
		{"{a: 1}", "{b: 1}"},             //
		{"1", "1.0"},                     //
		{"[1]", "1"},                     //
	}
	for _, pair := range differ {
		a, b := decodeValue(t, pair[0]), decodeValue(t, pair[1])
		if a.Equal(b) || b.Equal(a) {
			t.Errorf("%q and %q compared equal", pair[0], pair[1])
		}
	}
}

func TestValueOfAndDecode(t *testing.T) {
	v, err := ValueOf(map[string]any{"a": 1})
	if err != nil {
		t.Fatal(err)
	}
	if v.IsZero() {
		t.Fatal("ValueOf produced the absent value")
	}
	var out map[string]int
	if err := v.Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out["a"] != 1 {
		t.Errorf("decoded %v", out)
	}
	if _, err := ValueOf(func() {}); err == nil {
		t.Error("ValueOf accepted an unencodable value")
	}
}

func TestValueString(t *testing.T) {
	cases := [][2]string{
		{"30", "30"},
		{"localhost", "localhost"},
		{"a: 1\nb: 2\n", "{a: 1, b: 2}"},
		{"- 1\n- 2\n", "[1, 2]"},
		{"a: 1 # note\n", "{a: 1}"},
	}
	for _, tc := range cases {
		if got := decodeValue(t, tc[0]).String(); got != tc[1] {
			t.Errorf("String of %q = %q, want %q", tc[0], got, tc[1])
		}
	}
}

func TestCloneNodeIsDeep(t *testing.T) {
	orig := decodeValue(t, "a: [1, 2]\n")
	clone := NodeValue(cloneNode(orig.Node()))
	clone.Node().Content[1].Content[0].Value = "99"
	if orig.Node().Content[1].Content[0].Value != "1" {
		t.Error("cloneNode aliased the source tree")
	}
	if cloneNode(nil) != nil {
		t.Error("cloneNode(nil) is not nil")
	}
}
