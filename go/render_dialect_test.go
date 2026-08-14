package hew

import (
	"errors"
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
	got := renderNative(t, FormatMarkdown,
		Transform{Op: OpTest, Path: MustParsePath("/a/k"), Value: mustYAML(t, "v")},
	)
	if !strings.Contains(got, "  k: v") {
		t.Fatalf("got:\n%s", got)
	}
}

// TOML and HCL write `key = value`, and a string is always quoted: neither
// grammar has a bare-string spelling (§8.4, §8.5).
func TestRenderNativeEqDialects(t *testing.T) {
	for _, format := range []FormatID{FormatTOML, FormatHCL} {
		got := renderNative(t, format,
			Transform{Op: OpTest, Path: MustParsePath("/a/k"), Value: mustYAML(t, "v")},
			Transform{Op: OpTest, Path: MustParsePath("/a/n"), Value: mustYAML(t, "30")},
		)
		if !strings.Contains(got, `  k = "v"`) || !strings.Contains(got, "  n = 30") {
			t.Fatalf("%s:\n%s", format, got)
		}
	}
}

// §8.5's alignment rule, applied to a rendered hunk: the `=` signs of a run of
// member lines line up one column past the widest name, whatever margin each
// line carries. TOML does not align — its emitter writes one space.
func TestRenderNativeHCLAligns(t *testing.T) {
	ts := []Transform{
		{Op: OpTest, Path: MustParsePath("/p/project"), Value: mustYAML(t, "old")},
		{Op: OpAdd, Path: MustParsePath("/p/region"), After: MustParsePath("/p/project"), Value: mustYAML(t, "us")},
	}
	got := renderNative(t, FormatHCL, ts...)
	if !strings.Contains(got, `  project = "old"`) || !strings.Contains(got, `+ region  = "us"`) {
		t.Fatalf("hcl alignment:\n%s", got)
	}
	if strings.ContainsRune(got, 0) {
		t.Fatalf("alignment placeholder leaked into the output:\n%q", got)
	}
	toml := renderNative(t, FormatTOML, ts...)
	if !strings.Contains(toml, `+ region = "us"`) {
		t.Fatalf("toml must not align:\n%s", toml)
	}
}

// A line that is not a member breaks the alignment run, exactly as it does in
// the document: a comment between two attributes leaves each side to its own
// width.
func TestRenderNativeHCLAlignmentRunsBreak(t *testing.T) {
	got := renderNative(t, FormatHCL,
		Transform{Op: OpTest, Path: MustParsePath("/p/a"), Value: mustYAML(t, "1")},
		Transform{Op: OpTest, Path: MustParsePath("/p/#0"), Value: CommentValue("note")},
		Transform{Op: OpTest, Path: MustParsePath("/p/bbbb"), Value: mustYAML(t, "2")},
	)
	for _, want := range []string{"  a = 1", "  # note", "  bbbb = 2"} {
		if !strings.Contains(got, want) {
			t.Fatalf("want %q in:\n%s", want, got)
		}
	}
}

// A label is half of a block's address, and the mirror grammar has no body
// line that spells one. Appendix C's rule is to say so, not to write a line
// that would read as something else (§9.4-R6).
func TestRenderRefusesALabelBodyLine(t *testing.T) {
	ts := []Transform{
		{Op: OpTest, Path: MustParsePath(`/provider/"b"`), Value: mustYAML(t, "{k: 1}")},
		{Op: OpRemove, Path: MustParsePath(`/provider/"b"`)},
	}
	_, err := Render(TransformList{Target: "t.tf", Format: FormatHCL, Transform: ts},
		RenderOptions{Fragment: FragmentNative})
	if !errors.Is(err, ErrInexpressible) {
		t.Fatalf("want ErrInexpressible, got %v", err)
	}
	if !strings.Contains(err.Error(), `/provider/"b"`) {
		t.Fatalf("diagnostic does not name the address: %v", err)
	}
}

// A label in the ANCHOR is fine — that is where hcl/roundtrip-basic puts one.
func TestRenderAcceptsALabelInTheAnchor(t *testing.T) {
	got := renderNative(t, FormatHCL,
		Transform{Op: OpTest, Path: MustParsePath(`/provider/"google"/project`), Value: mustYAML(t, "p")},
	)
	if !strings.Contains(got, `@@ /provider/"google" @@`) || !strings.Contains(got, `  project = "p"`) {
		t.Fatalf("got:\n%s", got)
	}
}

func TestDialectFor(t *testing.T) {
	cases := []struct {
		style                       FragmentStyle
		format                      FormatID
		json, block, eq, quote, aln bool
		native                      bool
		marker                      string
	}{
		{FragmentNeutral, FormatJSON, false, false, false, false, false, false, "# "},
		{FragmentNative, FormatJSON, true, false, false, false, false, true, "// "},
		{FragmentNative, FormatJSONC, true, false, false, false, false, true, "// "},
		{FragmentNative, FormatYAML, false, true, false, false, false, true, "# "},
		{FragmentNative, FormatTOML, false, false, true, true, false, true, "# "},
		{FragmentNative, FormatHCL, false, false, true, true, true, true, "# "},
		{FragmentNative, FormatMarkdown, false, false, false, false, false, true, "# "},
	}
	for _, c := range cases {
		d := dialectFor(c.style, c.format)
		if d.json != c.json || d.block != c.block || d.native != c.native || d.marker != c.marker ||
			d.eq != c.eq || d.quote != c.quote || d.align != c.aln {
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

// A `before:` placement puts the added line ahead of the sibling it names —
// the prepend shape §9.1 step 5 derives when there is no preceding sibling.
func TestRenderPlacesAnAddBeforeItsSibling(t *testing.T) {
	got := renderNative(t, FormatYAML,
		Transform{Op: OpTest, Path: MustParsePath("/tags/=alpha"), Value: mustYAML(t, "alpha")},
		Transform{Op: OpAdd, Path: MustParsePath("/tags"), Value: mustYAML(t, "aardvark"),
			Before: MustParsePath("/tags/=alpha")},
	)
	if !strings.Contains(got, "+ - aardvark\n  - alpha") {
		t.Fatalf("got:\n%s", got)
	}
}

// Two adds naming the same sibling keep their authored order rather than
// stacking up in reverse.
func TestRenderChainsSeveralAddsAtOneSibling(t *testing.T) {
	got := renderNative(t, FormatJSON,
		Transform{Op: OpTest, Path: MustParsePath("/a/k"), Value: mustYAML(t, "1")},
		Transform{Op: OpAdd, Path: MustParsePath("/a/x"), Value: mustYAML(t, "1"), After: MustParsePath("/a/k")},
		Transform{Op: OpAdd, Path: MustParsePath("/a/y"), Value: mustYAML(t, "2"), After: MustParsePath("/a/k")},
	)
	if !strings.Contains(got, `+ "x": 1`+"\n"+`+ "y": 2`) {
		t.Fatalf("got:\n%s", got)
	}
	before := renderNative(t, FormatJSON,
		Transform{Op: OpTest, Path: MustParsePath("/a/k"), Value: mustYAML(t, "1")},
		Transform{Op: OpAdd, Path: MustParsePath("/a/x"), Value: mustYAML(t, "1"), Before: MustParsePath("/a/k")},
		Transform{Op: OpAdd, Path: MustParsePath("/a/y"), Value: mustYAML(t, "2"), Before: MustParsePath("/a/k")},
	)
	if !strings.Contains(before, `+ "x": 1`+"\n"+`+ "y": 2`+"\n"+`  "k": 1`) {
		t.Fatalf("got:\n%s", before)
	}
}

// A comment address whose value is not the `{comment: …}` wrapper still
// renders as a comment line, falling back to the value's own text rather than
// printing the wrapper.
func TestRenderCommentWithABareValue(t *testing.T) {
	got := renderNative(t, FormatYAML,
		Transform{Op: OpTest, Path: MustParsePath("/a/#0"), Value: mustYAML(t, "bare text")},
	)
	if !strings.Contains(got, "  # bare text") {
		t.Fatalf("got:\n%s", got)
	}
}

func TestRenderQuotesATargetWithASpace(t *testing.T) {
	out, err := Render(TransformList{Target: "my file.yaml", Format: FormatYAML,
		Transform: []Transform{{Op: OpTest, Path: MustParsePath("/a/k"), Value: mustYAML(t, "1")}}},
		RenderOptions{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(string(out), `--- "my file.yaml" format=yaml`) {
		t.Fatalf("got:\n%s", out)
	}
}
