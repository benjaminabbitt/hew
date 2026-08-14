package hewdiff

import (
	"testing"

	"github.com/benjaminabbitt/hew"
	"github.com/benjaminabbitt/hew/hewhcl"
	"github.com/benjaminabbitt/hew/hewjson"
	"github.com/benjaminabbitt/hew/hewjsonc"
	"github.com/benjaminabbitt/hew/hewtoml"
	"github.com/benjaminabbitt/hew/hewyaml"
)

func apply(target []byte, tl hew.TransformList) ([]byte, error) {
	switch tl.Format {
	case hew.FormatJSON:
		return hewjson.Apply(target, tl)
	case hew.FormatJSONC:
		return hewjsonc.Apply(target, tl)
	case hew.FormatTOML:
		return hewtoml.Apply(target, tl)
	case hew.FormatHCL:
		return hewhcl.Apply(target, tl)
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

		// TOML. The surface a member is written at is the document's, never the
		// differ's (§8.4 rule 1), so the same edit has to survive at all three
		// of them: a root-level line, a dotted key, and a `[table]` body.
		{"scalar edit in a table", hew.FormatTOML,
			"[server]\nport = 8080\ntimeout = 30\n",
			"[server]\nport = 8080\ntimeout = 60\n"},
		{"scalar edit at the root", hew.FormatTOML,
			"title = \"old\"\n[server]\nport = 8080\n",
			"title = \"new\"\n[server]\nport = 8080\n"},
		{"dotted key edit", hew.FormatTOML,
			"tool.ctxloom.timeout = 30\n",
			"tool.ctxloom.timeout = 60\n"},
		{"member appended to a table", hew.FormatTOML,
			"[server]\nport = 8080\n",
			"[server]\nport = 8080\ntls = true\n"},
		{"two members appended to a table", hew.FormatTOML,
			"[server]\nport = 8080\n",
			"[server]\nport = 8080\ntls = true\ntimeout = 30\n"},
		{"member removed from a table", hew.FormatTOML,
			"[server]\nhost = \"localhost\"\nport = 8080\n",
			"[server]\nport = 8080\n"},
		{"string value edit", hew.FormatTOML,
			"[hooks]\non_start = \"old\"\n",
			"[hooks]\non_start = \"new\"\n"},
		{"inline array element appended", hew.FormatTOML,
			"[tool]\nargs = [\"a\", \"b\"]\n",
			"[tool]\nargs = [\"a\", \"b\", \"c\"]\n"},
		{"array-of-tables inner edit", hew.FormatTOML,
			"[[plugin]]\nname = \"beta\"\nenabled = false\n",
			"[[plugin]]\nname = \"beta\"\nenabled = true\n"},
		{"edits in two tables at once", hew.FormatTOML,
			"[a]\nk = 1\n\n[b]\nk = 1\n",
			"[a]\nk = 2\n\n[b]\nk = 3\n"},

		// HCL. A block is addressed by its `(type, labels)` tuple (§4.3), and
		// an attribute's expression is a leaf compared as source text (§8.5).
		{"attribute edit in a block", hew.FormatHCL,
			"terraform {\n  required_version = \">= 1.6\"\n}\n",
			"terraform {\n  required_version = \">= 1.7\"\n}\n"},
		{"attribute edit in a labelled block", hew.FormatHCL,
			"provider \"google\" {\n  project = \"old\"\n}\n",
			"provider \"google\" {\n  project = \"new\"\n}\n"},
		{"attribute added to a labelled block", hew.FormatHCL,
			"provider \"google\" {\n  project = \"old-project\"\n}\n",
			"provider \"google\" {\n  project = \"old-project\"\n  region  = \"us-central1\"\n}\n"},
		{"attribute removed from a block", hew.FormatHCL,
			"provider \"google\" {\n  project = \"p\"\n  region  = \"r\"\n}\n",
			"provider \"google\" {\n  project = \"p\"\n}\n"},
		{"two-label block", hew.FormatHCL,
			"resource \"aws_instance\" \"web\" {\n  ami = \"old\"\n}\n",
			"resource \"aws_instance\" \"web\" {\n  ami = \"new\"\n}\n"},
		{"nested block attribute edit", hew.FormatHCL,
			"terraform {\n  backend \"s3\" {\n    bucket = \"old\"\n  }\n}\n",
			"terraform {\n  backend \"s3\" {\n    bucket = \"new\"\n  }\n}\n"},
		{"two blocks edited at once", hew.FormatHCL,
			"provider \"a\" {\n  k = \"1\"\n}\n\nprovider \"b\" {\n  k = \"1\"\n}\n",
			"provider \"a\" {\n  k = \"2\"\n}\n\nprovider \"b\" {\n  k = \"3\"\n}\n"},
		{"interpolated attribute edit", hew.FormatHCL,
			"locals {\n  x = \"${var.old}\"\n}\n",
			"locals {\n  x = \"${var.new}\"\n}\n"},
		{"standalone comment added", hew.FormatTOML,
			"[hooks]\non_start = \"x\"\n",
			"[hooks]\non_start = \"x\"\n# note\n"},
		{"array-of-tables element added", hew.FormatTOML,
			"[[plugin]]\nname = \"beta\"\n",
			"[[plugin]]\nname = \"beta\"\n\n[[plugin]]\nname = \"gamma\"\n"},
		{"two inline array elements appended", hew.FormatTOML,
			"[tool]\nargs = [\"a\"]\n",
			"[tool]\nargs = [\"a\", \"b\", \"c\"]\n"},
		{"two keyed inline elements appended", hew.FormatTOML,
			"[tool]\nargs = [{name = \"a\"}]\n",
			"[tool]\nargs = [{name = \"a\"}, {name = \"b\"}, {name = \"c\"}]\n"},
		{"two array-of-tables elements added", hew.FormatTOML,
			"[[plugin]]\nname = \"beta\"\n",
			"[[plugin]]\nname = \"beta\"\n\n[[plugin]]\nname = \"gamma\"\n\n[[plugin]]\nname = \"delta\"\n"},
		{"two attributes appended", hew.FormatHCL,
			"provider \"a\" {\n  k = \"1\"\n}\n",
			"provider \"a\" {\n  k = \"1\"\n  m = \"2\"\n  n = \"3\"\n}\n"},
		{"edit in the second block of a type", hew.FormatHCL,
			"provider \"a\" {\n  k = \"1\"\n}\n\nprovider \"b\" {\n  k = \"1\"\n}\n",
			"provider \"a\" {\n  k = \"1\"\n}\n\nprovider \"b\" {\n  k = \"2\"\n}\n"},
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
