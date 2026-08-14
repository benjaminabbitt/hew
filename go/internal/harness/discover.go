package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Case is a discovered corpus case: its manifest plus resolved fixture paths.
// Fixture filenames are stored relative to Dir so seam runs can re-resolve
// them inside a scratch copy.
type Case struct {
	Manifest
	Dir string // absolute path of the case directory
	Rel string // "family/case", equals Manifest.Name when the corpus is consistent

	// Fixture files (base names within Dir; "" = absent).
	PatchFile       string // patch.hew
	TransformsFile  string // transforms.hewt, or the sole other *.hewt (cli/apply-transforms ships move.hewt)
	TargetFile      string // sole target.* match
	ExpectedFile    string // sole expected.* match, excluding expected.hew / expected-ops.json
	OldFile         string // old.*
	NewFile         string // new.*
	ExpectedHewFile string // expected.hew
	ExpectedOpsFile string // expected-ops.json

	// Roundtrip cases (the six */roundtrip-basic) carry only old/new/expected.hew;
	// per-seam fixtures are derived: patch := expected.hew, target := old, expected := new.
	Roundtrip bool
}

// Discover walks corpusDir (two levels: family/case) and loads every case.
// Structural problems — a case directory without case.yaml, an invalid
// manifest, a declared seam whose required files are missing — are corpus
// ERRORS in the returned slice, never skips (runner obligation 5).
func Discover(corpusDir string) ([]*Case, []error) {
	var cases []*Case
	var errs []error

	families, err := os.ReadDir(corpusDir)
	if err != nil {
		return nil, []error{fmt.Errorf("corpus root: %w", err)}
	}
	for _, fam := range families {
		if !fam.IsDir() {
			continue // corpus/README.md
		}
		famDir := filepath.Join(corpusDir, fam.Name())
		entries, err := os.ReadDir(famDir)
		if err != nil {
			errs = append(errs, fmt.Errorf("family %s: %w", fam.Name(), err))
			continue
		}
		for _, ent := range entries {
			if !ent.IsDir() {
				errs = append(errs, fmt.Errorf("corpus error: stray file %s/%s (families contain only case directories)", fam.Name(), ent.Name()))
				continue
			}
			rel := fam.Name() + "/" + ent.Name()
			c, cerrs := loadCase(filepath.Join(famDir, ent.Name()), rel)
			errs = append(errs, cerrs...)
			if c != nil {
				cases = append(cases, c)
			}
		}
	}
	return cases, errs
}

func loadCase(dir, rel string) (*Case, []error) {
	src, err := os.ReadFile(filepath.Join(dir, "case.yaml"))
	if err != nil {
		return nil, []error{fmt.Errorf("corpus error: %s has no readable case.yaml: %w", rel, err)}
	}
	m, err := ParseManifest(src)
	if err != nil {
		return nil, []error{fmt.Errorf("corpus error in %s: %w", rel, err)}
	}
	var errs []error
	if err := m.Validate(rel); err != nil {
		errs = append(errs, err)
	}

	c := &Case{Manifest: *m, Dir: dir, Rel: rel}
	if ferrs := c.resolveFixtures(); len(ferrs) > 0 {
		errs = append(errs, ferrs...)
	}
	if verrs := c.validateSeamFiles(); len(verrs) > 0 {
		errs = append(errs, verrs...)
	}
	return c, errs
}

func (c *Case) resolveFixtures() []error {
	var errs []error
	entries, err := os.ReadDir(c.Dir)
	if err != nil {
		return []error{fmt.Errorf("%s: %w", c.Rel, err)}
	}
	var hewts, targets, expecteds []string
	for _, ent := range entries {
		name := ent.Name()
		switch {
		case name == "case.yaml":
		case name == "patch.hew":
			c.PatchFile = name
		case name == "expected.hew":
			c.ExpectedHewFile = name
		case name == "expected-ops.json":
			c.ExpectedOpsFile = name
		case strings.HasSuffix(name, ".hewt"):
			hewts = append(hewts, name)
		case strings.HasPrefix(name, "target."):
			targets = append(targets, name)
		case strings.HasPrefix(name, "expected."):
			expecteds = append(expecteds, name)
		case strings.HasPrefix(name, "old."):
			c.OldFile = name
		case strings.HasPrefix(name, "new."):
			c.NewFile = name
		}
	}
	// transforms.hewt wins; otherwise a sole differently-named .hewt (the
	// CLI transform cases name the file whatever argv names, e.g. move.hewt).
	for _, h := range hewts {
		if h == "transforms.hewt" {
			c.TransformsFile = h
		}
	}
	if c.TransformsFile == "" && len(hewts) == 1 {
		c.TransformsFile = hewts[0]
	} else if c.TransformsFile == "" && len(hewts) > 1 {
		errs = append(errs, fmt.Errorf("corpus error: %s has %d .hewt files and none named transforms.hewt", c.Rel, len(hewts)))
	}
	if len(targets) == 1 {
		c.TargetFile = targets[0]
	} else if len(targets) > 1 {
		errs = append(errs, fmt.Errorf("corpus error: %s has %d target.* files", c.Rel, len(targets)))
	}
	if len(expecteds) == 1 {
		c.ExpectedFile = expecteds[0]
	} else if len(expecteds) > 1 {
		errs = append(errs, fmt.Errorf("corpus error: %s has %d expected.* files", c.Rel, len(expecteds)))
	}
	c.Roundtrip = c.PatchFile == "" && c.OldFile != "" && c.NewFile != "" && c.ExpectedHewFile != ""
	return errs
}

// validateSeamFiles enforces runner obligation 5: a declared seam whose
// required fixture files are missing is a corpus error, not a skip.
func (c *Case) validateSeamFiles() []error {
	var errs []error
	miss := func(seam Seam, what string) {
		errs = append(errs, fmt.Errorf("corpus error: %s declares seam %s but has no %s", c.Rel, seam, what))
	}
	for _, s := range c.Seams {
		switch s {
		case SeamParse:
			if c.PatchFile == "" && !c.Roundtrip {
				miss(s, "patch.hew")
			}
			// An ok-kind parse case pins the IR when it ships a
			// transforms fixture. Roundtrip cases never do, and
			// yaml/pragma-idempotent-file declares the seam to pin that a
			// preamble pragma is a property of the patch TEXT without
			// pinning the lowered list; for both, parse succeeding is the
			// whole assertion (engine.runParse).
		case SeamApplyIR:
			if !c.Roundtrip {
				if c.TransformsFile == "" {
					miss(s, "transforms fixture")
				}
				if c.TargetFile == "" {
					miss(s, "target.*")
				}
				if c.Kind != "error" && c.ExpectedFile == "" {
					miss(s, "expected.*")
				}
			}
		case SeamE2E:
			if !c.Roundtrip {
				if c.PatchFile == "" {
					miss(s, "patch.hew")
				}
				if c.TargetFile == "" {
					miss(s, "target.*")
				}
				if c.Kind != "error" && c.ExpectedFile == "" {
					miss(s, "expected.*")
				}
			}
		case SeamRender:
			if c.TransformsFile == "" && !c.Roundtrip {
				miss(s, "transforms fixture")
			}
		case SeamDiff:
			if c.OldFile == "" || c.NewFile == "" || c.ExpectedHewFile == "" {
				miss(s, "old.*/new.*/expected.hew")
			}
		case SeamCLI:
			if c.Stdout != nil && *c.Stdout != "" {
				if _, err := os.Stat(filepath.Join(c.Dir, *c.Stdout)); err != nil {
					miss(s, "stdout fixture "+*c.Stdout)
				}
			}
			if c.Expected != "" {
				if _, err := os.Stat(filepath.Join(c.Dir, c.Expected)); err != nil {
					miss(s, "expected fixture "+c.Expected)
				}
			}
		}
	}
	return errs
}
