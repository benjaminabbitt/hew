package harness

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// syntheticSpec reproduces every §11 entry shape the real spec uses, without
// depending on the real spec: a plain header block, a combined header sharing
// one Status, a deferred entry, a header carrying no OP id, and the §11.9
// table tail.
const syntheticSpec = "# Spec\n" +
	"\n" +
	"## §11 Operations catalog\n" +
	"\n" +
	"#### OP-01 `set-scalar` — replace the value of an existing key\n" +
	"**Status** v0 · **Disp** `CORE` — `replace` · **Sources** 6902\n" +
	"**Absent/empty** Key absent → `HEW013`.\n" +
	"\n" +
	"#### OP-02 `add-key`\n" +
	"**Status** v0 · **Disp** `CORE` — `add`\n" +
	"\n" +
	"#### OP-32 `remove-comment` · #### OP-33 `replace-comment-text`\n" +
	"**Status** v0 · **Disp** `CORE` — `remove`, comment address\n" +
	"**Mirror** `- # old note` / `+ # new note`\n" +
	"\n" +
	"#### OP-40 `deferred-thing`\n" +
	"**Status** deferred · **Disp** `OUT`\n" +
	"Condition to add: a named case.\n" +
	"\n" +
	"#### Notes on addressing\n" +
	"**Status** v0 — this block declares no operation and must be ignored.\n" +
	"\n" +
	"#### OP-41 `statusless`\n" +
	"No status line at all.\n" +
	"\n" +
	"### §11.9 Tail entries\n" +
	"\n" +
	"| ID | Name | Sources | Tier | Note |\n" +
	"|---|---|---|---|---|\n" +
	"| OP-50 | `append-only-idempotent-block` | M8 | deferred | Needs a text binding. |\n" +
	"| OP-52 | `whole-file-replace` | M4, kiro | v0 | A replace at the root anchor. |\n"

func writeSpec(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "hew-spec.md")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func catalogMap(ops []CatalogOp) map[string]string {
	m := make(map[string]string, len(ops))
	for _, op := range ops {
		m[op.ID] = op.Tier
	}
	return m
}

func TestLoadCatalog(t *testing.T) {
	ops, err := LoadCatalog(writeSpec(t, syntheticSpec))
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	got := catalogMap(ops)
	want := map[string]string{
		"OP-01": "v0",
		"OP-02": "v0",
		"OP-32": "v0", // combined header, shared Status
		"OP-33": "v0",
		"OP-40": "deferred",
		"OP-41": "", // no Status line
		"OP-50": "deferred",
		"OP-52": "v0", // §11.9 table row
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("catalog = %v, want %v", got, want)
	}
	if len(ops) != len(want) {
		t.Errorf("catalog has %d entries, want %d (duplicates?)", len(ops), len(want))
	}
}

// TestLoadCatalogPreservesSpecOrder: the catalog is reported in reading order,
// header blocks before the table tail.
func TestLoadCatalogOrder(t *testing.T) {
	ops, err := LoadCatalog(writeSpec(t, syntheticSpec))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, op := range ops {
		ids = append(ids, op.ID)
	}
	want := []string{"OP-01", "OP-02", "OP-32", "OP-33", "OP-40", "OP-41", "OP-50", "OP-52"}
	if !reflect.DeepEqual(ids, want) {
		t.Errorf("ids = %v, want %v", ids, want)
	}
}

func TestLoadCatalogShapes(t *testing.T) {
	tests := []struct {
		name string
		spec string
		want map[string]string
	}{
		{
			name: "single block",
			spec: "#### OP-07 `rename-key`\n**Status** v0 · rest\n",
			want: map[string]string{"OP-07": "v0"},
		},
		{
			name: "three ops on one header line share the status",
			spec: "#### OP-34 a · #### OP-35 b · #### OP-36 c\n**Status** v0\n",
			want: map[string]string{"OP-34": "v0", "OP-35": "v0", "OP-36": "v0"},
		},
		{
			name: "status must be on the block, not the next block",
			spec: "#### OP-10 a\nbody with no status\n\n#### OP-11 b\n**Status** v0\n",
			want: map[string]string{"OP-10": "", "OP-11": "v0"},
		},
		{
			name: "op id appearing only in body text is not an entry",
			spec: "#### OP-12 a\n**Status** v0\nSee also OP-99 for the deferred variant.\n",
			want: map[string]string{"OP-12": "v0"},
		},
		{
			name: "first definition wins over a later duplicate",
			spec: "#### OP-20 a\n**Status** v0\n\n#### OP-20 a again\n**Status** deferred\n",
			want: map[string]string{"OP-20": "v0"},
		},
		{
			name: "header block wins over a table row for the same id",
			spec: "#### OP-52 a\n**Status** v0\n\n| OP-52 | n | s | deferred | note |\n",
			want: map[string]string{"OP-52": "v0"},
		},
		{
			name: "table row needs four columns",
			spec: "#### OP-01 a\n**Status** v0\n\n| OP-60 | name | v0 |\n",
			want: map[string]string{"OP-01": "v0"},
		},
		{
			name: "indented table row is not a catalog row",
			spec: "#### OP-01 a\n**Status** v0\n\n  | OP-61 | name | src | v0 | note |\n",
			want: map[string]string{"OP-01": "v0"},
		},
		{
			name: "deeper heading level is not an op block",
			spec: "#### OP-01 a\n**Status** v0\n\n##### OP-62 sub\n**Status** v0\n",
			want: map[string]string{"OP-01": "v0"},
		},
		{
			name: "shallower heading level is not an op block",
			spec: "#### OP-01 a\n**Status** v0\n\n### OP-63 section\n**Status** v0\n",
			want: map[string]string{"OP-01": "v0"},
		},
		{
			name: "bolded status value yields no tier (only v0 gates coverage)",
			spec: "#### OP-39 a\n**Status** **deferred** · rest\n",
			want: map[string]string{"OP-39": ""},
		},
		{
			name: "final block runs to end of file",
			spec: "#### OP-01 a\n**Status** v0\n\n#### OP-70 last\n**Status** v0",
			want: map[string]string{"OP-01": "v0", "OP-70": "v0"},
		},
		{
			name: "table row only",
			spec: "| OP-52 | `whole-file-replace` | M4 | v0 | note |\n",
			want: map[string]string{"OP-52": "v0"},
		},
		{
			name: "multi-digit op ids",
			spec: "#### OP-100 a\n**Status** v0\n",
			want: map[string]string{"OP-100": "v0"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ops, err := LoadCatalog(writeSpec(t, tc.spec))
			if err != nil {
				t.Fatalf("LoadCatalog: %v", err)
			}
			if got := catalogMap(ops); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("catalog = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLoadCatalogErrors(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		ops, err := LoadCatalog(filepath.Join(t.TempDir(), "nope.md"))
		if err == nil {
			t.Fatalf("want an error, got %v", ops)
		}
		if ops != nil {
			t.Errorf("ops = %v, want nil", ops)
		}
	})
	t.Run("no catalog entries", func(t *testing.T) {
		p := writeSpec(t, "# Spec\n\n#### Overview\nno operations here\n")
		ops, err := LoadCatalog(p)
		if err == nil {
			t.Fatalf("want an error, got %v", ops)
		}
		if !strings.Contains(err.Error(), "no OP-nn catalog entries found") || !strings.Contains(err.Error(), p) {
			t.Errorf("error %q must explain itself and name the file", err)
		}
	})
	t.Run("empty file", func(t *testing.T) {
		if _, err := LoadCatalog(writeSpec(t, "")); err == nil {
			t.Fatal("an empty spec has no catalog")
		}
	})
}

func TestIndexByte(t *testing.T) {
	tests := []struct {
		in   string
		c    byte
		want int
	}{
		{"abc\ndef", '\n', 3},
		{"\nabc", '\n', 0},
		{"abc", '\n', -1},
		{"", '\n', -1},
		{"aa", 'a', 0},
	}
	for _, tc := range tests {
		if got := indexByte([]byte(tc.in), tc.c); got != tc.want {
			t.Errorf("indexByte(%q, %q) = %d, want %d", tc.in, tc.c, got, tc.want)
		}
	}
}

func opsCase(rel string, ops ...string) *Case {
	return &Case{Manifest: Manifest{Ops: ops}, Rel: rel}
}

func TestComputeCoverage(t *testing.T) {
	catalog := []CatalogOp{
		{ID: "OP-01", Tier: "v0"},
		{ID: "OP-02", Tier: "v0"},
		{ID: "OP-03", Tier: "v0"},
		{ID: "OP-40", Tier: "deferred"},
		{ID: "OP-51", Tier: "out"},
		{ID: "OP-41", Tier: ""},
	}
	cases := []*Case{
		opsCase("json/a", "OP-01", "OP-02"),
		opsCase("json/b", "OP-01"),
		opsCase("yaml/c", "OP-40", "OP-99"),
		opsCase("toml/d", "OP-77"),
		opsCase("toml/e"),
	}
	cov := ComputeCoverage(catalog, cases)

	wantCounts := map[string]int{"OP-01": 2, "OP-02": 1, "OP-40": 1}
	if !reflect.DeepEqual(cov.CasesPerOp, wantCounts) {
		t.Errorf("CasesPerOp = %v, want %v", cov.CasesPerOp, wantCounts)
	}
	wantUnknown := []string{"toml/d: OP-77", "yaml/c: OP-99"}
	if !reflect.DeepEqual(cov.UnknownRefs, wantUnknown) {
		t.Errorf("UnknownRefs = %v, want %v (sorted, case-qualified)", cov.UnknownRefs, wantUnknown)
	}
	wantUncovered := []string{"OP-03"}
	if !reflect.DeepEqual(cov.UncoveredV0, wantUncovered) {
		t.Errorf("UncoveredV0 = %v, want %v (only v0-tier ops)", cov.UncoveredV0, wantUncovered)
	}
}

func TestComputeCoverageSorting(t *testing.T) {
	catalog := []CatalogOp{
		{ID: "OP-09", Tier: "v0"},
		{ID: "OP-03", Tier: "v0"},
		{ID: "OP-05", Tier: "v0"},
	}
	cases := []*Case{
		opsCase("z/z", "OP-XX"),
		opsCase("a/a", "OP-YY"),
		opsCase("m/m", "OP-AA"),
	}
	cov := ComputeCoverage(catalog, cases)
	if !reflect.DeepEqual(cov.UncoveredV0, []string{"OP-03", "OP-05", "OP-09"}) {
		t.Errorf("UncoveredV0 = %v, want sorted", cov.UncoveredV0)
	}
	if !reflect.DeepEqual(cov.UnknownRefs, []string{"a/a: OP-YY", "m/m: OP-AA", "z/z: OP-XX"}) {
		t.Errorf("UnknownRefs = %v, want sorted", cov.UnknownRefs)
	}
}

func TestComputeCoverageEdgeCases(t *testing.T) {
	t.Run("no cases leaves every v0 op uncovered", func(t *testing.T) {
		cov := ComputeCoverage([]CatalogOp{{ID: "OP-01", Tier: "v0"}, {ID: "OP-40", Tier: "deferred"}}, nil)
		if !reflect.DeepEqual(cov.UncoveredV0, []string{"OP-01"}) {
			t.Errorf("UncoveredV0 = %v", cov.UncoveredV0)
		}
		if len(cov.CasesPerOp) != 0 || cov.UnknownRefs != nil {
			t.Errorf("CasesPerOp = %v, UnknownRefs = %v", cov.CasesPerOp, cov.UnknownRefs)
		}
	})
	t.Run("empty catalog makes every reference unknown", func(t *testing.T) {
		cov := ComputeCoverage(nil, []*Case{opsCase("json/a", "OP-01")})
		if !reflect.DeepEqual(cov.UnknownRefs, []string{"json/a: OP-01"}) {
			t.Errorf("UnknownRefs = %v", cov.UnknownRefs)
		}
		if len(cov.UncoveredV0) != 0 {
			t.Errorf("UncoveredV0 = %v, want none", cov.UncoveredV0)
		}
	})
	t.Run("a repeated op inside one case counts twice", func(t *testing.T) {
		cov := ComputeCoverage([]CatalogOp{{ID: "OP-01", Tier: "v0"}}, []*Case{opsCase("json/a", "OP-01", "OP-01")})
		if cov.CasesPerOp["OP-01"] != 2 {
			t.Errorf("CasesPerOp[OP-01] = %d, want 2", cov.CasesPerOp["OP-01"])
		}
		if len(cov.UncoveredV0) != 0 {
			t.Errorf("UncoveredV0 = %v, want none", cov.UncoveredV0)
		}
	})
	t.Run("unknown reference does not count as coverage", func(t *testing.T) {
		cov := ComputeCoverage([]CatalogOp{{ID: "OP-01", Tier: "v0"}}, []*Case{opsCase("json/a", "OP-1")})
		if _, ok := cov.CasesPerOp["OP-1"]; ok {
			t.Error("an unknown ref must not appear in CasesPerOp")
		}
		if !reflect.DeepEqual(cov.UncoveredV0, []string{"OP-01"}) {
			t.Errorf("UncoveredV0 = %v, want OP-01 still uncovered", cov.UncoveredV0)
		}
	})
	t.Run("non-v0 tiers are never reported uncovered", func(t *testing.T) {
		catalog := []CatalogOp{
			{ID: "OP-40", Tier: "deferred"}, {ID: "OP-51", Tier: "out of scope"},
			{ID: "OP-41", Tier: ""}, {ID: "OP-42", Tier: "V0"},
		}
		cov := ComputeCoverage(catalog, nil)
		if len(cov.UncoveredV0) != 0 {
			t.Errorf("UncoveredV0 = %v, want none: only the exact tier %q counts", cov.UncoveredV0, "v0")
		}
	})
}

// TestComputeCoverageAgainstDiscoveredCases wires the two halves together on a
// synthetic corpus, the way the corpus frontend does.
func TestComputeCoverageAgainstDiscoveredCases(t *testing.T) {
	root := t.TempDir()
	writeCase(t, root, "json/add-key", happyFiles("json/add-key")) // ops: [OP-01]
	files := happyFiles("json/typo")
	files["case.yaml"] = manifestYAML("json/typo", "[parse, apply-ir, e2e]", "ok", "ops: [OP-999]")
	writeCase(t, root, "json/typo", files)

	cases, errs := Discover(root)
	requireNoErrs(t, errs)
	catalog, err := LoadCatalog(writeSpec(t, syntheticSpec))
	if err != nil {
		t.Fatal(err)
	}
	cov := ComputeCoverage(catalog, cases)
	if cov.CasesPerOp["OP-01"] != 1 {
		t.Errorf("CasesPerOp[OP-01] = %d, want 1", cov.CasesPerOp["OP-01"])
	}
	if !reflect.DeepEqual(cov.UnknownRefs, []string{"json/typo: OP-999"}) {
		t.Errorf("UnknownRefs = %v", cov.UnknownRefs)
	}
	if !reflect.DeepEqual(cov.UncoveredV0, []string{"OP-02", "OP-32", "OP-33", "OP-52"}) {
		t.Errorf("UncoveredV0 = %v", cov.UncoveredV0)
	}
}
