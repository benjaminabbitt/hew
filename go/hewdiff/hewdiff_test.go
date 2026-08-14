package hewdiff

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/benjaminabbitt/hew/go"
	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

func diffText(t *testing.T, old, new string, format hew.FormatID, opt hew.DiffOptions) string {
	t.Helper()
	if opt.Target == "" {
		opt.Target = "new." + string(format)
	}
	tl, err := Diff([]byte(old), []byte(new), format, opt)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(tl.Transform) == 0 {
		return ""
	}
	out, err := hew.Render(tl, hew.RenderOptions{Preamble: true, Fragment: hew.FragmentNative})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return string(out)
}

// The corpus round-trips are the normative outputs; this is the same
// composition the conformance binding runs, kept here so the dispatch has a
// killer of its own when the corpus suite is not running.
func TestDiffMatchesCorpusRoundTrips(t *testing.T) {
	root := filepath.Join("..", "..", "corpus")
	// The extension is the file's, not the format's: HCL's is ".tf".
	for _, c := range []struct{ format, ext string }{
		{"json", "json"}, {"jsonc", "jsonc"}, {"yaml", "yaml"}, {"toml", "toml"}, {"hcl", "tf"},
	} {
		t.Run(c.format, func(t *testing.T) {
			dir := filepath.Join(root, c.format, "roundtrip-basic")
			read := func(name string) string {
				b, err := os.ReadFile(filepath.Join(dir, name))
				if err != nil {
					t.Fatalf("corpus: %v", err)
				}
				return string(b)
			}
			got := diffText(t, read("old."+c.ext), read("new."+c.ext), hew.FormatID(c.format),
				hew.DiffOptions{Target: "new." + c.ext})
			if want := read("expected.hew"); got != want {
				t.Fatalf("diff != expected.hew\n--- got\n%s\n--- want\n%s", got, want)
			}
		})
	}
}

func TestDiffOfIdenticalDocumentsIsEmpty(t *testing.T) {
	src := "a: 1\nb: 2\n"
	tl, err := Diff([]byte(src), []byte(src), hew.FormatYAML, hew.DiffOptions{Target: "t.yaml"})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(tl.Transform) != 0 {
		t.Fatalf("want no transforms, got %d", len(tl.Transform))
	}
}

func TestDiffRejectsUnknownFormat(t *testing.T) {
	_, err := Diff([]byte("a"), []byte("b"), hew.FormatMarkdown, hew.DiffOptions{Target: "t.toml"})
	he, ok := hewerr.As(err)
	if !ok || he.Code != hewerr.CodeUnsupportedFormat || he.Component != hewerr.ComponentDiffer {
		t.Fatalf("want HEW021 from the differ, got %v", err)
	}
	if he.Target != "t.toml" {
		t.Fatalf("target not carried: %q", he.Target)
	}
}

func TestDiffRejectsMissingFormat(t *testing.T) {
	_, err := Diff([]byte("a"), []byte("b"), "", hew.DiffOptions{Target: "t"})
	if he, ok := hewerr.As(err); !ok || he.Code != hewerr.CodeUnsupportedFormat {
		t.Fatalf("want HEW021, got %v", err)
	}
}

// Both sides are parsed, and a failure on either is reported against the
// declared target rather than swallowed.
func TestDiffReportsUnparseableSides(t *testing.T) {
	for _, c := range []struct{ name, old, new string }{
		{"old", "{", `{"a": 1}`},
		{"new", `{"a": 1}`, "{"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := Diff([]byte(c.old), []byte(c.new), hew.FormatJSON, hew.DiffOptions{Target: "t.json"})
			he, ok := hewerr.As(err)
			if !ok || he.Code != hewerr.CodeTargetParse {
				t.Fatalf("want HEW002, got %v", err)
			}
			if he.Target != "t.json" {
				t.Fatalf("target not carried: %q", he.Target)
			}
		})
	}
}

func TestDiffContextFlowsThrough(t *testing.T) {
	old := "a: 1\nb: 2\nc: 3\nd: 4\n"
	new := "a: 1\nb: 2\nc: 3\nd: 9\n"
	tight := diffText(t, old, new, hew.FormatYAML, hew.DiffOptions{Context: hew.ContextNone})
	if strings.Contains(tight, "  c: 3") {
		t.Fatalf("radius 0 emits no context:\n%s", tight)
	}
	all := diffText(t, old, new, hew.FormatYAML, hew.DiffOptions{Context: hew.ContextAll})
	for _, want := range []string{"  a: 1", "  b: 2", "  c: 3"} {
		if !strings.Contains(all, want) {
			t.Fatalf("radius all must emit every sibling, missing %q:\n%s", want, all)
		}
	}
}

// TestDiffOfScopedPackageJSONIsRefused is the reported defect end to end: a
// real package.json whose scoped dependency gains a member. The key
// "@scope/pkg" has no v0 §4 spelling — its bare form re-reads as a marker
// segment — so `hew diff` must say so by name instead of emitting a patch that
// addresses something else (§9.3). A quoted-key segment form is
// ratified-pending (PR #1); this refusal is the honest behavior until then.
func TestDiffOfScopedPackageJSONIsRefused(t *testing.T) {
	old := `{"dependencies":{"left-pad":"1.0.0","@scope/pkg":{"version":"1.0.0"}}}`
	new := `{"dependencies":{"left-pad":"1.0.0","@scope/pkg":{"version":"1.0.0","resolved":"https://example.test/pkg"}}}`

	tl, err := Diff([]byte(old), []byte(new), hew.FormatJSON, hew.DiffOptions{Target: "package.json"})
	if err == nil {
		out, rerr := hew.Render(tl, hew.RenderOptions{Fragment: hew.FragmentNative})
		t.Fatalf("diff of a scoped dependency must be refused; got a patch instead:\n%s\n(render err: %v)", out, rerr)
	}
	he, ok := hewerr.As(err)
	if !ok || he.Code != hewerr.CodeInexpressible || he.Component != hewerr.ComponentDiffer {
		t.Fatalf("want HEW020 at the differ, got %v", err)
	}
	if he.Target != "package.json" || !strings.Contains(he.Detail, "@scope/pkg") {
		t.Fatalf("the diagnostic must name the file and the key: %v", err)
	}
}

func TestDiffKeyFieldsFlowThrough(t *testing.T) {
	old := "m:\n  - slug: a\n    v: 1\n  - slug: b\n    v: 2\n"
	new := "m:\n  - slug: a\n    v: 1\n  - slug: b\n    v: 2\n  - slug: c\n    v: 3\n"
	got := diffText(t, old, new, hew.FormatYAML, hew.DiffOptions{KeyFields: []string{"slug"}})
	if !strings.Contains(got, "  - slug: b") {
		t.Fatalf("--key-fields must reach the differ:\n%s", got)
	}
}
