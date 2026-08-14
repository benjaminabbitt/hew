package all

import (
	"reflect"
	"strings"
	"testing"

	hew "github.com/benjaminabbitt/hew/go"
	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

// This is the one package that can see every shipped extension at once, so it
// is where §8.0's table lives now that the table is DATA rather than spec
// (O4, finished by O48). The core's own registry suite tests the mechanism on
// fake formats; these tests pin what the six v0 extensions actually claim.

func TestAllRegistersEveryV0Format(t *testing.T) {
	want := []hew.FormatID{"hcl", "json", "jsonc", "markdown", "toml", "yaml"}
	if got := hew.Formats(); !reflect.DeepEqual(got, want) {
		t.Fatalf("hew.Formats() = %v, want %v", got, want)
	}
	for _, id := range want {
		if !id.Valid() {
			t.Errorf("%q is not Valid() although ext/all linked it", id)
		}
	}
}

// TestShippedHalves states which halves each v0 extension ships, so a binding
// that quietly loses one is a failure here rather than a HEW021 in the field.
func TestShippedHalves(t *testing.T) {
	for _, c := range []struct {
		id                        hew.FormatID
		applier, differ, document bool
	}{
		{"json", true, true, true},
		{"jsonc", true, true, false},
		{"yaml", true, true, false},
		{"toml", true, true, false},
		{"hcl", true, true, false},
		// Markdown ships neither half while §8.7's dialect evaluation is open
		// (O29). It is registered anyway, because detection and parsing a
		// markdown patch are real and do not need an applier.
		{"markdown", false, false, false},
	} {
		b, ok := hew.Lookup(c.id)
		if !ok {
			t.Errorf("%q is not registered", c.id)
			continue
		}
		if got := b.Applier != nil; got != c.applier {
			t.Errorf("%q applier present = %v, want %v", c.id, got, c.applier)
		}
		if got := b.Differ != nil; got != c.differ {
			t.Errorf("%q differ present = %v, want %v", c.id, got, c.differ)
		}
		if got := b.Document != nil; got != c.document {
			t.Errorf("%q document present = %v, want %v", c.id, got, c.document)
		}
	}
}

// TestShippedDetectionDefaults is §8.0's table, restated as the behaviour it
// describes. Every row of that table is here, plus the two rows it states in
// prose: package.json is plain JSON, and .tf.json is JSON and not HCL.
func TestShippedDetectionDefaults(t *testing.T) {
	for _, c := range []struct {
		name string
		want hew.FormatID
		ok   bool
	}{
		{"config.json", "json", true},
		{"package.json", "json", true},
		{"main.tf.json", "json", true},
		{"config.jsonc", "jsonc", true},
		{"settings.json", "jsonc", true},
		{"tasks.json", "jsonc", true},
		{"launch.json", "jsonc", true},
		{"tsconfig.json", "jsonc", true},
		{".mcp.json", "jsonc", true},
		{".vscode/settings.json", "jsonc", true},
		{"config.yaml", "yaml", true},
		{"config.yml", "yaml", true},
		{"config.toml", "toml", true},
		{"main.tf", "hcl", true},
		{"policy.hcl", "hcl", true},
		{"prod.tfvars", "hcl", true},
		{"job.nomad", "hcl", true},
		{"build.pkr.hcl", "hcl", true},
		{"README.md", "markdown", true},
		{"README.markdown", "markdown", true},
		{"CONFIG.YAML", "yaml", true},
		{"Makefile", "", false},
		{"config.ini", "", false},
		{"mcp.json", "json", true}, // not the well-known ".mcp.json"
	} {
		got, ok := hew.DetectFormat(c.name)
		if got != c.want || ok != c.ok {
			t.Errorf("DetectFormat(%q) = %q, %v; want %q, %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

// TestNoExtensionClaimsAnotherName guards the property DetectFormat refuses to
// guess about: no two shipped extensions may claim one extension or one
// well-known name. A collision would silently become "no detection" for a file
// that used to detect, which is a regression no single-format test can see.
func TestNoExtensionClaimsAnotherName(t *testing.T) {
	seenExt := map[string]hew.FormatID{}
	seenName := map[string]hew.FormatID{}
	for _, id := range hew.Formats() {
		b, _ := hew.Lookup(id)
		for _, e := range b.Detect.Extensions {
			if other, dup := seenExt[e]; dup {
				t.Errorf("extension %q is claimed by both %q and %q", e, other, id)
			}
			seenExt[e] = id
		}
		for _, n := range b.Detect.WellKnownNames {
			if other, dup := seenName[n]; dup {
				t.Errorf("well-known name %q is claimed by both %q and %q", n, other, id)
			}
			seenName[n] = id
		}
	}
}

// TestExtensionDeclaredVocabulary pins what §8.8 moved out of the core: the
// node kinds and transform qualifiers the extensions now own.
func TestExtensionDeclaredVocabulary(t *testing.T) {
	if !hew.NodeKind("block").Valid() {
		t.Error(`"block" is not a valid kind although ext/hcl and ext/markdown declare it`)
	}
	if !hew.NodeKind("section").Valid() {
		t.Error(`"section" is not a valid kind although ext/markdown declares it`)
	}
	if hew.NodeKind("stanza").Valid() {
		t.Error(`"stanza" is valid although no shipped extension declares it`)
	}
	if !hew.OwnsQualifier("yaml", "anchor") {
		t.Error("ext/yaml does not own `anchor:` (§9.6, O48)")
	}
	if !hew.OwnsQualifier("toml", "surface") {
		t.Error("ext/toml does not own `surface:` (§9.6, O48)")
	}
	if hew.OwnsQualifier("json", "anchor") {
		t.Error("ext/json owns `anchor:`, which is YAML's")
	}
	if b, _ := hew.Lookup("hcl"); !b.QuotedLabels {
		t.Error("ext/hcl does not declare that a quoted segment is a block label (§4.3, O48)")
	}
	for _, id := range []hew.FormatID{"json", "jsonc", "yaml", "toml", "markdown"} {
		if b, _ := hew.Lookup(id); b.QuotedLabels {
			t.Errorf("%q declares block-set labels; a quoted segment there is a KEY (§4.1, O41)", id)
		}
	}
}

// TestEveryBindingNamesTheNearestMiss is O46 across the formats that have an
// applier (§10.3). The rule is the same sentence everywhere because the wording
// is the core's (hew.NoMatchDetail) — a diagnostic that told a YAML author to
// quote a value and a TOML author only that nothing matched would be five
// answers to one question.
//
// Each target holds a version field whose value reads "1.0" as a STRING, and
// each address asks for the number. §4.2 compares after decoding, so the match
// fails for a reason the address cannot show.
func TestEveryBindingNamesTheNearestMiss(t *testing.T) {
	for _, c := range []struct {
		id     hew.FormatID
		target string
	}{
		{"json", `{"deps":[{"name":"a","version":"1.0"}]}`},
		{"jsonc", "{\n  // pinned\n  \"deps\": [{\"name\": \"a\", \"version\": \"1.0\"}]\n}"},
		{"yaml", "deps:\n  - name: a\n    version: \"1.0\"\n"},
		{"toml", "[[deps]]\nname = \"a\"\nversion = \"1.0\"\n"},
	} {
		t.Run(string(c.id), func(t *testing.T) {
			b, ok := hew.Lookup(c.id)
			if !ok || b.Applier == nil {
				t.Fatalf("%q has no applier", c.id)
			}
			tl := hew.TransformList{
				Target: "t", Format: c.id,
				Transform: []hew.Transform{{
					Op:   hew.OpRemove,
					Path: hew.MustParsePath("/deps/version=1.0"),
				}},
			}
			_, err := b.Applier([]byte(c.target), tl)
			if err == nil {
				t.Fatal("a number address must not match a string field (§4.2)")
			}
			he, ok := hewerr.As(err)
			if !ok || he.Code != hewerr.CodeNoMatch {
				t.Fatalf("want HEW013, got %v", err)
			}
			for _, want := range []string{
				`1 element has version="1.0" (string)`,
				"quote the value to match a string",
			} {
				if !strings.Contains(he.Detail, want) {
					t.Errorf("detail %q does not contain %q", he.Detail, want)
				}
			}
		})
	}
}
