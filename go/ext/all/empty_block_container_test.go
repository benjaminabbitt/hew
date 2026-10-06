package all

import (
	"testing"

	hew "github.com/benjaminabbitt/hew/go"
)

// An EMPTY container written across lines has no child whose indentation a new
// child can copy, so the indentation comes from the line the container OPENS
// on: its leading whitespace is where the closer goes back to, and one of the
// file's own indent steps deeper is where the child goes. The rest of that
// line (`"a": `) is content, not indentation — copying it wrote the key a
// second and third time and returned corrupt JSON with no error.
var emptyBlockCases = []struct{ name, format, doc, path, want string }{
	{"json/array-top", "json", "{\"a\": [\n  ]}", "/a/-", "{\"a\": [\n  9\n]}"},
	{"json/array-depth2", "json",
		"{\n  \"o\": {\n    \"a\": [\n    ]\n  }\n}\n", "/o/a/-",
		"{\n  \"o\": {\n    \"a\": [\n      9\n    ]\n  }\n}\n"},
	{"json/array-tabs", "json", "{\n\t\"a\": [\n\t]\n}\n", "/a/-", "{\n\t\"a\": [\n\t\t9\n\t]\n}\n"},
	{"json/object", "json", "{\n  \"o\": {\n  }\n}\n", "/o/k", "{\n  \"o\": {\n    \"k\": 9\n  }\n}\n"},
	{"json/object-depth2-4space", "json",
		"{\n    \"o\": {\n        \"p\": {\n        }\n    }\n}\n", "/o/p/k",
		"{\n    \"o\": {\n        \"p\": {\n            \"k\": 9\n        }\n    }\n}\n"},
	{"jsonc/array-top", "jsonc", "{\"a\": [\n  ]}", "/a/-", "{\"a\": [\n  9\n]}"},
	{"jsonc/array-depth2", "jsonc",
		"{\n  // c\n  \"o\": {\n    \"a\": [\n    ]\n  }\n}\n", "/o/a/-",
		"{\n  // c\n  \"o\": {\n    \"a\": [\n      9\n    ]\n  }\n}\n"},
	{"jsonc/array-tabs", "jsonc", "{\n\t\"a\": [\n\t]\n}\n", "/a/-", "{\n\t\"a\": [\n\t\t9\n\t]\n}\n"},
	{"jsonc/object", "jsonc", "{\n  \"o\": {\n  }\n}\n", "/o/k", "{\n  \"o\": {\n    \"k\": 9\n  }\n}\n"},
	{"jsonc/object-depth2-4space", "jsonc",
		"{\n    \"o\": {\n        \"p\": {\n        }\n    }\n}\n", "/o/p/k",
		"{\n    \"o\": {\n        \"p\": {\n            \"k\": 9\n        }\n    }\n}\n"},
	{"toml/array-top", "toml", "a = [\n]\n", "/a/-", "a = [\n  9\n]\n"},
	{"toml/array-in-table", "toml", "[o]\na = [\n  ]\n", "/o/a/-", "[o]\na = [\n  9\n]\n"},
	{"toml/array-indented", "toml", "[o]\n    a = [\n    ]\n", "/o/a/-", "[o]\n    a = [\n        9\n    ]\n"},
	// A TOML comment is not an element: the array is empty, and the comment
	// stays, ahead of the new element.
	{"toml/array-with-comment", "toml", "a = [\n  # c\n]\n", "/a/-", "a = [\n  # c\n  9\n]\n"},
	{"toml/inline-table", "toml", "o = {\n}\n", "/o/k", "o = {\n  k = 9\n}\n"},
	// YAML expands an empty flow collection into block style under its key
	// (TestInsertFlow's contract), so the multi-line spelling changes nothing.
	{"yaml/flow-seq", "yaml", "o:\n  a: [\n  ]\n", "/o/a/-", "o:\n  a:\n    - 9\n"},
	{"yaml/flow-map", "yaml", "o: {\n  }\n", "/o/k", "o:\n  k: 9\n"},
}

func TestAddIntoAnEmptyBlockContainer(t *testing.T) {
	for _, c := range emptyBlockCases {
		t.Run(c.name, func(t *testing.T) {
			v, err := hew.ValueOf(9)
			if err != nil {
				t.Fatal(err)
			}
			out, aerr := applyOne(t, c.format, c.doc, hew.Transform{Op: hew.OpAdd, Path: hew.MustParsePath(c.path), Value: v})
			if aerr != nil {
				t.Fatalf("add: %v", aerr)
			}
			if got := string(out); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
			if _, err := hew.OpenBytes("t", out, hew.As(hew.FormatID(c.format))); err != nil {
				t.Fatalf("output does not parse: %v", err)
			}
		})
	}
}

// The shape this was found through: the document API's Add on a key holding
// an empty multi-line array.
func TestDocAddIntoAnEmptyBlockArray(t *testing.T) {
	d, err := hew.OpenBytes("x.json", []byte("{\"a\": [\n  ]}"), hew.As("json"))
	if err != nil {
		t.Fatal(err)
	}
	d.AtPath(hew.NewPath().Append(hew.Key("a").(hew.Segment))).Add(map[string]any{"y": 2})
	out, err := d.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	if got, want := string(out), "{\"a\": [\n  { \"y\": 2 }\n]}"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
