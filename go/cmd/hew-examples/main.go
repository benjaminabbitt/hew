// Command hew-examples renders the executable example scenarios in examples/
// into Starlight pages under website/src/content/docs/examples/.
//
// The transcripts are COMPUTED: the generator copies each scenario's fixtures
// into a scratch directory, runs the real `hew` binary there, and writes down
// what happened — argv, stdout, stderr, exit code, resulting files. Nothing on
// a generated page is transcribed by hand, so no page can drift from the CLI's
// actual behavior. The generated pages are therefore not committed; the
// scenarios are.
//
// Determinism is a requirement, not an aspiration: every command runs with the
// scratch directory as its cwd and relative paths only (no temporary path can
// leak), directory reads are sorted, git runs with a pinned identity and clock,
// and the one wall-clock value hew emits — §9.7's `applied_at` — is replaced
// with a fixed stamp. Two runs of this program produce byte-identical output.
//
//	go run ./cmd/hew-examples -hew ./hew
//	go run ./cmd/hew-examples -hew ./hew -check   # fail if output would change
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func main() {
	scenarios := flag.String("scenarios", "../examples", "directory of scenario directories")
	out := flag.String("out", "../website/src/content/docs/examples", "directory to write the generated pages into")
	hewBin := flag.String("hew", "hew", "path to the hew binary to run")
	check := flag.Bool("check", false, "do not write; fail if the output would differ from what is already there")
	flag.Parse()

	if err := run(*scenarios, *out, *hewBin, *check); err != nil {
		fmt.Fprintf(os.Stderr, "hew-examples: %v\n", err)
		os.Exit(1)
	}
}

func run(scenariosDir, outDir, hewBin string, check bool) error {
	bin, err := resolveBinary(hewBin)
	if err != nil {
		return err
	}
	scs, err := loadScenarios(scenariosDir)
	if err != nil {
		return err
	}
	if len(scs) == 0 {
		return fmt.Errorf("%s: no scenarios found", scenariosDir)
	}

	pages := map[string]string{}
	var exs []*execution
	for _, sc := range scs {
		ex, err := runScenario(sc, bin)
		if err != nil {
			return err
		}
		exs = append(exs, ex)
		pages[sc.Name+".md"] = renderScenario(ex)
	}
	pages["index.md"] = renderIndex(exs)

	if check {
		return verify(outDir, pages)
	}
	return writePages(outDir, pages)
}

// resolveBinary turns the -hew value into an absolute path, because every
// command runs with its cwd set to a scratch directory and a relative binary
// path would resolve against that instead.
func resolveBinary(bin string) (string, error) {
	if strings.ContainsRune(bin, os.PathSeparator) {
		abs, err := filepath.Abs(bin)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(abs); err != nil {
			return "", fmt.Errorf("hew binary %s: %w", bin, err)
		}
		return abs, nil
	}
	return bin, nil // resolved from PATH by exec
}

func writePages(outDir string, pages map[string]string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	// Remove pages from scenarios that no longer exist: the output directory
	// is wholly owned by this program, and a stale page is worse than a
	// missing one.
	existing, err := os.ReadDir(outDir)
	if err != nil {
		return err
	}
	for _, e := range existing {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		if _, want := pages[e.Name()]; !want {
			if err := os.Remove(filepath.Join(outDir, e.Name())); err != nil {
				return err
			}
		}
	}
	for _, name := range sortedKeys(pages) {
		if err := os.WriteFile(filepath.Join(outDir, name), []byte(pages[name]), 0o644); err != nil {
			return err
		}
	}
	fmt.Printf("hew-examples: wrote %d pages to %s\n", len(pages), outDir)
	return nil
}

func verify(outDir string, pages map[string]string) error {
	for _, name := range sortedKeys(pages) {
		got, err := os.ReadFile(filepath.Join(outDir, name))
		if err != nil {
			return fmt.Errorf("%s: %w (run `just examples`)", name, err)
		}
		if string(got) != pages[name] {
			return fmt.Errorf("%s: generated output differs from what is on disk — the generator is not deterministic, or the pages are stale", name)
		}
	}
	fmt.Printf("hew-examples: %d pages match\n", len(pages))
	return nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
