package harness

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// --- synthetic corpus builders -------------------------------------------
//
// Every discovery test builds its own mini-corpus in t.TempDir. The real
// corpus is deliberately out of reach here: these tests pin the harness, not
// the fixtures.

// writeCase materializes corpusDir/<rel>/ from a name->content map.
func writeCase(t *testing.T, corpusDir, rel string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(corpusDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// corpusWith builds a one-case corpus and returns the corpus root.
func corpusWith(t *testing.T, rel string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	writeCase(t, root, rel, files)
	return root
}

func manifestYAML(name string, seams string, kind string, extra ...string) string {
	var b strings.Builder
	b.WriteString("name: " + name + "\n")
	b.WriteString("seams: " + seams + "\n")
	b.WriteString("kind: " + kind + "\n")
	b.WriteString("format: json\n")
	for _, line := range extra {
		b.WriteString(line + "\n")
	}
	return b.String()
}

// happyFiles is a directory pinning parse, apply-ir and e2e from one set of
// fixtures (corpus README, "Files" table).
func happyFiles(name string) map[string]string {
	return map[string]string{
		"case.yaml":       manifestYAML(name, "[parse, apply-ir, e2e]", "ok", "ops: [OP-01]"),
		"patch.hew":       "@ config.json\n- {\"a\": 1}\n+ {\"a\": 2}\n",
		"transforms.hewt": "transforms:\n  - op: replace\n",
		"target.json":     "{\"a\": 1}\n",
		"expected.json":   "{\"a\": 2}\n",
	}
}

// roundtripFiles is the */roundtrip-basic shape: no patch.hew, five seams
// derived from old/new/expected.hew.
func roundtripFiles(name string) map[string]string {
	return map[string]string{
		"case.yaml":         manifestYAML(name, "[parse, apply-ir, e2e, render, diff]", "ok"),
		"old.json":          "{\"a\": 1}\n",
		"new.json":          "{\"a\": 2}\n",
		"expected.hew":      "@ config.json\n- {\"a\": 1}\n+ {\"a\": 2}\n",
		"expected-ops.json": "[]\n",
	}
}

func errStrings(errs []error) []string {
	out := make([]string, 0, len(errs))
	for _, e := range errs {
		out = append(out, e.Error())
	}
	sort.Strings(out)
	return out
}

func requireNoErrs(t *testing.T, errs []error) {
	t.Helper()
	if len(errs) > 0 {
		t.Fatalf("unexpected corpus errors: %v", errStrings(errs))
	}
}

func requireErrContaining(t *testing.T, errs []error, want string) {
	t.Helper()
	for _, e := range errs {
		if strings.Contains(e.Error(), want) {
			return
		}
	}
	t.Fatalf("no corpus error mentions %q; got %v", want, errStrings(errs))
}

func requireNoErrContaining(t *testing.T, errs []error, unwanted string) {
	t.Helper()
	for _, e := range errs {
		if strings.Contains(e.Error(), unwanted) {
			t.Fatalf("unexpected corpus error mentioning %q: %v", unwanted, e)
		}
	}
}

// --- tests ---------------------------------------------------------------

func TestDiscoverHappyCase(t *testing.T) {
	root := corpusWith(t, "json/add-key", happyFiles("json/add-key"))
	cases, errs := Discover(root)
	requireNoErrs(t, errs)
	if len(cases) != 1 {
		t.Fatalf("discovered %d cases, want 1", len(cases))
	}
	c := cases[0]
	if c.Rel != "json/add-key" {
		t.Errorf("Rel = %q", c.Rel)
	}
	if c.Dir != filepath.Join(root, "json", "add-key") {
		t.Errorf("Dir = %q", c.Dir)
	}
	if c.Name != "json/add-key" || c.Kind != "ok" || c.Format != "json" {
		t.Errorf("manifest not embedded: %+v", c.Manifest)
	}
	if c.PatchFile != "patch.hew" {
		t.Errorf("PatchFile = %q", c.PatchFile)
	}
	if c.TransformsFile != "transforms.hewt" {
		t.Errorf("TransformsFile = %q", c.TransformsFile)
	}
	if c.TargetFile != "target.json" {
		t.Errorf("TargetFile = %q", c.TargetFile)
	}
	if c.ExpectedFile != "expected.json" {
		t.Errorf("ExpectedFile = %q", c.ExpectedFile)
	}
	if c.ExpectedHewFile != "" || c.ExpectedOpsFile != "" || c.OldFile != "" || c.NewFile != "" {
		t.Errorf("diff fixtures must be empty: %+v", c)
	}
	if c.Roundtrip {
		t.Error("Roundtrip must be false when patch.hew is present")
	}
}

// TestDiscoverFixtureClassification pins which filename lands in which field —
// notably that expected.hew and expected-ops.json are NOT the expected.* the
// apply seams compare against.
func TestDiscoverFixtureClassification(t *testing.T) {
	files := map[string]string{
		"case.yaml":         manifestYAML("json/all", "[parse]", "ok"),
		"patch.hew":         "p\n",
		"transforms.hewt":   "t\n",
		"target.json":       "tg\n",
		"expected.json":     "e\n",
		"expected.hew":      "eh\n",
		"expected-ops.json": "[]\n",
		"old.json":          "o\n",
		"new.json":          "n\n",
	}
	root := corpusWith(t, "json/all", files)
	cases, errs := Discover(root)
	requireNoErrs(t, errs)
	c := cases[0]
	got := map[string]string{
		"patch": c.PatchFile, "transforms": c.TransformsFile, "target": c.TargetFile,
		"expected": c.ExpectedFile, "expectedHew": c.ExpectedHewFile,
		"expectedOps": c.ExpectedOpsFile, "old": c.OldFile, "new": c.NewFile,
	}
	want := map[string]string{
		"patch": "patch.hew", "transforms": "transforms.hewt", "target": "target.json",
		"expected": "expected.json", "expectedHew": "expected.hew",
		"expectedOps": "expected-ops.json", "old": "old.json", "new": "new.json",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s file = %q, want %q", k, got[k], v)
		}
	}
	if c.Roundtrip {
		t.Error("a case with patch.hew is never a roundtrip case")
	}
}

func TestDiscoverRoundtripShape(t *testing.T) {
	root := corpusWith(t, "json/roundtrip-basic", roundtripFiles("json/roundtrip-basic"))
	cases, errs := Discover(root)
	requireNoErrs(t, errs)
	c := cases[0]
	if !c.Roundtrip {
		t.Fatal("old/new/expected.hew without patch.hew must set Roundtrip")
	}
	if c.PatchFile != "" || c.TransformsFile != "" || c.TargetFile != "" || c.ExpectedFile != "" {
		t.Errorf("roundtrip case must have no per-seam fixtures: %+v", c)
	}
	if c.OldFile != "old.json" || c.NewFile != "new.json" || c.ExpectedHewFile != "expected.hew" {
		t.Errorf("roundtrip fixtures = %q/%q/%q", c.OldFile, c.NewFile, c.ExpectedHewFile)
	}
	if c.ExpectedOpsFile != "expected-ops.json" {
		t.Errorf("ExpectedOpsFile = %q", c.ExpectedOpsFile)
	}
	if len(c.Seams) != 5 {
		t.Fatalf("expected five declared seams, got %v", c.Seams)
	}
}

// TestRoundtripRequiresAllThreeFiles pins the exact conjunction that turns a
// directory into a roundtrip case.
func TestRoundtripDetection(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  bool
	}{
		{
			name: "old+new+expected.hew, no patch",
			files: map[string]string{
				"case.yaml": manifestYAML("f/c", "[diff]", "ok"), "old.json": "o", "new.json": "n", "expected.hew": "e",
			},
			want: true,
		},
		{
			name: "patch present disqualifies",
			files: map[string]string{
				"case.yaml": manifestYAML("f/c", "[diff]", "ok"), "patch.hew": "p",
				"old.json": "o", "new.json": "n", "expected.hew": "e",
			},
			want: false,
		},
		{
			name: "no old file",
			files: map[string]string{
				"case.yaml": manifestYAML("f/c", "[render]", "ok"), "new.json": "n", "expected.hew": "e", "transforms.hewt": "t",
			},
			want: false,
		},
		{
			name: "no new file",
			files: map[string]string{
				"case.yaml": manifestYAML("f/c", "[render]", "ok"), "old.json": "o", "expected.hew": "e", "transforms.hewt": "t",
			},
			want: false,
		},
		{
			name: "no expected.hew",
			files: map[string]string{
				"case.yaml": manifestYAML("f/c", "[render]", "ok"), "old.json": "o", "new.json": "n", "transforms.hewt": "t",
			},
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := corpusWith(t, "f/c", tc.files)
			cases, _ := Discover(root)
			if len(cases) != 1 {
				t.Fatalf("discovered %d cases", len(cases))
			}
			if cases[0].Roundtrip != tc.want {
				t.Errorf("Roundtrip = %v, want %v", cases[0].Roundtrip, tc.want)
			}
		})
	}
}

func TestDiscoverMissingDeclaredFiles(t *testing.T) {
	tests := []struct {
		name   string
		files  map[string]string
		want   []string
		absent []string
	}{
		{
			name: "parse seam without patch.hew",
			files: map[string]string{
				"case.yaml": manifestYAML("json/c", "[parse]", "ok"), "transforms.hewt": "t",
			},
			want: []string{"declares seam parse but has no patch.hew"},
		},
		{
			// A transforms fixture is optional on an ok-kind parse seam: some
			// cases (e.g. yaml/pragma-idempotent-file) declare parse to pin
			// that the patch TEXT carries a property, not the lowered list —
			// parse succeeding is the whole assertion then (engine.runParse).
			name: "ok parse seam without transforms fixture is not a corpus error",
			files: map[string]string{
				"case.yaml": manifestYAML("json/c", "[parse]", "ok"), "patch.hew": "p",
			},
			absent: []string{"transforms fixture"},
		},
		{
			name: "error-kind parse seam needs no transforms fixture",
			files: map[string]string{
				"case.yaml": manifestYAML("json/c", "[parse]", "error",
					"error: HEW001", "error_seam: parse", "message_contains: [parse-error]"),
				"patch.hew": "p",
			},
			absent: []string{"transforms fixture"},
		},
		{
			name: "apply-ir seam missing everything",
			files: map[string]string{
				"case.yaml": manifestYAML("json/c", "[apply-ir]", "ok"),
			},
			want: []string{
				"declares seam apply-ir but has no transforms fixture",
				"declares seam apply-ir but has no target.*",
				"declares seam apply-ir but has no expected.*",
			},
		},
		{
			name: "apply-ir error kind needs no expected",
			files: map[string]string{
				"case.yaml": manifestYAML("json/c", "[apply-ir]", "error",
					"error: HEW010", "error_seam: apply-ir", "message_contains: [stale-target]"),
				"transforms.hewt": "t", "target.json": "tg",
			},
			absent: []string{"expected.*"},
		},
		{
			name: "e2e seam missing patch and target",
			files: map[string]string{
				"case.yaml": manifestYAML("json/c", "[e2e]", "ok"), "expected.json": "e",
			},
			want: []string{
				"declares seam e2e but has no patch.hew",
				"declares seam e2e but has no target.*",
			},
			absent: []string{"seam e2e but has no expected"},
		},
		{
			name: "e2e error kind needs no expected",
			files: map[string]string{
				"case.yaml": manifestYAML("json/c", "[e2e]", "error",
					"error: HEW013", "error_seam: apply-ir", "message_contains: [no-match]"),
				"patch.hew": "p", "target.json": "tg",
			},
			absent: []string{"corpus error"},
		},
		{
			name: "render seam without transforms",
			files: map[string]string{
				"case.yaml": manifestYAML("json/c", "[render]", "ok"),
			},
			want: []string{"declares seam render but has no transforms fixture"},
		},
		{
			name: "diff seam without old/new/expected.hew",
			files: map[string]string{
				"case.yaml": manifestYAML("json/c", "[diff]", "ok"), "old.json": "o",
			},
			want: []string{"declares seam diff but has no old.*/new.*/expected.hew"},
		},
		{
			name: "diff seam complete",
			files: map[string]string{
				"case.yaml": manifestYAML("json/c", "[diff]", "ok"),
				"old.json":  "o", "new.json": "n", "expected.hew": "e",
			},
			absent: []string{"corpus error"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := corpusWith(t, "json/c", tc.files)
			_, errs := Discover(root)
			for _, want := range tc.want {
				requireErrContaining(t, errs, want)
			}
			for _, bad := range tc.absent {
				requireNoErrContaining(t, errs, bad)
			}
			if len(tc.want) > 0 && len(errs) < len(tc.want) {
				t.Errorf("want at least %d errors, got %v", len(tc.want), errStrings(errs))
			}
		})
	}
}

// TestDiscoverRoundtripDeclaresFiveSeamsWithoutMissingFileErrors is runner
// obligation 5 read the other way: the roundtrip shape legitimately lacks
// patch.hew/transforms.hewt/target.*, and must not be reported as broken.
func TestDiscoverRoundtripHasNoMissingFileErrors(t *testing.T) {
	root := corpusWith(t, "yaml/roundtrip-basic", roundtripFiles("yaml/roundtrip-basic"))
	_, errs := Discover(root)
	if len(errs) != 0 {
		t.Fatalf("roundtrip case must be error-free, got %v", errStrings(errs))
	}
}

func TestDiscoverStrayFileInFamilyDir(t *testing.T) {
	root := corpusWith(t, "json/add-key", happyFiles("json/add-key"))
	if err := os.WriteFile(filepath.Join(root, "json", "NOTES.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases, errs := Discover(root)
	requireErrContaining(t, errs, "stray file json/NOTES.md")
	requireErrContaining(t, errs, "families contain only case directories")
	if len(cases) != 1 {
		t.Errorf("the valid sibling case must still be discovered, got %d", len(cases))
	}
}

// TestDiscoverIgnoresCorpusRootFiles: corpus/README.md is not a family.
func TestDiscoverIgnoresCorpusRootFiles(t *testing.T) {
	root := corpusWith(t, "json/add-key", happyFiles("json/add-key"))
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# corpus"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases, errs := Discover(root)
	requireNoErrs(t, errs)
	if len(cases) != 1 {
		t.Fatalf("discovered %d cases, want 1", len(cases))
	}
}

func TestDiscoverCaseWithoutManifest(t *testing.T) {
	root := t.TempDir()
	writeCase(t, root, "json/no-manifest", map[string]string{"patch.hew": "p"})
	cases, errs := Discover(root)
	requireErrContaining(t, errs, "json/no-manifest has no readable case.yaml")
	if len(cases) != 0 {
		t.Errorf("a case without case.yaml must not be discovered, got %d", len(cases))
	}
}

func TestDiscoverUnparseableManifest(t *testing.T) {
	root := corpusWith(t, "json/bad", map[string]string{
		"case.yaml": "name: json/bad\nbogus_field: 1\n",
	})
	cases, errs := Discover(root)
	requireErrContaining(t, errs, "corpus error in json/bad")
	requireErrContaining(t, errs, "bogus_field")
	if len(cases) != 0 {
		t.Errorf("an undecodable manifest yields no case, got %d", len(cases))
	}
}

// TestDiscoverInvalidManifestStillYieldsCase: a schema-invalid manifest is an
// error AND a case, so downstream reporting can name every seam it declared.
func TestDiscoverInvalidManifestStillYieldsCase(t *testing.T) {
	root := corpusWith(t, "json/mismatch", map[string]string{
		"case.yaml":       manifestYAML("json/WRONG", "[parse]", "ok"),
		"patch.hew":       "p",
		"transforms.hewt": "t",
	})
	cases, errs := Discover(root)
	requireErrContaining(t, errs, `name "json/WRONG" != directory "json/mismatch"`)
	if len(cases) != 1 {
		t.Fatalf("discovered %d cases, want 1", len(cases))
	}
}

func TestDiscoverAmbiguousFixtures(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{
			name: "two target files",
			files: map[string]string{
				"case.yaml":       manifestYAML("json/c", "[apply-ir]", "ok"),
				"transforms.hewt": "t", "expected.json": "e",
				"target.json": "a", "target.yaml": "b",
			},
			want: "json/c has 2 target.* files",
		},
		{
			name: "two expected files",
			files: map[string]string{
				"case.yaml":       manifestYAML("json/c", "[apply-ir]", "ok"),
				"transforms.hewt": "t", "target.json": "tg",
				"expected.json": "a", "expected.yaml": "b",
			},
			want: "json/c has 2 expected.* files",
		},
		{
			name: "two hewt files, none named transforms.hewt",
			files: map[string]string{
				"case.yaml": manifestYAML("json/c", "[render]", "ok"),
				"move.hewt": "a", "copy.hewt": "b",
			},
			want: "json/c has 2 .hewt files and none named transforms.hewt",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := corpusWith(t, "json/c", tc.files)
			_, errs := Discover(root)
			requireErrContaining(t, errs, tc.want)
		})
	}
}

// TestDiscoverSoleNonTransformsHewt: cli/apply-transforms ships move.hewt,
// named after what argv names. A lone .hewt is the transforms fixture.
func TestDiscoverSoleNonTransformsHewt(t *testing.T) {
	root := corpusWith(t, "cli/apply-transforms", map[string]string{
		"case.yaml":   manifestYAML("cli/apply-transforms", "[render]", "ok"),
		"move.hewt":   "transforms:\n",
		"target.json": "{}\n",
	})
	cases, errs := Discover(root)
	requireNoErrs(t, errs)
	if cases[0].TransformsFile != "move.hewt" {
		t.Errorf("TransformsFile = %q, want move.hewt", cases[0].TransformsFile)
	}
}

// TestDiscoverTransformsWinsOverSiblingHewt: when transforms.hewt exists, a
// second .hewt is not ambiguous.
func TestDiscoverTransformsWinsOverSiblingHewt(t *testing.T) {
	root := corpusWith(t, "cli/two-hewt", map[string]string{
		"case.yaml":       manifestYAML("cli/two-hewt", "[render]", "ok"),
		"transforms.hewt": "t\n",
		"move.hewt":       "m\n",
	})
	cases, errs := Discover(root)
	requireNoErrs(t, errs)
	if cases[0].TransformsFile != "transforms.hewt" {
		t.Errorf("TransformsFile = %q, want transforms.hewt", cases[0].TransformsFile)
	}
}

func TestDiscoverCLIFixtureFilesMustExist(t *testing.T) {
	base := func(extra ...string) map[string]string {
		return map[string]string{
			"case.yaml": manifestYAML("cli/c", "[cli]", "cli",
				append([]string{"argv: [apply, patch.hew]", "exit: 0"}, extra...)...),
			"patch.hew":   "p\n",
			"target.json": "{}\n",
		}
	}
	tests := []struct {
		name   string
		files  map[string]string
		want   string
		absent bool
	}{
		{
			name:  "missing stdout fixture",
			files: base("stdout: out.txt"),
			want:  "declares seam cli but has no stdout fixture out.txt",
		},
		{
			name:  "missing expected fixture",
			files: base(`expected: expected.json`),
			want:  "declares seam cli but has no expected fixture expected.json",
		},
		{
			name:   "stdout asserted empty needs no fixture",
			files:  base(`stdout: ""`),
			absent: true,
		},
		{
			name:   "stdout unasserted needs no fixture",
			files:  base(),
			absent: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := corpusWith(t, "cli/c", tc.files)
			_, errs := Discover(root)
			if tc.absent {
				requireNoErrs(t, errs)
				return
			}
			requireErrContaining(t, errs, tc.want)
		})
	}
}

// TestDiscoverCLIFixturesPresent: the same manifests pass once the fixtures
// exist, so the check is about the files and not about the fields.
func TestDiscoverCLIFixturesPresent(t *testing.T) {
	root := corpusWith(t, "cli/c", map[string]string{
		"case.yaml": manifestYAML("cli/c", "[cli]", "cli",
			"argv: [apply, patch.hew]", "exit: 0", "stdout: out.txt", "expected: expected.json"),
		"patch.hew":     "p\n",
		"target.json":   "{}\n",
		"out.txt":       "ok\n",
		"expected.json": "{}\n",
	})
	_, errs := Discover(root)
	requireNoErrs(t, errs)
}

func TestDiscoverMultipleFamiliesAndCases(t *testing.T) {
	root := t.TempDir()
	writeCase(t, root, "json/a", happyFiles("json/a"))
	writeCase(t, root, "json/b", happyFiles("json/b"))
	writeCase(t, root, "yaml/a", happyFiles("yaml/a"))
	cases, errs := Discover(root)
	requireNoErrs(t, errs)
	var rels []string
	for _, c := range cases {
		rels = append(rels, c.Rel)
	}
	sort.Strings(rels)
	want := []string{"json/a", "json/b", "yaml/a"}
	if strings.Join(rels, ",") != strings.Join(want, ",") {
		t.Errorf("discovered %v, want %v", rels, want)
	}
}

func TestDiscoverUnreadableRoot(t *testing.T) {
	cases, errs := Discover(filepath.Join(t.TempDir(), "nope"))
	if cases != nil {
		t.Errorf("cases = %v, want nil", cases)
	}
	if len(errs) != 1 {
		t.Fatalf("errs = %v, want exactly one", errStrings(errs))
	}
	if !strings.Contains(errs[0].Error(), "corpus root") {
		t.Errorf("error %q must name the corpus root", errs[0])
	}
}

func TestDiscoverEmptyCorpus(t *testing.T) {
	cases, errs := Discover(t.TempDir())
	requireNoErrs(t, errs)
	if len(cases) != 0 {
		t.Errorf("cases = %v, want none", cases)
	}
}

// TestDiscoverEmptyFamily: a family directory with no cases is not an error,
// only an empty family.
func TestDiscoverEmptyFamily(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "toml"), 0o755); err != nil {
		t.Fatal(err)
	}
	cases, errs := Discover(root)
	requireNoErrs(t, errs)
	if len(cases) != 0 {
		t.Errorf("cases = %v, want none", cases)
	}
}

// TestDiscoverUnreadableFamily exercises the family-level ReadDir error path.
func TestDiscoverUnreadableFamily(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	root := t.TempDir()
	fam := filepath.Join(root, "json")
	if err := os.MkdirAll(fam, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(fam, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(fam, 0o755) })
	_, errs := Discover(root)
	requireErrContaining(t, errs, "family json:")
}

// TestDiscoverUnreadableCaseDir exercises resolveFixtures' ReadDir error path:
// case.yaml is readable (opened by name) but the directory listing is not.
func TestDiscoverUnreadableCaseDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	root := corpusWith(t, "json/c", map[string]string{
		"case.yaml": manifestYAML("json/c", "[parse]", "ok"),
	})
	dir := filepath.Join(root, "json", "c")
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	// 0o500 still lists; drop read to break ReadDir while keeping traversal.
	if err := os.Chmod(dir, 0o100); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	_, errs := Discover(root)
	requireErrContaining(t, errs, "json/c")
}
