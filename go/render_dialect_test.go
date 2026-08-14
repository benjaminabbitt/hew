package hew

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func renderNative(t *testing.T, format FormatID, ts ...Transform) string {
	t.Helper()
	out, err := Render(TransformList{Target: "t", Format: format, Transform: ts},
		RenderOptions{Fragment: FragmentNative})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return string(out)
}

func mustYAML(t *testing.T, src string) Value {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(src), &n); err != nil {
		t.Fatalf("yaml: %v", err)
	}
	return NodeValue(n.Content[0])
}

// §5 hands both hunk images to the target format's own fragment parser, so a
// JSON patch may wear JSON's clothes: quoted keys, quoted strings.
func TestRenderNativeJSONSpelling(t *testing.T) {
	got := renderNative(t, FormatJSON,
		Transform{Op: OpTest, Path: MustParsePath("/server/port"), Value: mustYAML(t, "8080")},
		Transform{Op: OpTest, Path: MustParsePath("/server/host"), Value: mustYAML(t, "localhost")},
		Transform{Op: OpRemove, Path: MustParsePath("/server/host")},
	)
	for _, want := range []string{`  "port": 8080`, `- "host": "localhost"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestRenderNativeJSONCollections(t *testing.T) {
	got := renderNative(t, FormatJSONC,
		Transform{Op: OpAdd, Path: MustParsePath("/a/obj"), Value: mustYAML(t, "{k: 1, s: [true, null]}")},
	)
	if !strings.Contains(got, `+ "obj": {"k": 1, "s": [true, null]}`) {
		t.Fatalf("got:\n%s", got)
	}
}

func TestRenderNativeJSONCComments(t *testing.T) {
	got := renderNative(t, FormatJSONC,
		Transform{Op: OpTest, Path: MustParsePath("/a/k"), Value: mustYAML(t, "1")},
		Transform{Op: OpAdd, Path: MustParsePath("/a/#0"), Value: CommentValue("slow upstream"), After: MustParsePath("/a/k")},
	)
	if !strings.Contains(got, "+ // slow upstream") {
		t.Fatalf("JSONC comments are // comments:\n%s", got)
	}
	yamlOut := renderNative(t, FormatYAML,
		Transform{Op: OpTest, Path: MustParsePath("/a/k"), Value: mustYAML(t, "1")},
		Transform{Op: OpAdd, Path: MustParsePath("/a/#0"), Value: CommentValue("note"), After: MustParsePath("/a/k")},
	)
	if !strings.Contains(yamlOut, "+ # note") {
		t.Fatalf("YAML comments are # comments:\n%s", yamlOut)
	}
}

// §9.4-R5: an added node keeps the new document's own shape. A block mapping
// stays a block mapping, one body line per source line, margin on each.
func TestRenderNativeYAMLBlockValue(t *testing.T) {
	got := renderNative(t, FormatYAML,
		Transform{Op: OpTest, Path: MustParsePath("/mcpServers/name=github/name"), Value: mustYAML(t, "github")},
		Transform{Op: OpAdd, Path: MustParsePath("/mcpServers"),
			Value: mustYAML(t, "name: ctxloom\ncommand: ctxloom\n"),
			After: MustParsePath("/mcpServers/name=github")},
	)
	if !strings.Contains(got, "+ - name: ctxloom\n+   command: ctxloom") {
		t.Fatalf("got:\n%s", got)
	}
}

func TestRenderNativeYAMLBlockUnderAKey(t *testing.T) {
	got := renderNative(t, FormatYAML,
		Transform{Op: OpAdd, Path: MustParsePath("/a/env"), Value: mustYAML(t, "GITHUB_TOKEN: x\n")},
	)
	if !strings.Contains(got, "+ env:\n+   GITHUB_TOKEN: x") {
		t.Fatalf("got:\n%s", got)
	}
}

// A flow collection in the source stays flow: R5 preserves what was written,
// it does not impose a house style.
func TestRenderNativeYAMLKeepsFlowStyle(t *testing.T) {
	got := renderNative(t, FormatYAML,
		Transform{Op: OpAdd, Path: MustParsePath("/a/args"), Value: NodeValue(mustYAML(t, "args: [-y, x]\n").Node().Content[1])},
	)
	if !strings.Contains(got, "+ args: [-y, x]") {
		t.Fatalf("got:\n%s", got)
	}
}

func TestRenderNativeYAMLSequenceScalars(t *testing.T) {
	got := renderNative(t, FormatYAML,
		Transform{Op: OpTest, Path: MustParsePath("/tags/=beta"), Value: mustYAML(t, "beta")},
		Transform{Op: OpAdd, Path: MustParsePath("/tags"), Value: mustYAML(t, "gamma"), After: MustParsePath("/tags/=beta")},
	)
	if !strings.Contains(got, "  - beta\n+ - gamma") {
		t.Fatalf("got:\n%s", got)
	}
}

// A keyed element's subset context line follows the dialect: block under a
// YAML dash, flow inside JSON braces.
func TestRenderNativeKeyedSubsetLines(t *testing.T) {
	fieldTest := Transform{Op: OpTest, Path: MustParsePath("/servers/name=github/name"), Value: mustYAML(t, "github")}
	if got := renderNative(t, FormatYAML, fieldTest); !strings.Contains(got, "  - name: github") {
		t.Fatalf("yaml:\n%s", got)
	}
	if got := renderNative(t, FormatJSON, fieldTest); !strings.Contains(got, `  {"name": "github"}`) {
		t.Fatalf("json:\n%s", got)
	}
}

// The neutral dialect is what a caller who names no fragment style gets, and
// it must keep spelling body lines exactly as it always has.
func TestRenderNeutralDialectIsUnchanged(t *testing.T) {
	ts := []Transform{
		{Op: OpTest, Path: MustParsePath("/server/host"), Value: mustYAML(t, "localhost")},
		{Op: OpRemove, Path: MustParsePath("/server/host")},
	}
	out, err := Render(TransformList{Target: "t.json", Format: FormatJSON, Transform: ts}, RenderOptions{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(string(out), "- host: localhost") {
		t.Fatalf("neutral spelling changed:\n%s", out)
	}
}

// A format with no dialect of its own falls back to the neutral spelling
// rather than guessing — a wrong guess would produce a fragment its parser
// cannot read.
func TestRenderNativeUnknownFormatFallsBack(t *testing.T) {
	got := renderNative(t, FormatTOML,
		Transform{Op: OpTest, Path: MustParsePath("/a/k"), Value: mustYAML(t, "v")},
	)
	if !strings.Contains(got, "  k: v") {
		t.Fatalf("got:\n%s", got)
	}
}

func TestDialectFor(t *testing.T) {
	cases := []struct {
		style       FragmentStyle
		format      FormatID
		json, block bool
		native      bool
		marker      string
	}{
		{FragmentNeutral, FormatJSON, false, false, false, "# "},
		{FragmentNative, FormatJSON, true, false, true, "// "},
		{FragmentNative, FormatJSONC, true, false, true, "// "},
		{FragmentNative, FormatYAML, false, true, true, "# "},
		{FragmentNative, FormatHCL, false, false, true, "# "},
	}
	for _, c := range cases {
		d := dialectFor(c.style, c.format)
		if d.json != c.json || d.block != c.block || d.native != c.native || d.marker != c.marker {
			t.Fatalf("dialectFor(%q,%q) = %+v", c.style, c.format, d)
		}
	}
}

func TestDialectKeySpelling(t *testing.T) {
	if got := dialectFor(FragmentNative, FormatJSON).key("a b"); got != `"a b"` {
		t.Fatalf("json key = %q", got)
	}
	if got := dialectFor(FragmentNative, FormatYAML).key("plain"); got != "plain" {
		t.Fatalf("yaml plain key = %q", got)
	}
	if got := dialectFor(FragmentNative, FormatYAML).key("needs: quoting"); got != `"needs: quoting"` {
		t.Fatalf("yaml awkward key = %q", got)
	}
	if got := dialectFor(FragmentNeutral, FormatYAML).key("a/b"); got != "a~1b" {
		t.Fatalf("neutral key = %q", got)
	}
}

func TestJSONText(t *testing.T) {
	cases := []struct{ in, want string }{
		{`"s"`, `"s"`},
		{`1`, `1`},
		{`1.5`, `1.5`},
		{`true`, `true`},
		{`null`, `null`},
		{`{}`, `{}`},
		{`[]`, `[]`},
		{`{a: 1, b: [1, "x"]}`, `{"a": 1, "b": [1, "x"]}`},
	}
	for _, c := range cases {
		if got := jsonText(mustYAML(t, c.in).Node()); got != c.want {
			t.Fatalf("jsonText(%s) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestYAMLScalarTextKeepsExplicitQuotes(t *testing.T) {
	quoted := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "8080", Style: yaml.DoubleQuotedStyle}
	if got := yamlScalarText(quoted); got != `"8080"` {
		t.Fatalf("got %q", got)
	}
	plain := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "plain"}
	if got := yamlScalarText(plain); got != "plain" {
		t.Fatalf("got %q", got)
	}
}

func TestBlockLinesStripsComments(t *testing.T) {
	n := mustYAML(t, "a: 1\n").Node()
	n.HeadComment = "head"
	n.Content[1].LineComment = "trailing"
	lines := blockLines(n)
	if len(lines) != 1 || lines[0] != "a: 1" {
		t.Fatalf("got %q", lines)
	}
}

func TestValueLinesNilValue(t *testing.T) {
	if got := dialectFor(FragmentNative, FormatYAML).valueLines(Value{}); len(got) != 1 || got[0] != "null" {
		t.Fatalf("got %q", got)
	}
}
