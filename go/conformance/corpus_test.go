// Package conformance runs the shared corpus (repo-level corpus/) against
// this implementation. It is a thin frontend over internal/harness — the
// godog acceptance suite in this package drives the same engine.
package conformance

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	"github.com/hew-format/hew/internal/harness"
)

var (
	initOnce    sync.Once
	gEngine     *harness.Engine
	gCases      []*harness.Case
	gCorpusErrs []error
	gRepoRoot   string
	gInitErr    error
)

// skipSlow guards the expensive suites so mutation runs (HEW_SKIP_SLOW_SUITES=1)
// kill mutants with unit tests only. This is dev tooling, outside the
// corpus-skip discipline.
func skipSlow(t *testing.T) {
	t.Helper()
	if os.Getenv("HEW_SKIP_SLOW_SUITES") == "1" {
		t.Skip("mutation run: unit-test killers only")
	}
}

// repoRoot walks up from the package directory until it finds the corpus and
// justfile that mark the repository root.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if st, err := os.Stat(filepath.Join(dir, "corpus")); err == nil && st.IsDir() {
			if _, err := os.Stat(filepath.Join(dir, "justfile")); err == nil {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("repo root (corpus/ + justfile) not found above test dir")
		}
		dir = parent
	}
}

func initCorpus() {
	initOnce.Do(func() {
		root, err := repoRoot()
		if err != nil {
			gInitErr = err
			return
		}
		gRepoRoot = root
		corpusDir := filepath.Join(root, "corpus")
		gCases, gCorpusErrs = harness.Discover(corpusDir)
		scratchRoot, err := os.MkdirTemp("", "hew-corpus-*")
		if err != nil {
			gInitErr = err
			return
		}
		gEngine = &harness.Engine{
			CorpusDir: corpusDir,
			Bind:      newBinding(),
			Skips:     harness.NewSkipRegistry(skipRules, os.Getenv("HEW_CORPUS_NO_SKIPS") == "1"),
			Scratch: func() (string, error) {
				return os.MkdirTemp(scratchRoot, "run-*")
			},
		}
	})
}

func engine(t testing.TB) (*harness.Engine, []*harness.Case) {
	t.Helper()
	initCorpus()
	if gInitErr != nil {
		t.Fatalf("corpus init: %v", gInitErr)
	}
	return gEngine, gCases
}

// TestCorpus runs every declared seam of every corpus case independently
// (runner obligation 2: collapsing seams is non-conformant).
func TestCorpus(t *testing.T) {
	skipSlow(t)
	eng, cases := engine(t)
	for _, err := range gCorpusErrs {
		t.Errorf("%v", err)
	}
	if len(cases) == 0 {
		t.Fatal("no corpus cases discovered")
	}
	for _, c := range cases {
		for _, seam := range c.SortedSeams() {
			t.Run(c.Rel+"/"+string(seam), func(t *testing.T) {
				out := eng.RunSeam(c, seam)
				switch out.Status {
				case harness.StatusPass:
				case harness.StatusSkip:
					t.Skip(out.Detail)
				default:
					t.Errorf("[%s] %s", out.Status, out.Detail)
				}
			})
		}
	}
}

// TestCorpusCoverage enforces runner obligation 7: unknown ops: references
// are corpus errors; v0 catalog ops with no case are reported (the spec is
// known to cite cases that do not exist — spec bugs, reported not fatal).
func TestCorpusCoverage(t *testing.T) {
	skipSlow(t)
	_, cases := engine(t)
	catalog, err := harness.LoadCatalog(filepath.Join(gRepoRoot, "docs", "hew-spec.md"))
	if err != nil {
		t.Fatalf("loading §11 catalog: %v", err)
	}
	cov := harness.ComputeCoverage(catalog, cases)
	for _, ref := range cov.UnknownRefs {
		t.Errorf("ops: reference names no catalog entry: %s", ref)
	}
	if len(cov.UncoveredV0) > 0 {
		t.Logf("v0 catalog ops with no corpus case (spec-bug candidates): %v", cov.UncoveredV0)
	}
	ids := make([]string, 0, len(cov.CasesPerOp))
	for id := range cov.CasesPerOp {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		t.Logf("%s: %d case(s)", id, cov.CasesPerOp[id])
	}
}

// TestCorpusSkips is the skip-table ratchet: every rule must match at least
// one (case, declared seam) pair, or it is dead and must be deleted. It
// probes a FRESH registry against the full case×seam matrix, so it is
// independent of which other tests ran in this invocation.
func TestCorpusSkips(t *testing.T) {
	skipSlow(t)
	_, cases := engine(t)
	probe := harness.NewSkipRegistry(skipRules, false)
	for _, c := range cases {
		for _, seam := range c.Seams {
			probe.Lookup(c.Rel, seam)
		}
	}
	for _, rule := range probe.Unused() {
		t.Errorf("dead skip rule (matched nothing — delete it): %s", rule.String())
	}
}
