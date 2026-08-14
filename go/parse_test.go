package hew

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

// corpusDir resolves a corpus case's directory, walking up from the package
// directory to find the repo root — the same discovery the conformance
// harness uses.
func corpusDir(t *testing.T, rel string) string {
	t.Helper()
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		cand := filepath.Join(root, "corpus", rel)
		if st, err := os.Stat(cand); err == nil && st.IsDir() {
			return cand
		}
		parent := filepath.Dir(root)
		if parent == root {
			t.Fatalf("could not find corpus/%s above %s", rel, root)
		}
		root = parent
	}
}

// corpusCase loads patch.hew and (if present) transforms.hewt from a corpus
// case directory.
func corpusCase(t *testing.T, rel string) (patch, transforms []byte, dir string) {
	t.Helper()
	dir = corpusDir(t, rel)
	var err error
	patch, err = os.ReadFile(filepath.Join(dir, "patch.hew"))
	if err != nil {
		t.Fatalf("reading patch.hew: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "transforms.hewt")); err == nil {
		transforms = b
	}
	return patch, transforms, dir
}

func assertParsesTo(t *testing.T, rel string) {
	t.Helper()
	patch, fixture, _ := corpusCase(t, rel)
	if fixture == nil {
		t.Fatalf("%s has no transforms.hewt fixture", rel)
	}
	tls, err := Parse(patch)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(tls) != 1 {
		t.Fatalf("want 1 file section, got %d", len(tls))
	}
	want, err := UnmarshalTransforms(fixture)
	if err != nil {
		t.Fatalf("fixture UnmarshalTransforms: %v", err)
	}
	got := tls[0]
	if !got.Equal(want) {
		gotBytes, _ := MarshalTransforms(got)
		t.Fatalf("parsed IR != fixture (%s)\ngot:\n%s\nwant:\n%s", rel, gotBytes, fixture)
	}
}

// TestParseAgainstCorpusFixtures pins the parser against every corpus case
// that declares a parse seam AND a transforms.hewt: the pair IS the spec, and
// the families differ in exactly the fragment syntax §8 gives them.
func TestParseAgainstCorpusFixtures(t *testing.T) {
	cases := []string{
		"json/add-key",
		"json/set-scalar",
		"json/array-remove-element",
		"json/keyed-array-add",
		"jsonc/add-with-leading-comment",
		"yaml/set-scalar",
		"toml/surface-directive-table",
		"hcl/repeated-label-ordinal",
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) { assertParsesTo(t, c) })
	}
}

// preamble+target header shared by the lowering cases, so each case shows
// only the hunk that is under test.
const hdr = "hew: 1\n\n--- t.json format=json\n\n"

// lowered parses src and returns its single file section's transforms as
// .hewt text, with the document header stripped: the cases below are about
// the transform list, and §9.6's document keys are hewt_test.go's business.
func lowered(t *testing.T, src string) string {
	t.Helper()
	tls, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(tls) != 1 {
		t.Fatalf("Parse = %d file sections, want 1", len(tls))
	}
	b, err := MarshalTransforms(tls[0])
	if err != nil {
		t.Fatalf("MarshalTransforms: %v", err)
	}
	_, rest, ok := strings.Cut(string(b), "transforms:\n")
	if !ok {
		t.Fatalf("marshalled .hewt has no transforms key:\n%s", b)
	}
	return rest
}

// TestParseLowering pins §9.1: every context and `-` line becomes a test with
// its before-image value, `-`/`+` at the same position pair into a replace,
// an unpaired `+` becomes an add with a relative placement, and annotations
// ride the transforms they govern as qualifiers.
func TestParseLowering(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{{
		name: "context and removal compile to tests, the pair to a replace",
		body: "@@ /server @@\n" +
			`  "port": 8080` + "\n" +
			`- "timeout": 30` + "\n" +
			`+ "timeout": 60` + "\n",
		want: `  - op: test
    path: /server/port
    value: 8080
  - op: test
    path: /server/timeout
    value: 30
  - op: replace
    path: /server/timeout
    value: 60
`,
	}, {
		name: "an add takes the preceding context sibling as its position",
		body: "@@ /server @@\n  host: \"localhost\"\n+ tls: true\n  port: 8080\n",
		want: `  - op: test
    path: /server/host
    value: localhost
  - op: test
    path: /server/port
    value: 8080
  - op: add
    path: /server/tls
    after: /server/host
    value: true
`,
	}, {
		name: "with no preceding sibling the following one places it before",
		body: "@@ /tags @@\n+ - aardvark\n  - alpha\n",
		want: `  - op: test
    path: /tags/=alpha
    value: alpha
  - op: add
    path: /tags
    before: /tags/=alpha
    value: aardvark
`,
	}, {
		name: "with no context at all the add goes to the end",
		body: "@@ /server @@\n+ tls: true\n",
		want: `  - op: add
    path: /server/tls
    value: true
`,
	}, {
		name: "a keyed element is matched as a subset, one test per field",
		body: "@@ /servers @@\n" + `  { "name": "github", "command": "npx" }` + "\n" +
			`+ { "name": "ctxloom", "command": "ctxloom" }` + "\n",
		want: `  - op: test
    path: /servers/name=github/name
    value: github
  - op: test
    path: /servers/name=github/command
    value: npx
  - op: add
    path: /servers
    after: /servers/name=github
    value:
      name: ctxloom
      command: ctxloom
`,
	}, {
		name: "a keyed element written over several lines addresses the same node",
		body: "@@ /mcpServers @@\n  - name: github\n+ - name: ctxloom\n+   command: ctxloom\n",
		want: `  - op: test
    path: /mcpServers/name=github/name
    value: github
  - op: add
    path: /mcpServers
    after: /mcpServers/name=github
    value:
      name: ctxloom
      command: ctxloom
`,
	}, {
		name: "the key field preference is name, id, key, then the first listed",
		body: "@@ /a @@\n  - other: x\n    id: 7\n  - only: z\n",
		want: `  - op: test
    path: /a/id=7/other
    value: x
  - op: test
    path: /a/id=7/id
    value: 7
  - op: test
    path: /a/only=z/only
    value: z
`,
	}, {
		name: "a scalar list is addressed by value, never by index",
		body: "@@ /tags @@\n- - beta\n",
		want: `  - op: test
    path: /tags/=beta
    value: beta
  - op: remove
    path: /tags/=beta
`,
	}, {
		name: "sequence elements pair by offset within an adjacent run",
		body: "@@ /tags @@\n- - alpha\n- - beta\n+ - ALPHA\n+ - BETA\n+ - gamma\n",
		want: `  - op: test
    path: /tags/=alpha
    value: alpha
  - op: test
    path: /tags/=beta
    value: beta
  - op: replace
    path: /tags/=alpha
    value: ALPHA
  - op: replace
    path: /tags/=beta
    value: BETA
  - op: add
    path: /tags
    after: /tags/=BETA
    value: gamma
`,
	}, {
		name: "a removed element with no add after it is just a removal",
		body: "@@ /tags @@\n- - alpha\n  - beta\n+ - gamma\n",
		want: `  - op: test
    path: /tags/=alpha
    value: alpha
  - op: test
    path: /tags/=beta
    value: beta
  - op: remove
    path: /tags/=alpha
  - op: add
    path: /tags
    after: /tags/=beta
    value: gamma
`,
	}, {
		name: "a removed container asserts its children and removes only itself",
		body: "@@ / @@\n- provider \"azurerm\" {\n-   features {}\n- }\n",
		want: `  - op: test
    path: /provider/"azurerm"/features
    value: {}
  - op: remove
    path: /provider/"azurerm"
`,
	}, {
		name: "a context container is lowered in full where it stands",
		body: "@@ / @@\n! match label=[\"aws\"] ord=0\n  provider \"aws\" {\n" +
			"-   region = \"us-west-1\"\n+   region = \"us-west-2\"\n    profile = \"default\"\n  }\n" +
			"! match label=[\"aws\"] ord=1\n  provider \"aws\" {\n    alias = \"east\"\n+   profile = \"ctxloom\"\n  }\n",
		want: `  - op: test
    path: /provider/"aws"[0]/region
    value: us-west-1
  - op: test
    path: /provider/"aws"[0]/profile
    value: default
  - op: replace
    path: /provider/"aws"[0]/region
    value: us-west-2
  - op: test
    path: /provider/"aws"[1]/alias
    value: east
  - op: add
    path: /provider/"aws"[1]/profile
    after: /provider/"aws"[1]/alias
    value: ctxloom
`,
	}, {
		name: "a first-line ordinal with no block after it selects the anchor",
		body: "@@ /provider/\"aws\" @@\n! match ord=1\n  alias = \"east\"\n+ profile = \"ctxloom\"\n",
		want: `  - op: test
    path: /provider/"aws"[1]/alias
    value: east
  - op: add
    path: /provider/"aws"[1]/profile
    after: /provider/"aws"[1]/alias
    value: ctxloom
`,
	}, {
		name: "an added block writes its body as the value, labels stay in the address",
		body: "@@ /terraform @@\n  required_version = \">= 1.6\"\n+ required_providers {\n" +
			"+   aws = {\n+     source = \"hashicorp/aws\"\n+   }\n+ }\n",
		want: `  - op: test
    path: /terraform/required_version
    value: '>= 1.6'
  - op: add
    path: /terraform/required_providers
    after: /terraform/required_version
    value:
      aws:
        source: hashicorp/aws
`,
	}, {
		name: "a comment is a node with an address in each projection",
		body: "@@ /server @@\n  # ports below 1024 need CAP_NET_BIND_SERVICE\n  port: 8080\n- # TODO\n+ # done\n",
		want: `  - op: test
    path: /server/#0
    value:
      comment: ports below 1024 need CAP_NET_BIND_SERVICE
  - op: test
    path: /server/port
    value: 8080
  - op: test
    path: /server/#1
    value:
      comment: TODO
  - op: replace
    path: /server/#1
    value:
      comment: done
`,
	}, {
		name: "an added comment and the member it documents are two adds",
		body: "@@ / @@\n  \"port\": 8080\n+ // added by taskloom\n+ \"telemetry\": false\n",
		want: `  - op: test
    path: /port
    value: 8080
  - op: add
    path: /#0
    after: /port
    value:
      comment: added by taskloom
  - op: add
    path: /telemetry
    after: /#0
    value: false
`,
	}, {
		name: "a TOML table header addresses the child it introduces",
		body: "@@ /mcp_servers @@\n? absent /mcp_servers/taskloom\n! surface table\n" +
			"+ [mcp_servers.taskloom]\n+ command = \"taskloom\"\n+ args = [\"mcp\"]\n",
		want: `  - op: test
    path: /mcp_servers/taskloom
    absent: true
  - op: add
    path: /mcp_servers/taskloom
    surface: table
    value:
      command: taskloom
      args: [mcp]
`,
	}, {
		name: "an array-of-tables header is a sequence element",
		body: "@@ /plugin @@\n  [[plugin]]\n  name = \"beta\"\n+ [[plugin]]\n+ name = \"gamma\"\n",
		want: `  - op: test
    path: /plugin/name=beta/name
    value: beta
  - op: add
    path: /plugin
    after: /plugin/name=beta
    value:
      name: gamma
`,
	}, {
		name: "a table header that does not restate the anchor nests from it",
		body: "@@ /tool @@\n+ [ctxloom.limits]\n+ timeout = 30\n",
		want: `  - op: add
    path: /tool/ctxloom/limits
    value:
      timeout: 30
`,
	}, {
		name: "exhaustive counts the container's before-image children",
		body: "@@ / @@\n? exhaustive\n- generated: \"by ctxloom\"\n- version: 1\n+ generated: \"by ctxloom 0.7\"\n+ version: 2\n+ agents: [\"coordinator\"]\n",
		want: `  - op: test
    path: /
    count: 2
    exhaustive: true
  - op: test
    path: /generated
    value: by ctxloom
  - op: test
    path: /version
    value: 1
  - op: replace
    path: /generated
    value: by ctxloom 0.7
  - op: replace
    path: /version
    value: 2
  - op: add
    path: /agents
    after: /version
    value:
      - coordinator
`,
	}, {
		name: "free-standing assertions carry their own paths, relative to the anchor",
		body: "@@ /server @@\n? expect ./port = 8080\n? absent /env/KEY\n? count /tags = 2\n? kind /permissions = map\n  timeout: 30\n",
		want: `  - op: test
    path: /server/port
    value: 8080
  - op: test
    path: /env/KEY
    absent: true
  - op: test
    path: /tags
    count: 2
  - op: test
    path: /permissions
    kind: map
  - op: test
    path: /server/timeout
    value: 30
`,
	}, {
		name: "optional rides the test as well as the removal",
		body: "@@ /server @@\n! optional\n- deprecated_flag: true\n",
		want: `  - op: test
    path: /server/deprecated_flag
    optional: true
    value: true
  - op: remove
    path: /server/deprecated_flag
    optional: true
`,
	}, {
		name: "upsert and default are the two add-semantics variants",
		body: "@@ /server @@\n  port: 8080\n! upsert\n+ host: localhost\n! default\n+ timeout: 30\n",
		want: `  - op: test
    path: /server/port
    value: 8080
  - op: add
    path: /server/host
    on_conflict: replace
    after: /server/port
    value: localhost
  - op: add
    path: /server/timeout
    on_conflict: keep
    after: /server/host
    value: 30
`,
	}, {
		name: "anchor policy rides every transform of the hunk it heads",
		body: "@@ /service_a @@\n! anchor fork\n- timeout: 30\n+ timeout: 60\n",
		want: `  - op: test
    path: /service_a/timeout
    anchor: fork
    value: 30
  - op: replace
    path: /service_a/timeout
    anchor: fork
    value: 60
`,
	}, {
		name: "a member with an inline collection is asserted whole",
		body: "@@ / @@\n- tags: [\"alpha\", \"beta\"]\n+ tags: []\n  keep: 1\n",
		want: `  - op: test
    path: /tags
    value:
      - alpha
      - beta
  - op: test
    path: /keep
    value: 1
  - op: replace
    path: /tags
    value: []
`,
	}, {
		name: "a member with no value is the null value",
		body: "@@ /server @@\n  tls:\n",
		want: `  - op: test
    path: /server/tls
    value: null
`,
	}, {
		name: "separating commas are optional and ignored",
		body: "@@ /server @@\n  \"port\": 8080,\n- \"timeout\": 30,\n+ \"timeout\": 60,\n",
		want: `  - op: test
    path: /server/port
    value: 8080
  - op: test
    path: /server/timeout
    value: 30
  - op: replace
    path: /server/timeout
    value: 60
`,
	}, {
		name: "a string that would read back as another kind is quoted in an address",
		body: "@@ /tags @@\n- \"8080\"\n- \"true\"\n- \"a b\"\n",
		want: `  - op: test
    path: /tags/="8080"
    value: "8080"
  - op: test
    path: /tags/="true"
    value: "true"
  - op: test
    path: /tags/="a b"
    value: a b
  - op: remove
    path: /tags/="8080"
  - op: remove
    path: /tags/="true"
  - op: remove
    path: /tags/="a b"
`,
	}, {
		name: "non-string element addresses keep their decoded kind",
		body: "@@ /flags @@\n  true\n  7\n  null\n",
		want: `  - op: test
    path: /flags/=true
    value: true
  - op: test
    path: /flags/=7
    value: 7
  - op: test
    path: /flags/=null
    value: null
`,
	}, {
		name: "blank lines and hew comments are insignificant",
		body: "@@ /server @@\n# why this changed\n\n  port: 8080\n   \n#\n- timeout: 30\n+ timeout: 60\n",
		want: `  - op: test
    path: /server/port
    value: 8080
  - op: test
    path: /server/timeout
    value: 30
  - op: replace
    path: /server/timeout
    value: 60
`,
	}, {
		name: "an expression separator is not a member separator",
		body: "@@ /a @@\n  url = \"http://x/y\"\n  guard = var.x == 1\n  ver = \"~> 5.0\"\n",
		want: `  - op: test
    path: /a/url
    value: http://x/y
  - op: test
    path: /a/guard
    value: var.x == 1
  - op: test
    path: /a/ver
    value: ~> 5.0
`,
	}, {
		name: "an assertion path is one token even when a quoted value holds spaces",
		body: "@@ /servers @@\n? expect /servers/name=\"my server\"/port = 8080\n? absent /provider/\"aws\"/region\n  keep: 1\n",
		want: `  - op: test
    path: /servers/name="my server"/port
    value: 8080
  - op: test
    path: /provider/"aws"/region
    absent: true
  - op: test
    path: /servers/keep
    value: 1
`,
	}, {
		name: "a quoted match value may carry an escaped quote",
		body: "@@ /a @@\n? expect /a/name=\"say \\\"hi\\\"\"/n = 1\n  keep: 1\n",
		want: `  - op: test
    path: /a/name="say \"hi\""/n
    value: 1
  - op: test
    path: /a/keep
    value: 1
`,
	}, {
		name: "an empty block and an empty container have their empty values",
		body: "@@ / @@\n  features {}\n  opts: {\n  }\n  list: [\n  ]\n",
		want: `  - op: test
    path: /features
    value: {}
  - op: test
    path: /opts
    value: {}
  - op: test
    path: /list
    value: []
`,
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := lowered(t, hdr+tc.body); got != tc.want {
				t.Errorf("lowered transforms:\n%s\nwant:\n%s", got, tc.want)
			}
		})
	}
}

// TestParsePragmaAndPrecedence pins §7.5's precedence ladder: a hunk
// directive beats the file pragma beats the strict default. The pragma is
// yaml/pragma-idempotent-file's whole point (ruling O3).
func TestParsePragmaAndPrecedence(t *testing.T) {
	pragma := "hew: 1\nidempotent: true\n\n--- t.yaml format=yaml\n\n"
	// Idempotence is two grants (§7.5, ruling O3): the pragma (or a hunk
	// `! idempotent`) tolerates BOTH the assert and the write, while
	// `! strict` opts only the write back out — the assert stays tolerant
	// (yaml/reapply-not-idempotent pins the assert's line,
	// yaml/pragma-strict-override the write's).
	want := `  - op: test
    path: /server/timeout
    idempotent: true
    value: 30
  - op: replace
    path: /server/timeout
    idempotent: true
    value: 60
`
	if got := lowered(t, pragma+"@@ /server @@\n- timeout: 30\n+ timeout: 60\n"); got != want {
		t.Errorf("file pragma:\n%s\nwant:\n%s", got, want)
	}
	strict := `  - op: test
    path: /server/timeout
    idempotent: true
    value: 30
  - op: replace
    path: /server/timeout
    value: 60
`
	if got := lowered(t, pragma+"@@ /server @@\n! strict\n- timeout: 30\n+ timeout: 60\n"); got != strict {
		t.Errorf("! strict over the pragma:\n%s\nwant:\n%s", got, strict)
	}
	if got := lowered(t, hdr+"@@ /server @@\n! idempotent\n- timeout: 30\n+ timeout: 60\n"); got != want {
		t.Errorf("! idempotent without the pragma:\n%s\nwant:\n%s", got, want)
	}
	// A line-scoped directive governs its own line only.
	perLine := `  - op: test
    path: /server/timeout
    value: 30
  - op: remove
    path: /server/timeout
  - op: add
    path: /server/tls
    idempotent: true
    value: true
`
	if got := lowered(t, hdr+"@@ /server @@\n- timeout: 30\n! idempotent\n+ tls: true\n"); got != perLine {
		t.Errorf("line-scoped idempotent:\n%s\nwant:\n%s", got, perLine)
	}
}

// TestParseDocument pins the §2 file grammar: the preamble default format,
// the per-section format= attribute, a quoted target path, and one
// TransformList per file section.
func TestParseDocument(t *testing.T) {
	src := "# a leading hew comment\nhew: 1\nformat: yaml\n\n" +
		"--- \"my config.yaml\"\n\n@@ /server @@\n  port: 8080\n\n" +
		"--- other.json format=json\n@@ / @@\n- a: 1\n"
	tls, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(tls) != 2 {
		t.Fatalf("Parse = %d file sections, want 2", len(tls))
	}
	if got := tls[0].Target; got != "my config.yaml" {
		t.Errorf("Target = %q, want %q (a quoted path with a space, §2.2)", got, "my config.yaml")
	}
	if got := tls[0].Format; got != FormatYAML {
		t.Errorf("Format = %q, want the preamble default %q", got, FormatYAML)
	}
	if got := tls[1].Format; got != FormatJSON {
		t.Errorf("Format = %q, want the format= attribute %q", got, FormatJSON)
	}
	if got := tls[0].Transform; len(got) != 1 || got[0].Op != OpTest || got[0].PatchLine != 8 {
		t.Errorf("transforms = %+v, want one test from line 8", got)
	}
	if got := tls[1].Transform; len(got) != 2 || got[0].Op != OpTest || got[1].Op != OpRemove {
		t.Errorf("transforms = %+v, want a test and a remove", got)
	}
}

// TestParseSingle pins Appendix B.1's --target contract: exactly one section
// or a loud refusal.
func TestParseSingle(t *testing.T) {
	tl, err := ParseSingle([]byte(hdr + "@@ /server @@\n  port: 8080\n"))
	if err != nil {
		t.Fatalf("ParseSingle: %v", err)
	}
	if tl.Target != "t.json" {
		t.Errorf("Target = %q, want t.json", tl.Target)
	}
	two := "hew: 1\n\n--- a.json format=json\n@@ / @@\n  a: 1\n\n--- b.json format=json\n@@ / @@\n  b: 2\n"
	if _, err := ParseSingle([]byte(two)); err == nil {
		t.Error("ParseSingle must refuse a patch with two file sections")
	}
	if _, err := ParseSingle([]byte("hew: 1\n")); err == nil {
		t.Error("ParseSingle must propagate a parse error")
	}
}

// TestParseTargetLineWins pins §2.2's disambiguation: `--- ` starts a file
// section even inside a hunk body, so a removal of literal `-- x` is written
// `- -- x`.
func TestParseTargetLineWins(t *testing.T) {
	tls, err := Parse([]byte("hew: 1\n\n--- a.json format=json\n@@ / @@\n- a: 1\n--- b.json format=json\n@@ / @@\n- b: 2\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(tls) != 2 {
		t.Fatalf("Parse = %d file sections, want 2", len(tls))
	}
	if got := lowered(t, hdr+"@@ /tags @@\n- -- x\n"); !strings.Contains(got, `path: /tags/="-- x"`) {
		t.Errorf("`- -- x` should lower as a removal of the text `-- x`, got:\n%s", got)
	}
}

// TestParseErrors pins §10's parser-layer contract: code, component, patch
// line, and a message that names what is wrong.
func TestParseErrors(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		code     hewerr.Code
		line     int
		contains string
	}{
		{"missing version", "--- t.json format=json\n@@ / @@\n  a: 1\n", hewerr.CodeParse, 0, "hew: 1"},
		{"unknown version", "hew: 2\n\n--- t.json\n", hewerr.CodeTargetParse, 1, "unknown hew version"},
		{"non-integer version", "hew: one\n", hewerr.CodeTargetParse, 1, "unknown hew version"},
		{"directive before hew", "format: json\nhew: 1\n", hewerr.CodeParse, 1, "must be the first significant line"},
		{"unknown preamble key", "hew: 1\nfuzz: 3\n\n--- t.json\n", hewerr.CodeParse, 2, `unknown preamble key "fuzz"`},
		{"duplicate preamble key", "hew: 1\nhew: 1\n", hewerr.CodeParse, 2, "duplicate preamble key"},
		{"preamble junk", "hew: 1\nnonsense\n", hewerr.CodeParse, 2, "preamble directive"},
		{"bad pragma", "hew: 1\nidempotent: sometimes\n", hewerr.CodeParse, 2, "expected a boolean"},
		{"unsupported preamble format", "hew: 1\nformat: ini\n\n--- t.x\n", hewerr.CodeUnsupportedFormat, 2, "unknown format"},
		{"empty patch", "hew: 1\n", hewerr.CodeParse, 0, "no hunks"},
		{"section with no hunks", "hew: 1\n\n--- t.json format=json\n", hewerr.CodeParse, 3, "no hunks"},
		{"target line with no path", "hew: 1\n\n---\n", hewerr.CodeParse, 3, "no path"},
		{"unterminated quoted target", "hew: 1\n\n--- \"a b\n", hewerr.CodeParse, 3, "unterminated"},
		{"unknown target attribute", "hew: 1\n\n--- t.json mode=fast\n", hewerr.CodeParse, 3, `unknown target attribute "mode"`},
		{"malformed target attribute", "hew: 1\n\n--- t.json json\n", hewerr.CodeParse, 3, "not key=value"},
		{"unsupported format", "hew: 1\n\n--- t.x format=ini\n@@ / @@\n  a: 1\n", hewerr.CodeUnsupportedFormat, 3, "unknown format"},
		{"junk between sections", "hew: 1\n\nnonsense\n", hewerr.CodeParse, 3, "target line"},
		{"junk inside a section", "hew: 1\n\n--- t.json format=json\nnonsense\n", hewerr.CodeParse, 4, "hunk header"},
		{"unclosed hunk header", "hew: 1\n\n--- t.json format=json\n@@ /server\n  a: 1\n", hewerr.CodeParse, 4, "not closed"},
		{"hunk attribute", "hew: 1\n\n--- t.json format=json\n@@ / @@ mode=x\n  a: 1\n", hewerr.CodeParse, 4, "unknown hunk attribute"},
		{"bad anchor path", "hew: 1\n\n--- t.json format=json\n@@ server @@\n  a: 1\n", hewerr.CodeParse, 4, "must begin with"},
		{"IR-only ordinal in the notation", "hew: 1\n\n--- t.json format=json\n@@ /a[0] @@\n  b: 1\n", hewerr.CodeParse, 4, "IR-only"},
		{"empty hunk", "hew: 1\n\n--- t.json format=json\n@@ / @@\n\n", hewerr.CodeParse, 4, "no body lines"},
		{"bad margin", hdr + "@@ / @@\n* a: 1\n", hewerr.CodeParse, 6, "not a margin character"},
		{"missing margin space", hdr + "@@ / @@\n+a: 1\n", hewerr.CodeParse, 6, "must be a single space"},
		{"unexpected indentation", hdr + "@@ / @@\n  a: 1\n    b: 2\n", hewerr.CodeParse, 7, "unexpected indentation"},
		{"a body line shallower than the hunk's first", hdr + "@@ / @@\n    a: 1\n  b: 2\n", hewerr.CodeParse, 7, "unexpected indentation"},
		{"stray closing delimiter", hdr + "@@ / @@\n  }\n", hewerr.CodeParse, 6, "unexpected closing delimiter"},
		{"mixed margins in an added container", hdr + "@@ / @@\n+ a {\n-   b = 1\n+ }\n", hewerr.CodeParse, 7, "mixed margins"},
		{"unknown assertion", hdr + "@@ / @@\n? probably /a\n  b: 1\n", hewerr.CodeParse, 6, "unknown assertion"},
		{"unknown directive", hdr + "@@ / @@\n! hurry\n  b: 1\n", hewerr.CodeParse, 6, "unknown directive"},
		{"empty annotation", hdr + "@@ / @@\n?\n  b: 1\n", hewerr.CodeParse, 6, "carries no directive"},
		{"assertion without a value", hdr + "@@ / @@\n? expect /a\n  b: 1\n", hewerr.CodeParse, 6, "<path> = <value>"},
		{"assertion with an empty value", hdr + "@@ / @@\n? expect /a =\n  b: 1\n", hewerr.CodeParse, 6, "<path> = <value>"},
		{"bad count", hdr + "@@ / @@\n? count /a = many\n  b: 1\n", hewerr.CodeParse, 6, "non-negative integer"},
		{"bad kind", hdr + "@@ / @@\n? kind /a = table\n  b: 1\n", hewerr.CodeParse, 6, "unknown node kind"},
		{"bad assertion path", hdr + "@@ / @@\n? absent a\n  b: 1\n", hewerr.CodeParse, 6, "must begin with"},
		{"unattached directive", hdr + "@@ / @@\n  a: 1\n! optional\n", hewerr.CodeParse, 7, "not followed by a body line"},
		{"directive with an argument it does not take", hdr + "@@ / @@\n! optional now\n- a: 1\n", hewerr.CodeParse, 6, "takes no arguments"},
		{"anchor mode", hdr + "@@ / @@\n! anchor maybe\n- a: 1\n", hewerr.CodeParse, 6, "`! anchor` takes"},
		{"anchor arity", hdr + "@@ / @@\n! anchor fork rewrite\n- a: 1\n", hewerr.CodeParse, 6, "exactly one argument"},
		{"surface value", hdr + "@@ / @@\n! surface inline\n+ a: 1\n", hewerr.CodeParse, 6, "`! surface` takes"},
		{"match without ord", hdr + "@@ /provider/\"aws\" @@\n! match label=[\"aws\"]\n  a: 1\n", hewerr.CodeParse, 6, "requires `ord=`"},
		{"match with a bad ord", hdr + "@@ /provider/\"aws\" @@\n! match ord=last\n  a: 1\n", hewerr.CodeParse, 6, "non-negative integer"},
		{"match with a bad label list", hdr + "@@ /provider/\"aws\" @@\n! match label=aws ord=0\n  a: 1\n", hewerr.CodeParse, 6, "list of labels"},
		{"match with an unknown attribute", hdr + "@@ /provider/\"aws\" @@\n! match ord=0 fuzzy=1\n  a: 1\n", hewerr.CodeParse, 6, "unknown `! match` attribute"},
		{"match attribute without a value", hdr + "@@ /provider/\"aws\" @@\n! match ord\n  a: 1\n", hewerr.CodeParse, 6, "not key=value"},
		{"two match annotations on one line", hdr + "@@ / @@\n! match ord=0\n! match ord=1\n  provider \"aws\" {\n    a = 1\n  }\n", hewerr.CodeParse, 7, "two `! match`"},
		{"match on the document root", hdr + "@@ / @@\n! match ord=1\n  a: 1\n", hewerr.CodeParse, 0, "document root"},
		{"match away from a block", hdr + "@@ /provider/\"aws\" @@\n  a: 1\n! match ord=1\n+ b: 2\n", hewerr.CodeParse, 7, "must precede the block"},
		{"ordinal without a distinguishing assert", hdr + "@@ /provider/\"aws\" @@\n! match ord=1\n+ profile = \"x\"\n", hewerr.CodeParse, 6, "distinguishing"},
		{"label cross-check", hdr + "@@ /provider/\"aws\" @@\n! match label=[\"gcp\"] ord=1\n  alias = \"east\"\n", hewerr.CodeAssertionFailed, 6, "does not match the selected block's labels"},
		{"label cross-check beside a block", hdr + "@@ / @@\n! match label=[\"gcp\"] ord=1\n  provider \"aws\" {\n    alias = \"east\"\n  }\n", hewerr.CodeAssertionFailed, 6, "does not match the selected block's labels"},
		{"element with no identity", hdr + "@@ /a @@\n  - inner:\n      deep: 1\n", hewerr.CodeParse, 6, "no usable identity field"},
		{"comment inside an added container", hdr + "@@ / @@\n+ server:\n+   # why\n+   port: 8080\n", hewerr.CodeInexpressible, 7, "no address in the IR"},
		{"unreadable value", hdr + "@@ / @@\n  a: \"unterminated\n", hewerr.CodeParse, 6, "cannot read"},
		{"empty table header", hdr + "@@ / @@\n  [[]]\n", hewerr.CodeParse, 6, "empty table header"},
		{"empty table component", hdr + "@@ / @@\n  [a..b]\n", hewerr.CodeParse, 6, "empty component"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.src))
			if err == nil {
				t.Fatalf("Parse succeeded, want %s", tc.code)
			}
			he, ok := hewerr.As(err)
			if !ok {
				t.Fatalf("error is not a *hewerr.Error: %v", err)
			}
			if he.Code != tc.code {
				t.Errorf("code = %s, want %s (%v)", he.Code, tc.code, err)
			}
			if he.Component != hewerr.ComponentParser {
				t.Errorf("component = %s, want parser", he.Component)
			}
			if he.PatchLine != tc.line {
				t.Errorf("patch line = %d, want %d (%v)", he.PatchLine, tc.line, err)
			}
			if !strings.Contains(err.Error(), tc.contains) {
				t.Errorf("message %q does not contain %q", err.Error(), tc.contains)
			}
		})
	}
}

// TestParseValueStyle pins the one presentation rule the corpus fixes: a
// transform's own value is written in the .hewt document's shape, while a
// nested collection keeps the flow spelling the author chose (§6.3).
func TestParseValueStyle(t *testing.T) {
	got := lowered(t, hdr+"@@ / @@\n+ a: { b: [1, 2] }\n")
	want := `  - op: add
    path: /a
    value:
      b: [1, 2]
`
	if got != want {
		t.Errorf("value style:\n%s\nwant:\n%s", got, want)
	}
}

// TestParseNoTrailingNewline pins that a trailing newline on the last line is
// optional (§2).
func TestParseNoTrailingNewline(t *testing.T) {
	got := lowered(t, strings.TrimSuffix(hdr+"@@ /server @@\n  port: 8080\n", "\n"))
	want := "  - op: test\n    path: /server/port\n    value: 8080\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestParseWholeFileReplace(t *testing.T) {
	patch, _, _ := corpusCase(t, "json/whole-file-replace")
	tls, err := Parse(patch)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(tls) != 1 {
		t.Fatalf("want 1 file section, got %d", len(tls))
	}
	tl := tls[0]
	var replaces, adds, tests int
	for _, tr := range tl.Transform {
		switch tr.Op {
		case OpReplace:
			replaces++
		case OpAdd:
			adds++
		case OpTest:
			tests++
		}
	}
	if replaces != 2 {
		t.Errorf("want 2 replace ops (generated, version), got %d", replaces)
	}
	if adds != 1 {
		t.Errorf("want 1 add op (agents), got %d", adds)
	}
	if tests != 3 {
		t.Errorf("want 3 test ops (2 field tests from the '-' lines, plus 1 exhaustive count test), got %d", tests)
	}
}

func TestParseExhaustiveFail(t *testing.T) {
	patch, _, _ := corpusCase(t, "json/assert-exhaustive-fail")
	tls, err := Parse(patch)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var found bool
	for _, tr := range tls[0].Transform {
		if tr.Exhaustive {
			found = true
			if tr.Count == nil || *tr.Count != 2 {
				t.Fatalf("exhaustive count: want 2, got %v", tr.Count)
			}
			if tr.Path.String() != "/permissions" {
				t.Fatalf("exhaustive path: want /permissions, got %s", tr.Path)
			}
		}
	}
	if !found {
		t.Fatal("no exhaustive transform emitted")
	}
}

func TestParseAssertKind(t *testing.T) {
	patch, _, _ := corpusCase(t, "json/assert-kind-fail")
	tls, err := Parse(patch)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var found bool
	for _, tr := range tls[0].Transform {
		if tr.NodeKind != nil {
			found = true
			if *tr.NodeKind != KindMap {
				t.Fatalf("kind: want map, got %s", *tr.NodeKind)
			}
			if tr.Path.String() != "/mcpServers" {
				t.Fatalf("kind path: want /mcpServers, got %s", tr.Path)
			}
		}
	}
	if !found {
		t.Fatal("no kind assertion emitted")
	}
}

func TestParseCommentAdd(t *testing.T) {
	patch, _, _ := corpusCase(t, "json/comment-inexpressible")
	tls, err := Parse(patch)
	if err != nil {
		t.Fatalf("Parse should succeed for a comment add (the applier refuses it, not the parser): %v", err)
	}
	var found bool
	for _, tr := range tls[0].Transform {
		if tr.Op == OpAdd && tr.Path.Len() > 0 && tr.Path.Segment(tr.Path.Len()-1).Kind == SegComment {
			found = true
		}
	}
	if !found {
		t.Fatal("expected an add transform addressing a comment node")
	}
}

func TestParseEmptyPatchIsError(t *testing.T) {
	he, ok := hewerr.As(mustErr(t, []byte("")))
	if !ok || he.Code != hewerr.CodeParse {
		t.Fatalf("want HEW001, got %v", he)
	}
}

func mustErr(t *testing.T, src []byte) error {
	t.Helper()
	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected an error")
	}
	return err
}

// --- target comment lines (§3, §8.2) ---------------------------------------

func parseOne(t *testing.T, src string) []Transform {
	t.Helper()
	tls, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(tls) != 1 {
		t.Fatalf("want 1 file section, got %d", len(tls))
	}
	return tls[0].Transform
}

func TestTargetCommentTextStripsEverySyntax(t *testing.T) {
	for _, c := range []struct {
		in, want string
		ok       bool
	}{
		{"// x", "x", true},
		{"//x", "x", true},
		{"//  x", " x", true},
		{"/* x */", "x", true},
		{"/**/", "", true},
		{"# x", "x", true},
		{"#x", "x", true},
		{"port: 8080", "", false},
		{"/ x", "", false},
	} {
		got, ok := targetCommentText(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("targetCommentText(%q) = %q,%v; want %q,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestCommentValueRoundTrip(t *testing.T) {
	v := commentValue("bumped by ctxloom")
	got, ok := commentTextOf(v)
	if !ok || got != "bumped by ctxloom" {
		t.Fatalf("commentTextOf(commentValue(x)) = %q,%v", got, ok)
	}
	if got, ok := commentTextOf(Value{}); ok || got != "" {
		t.Errorf("the absent value is not a comment: %q,%v", got, ok)
	}
	sc, err := ValueOf("bare")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := commentTextOf(sc); !ok || got != "bare" {
		t.Errorf("a bare scalar spells the same comment: %q,%v", got, ok)
	}
	seq, err := ValueOf([]int{1})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := commentTextOf(seq); ok {
		t.Error("a sequence is not a comment node")
	}
}

// TestAddedCommentAnchorsTheMemberBelowIt pins the lowering
// corpus/jsonc/add-with-leading-comment's transforms.hewt fixture states: two
// adds, the member placed after the comment node rather than after the
// context line above them both (§8.2).
func TestAddedCommentAnchorsTheMemberBelowIt(t *testing.T) {
	ts := parseOne(t, "hew: 1\n\n--- t.jsonc format=jsonc\n\n@@ / @@\n  port: 8080\n"+
		"+ // added by taskloom\n+ telemetry: false\n")
	if len(ts) != 3 {
		t.Fatalf("want 3 transforms, got %d: %+v", len(ts), ts)
	}
	if ts[1].Op != OpAdd || ts[1].Path.String() != "/#0" || ts[1].After.String() != "/port" {
		t.Fatalf("comment add: %+v", ts[1])
	}
	if got, ok := commentTextOf(ts[1].Value); !ok || got != "added by taskloom" {
		t.Fatalf("comment value: %q,%v", got, ok)
	}
	if ts[2].Op != OpAdd || ts[2].Path.String() != "/telemetry" || ts[2].After.String() != "/#0" {
		t.Fatalf("member add: %+v", ts[2])
	}
}

// TestRemovedLeadingCommentNeedsNoRemoveRecord pins the other half of §8.2:
// removing a member removes its leading comment, so the comment's own "-"
// line asserts but does not emit a second remove.
func TestRemovedLeadingCommentNeedsNoRemoveRecord(t *testing.T) {
	ts := parseOne(t, "hew: 1\n\n--- t.jsonc format=jsonc\n\n@@ / @@\n"+
		"- // the old opt-out\n- telemetry: false\n  port: 8080\n")
	var removes []string
	for _, tr := range ts {
		if tr.Op == OpRemove {
			removes = append(removes, tr.Path.String())
		}
	}
	if len(removes) != 1 || removes[0] != "/telemetry" {
		t.Fatalf("want exactly the member removed, got %v", removes)
	}
	if ts[0].Op != OpTest || ts[0].Path.String() != "/#0" {
		t.Fatalf("the comment line still asserts: %+v", ts[0])
	}
}

// A standalone "-" comment line — one that does NOT lead a removed member —
// keeps its own remove record.
func TestStandaloneCommentRemovalKeepsItsRecord(t *testing.T) {
	ts := parseOne(t, "hew: 1\n\n--- t.jsonc format=jsonc\n\n@@ / @@\n"+
		"- // stale note\n  port: 8080\n")
	var removes []string
	for _, tr := range ts {
		if tr.Op == OpRemove {
			removes = append(removes, tr.Path.String())
		}
	}
	if len(removes) != 1 || removes[0] != "/#0" {
		t.Fatalf("want the comment node removed, got %v", removes)
	}
}

// A leading comment above a member that is REPLACED (a "-"/"+" pair) is not
// absorbed: the member survives, so the comment's removal stands on its own.
func TestLeadingCommentAboveAReplacedMemberIsNotAbsorbed(t *testing.T) {
	ts := parseOne(t, "hew: 1\n\n--- t.jsonc format=jsonc\n\n@@ / @@\n"+
		"- // stale note\n- timeout: 30\n+ timeout: 60\n")
	var ops []string
	for _, tr := range ts {
		if tr.Op != OpTest {
			ops = append(ops, string(tr.Op)+" "+tr.Path.String())
		}
	}
	if len(ops) != 2 || ops[0] != "remove /#0" || ops[1] != "replace /timeout" {
		t.Fatalf("want the comment removed and the member replaced, got %v", ops)
	}
}
