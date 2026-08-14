package hewdiff

import (
	"testing"

	"github.com/hew-format/hew"
	"github.com/hew-format/hew/hewjson"
	"github.com/hew-format/hew/hewjsonc"
	"github.com/hew-format/hew/hewyaml"
)

func apply(target []byte, tl hew.TransformList) ([]byte, error) {
	switch tl.Format {
	case hew.FormatJSON:
		return hewjson.Apply(target, tl)
	case hew.FormatJSONC:
		return hewjsonc.Apply(target, tl)
	default:
		return hewyaml.Apply(target, tl)
	}
}

// RT1, the round-trip identity of §13.5: apply(parse(render(diff(old,new))), old)
// must be byte-identical to new. It is the one assertion that ties all four
// components together, and it is the reason a differ that emits a plausible
// but unapplicable address — a placement naming a node that does not exist
// yet, an identity segment that resolves to the wrong element — cannot pass.
func TestRoundTripIdentity(t *testing.T) {
	cases := []struct {
		name     string
		format   hew.FormatID
		old, new string
	}{
		{"scalar edit", hew.FormatYAML,
			"server:\n  port: 8080\n  timeout: 30\n",
			"server:\n  port: 8080\n  timeout: 60\n"},
		{"key removal", hew.FormatYAML,
			"server:\n  host: localhost\n  port: 8080\n",
			"server:\n  port: 8080\n"},
		{"append", hew.FormatYAML,
			"tags:\n  - alpha\n  - beta\n",
			"tags:\n  - alpha\n  - beta\n  - gamma\n"},
		{"prepend", hew.FormatYAML,
			"tags:\n  - alpha\n",
			"tags:\n  - aardvark\n  - alpha\n"},
		{"two prepends", hew.FormatYAML,
			"tags:\n  - alpha\n",
			"tags:\n  - a1\n  - a2\n  - alpha\n"},
		{"two appends", hew.FormatYAML,
			"tags:\n  - alpha\n",
			"tags:\n  - alpha\n  - b\n  - c\n"},
		{"three appends", hew.FormatYAML,
			"tags:\n  - alpha\n",
			"tags:\n  - alpha\n  - b\n  - c\n  - d\n"},
		{"two members appended", hew.FormatYAML,
			"server:\n  port: 8080\n",
			"server:\n  port: 8080\n  timeout: 30\n  tls: true\n"},
		{"two keyed elements appended", hew.FormatYAML,
			"m:\n  - name: a\n    command: x\n",
			"m:\n  - name: a\n    command: x\n  - name: b\n    command: y\n  - name: c\n    command: z\n"},
		{"insert middle", hew.FormatYAML,
			"tags:\n  - alpha\n  - beta\n",
			"tags:\n  - alpha\n  - alpha2\n  - beta\n"},
		{"keyed element added", hew.FormatYAML,
			"m:\n  - name: a\n    command: x\n",
			"m:\n  - name: a\n    command: x\n  - name: b\n    command: y\n"},
		{"keyed element removed", hew.FormatYAML,
			"m:\n  - name: a\n    command: x\n  - name: b\n    command: y\n",
			"m:\n  - name: b\n    command: y\n"},
		{"keyed element inner edit", hew.FormatYAML,
			"m:\n  - name: a\n    command: x\n  - name: b\n    command: y\n",
			"m:\n  - name: a\n    command: x\n  - name: b\n    command: z\n"},
		{"deep nesting", hew.FormatYAML,
			"a:\n  b:\n    c:\n      d: 1\n",
			"a:\n  b:\n    c:\n      d: 2\n"},
		{"several containers at once", hew.FormatYAML,
			"one:\n  k: 1\ntwo:\n  k: 1\nthree:\n  k: 1\n",
			"one:\n  k: 2\ntwo:\n  k: 1\nthree:\n  k: 3\n"},
		{"member added at the end", hew.FormatJSON,
			"{\n  \"a\": 1\n}\n",
			"{\n  \"a\": 1,\n  \"b\": 2\n}\n"},
		{"member added at the front", hew.FormatJSON,
			"{\n  \"b\": 2\n}\n",
			"{\n  \"a\": 1,\n  \"b\": 2\n}\n"},
		{"two members appended", hew.FormatJSON,
			"{\n  \"a\": 1\n}\n",
			"{\n  \"a\": 1,\n  \"b\": 2,\n  \"c\": 3\n}\n"},
		{"two elements appended", hew.FormatJSON,
			"{\n  \"tags\": [\n    \"a\"\n  ]\n}\n",
			"{\n  \"tags\": [\n    \"a\",\n    \"b\",\n    \"c\"\n  ]\n}\n"},
		{"nested array of scalars", hew.FormatJSON,
			"{\n  \"tags\": [\"a\", \"b\"]\n}\n",
			"{\n  \"tags\": [\"a\", \"b\", \"c\"]\n}\n"},
		{"value kind change", hew.FormatJSON,
			"{\n  \"k\": {\"deep\": 1}\n}\n",
			"{\n  \"k\": \"flat\"\n}\n"},
		{"two members appended", hew.FormatJSONC,
			"{\n  \"a\": 1\n}\n",
			"{\n  \"a\": 1,\n  \"b\": 2,\n  \"c\": 3\n}\n"},
		{"comment added with its member", hew.FormatJSONC,
			"{\n  \"server\": {\n    \"timeout\": 30\n  }\n}\n",
			"{\n  \"server\": {\n    \"timeout\": 60,\n    // slow upstream\n    \"retries\": 5\n  }\n}\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			target := "t." + string(c.format)
			tl, err := Diff([]byte(c.old), []byte(c.new), c.format, hew.DiffOptions{Target: target})
			if err != nil {
				t.Fatalf("diff: %v", err)
			}
			if len(tl.Transform) == 0 {
				t.Fatal("the documents differ, so the patch must not be empty")
			}
			text, err := hew.Render(tl, hew.RenderOptions{Preamble: true, Fragment: hew.FragmentNative})
			if err != nil {
				t.Fatalf("render: %v\n%+v", err, tl)
			}
			reparsed, err := hew.Parse(text)
			if err != nil {
				t.Fatalf("re-parse: %v\n%s", err, text)
			}
			if len(reparsed) != 1 {
				t.Fatalf("want one file section, got %d", len(reparsed))
			}
			reparsed[0].Format = c.format
			got, err := apply([]byte(c.old), reparsed[0])
			if err != nil {
				t.Fatalf("apply: %v\n%s", err, text)
			}
			if string(got) != c.new {
				t.Fatalf("RT1 violated\n--- patch\n%s\n--- got\n%q\n--- want\n%q", text, got, c.new)
			}
		})
	}
}
