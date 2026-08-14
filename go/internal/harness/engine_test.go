package harness

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ctxloom/hew/internal/hewerr"
)

// --- stub implementation under test --------------------------------------
//
// The stubs speak a toy notation so seam plumbing can be asserted without a
// parser: "PATCH:x" is notation, "IR:x" is the canonical IR, and
// canonicalization is "trim surrounding whitespace, one trailing newline".

func stubCanon(b []byte) ([]byte, error) {
	return []byte(strings.TrimSpace(string(b)) + "\n"), nil
}

func stubParse(patch []byte) ([]byte, error) {
	if bytes.Contains(patch, []byte("BAD")) {
		return nil, &hewerr.Error{Code: hewerr.CodeParse, Component: hewerr.ComponentParser}
	}
	return stubCanon(bytes.ReplaceAll(patch, []byte("PATCH:"), []byte("IR:")))
}

func stubRender(hewt []byte) ([]byte, error) {
	return bytes.ReplaceAll(hewt, []byte("IR:"), []byte("PATCH:")), nil
}

func failHook(msg string) error { return errors.New(msg) }

// --- fixture -------------------------------------------------------------

type engineFixture struct {
	t    *testing.T
	Root string
	Case *Case
	Eng  *Engine
}

// setupEngine builds a one-case synthetic corpus, discovers it (asserting the
// corpus itself is clean unless the test says otherwise) and wires an engine.
func setupEngine(t *testing.T, rel string, files map[string]string, bind Binding) *engineFixture {
	t.Helper()
	root := corpusWith(t, rel, files)
	cases, errs := Discover(root)
	requireNoErrs(t, errs)
	if len(cases) != 1 {
		t.Fatalf("discovered %d cases, want 1", len(cases))
	}
	scratchRoot := t.TempDir()
	return &engineFixture{
		t: t, Root: root, Case: cases[0],
		Eng: &Engine{
			CorpusDir: root,
			Bind:      bind,
			Scratch:   func() (string, error) { return os.MkdirTemp(scratchRoot, "run-*") },
		},
	}
}

// remove deletes a fixture file after discovery, so the engine's own
// missing-file paths (as opposed to discovery's) can be reached.
func (f *engineFixture) remove(name string) {
	f.t.Helper()
	if err := os.Remove(filepath.Join(f.Case.Dir, name)); err != nil {
		f.t.Fatal(err)
	}
}

func (f *engineFixture) run(seam Seam) Outcome {
	f.t.Helper()
	return f.Eng.RunSeam(f.Case, seam)
}

func wantOutcome(t *testing.T, out Outcome, c *Case, seam Seam, status Status, substrings ...string) {
	t.Helper()
	if out.Case != c.Rel {
		t.Errorf("Outcome.Case = %q, want %q", out.Case, c.Rel)
	}
	if out.Seam != seam {
		t.Errorf("Outcome.Seam = %q, want %q", out.Seam, seam)
	}
	if out.Status != status {
		t.Errorf("Status = %s (%s), want %s", out.Status, out.Detail, status)
	}
	for _, s := range substrings {
		if !strings.Contains(out.Detail, s) {
			t.Errorf("Detail %q missing %q", out.Detail, s)
		}
	}
	if status == StatusPass && out.Detail != "" {
		t.Errorf("a passing outcome must carry no detail, got %q", out.Detail)
	}
}

// --- shared case shapes --------------------------------------------------

func parseCaseFiles() map[string]string {
	return map[string]string{
		"case.yaml":       manifestYAML("json/parse-case", "[parse]", "ok"),
		"patch.hew":       "PATCH:add-key\n",
		"transforms.hewt": "  IR:add-key  \n\n", // sloppy on purpose: canonicalization must fix it
	}
}

func applyCaseFiles(seams string) map[string]string {
	return map[string]string{
		"case.yaml":       manifestYAML("json/apply-case", seams, "ok"),
		"patch.hew":       "PATCH:add-key\n",
		"transforms.hewt": "IR:add-key\n",
		"target.json":     "{\"a\": 1}\n",
		"expected.json":   "{\"a\": 2}\n",
	}
}

func errorApplyCaseFiles() map[string]string {
	return map[string]string{
		"case.yaml": manifestYAML("json/error-case", "[apply-ir, e2e]", "error",
			"error: HEW010", "error_seam: apply-ir", "message_contains: [stale-target]"),
		"patch.hew":       "PATCH:stale\n",
		"transforms.hewt": "IR:stale\n",
		"target.json":     "{\"a\": 1}\n",
	}
}

func staleErr() *hewerr.Error {
	return &hewerr.Error{Code: hewerr.CodeStaleTarget, Component: hewerr.ComponentApplier, Target: "target.json"}
}

func cliCaseFiles(extra ...string) map[string]string {
	m := map[string]string{
		"case.yaml": manifestYAML("cli/case", "[cli]", "cli",
			append([]string{"argv: [apply, patch.hew, target.json]", "exit: 0"}, extra...)...),
		"patch.hew":   "PATCH:add-key\n",
		"target.json": "{\"a\": 1}\n",
	}
	return m
}

// --- dispatch, scratch and skip plumbing ---------------------------------

func TestRunSeamSkipRuleHonored(t *testing.T) {
	called := false
	f := setupEngine(t, "json/parse-case", parseCaseFiles(), Binding{
		ParseToHewt: func([]byte) ([]byte, error) { called = true; return nil, nil },
	})
	f.Eng.Skips = NewSkipRegistry([]SkipRule{{Case: "json/*", Seam: "parse", Reason: "M3 not built"}}, false)
	out := f.run(SeamParse)
	wantOutcome(t, out, f.Case, SeamParse, StatusSkip, "M3 not built")
	if out.Detail != "M3 not built" {
		t.Errorf("Detail = %q, want the bare rule reason", out.Detail)
	}
	if called {
		t.Error("a skipped seam must not run the implementation")
	}
}

func TestRunSeamNoSkipsConvertsSkipToFail(t *testing.T) {
	called := false
	f := setupEngine(t, "json/parse-case", parseCaseFiles(), Binding{
		ParseToHewt: func([]byte) ([]byte, error) { called = true; return nil, nil },
	})
	f.Eng.Skips = NewSkipRegistry([]SkipRule{{Case: "json/*", Seam: "*", Reason: "M3 not built"}}, true)
	out := f.run(SeamParse)
	wantOutcome(t, out, f.Case, SeamParse, StatusFail,
		"skips disallowed (HEW_CORPUS_NO_SKIPS=1)", "rule reason: M3 not built")
	if called {
		t.Error("HEW_CORPUS_NO_SKIPS must fail the seam, not run it")
	}
}

func TestRunSeamNonMatchingSkipRuleRuns(t *testing.T) {
	f := setupEngine(t, "json/parse-case", parseCaseFiles(), Binding{
		ParseToHewt: stubParse, CanonHewt: stubCanon,
	})
	f.Eng.Skips = NewSkipRegistry([]SkipRule{{Case: "yaml/*", Seam: "parse", Reason: "other family"}}, true)
	wantOutcome(t, f.run(SeamParse), f.Case, SeamParse, StatusPass)
}

func TestRunSeamNilSkipRegistry(t *testing.T) {
	f := setupEngine(t, "json/parse-case", parseCaseFiles(), Binding{
		ParseToHewt: stubParse, CanonHewt: stubCanon,
	})
	f.Eng.Skips = nil
	wantOutcome(t, f.run(SeamParse), f.Case, SeamParse, StatusPass)
}

func TestRunSeamScratchError(t *testing.T) {
	f := setupEngine(t, "json/parse-case", parseCaseFiles(), Binding{ParseToHewt: stubParse})
	f.Eng.Scratch = func() (string, error) { return "", errors.New("disk full") }
	wantOutcome(t, f.run(SeamParse), f.Case, SeamParse, StatusFail, "scratch: disk full")
}

func TestRunSeamCopyFailureIsCorpusError(t *testing.T) {
	f := setupEngine(t, "json/parse-case", parseCaseFiles(), Binding{ParseToHewt: stubParse})
	link := filepath.Join(f.Case.Dir, "link.hew")
	if err := os.Symlink(filepath.Join(f.Case.Dir, "patch.hew"), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	wantOutcome(t, f.run(SeamParse), f.Case, SeamParse, StatusCorpusError, "not a regular file")
}

func TestRunSeamUnknownSeam(t *testing.T) {
	f := setupEngine(t, "json/parse-case", parseCaseFiles(), Binding{})
	out := f.Eng.RunSeam(f.Case, Seam("teleport"))
	wantOutcome(t, out, f.Case, Seam("teleport"), StatusCorpusError, `unknown seam "teleport"`)
}

// TestRunSeamWorksOnAScratchCopy is runner obligation 1: the corpus directory
// itself must never be touched, even by an in-place CLI case.
func TestRunSeamWorksOnAScratchCopy(t *testing.T) {
	files := cliCaseFiles()
	f := setupEngine(t, "cli/case", files, Binding{
		RunCLI: func(argv []string, dir string, _ io.Reader, _, _ io.Writer) int {
			_ = os.WriteFile(filepath.Join(dir, "target.json"), []byte("REWRITTEN\n"), 0o644)
			return 0
		},
	})
	wantOutcome(t, f.run(SeamCLI), f.Case, SeamCLI, StatusPass)
	orig, err := os.ReadFile(filepath.Join(f.Case.Dir, "target.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(orig) != files["target.json"] {
		t.Errorf("the corpus copy was mutated: %q", orig)
	}
}

// --- parse seam ----------------------------------------------------------

func TestRunParseUnboundHooks(t *testing.T) {
	t.Run("ParseToHewt nil", func(t *testing.T) {
		f := setupEngine(t, "json/parse-case", parseCaseFiles(), Binding{})
		wantOutcome(t, f.run(SeamParse), f.Case, SeamParse, StatusFail,
			"seam parse unbound (ParseToHewt hook is nil) and no skip rule matches")
	})
	t.Run("CanonHewt nil", func(t *testing.T) {
		f := setupEngine(t, "json/parse-case", parseCaseFiles(), Binding{ParseToHewt: stubParse})
		wantOutcome(t, f.run(SeamParse), f.Case, SeamParse, StatusFail,
			"seam parse unbound (CanonHewt hook is nil)")
	})
}

func TestRunParseOK(t *testing.T) {
	f := setupEngine(t, "json/parse-case", parseCaseFiles(), Binding{
		ParseToHewt: stubParse, CanonHewt: stubCanon,
	})
	var gotPatch []byte
	f.Eng.Bind.ParseToHewt = func(p []byte) ([]byte, error) { gotPatch = p; return stubParse(p) }
	wantOutcome(t, f.run(SeamParse), f.Case, SeamParse, StatusPass)
	if string(gotPatch) != "PATCH:add-key\n" {
		t.Errorf("ParseToHewt saw %q, want the patch.hew bytes", gotPatch)
	}
}

// TestRunParseWithoutPinnedIR: a case that ships no transforms fixture (e.g.
// yaml/pragma-idempotent-file) asserts only that parsing succeeds.
func TestRunParseWithoutPinnedIR(t *testing.T) {
	files := parseCaseFiles()
	delete(files, "transforms.hewt")
	f := setupEngine(t, "json/parse-case", files, Binding{
		ParseToHewt: stubParse,
		CanonHewt:   func([]byte) ([]byte, error) { t.Error("nothing to canonicalize"); return nil, nil },
	})
	wantOutcome(t, f.run(SeamParse), f.Case, SeamParse, StatusPass)
}

func TestRunParseWithoutPinnedIRStillFailsOnParseError(t *testing.T) {
	files := parseCaseFiles()
	delete(files, "transforms.hewt")
	files["patch.hew"] = "PATCH:BAD\n"
	f := setupEngine(t, "json/parse-case", files, Binding{ParseToHewt: stubParse})
	wantOutcome(t, f.run(SeamParse), f.Case, SeamParse, StatusFail, "parse failed:")
}

func TestRunParseIRMismatch(t *testing.T) {
	files := parseCaseFiles()
	files["transforms.hewt"] = "IR:something-else\n"
	f := setupEngine(t, "json/parse-case", files, Binding{ParseToHewt: stubParse, CanonHewt: stubCanon})
	wantOutcome(t, f.run(SeamParse), f.Case, SeamParse, StatusFail,
		"parsed IR != pinned transforms fixture (both canonicalized)", "byte mismatch:")
}

func TestRunParseFailures(t *testing.T) {
	tests := []struct {
		name     string
		bind     Binding
		status   Status
		contains []string
	}{
		{
			name: "parser returns an error on an ok case",
			bind: Binding{
				ParseToHewt: func([]byte) ([]byte, error) { return nil, failHook("bad token") },
				CanonHewt:   stubCanon,
			},
			status:   StatusFail,
			contains: []string{"parse failed: bad token"},
		},
		{
			name: "fixture does not canonicalize",
			bind: Binding{
				ParseToHewt: stubParse,
				CanonHewt:   func([]byte) ([]byte, error) { return nil, failHook("not valid hewt") },
			},
			status:   StatusFail,
			contains: []string{"transforms fixture does not canonicalize: not valid hewt"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := setupEngine(t, "json/parse-case", parseCaseFiles(), tc.bind)
			wantOutcome(t, f.run(SeamParse), f.Case, SeamParse, tc.status, tc.contains...)
		})
	}
}

func TestRunParseMissingFilesAreCorpusErrors(t *testing.T) {
	t.Run("patch.hew vanished", func(t *testing.T) {
		f := setupEngine(t, "json/parse-case", parseCaseFiles(), Binding{ParseToHewt: stubParse, CanonHewt: stubCanon})
		f.remove("patch.hew")
		wantOutcome(t, f.run(SeamParse), f.Case, SeamParse, StatusCorpusError,
			"corpus error: json/parse-case missing patch.hew")
	})
	t.Run("transforms fixture vanished", func(t *testing.T) {
		f := setupEngine(t, "json/parse-case", parseCaseFiles(), Binding{ParseToHewt: stubParse, CanonHewt: stubCanon})
		f.remove("transforms.hewt")
		wantOutcome(t, f.run(SeamParse), f.Case, SeamParse, StatusCorpusError,
			"corpus error: json/parse-case missing transforms.hewt")
	})
}

// TestRunParseRoundtrip: a roundtrip case parses expected.hew and has no
// pinned IR, so parse success IS the assertion — CanonHewt is never needed.
func TestRunParseRoundtrip(t *testing.T) {
	files := roundtripFiles("json/roundtrip-basic")
	files["expected.hew"] = "PATCH:add-key\n"
	var seen []byte
	f := setupEngine(t, "json/roundtrip-basic", files, Binding{
		ParseToHewt: func(p []byte) ([]byte, error) { seen = p; return stubParse(p) },
	})
	wantOutcome(t, f.run(SeamParse), f.Case, SeamParse, StatusPass)
	if string(seen) != "PATCH:add-key\n" {
		t.Errorf("roundtrip parse saw %q, want expected.hew's bytes", seen)
	}
}

func TestRunParseRoundtripParseFailure(t *testing.T) {
	files := roundtripFiles("json/roundtrip-basic")
	files["expected.hew"] = "PATCH:BAD\n"
	f := setupEngine(t, "json/roundtrip-basic", files, Binding{ParseToHewt: stubParse})
	wantOutcome(t, f.run(SeamParse), f.Case, SeamParse, StatusFail, "parse failed:")
}

func TestRunParseErrorKind(t *testing.T) {
	files := map[string]string{
		"case.yaml": manifestYAML("json/parse-error", "[parse]", "error",
			"error: HEW001", "error_seam: parse", "message_contains: [parse-error]"),
		"patch.hew": "PATCH:BAD\n",
	}
	tests := []struct {
		name     string
		parse    func([]byte) ([]byte, error)
		status   Status
		contains []string
	}{
		{
			name:   "conformant error",
			parse:  stubParse,
			status: StatusPass,
		},
		{
			name:     "no error at all",
			parse:    func([]byte) ([]byte, error) { return []byte("IR:ok\n"), nil },
			status:   StatusFail,
			contains: []string{"expected error HEW001, got success"},
		},
		{
			name: "wrong code and component",
			parse: func([]byte) ([]byte, error) {
				return nil, &hewerr.Error{Code: hewerr.CodeNoMatch, Component: hewerr.ComponentApplier}
			},
			status: StatusFail,
			contains: []string{
				"code: want HEW001, got HEW013",
				"component: want parser",
				`message missing "parse-error"`,
			},
		},
		{
			name:     "not a hewerr",
			parse:    func([]byte) ([]byte, error) { return nil, failHook("plain failure") },
			status:   StatusFail,
			contains: []string{"not a *hewerr.Error"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := setupEngine(t, "json/parse-error", files, Binding{ParseToHewt: tc.parse})
			wantOutcome(t, f.run(SeamParse), f.Case, SeamParse, tc.status, tc.contains...)
		})
	}
}

// TestRunParseCLIKind covers the cli/empty-patch-exit-2 quirk adapter: a
// cli-kind case with a bonus parse seam takes its expected code from
// stderr_contains.
func TestRunParseCLIKind(t *testing.T) {
	withCode := func(extra ...string) map[string]string {
		return map[string]string{
			"case.yaml": manifestYAML("cli/empty-patch", "[parse, cli]", "cli",
				append([]string{"argv: [apply, patch.hew]", "exit: 2"}, extra...)...),
			"patch.hew": "PATCH:BAD\n",
		}
	}
	tests := []struct {
		name     string
		files    map[string]string
		parse    func([]byte) ([]byte, error)
		status   Status
		contains []string
	}{
		{
			name:   "code matches",
			files:  withCode(`stderr_contains: ["HEW001", "empty patch"]`),
			parse:  stubParse,
			status: StatusPass,
		},
		{
			name:     "no HEW code in stderr_contains",
			files:    withCode(`stderr_contains: ["empty patch"]`),
			parse:    stubParse,
			status:   StatusCorpusError,
			contains: []string{"cli-kind parse seam with no HEW code in stderr_contains"},
		},
		{
			name:     "parser succeeded",
			files:    withCode(`stderr_contains: ["HEW001"]`),
			parse:    func([]byte) ([]byte, error) { return []byte("IR:x\n"), nil },
			status:   StatusFail,
			contains: []string{"expected parse failure HEW001, got success"},
		},
		{
			name:     "not a hewerr",
			files:    withCode(`stderr_contains: ["HEW001"]`),
			parse:    func([]byte) ([]byte, error) { return nil, failHook("plain") },
			status:   StatusFail,
			contains: []string{"error is not a *hewerr.Error: plain"},
		},
		{
			name:  "wrong code",
			files: withCode(`stderr_contains: ["HEW021"]`),
			parse: func([]byte) ([]byte, error) {
				return nil, &hewerr.Error{Code: hewerr.CodeParse, Component: hewerr.ComponentParser}
			},
			status:   StatusFail,
			contains: []string{"code: want HEW021, got HEW001"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := setupEngine(t, "cli/empty-patch", tc.files, Binding{ParseToHewt: tc.parse})
			wantOutcome(t, f.run(SeamParse), f.Case, SeamParse, tc.status, tc.contains...)
		})
	}
}

// --- apply-ir and e2e seams ----------------------------------------------

func TestRunApplyIROK(t *testing.T) {
	var gotIR, gotTarget []byte
	var gotFormat string
	f := setupEngine(t, "json/apply-case", applyCaseFiles("[apply-ir]"), Binding{
		ApplyHewt: func(hewt, target []byte, format string) ([]byte, error) {
			gotIR, gotTarget, gotFormat = hewt, target, format
			return []byte("{\"a\": 2}\n"), nil
		},
		ApplyPatch: func([]byte, []byte, string) ([]byte, error) {
			t.Error("apply-ir must not call ApplyPatch")
			return nil, nil
		},
	})
	wantOutcome(t, f.run(SeamApplyIR), f.Case, SeamApplyIR, StatusPass)
	if string(gotIR) != "IR:add-key\n" {
		t.Errorf("ApplyHewt saw IR %q", gotIR)
	}
	if string(gotTarget) != "{\"a\": 1}\n" {
		t.Errorf("ApplyHewt saw target %q", gotTarget)
	}
	if gotFormat != "json" {
		t.Errorf("format = %q, want the manifest's format", gotFormat)
	}
}

func TestRunE2EOK(t *testing.T) {
	var gotPatch []byte
	f := setupEngine(t, "json/apply-case", applyCaseFiles("[e2e]"), Binding{
		ApplyPatch: func(patch, target []byte, format string) ([]byte, error) {
			gotPatch = patch
			return []byte("{\"a\": 2}\n"), nil
		},
		ApplyHewt: func([]byte, []byte, string) ([]byte, error) {
			t.Error("e2e must not call ApplyHewt")
			return nil, nil
		},
	})
	wantOutcome(t, f.run(SeamE2E), f.Case, SeamE2E, StatusPass)
	if string(gotPatch) != "PATCH:add-key\n" {
		t.Errorf("ApplyPatch saw %q, want patch.hew's bytes", gotPatch)
	}
}

func TestRunApplyUnboundHooks(t *testing.T) {
	t.Run("apply-ir", func(t *testing.T) {
		f := setupEngine(t, "json/apply-case", applyCaseFiles("[apply-ir]"), Binding{})
		wantOutcome(t, f.run(SeamApplyIR), f.Case, SeamApplyIR, StatusFail,
			"seam apply-ir unbound (ApplyHewt hook is nil)")
	})
	t.Run("e2e", func(t *testing.T) {
		f := setupEngine(t, "json/apply-case", applyCaseFiles("[e2e]"), Binding{})
		wantOutcome(t, f.run(SeamE2E), f.Case, SeamE2E, StatusFail,
			"seam e2e unbound (ApplyPatch hook is nil)")
	})
}

func TestRunApplyOutputMismatch(t *testing.T) {
	f := setupEngine(t, "json/apply-case", applyCaseFiles("[apply-ir]"), Binding{
		ApplyHewt: func([]byte, []byte, string) ([]byte, error) { return []byte("{\"a\": 3}\n"), nil },
	})
	wantOutcome(t, f.run(SeamApplyIR), f.Case, SeamApplyIR, StatusFail, "byte mismatch:", "offset 6")
}

func TestRunApplyUnexpectedError(t *testing.T) {
	f := setupEngine(t, "json/apply-case", applyCaseFiles("[apply-ir]"), Binding{
		ApplyHewt: func([]byte, []byte, string) ([]byte, error) { return nil, failHook("no such path") },
	})
	wantOutcome(t, f.run(SeamApplyIR), f.Case, SeamApplyIR, StatusFail, "apply failed: no such path")
}

func TestRunApplyMissingFixtures(t *testing.T) {
	tests := []struct {
		name     string
		seam     Seam
		seams    string
		remove   string
		contains string
	}{
		{name: "missing target", seam: SeamApplyIR, seams: "[apply-ir]", remove: "target.json", contains: "missing target target.json"},
		{name: "missing transforms", seam: SeamApplyIR, seams: "[apply-ir]", remove: "transforms.hewt", contains: "missing transforms fixture"},
		{name: "missing expected", seam: SeamApplyIR, seams: "[apply-ir]", remove: "expected.json", contains: "missing expected expected.json"},
		{name: "missing patch for e2e", seam: SeamE2E, seams: "[e2e]", remove: "patch.hew", contains: "missing patch patch.hew"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := setupEngine(t, "json/apply-case", applyCaseFiles(tc.seams), Binding{
				ApplyHewt:  func([]byte, []byte, string) ([]byte, error) { return []byte("{\"a\": 2}\n"), nil },
				ApplyPatch: func([]byte, []byte, string) ([]byte, error) { return []byte("{\"a\": 2}\n"), nil },
			})
			f.remove(tc.remove)
			wantOutcome(t, f.run(tc.seam), f.Case, tc.seam, StatusCorpusError, tc.contains)
		})
	}
}

func TestRunApplyErrorKind(t *testing.T) {
	tests := []struct {
		name     string
		apply    func(hewt, target []byte, format string) ([]byte, error)
		status   Status
		contains []string
	}{
		{
			name:   "conformant error, nothing returned",
			apply:  func([]byte, []byte, string) ([]byte, error) { return nil, staleErr() },
			status: StatusPass,
		},
		{
			name:     "no error",
			apply:    func([]byte, []byte, string) ([]byte, error) { return []byte("x"), nil },
			status:   StatusFail,
			contains: []string{"expected error HEW010, got success", "non-nil bytes alongside an error"},
		},
		{
			name:     "bytes alongside the error violate all-or-nothing",
			apply:    func([]byte, []byte, string) ([]byte, error) { return []byte("partial"), staleErr() },
			status:   StatusFail,
			contains: []string{"applier returned non-nil bytes alongside an error (all-or-nothing violated)"},
		},
		{
			name:     "empty non-nil slice is still bytes",
			apply:    func([]byte, []byte, string) ([]byte, error) { return []byte{}, staleErr() },
			status:   StatusFail,
			contains: []string{"non-nil bytes alongside an error"},
		},
		{
			name: "wrong code is reported with the contract dimensions",
			apply: func([]byte, []byte, string) ([]byte, error) {
				return nil, &hewerr.Error{Code: hewerr.CodeNoMatch, Component: hewerr.ComponentApplier}
			},
			status:   StatusFail,
			contains: []string{"code: want HEW010, got HEW013", `message missing "stale-target"`},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := setupEngine(t, "json/error-case", errorApplyCaseFiles(), Binding{ApplyHewt: tc.apply})
			wantOutcome(t, f.run(SeamApplyIR), f.Case, SeamApplyIR, tc.status, tc.contains...)
		})
	}
}

// TestRunApplyErrorKindTargetMustBeUnchanged is runner obligation 4's second
// half: a failed apply that rewrote the target is non-conformant even if the
// error itself is perfect.
func TestRunApplyErrorKindTargetMustBeUnchanged(t *testing.T) {
	var scratchDir string
	f := setupEngine(t, "json/error-case", errorApplyCaseFiles(), Binding{})
	f.Eng.Scratch = func() (string, error) {
		d, err := os.MkdirTemp(t.TempDir(), "run-*")
		scratchDir = d
		return d, err
	}
	f.Eng.Bind.ApplyHewt = func([]byte, []byte, string) ([]byte, error) {
		if err := os.WriteFile(filepath.Join(scratchDir, "target.json"), []byte("HALF-WRITTEN\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return nil, staleErr()
	}
	wantOutcome(t, f.run(SeamApplyIR), f.Case, SeamApplyIR, StatusFail,
		"target target.json was modified by a failed apply", "byte mismatch:")
}

func TestRunE2EErrorKind(t *testing.T) {
	f := setupEngine(t, "json/error-case", errorApplyCaseFiles(), Binding{
		ApplyPatch: func([]byte, []byte, string) ([]byte, error) { return nil, staleErr() },
	})
	wantOutcome(t, f.run(SeamE2E), f.Case, SeamE2E, StatusPass)
}

// TestRunApplyRoundtrip: apply-ir on a roundtrip case derives the IR from
// expected.hew and applies it to old.*, comparing against new.*.
func TestRunApplyRoundtrip(t *testing.T) {
	files := roundtripFiles("json/roundtrip-basic")
	files["expected.hew"] = "PATCH:add-key\n"

	t.Run("apply-ir derives the IR from expected.hew", func(t *testing.T) {
		var gotIR, gotTarget []byte
		f := setupEngine(t, "json/roundtrip-basic", files, Binding{
			ParseToHewt: stubParse,
			ApplyHewt: func(hewt, target []byte, _ string) ([]byte, error) {
				gotIR, gotTarget = hewt, target
				return []byte("{\"a\": 2}\n"), nil
			},
		})
		wantOutcome(t, f.run(SeamApplyIR), f.Case, SeamApplyIR, StatusPass)
		if string(gotIR) != "IR:add-key\n" {
			t.Errorf("IR = %q, want the parse of expected.hew", gotIR)
		}
		if string(gotTarget) != "{\"a\": 1}\n" {
			t.Errorf("target = %q, want old.json", gotTarget)
		}
	})

	t.Run("apply-ir needs the parser to derive the IR", func(t *testing.T) {
		f := setupEngine(t, "json/roundtrip-basic", files, Binding{
			ApplyHewt: func([]byte, []byte, string) ([]byte, error) { return nil, nil },
		})
		wantOutcome(t, f.run(SeamApplyIR), f.Case, SeamApplyIR, StatusFail,
			"seam apply-ir unbound (ParseToHewt hook is nil)")
	})

	t.Run("derivation failure is reported as such", func(t *testing.T) {
		bad := roundtripFiles("json/roundtrip-basic")
		bad["expected.hew"] = "PATCH:BAD\n"
		f := setupEngine(t, "json/roundtrip-basic", bad, Binding{
			ParseToHewt: stubParse,
			ApplyHewt:   func([]byte, []byte, string) ([]byte, error) { return nil, nil },
		})
		wantOutcome(t, f.run(SeamApplyIR), f.Case, SeamApplyIR, StatusFail, "deriving IR from expected.hew:")
	})

	t.Run("e2e applies expected.hew to old.json", func(t *testing.T) {
		var gotPatch []byte
		f := setupEngine(t, "json/roundtrip-basic", files, Binding{
			ApplyPatch: func(patch, target []byte, _ string) ([]byte, error) {
				gotPatch = patch
				return []byte("{\"a\": 2}\n"), nil
			},
		})
		wantOutcome(t, f.run(SeamE2E), f.Case, SeamE2E, StatusPass)
		if string(gotPatch) != "PATCH:add-key\n" {
			t.Errorf("patch = %q, want expected.hew", gotPatch)
		}
	})
}

// --- render seam ---------------------------------------------------------

func renderCaseFiles() map[string]string {
	return map[string]string{
		"case.yaml":       manifestYAML("json/render-case", "[render]", "ok"),
		"transforms.hewt": " IR:add-key \n",
	}
}

func TestRunRenderRT2(t *testing.T) {
	var rendered []byte
	f := setupEngine(t, "json/render-case", renderCaseFiles(), Binding{
		ParseToHewt: stubParse,
		CanonHewt:   stubCanon,
		RenderHew: func(hewt []byte) ([]byte, error) {
			out, err := stubRender(hewt)
			rendered = out
			return out, err
		},
	})
	wantOutcome(t, f.run(SeamRender), f.Case, SeamRender, StatusPass)
	if string(rendered) != "PATCH:add-key\n" {
		t.Errorf("RenderHew produced %q from the canonicalized fixture", rendered)
	}
}

func TestRunRenderViolation(t *testing.T) {
	f := setupEngine(t, "json/render-case", renderCaseFiles(), Binding{
		ParseToHewt: stubParse,
		CanonHewt:   stubCanon,
		// Renders notation that parses back to a different IR: RT2 broken.
		RenderHew: func([]byte) ([]byte, error) { return []byte("PATCH:lossy\n"), nil },
	})
	wantOutcome(t, f.run(SeamRender), f.Case, SeamRender, StatusFail,
		"RT2 violated: parse(render(ir)) != ir", "byte mismatch:")
}

func TestRunRenderFailures(t *testing.T) {
	tests := []struct {
		name     string
		bind     Binding
		status   Status
		contains []string
	}{
		{
			name:     "RenderHew nil",
			bind:     Binding{ParseToHewt: stubParse, CanonHewt: stubCanon},
			status:   StatusFail,
			contains: []string{"seam render unbound (RenderHew hook is nil)"},
		},
		{
			name:     "ParseToHewt nil",
			bind:     Binding{RenderHew: stubRender, CanonHewt: stubCanon},
			status:   StatusFail,
			contains: []string{"seam render unbound (ParseToHewt hook is nil)"},
		},
		{
			name:     "CanonHewt nil",
			bind:     Binding{RenderHew: stubRender, ParseToHewt: stubParse},
			status:   StatusFail,
			contains: []string{"seam render unbound (CanonHewt hook is nil)"},
		},
		{
			name: "fixture does not canonicalize",
			bind: Binding{
				RenderHew: stubRender, ParseToHewt: stubParse,
				CanonHewt: func([]byte) ([]byte, error) { return nil, failHook("bad hewt") },
			},
			status:   StatusFail,
			contains: []string{"transforms fixture does not canonicalize: bad hewt"},
		},
		{
			name: "render fails",
			bind: Binding{
				ParseToHewt: stubParse, CanonHewt: stubCanon,
				RenderHew: func([]byte) ([]byte, error) { return nil, failHook("cannot render") },
			},
			status:   StatusFail,
			contains: []string{"render failed: cannot render"},
		},
		{
			name: "rendered notation does not re-parse",
			bind: Binding{
				CanonHewt: stubCanon, ParseToHewt: stubParse,
				RenderHew: func([]byte) ([]byte, error) { return []byte("PATCH:BAD\n"), nil },
			},
			status:   StatusFail,
			contains: []string{"re-parse of rendered notation failed:"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := setupEngine(t, "json/render-case", renderCaseFiles(), tc.bind)
			wantOutcome(t, f.run(SeamRender), f.Case, SeamRender, tc.status, tc.contains...)
		})
	}
}

func TestRunRenderMissingFixture(t *testing.T) {
	f := setupEngine(t, "json/render-case", renderCaseFiles(), Binding{
		ParseToHewt: stubParse, CanonHewt: stubCanon, RenderHew: stubRender,
	})
	f.remove("transforms.hewt")
	wantOutcome(t, f.run(SeamRender), f.Case, SeamRender, StatusCorpusError,
		"corpus error: json/render-case missing transforms.hewt")
}

func TestRunRenderRoundtrip(t *testing.T) {
	files := roundtripFiles("json/roundtrip-basic")
	files["expected.hew"] = "PATCH:add-key\n"

	t.Run("base IR comes from expected.hew, not a transforms fixture", func(t *testing.T) {
		f := setupEngine(t, "json/roundtrip-basic", files, Binding{
			ParseToHewt: stubParse,
			RenderHew:   stubRender,
			CanonHewt: func([]byte) ([]byte, error) {
				t.Error("roundtrip render must not canonicalize a fixture")
				return nil, nil
			},
		})
		wantOutcome(t, f.run(SeamRender), f.Case, SeamRender, StatusPass)
	})

	t.Run("derivation failure", func(t *testing.T) {
		bad := roundtripFiles("json/roundtrip-basic")
		bad["expected.hew"] = "PATCH:BAD\n"
		f := setupEngine(t, "json/roundtrip-basic", bad, Binding{ParseToHewt: stubParse, RenderHew: stubRender})
		wantOutcome(t, f.run(SeamRender), f.Case, SeamRender, StatusFail, "deriving IR from expected.hew:")
	})

	t.Run("missing expected.hew", func(t *testing.T) {
		f := setupEngine(t, "json/roundtrip-basic", files, Binding{ParseToHewt: stubParse, RenderHew: stubRender})
		f.remove("expected.hew")
		wantOutcome(t, f.run(SeamRender), f.Case, SeamRender, StatusCorpusError, "missing expected.hew")
	})
}

// --- diff seam -----------------------------------------------------------

func diffCaseFiles() map[string]string {
	return map[string]string{
		"case.yaml":    manifestYAML("json/diff-case", "[diff]", "ok"),
		"old.json":     "{\"a\": 1}\n",
		"new.json":     "{\"a\": 2}\n",
		"expected.hew": "PATCH:add-key\n",
	}
}

func TestRunDiffOK(t *testing.T) {
	var gotOld, gotNew []byte
	var gotFormat string
	f := setupEngine(t, "json/diff-case", diffCaseFiles(), Binding{
		DiffToHew: func(old, new []byte, format string) ([]byte, error) {
			gotOld, gotNew, gotFormat = old, new, format
			return []byte("PATCH:add-key\n"), nil
		},
	})
	wantOutcome(t, f.run(SeamDiff), f.Case, SeamDiff, StatusPass)
	if string(gotOld) != "{\"a\": 1}\n" || string(gotNew) != "{\"a\": 2}\n" {
		t.Errorf("DiffToHew saw %q / %q", gotOld, gotNew)
	}
	if gotFormat != "json" {
		t.Errorf("format = %q", gotFormat)
	}
}

func TestRunDiffFailures(t *testing.T) {
	tests := []struct {
		name     string
		bind     Binding
		remove   string
		status   Status
		contains []string
	}{
		{
			name:     "unbound",
			bind:     Binding{},
			status:   StatusFail,
			contains: []string{"seam diff unbound (DiffToHew hook is nil)"},
		},
		{
			name:     "differ errors",
			bind:     Binding{DiffToHew: func([]byte, []byte, string) ([]byte, error) { return nil, failHook("cannot diff") }},
			status:   StatusFail,
			contains: []string{"diff failed: cannot diff"},
		},
		{
			name:     "output mismatch",
			bind:     Binding{DiffToHew: func([]byte, []byte, string) ([]byte, error) { return []byte("PATCH:other\n"), nil }},
			status:   StatusFail,
			contains: []string{"byte mismatch:"},
		},
		{
			name:     "missing old",
			bind:     Binding{DiffToHew: func([]byte, []byte, string) ([]byte, error) { return nil, nil }},
			remove:   "old.json",
			status:   StatusCorpusError,
			contains: []string{"missing old.json"},
		},
		{
			name:     "missing new",
			bind:     Binding{DiffToHew: func([]byte, []byte, string) ([]byte, error) { return nil, nil }},
			remove:   "new.json",
			status:   StatusCorpusError,
			contains: []string{"missing new.json"},
		},
		{
			name:     "missing expected.hew",
			bind:     Binding{DiffToHew: func([]byte, []byte, string) ([]byte, error) { return nil, nil }},
			remove:   "expected.hew",
			status:   StatusCorpusError,
			contains: []string{"missing expected.hew"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := setupEngine(t, "json/diff-case", diffCaseFiles(), tc.bind)
			if tc.remove != "" {
				f.remove(tc.remove)
			}
			wantOutcome(t, f.run(SeamDiff), f.Case, SeamDiff, tc.status, tc.contains...)
		})
	}
}

// --- cli seam ------------------------------------------------------------

// cliStub is a scripted CLI: it records what it was handed, optionally
// rewrites the scratch target (the in-place cases) and returns a fixed exit.
type cliStub struct {
	exit      int
	stdout    string
	stderr    string
	rewrite   string // if non-empty, target.json is rewritten with this
	gotArgv   []string
	gotDir    string
	gotStdin  string
	callCount int
}

func (s *cliStub) run(argv []string, dir string, stdin io.Reader, stdout, stderr io.Writer) int {
	s.callCount++
	s.gotArgv = argv
	s.gotDir = dir
	if stdin != nil {
		b, _ := io.ReadAll(stdin)
		s.gotStdin = string(b)
	}
	if s.rewrite != "" {
		if err := os.WriteFile(filepath.Join(dir, "target.json"), []byte(s.rewrite), 0o644); err != nil {
			fmt.Fprintln(stderr, err)
			return 70
		}
	}
	io.WriteString(stdout, s.stdout)
	io.WriteString(stderr, s.stderr)
	return s.exit
}

func TestRunCLIUnbound(t *testing.T) {
	f := setupEngine(t, "cli/case", cliCaseFiles(), Binding{})
	wantOutcome(t, f.run(SeamCLI), f.Case, SeamCLI, StatusFail, "seam cli unbound (RunCLI hook is nil)")
}

func TestRunCLIInvocation(t *testing.T) {
	stub := &cliStub{}
	f := setupEngine(t, "cli/case", cliCaseFiles(), Binding{RunCLI: stub.run})
	wantOutcome(t, f.run(SeamCLI), f.Case, SeamCLI, StatusPass)
	if stub.callCount != 1 {
		t.Errorf("RunCLI called %d times, want once", stub.callCount)
	}
	if !reflect.DeepEqual(stub.gotArgv, []string{"apply", "patch.hew", "target.json"}) {
		t.Errorf("argv = %v, want the manifest's argv without argv0", stub.gotArgv)
	}
	if stub.gotStdin != "" {
		t.Errorf("stdin = %q, want empty", stub.gotStdin)
	}
	if _, err := os.Stat(filepath.Join(stub.gotDir, "patch.hew")); err != nil {
		t.Errorf("the CLI must run in the scratch copy: %v", err)
	}
	if stub.gotDir == f.Case.Dir {
		t.Error("the CLI must not run in the corpus directory itself")
	}
}

func TestRunCLIExitCode(t *testing.T) {
	tests := []struct {
		name     string
		want     string
		got      int
		stderr   string
		status   Status
		contains []string
	}{
		{name: "matching zero", want: "exit: 0", got: 0, status: StatusPass},
		{name: "matching non-zero", want: "exit: 2", got: 2, status: StatusPass},
		{
			name: "mismatch reports stderr", want: "exit: 0", got: 3, stderr: "boom\n",
			status:   StatusFail,
			contains: []string{"exit: want 0, got 3", "(stderr: boom", ")"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := cliCaseFiles()
			files["case.yaml"] = manifestYAML("cli/case", "[cli]", "cli",
				"argv: [apply, patch.hew]", tc.want)
			stub := &cliStub{exit: tc.got, stderr: tc.stderr}
			f := setupEngine(t, "cli/case", files, Binding{RunCLI: stub.run})
			wantOutcome(t, f.run(SeamCLI), f.Case, SeamCLI, tc.status, tc.contains...)
		})
	}
}

func TestRunCLIStdout(t *testing.T) {
	tests := []struct {
		name     string
		manifest []string
		files    map[string]string
		stdout   string
		status   Status
		contains []string
	}{
		{
			name:   "unasserted stdout is ignored",
			stdout: "whatever it likes\n",
			status: StatusPass,
		},
		{
			name:     "empty-string assertion passes on empty output",
			manifest: []string{`stdout: ""`},
			stdout:   "",
			status:   StatusPass,
		},
		{
			name:     "empty-string assertion fails on any output",
			manifest: []string{`stdout: ""`},
			stdout:   "noise\n",
			status:   StatusFail,
			contains: []string{"stdout mismatch:", "byte mismatch: want 0 bytes, got 6 bytes"},
		},
		{
			name:     "fixture match",
			manifest: []string{"stdout: out.txt"},
			files:    map[string]string{"out.txt": "{\"a\": 2}\n"},
			stdout:   "{\"a\": 2}\n",
			status:   StatusPass,
		},
		{
			name:     "fixture mismatch",
			manifest: []string{"stdout: out.txt"},
			files:    map[string]string{"out.txt": "{\"a\": 2}\n"},
			stdout:   "{\"a\": 3}\n",
			status:   StatusFail,
			contains: []string{"stdout mismatch:", "byte mismatch:"},
		},
		{
			name:     "trailing newline difference is not tolerated",
			manifest: []string{"stdout: out.txt"},
			files:    map[string]string{"out.txt": "ok\n"},
			stdout:   "ok",
			status:   StatusFail,
			contains: []string{"stdout mismatch:", "␊"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := cliCaseFiles(tc.manifest...)
			for k, v := range tc.files {
				files[k] = v
			}
			stub := &cliStub{stdout: tc.stdout}
			f := setupEngine(t, "cli/case", files, Binding{RunCLI: stub.run})
			wantOutcome(t, f.run(SeamCLI), f.Case, SeamCLI, tc.status, tc.contains...)
		})
	}
}

func TestRunCLIMissingStdoutFixture(t *testing.T) {
	files := cliCaseFiles("stdout: out.txt")
	files["out.txt"] = "ok\n"
	stub := &cliStub{stdout: "ok\n"}
	f := setupEngine(t, "cli/case", files, Binding{RunCLI: stub.run})
	f.remove("out.txt")
	wantOutcome(t, f.run(SeamCLI), f.Case, SeamCLI, StatusCorpusError, "missing stdout fixture out.txt")
}

func TestRunCLIStderrContains(t *testing.T) {
	tests := []struct {
		name     string
		declared string
		stderr   string
		status   Status
		contains []string
	}{
		{
			name:     "all substrings present",
			declared: `stderr_contains: ["HEW010", "stale-target"]`,
			stderr:   "hew: target.json: HEW010 stale-target\n",
			status:   StatusPass,
		},
		{
			name:     "one substring missing",
			declared: `stderr_contains: ["HEW010", "stale-target"]`,
			stderr:   "hew: HEW010\n",
			status:   StatusFail,
			contains: []string{`stderr missing "stale-target"`, `(stderr: "hew: HEW010\n")`},
		},
		{
			name:     "every missing substring is reported",
			declared: `stderr_contains: ["alpha", "beta"]`,
			stderr:   "",
			status:   StatusFail,
			contains: []string{`stderr missing "alpha"`, `stderr missing "beta"`},
		},
		{
			name:     "no assertions",
			declared: "",
			stderr:   "anything\n",
			status:   StatusPass,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var extra []string
			if tc.declared != "" {
				extra = append(extra, tc.declared)
			}
			stub := &cliStub{stderr: tc.stderr}
			f := setupEngine(t, "cli/case", cliCaseFiles(extra...), Binding{RunCLI: stub.run})
			wantOutcome(t, f.run(SeamCLI), f.Case, SeamCLI, tc.status, tc.contains...)
		})
	}
}

func TestRunCLITargetUnchanged(t *testing.T) {
	t.Run("untouched target passes", func(t *testing.T) {
		stub := &cliStub{}
		f := setupEngine(t, "cli/case", cliCaseFiles("target_unchanged: true"), Binding{RunCLI: stub.run})
		wantOutcome(t, f.run(SeamCLI), f.Case, SeamCLI, StatusPass)
	})
	t.Run("rewritten target fails", func(t *testing.T) {
		stub := &cliStub{rewrite: "{\"a\": 99}\n"}
		f := setupEngine(t, "cli/case", cliCaseFiles("target_unchanged: true"), Binding{RunCLI: stub.run})
		wantOutcome(t, f.run(SeamCLI), f.Case, SeamCLI, StatusFail,
			"target target.json was modified by a failed apply")
	})
	t.Run("deleted target fails the check", func(t *testing.T) {
		f := setupEngine(t, "cli/case", cliCaseFiles("target_unchanged: true"), Binding{
			RunCLI: func(_ []string, dir string, _ io.Reader, _, _ io.Writer) int {
				_ = os.Remove(filepath.Join(dir, "target.json"))
				return 0
			},
		})
		wantOutcome(t, f.run(SeamCLI), f.Case, SeamCLI, StatusFail, "unchanged-target check:")
	})
	t.Run("not declared, rewriting is allowed", func(t *testing.T) {
		stub := &cliStub{rewrite: "{\"a\": 99}\n"}
		f := setupEngine(t, "cli/case", cliCaseFiles(), Binding{RunCLI: stub.run})
		wantOutcome(t, f.run(SeamCLI), f.Case, SeamCLI, StatusPass)
	})
}

// TestCheckUnchangedScope pins exactly which files the unchanged-target check
// covers: every target.* plus config.yaml (the git-fixture cases' target),
// and nothing else — a patch file the run rewrites is not a target.
func TestCheckUnchangedScope(t *testing.T) {
	files := func() map[string]string {
		return map[string]string{
			"case.yaml": manifestYAML("cli/case", "[cli]", "cli",
				"argv: [diff, config.yaml]", "exit: 0", "target_unchanged: true"),
			"patch.hew":   "PATCH:add-key\n",
			"config.yaml": "server:\n  timeout: 30\n",
		}
	}
	t.Run("config.yaml is covered", func(t *testing.T) {
		f := setupEngine(t, "cli/case", files(), Binding{
			RunCLI: writerCLI(map[string]*string{"config.yaml": strptr("server:\n  timeout: 45\n")}),
		})
		wantOutcome(t, f.run(SeamCLI), f.Case, SeamCLI, StatusFail,
			"target config.yaml was modified by a failed apply")
	})
	t.Run("a non-target file is not covered", func(t *testing.T) {
		f := setupEngine(t, "cli/case", files(), Binding{
			RunCLI: writerCLI(map[string]*string{"patch.hew": strptr("PATCH:rewritten\n")}),
		})
		wantOutcome(t, f.run(SeamCLI), f.Case, SeamCLI, StatusPass)
	})
}

// TestRunCLIExpectedPostRun is the in-place case: the CLI rewrites the scratch
// target and the harness compares it against the expected fixture.
func TestRunCLIExpectedPostRun(t *testing.T) {
	base := func(extra ...string) map[string]string {
		files := cliCaseFiles(append([]string{"expected: expected.json"}, extra...)...)
		files["expected.json"] = "{\"a\": 2}\n"
		return files
	}
	t.Run("rewritten to the expected bytes", func(t *testing.T) {
		stub := &cliStub{rewrite: "{\"a\": 2}\n"}
		f := setupEngine(t, "cli/case", base(), Binding{RunCLI: stub.run})
		wantOutcome(t, f.run(SeamCLI), f.Case, SeamCLI, StatusPass)
	})
	t.Run("rewritten to something else", func(t *testing.T) {
		stub := &cliStub{rewrite: "{\"a\": 3}\n"}
		f := setupEngine(t, "cli/case", base(), Binding{RunCLI: stub.run})
		wantOutcome(t, f.run(SeamCLI), f.Case, SeamCLI, StatusFail,
			"post-run target != expected fixture:", "byte mismatch:")
	})
	t.Run("target left untouched still fails the comparison", func(t *testing.T) {
		stub := &cliStub{}
		f := setupEngine(t, "cli/case", base(), Binding{RunCLI: stub.run})
		wantOutcome(t, f.run(SeamCLI), f.Case, SeamCLI, StatusFail, "post-run target != expected fixture:")
	})
	t.Run("target deleted by the run", func(t *testing.T) {
		f := setupEngine(t, "cli/case", base(), Binding{
			RunCLI: func(_ []string, dir string, _ io.Reader, _, _ io.Writer) int {
				_ = os.Remove(filepath.Join(dir, "target.json"))
				return 0
			},
		})
		wantOutcome(t, f.run(SeamCLI), f.Case, SeamCLI, StatusFail, "reading post-run target:")
	})
	t.Run("missing expected fixture", func(t *testing.T) {
		stub := &cliStub{rewrite: "{\"a\": 2}\n"}
		f := setupEngine(t, "cli/case", base(), Binding{RunCLI: stub.run})
		f.remove("expected.json")
		wantOutcome(t, f.run(SeamCLI), f.Case, SeamCLI, StatusCorpusError, "missing expected fixture expected.json")
	})
	t.Run("expected declared with no target file", func(t *testing.T) {
		files := map[string]string{
			"case.yaml": manifestYAML("cli/case", "[cli]", "cli",
				"argv: [apply, patch.hew]", "exit: 0", "expected: expected.json"),
			"patch.hew":     "PATCH:add-key\n",
			"expected.json": "{\"a\": 2}\n",
		}
		stub := &cliStub{}
		f := setupEngine(t, "cli/case", files, Binding{RunCLI: stub.run})
		wantOutcome(t, f.run(SeamCLI), f.Case, SeamCLI, StatusCorpusError,
			"expected: declared but no target.* file to compare")
	})
}

func TestRunCLIReportsEveryProblemTogether(t *testing.T) {
	files := cliCaseFiles(`stdout: ""`, `stderr_contains: ["needle"]`, "target_unchanged: true")
	stub := &cliStub{exit: 4, stdout: "noise\n", stderr: "haystack\n", rewrite: "{\"a\": 9}\n"}
	f := setupEngine(t, "cli/case", files, Binding{RunCLI: stub.run})
	out := f.run(SeamCLI)
	wantOutcome(t, out, f.Case, SeamCLI, StatusFail,
		"exit: want 0, got 4",
		"stdout mismatch:",
		`stderr missing "needle"`,
		"was modified by a failed apply",
	)
	if n := strings.Count(out.Detail, "\n"); n < 3 {
		t.Errorf("problems must be reported on separate lines, got %q", out.Detail)
	}
}

// TestRunCLIBuildsRequiredFixture: `requires:` runs before the CLI, in the
// scratch copy.
func TestRunCLIBuildsRequiredFixture(t *testing.T) {
	withBuilder(t, "test-fixture", func(dir string) error {
		return os.WriteFile(filepath.Join(dir, "built.txt"), []byte("built\n"), 0o644)
	})
	files := cliCaseFiles("requires: test-fixture")
	var sawBuilt bool
	f := setupEngine(t, "cli/case", files, Binding{
		RunCLI: func(_ []string, dir string, _ io.Reader, _, _ io.Writer) int {
			_, err := os.Stat(filepath.Join(dir, "built.txt"))
			sawBuilt = err == nil
			return 0
		},
	})
	wantOutcome(t, f.run(SeamCLI), f.Case, SeamCLI, StatusPass)
	if !sawBuilt {
		t.Error("the fixture builder must run before the CLI, in the scratch dir")
	}
}

func TestRunCLIFixtureBuildFailure(t *testing.T) {
	withBuilder(t, "test-fixture-fail", func(string) error { return failHook("no git here") })
	stub := &cliStub{}
	f := setupEngine(t, "cli/case", cliCaseFiles("requires: test-fixture-fail"), Binding{RunCLI: stub.run})
	wantOutcome(t, f.run(SeamCLI), f.Case, SeamCLI, StatusFail, "no git here")
	if stub.callCount != 0 {
		t.Error("a failed fixture build must not run the CLI")
	}
}

// --- multi-target cli assertions (ruling O12, §9.7) ----------------------

// multiTargetFiles is a two-target cli case: neither file is a target.*, so
// the manifest must name them explicitly.
func multiTargetFiles(extra ...string) map[string]string {
	return map[string]string{
		"case.yaml": manifestYAML("cli/multi", "[cli]", "cli",
			append([]string{"argv: [apply, patch.hew]", "exit: 0"}, extra...)...),
		"patch.hew":       "PATCH:two-targets\n",
		"a.json":          "{\"a\": 1}\n",
		"b.json":          "{\"b\": 1}\n",
		"expected-a.json": "{\"a\": 2}\n",
		"expected-b.json": "{\"b\": 2}\n",
	}
}

// writer rewrites named scratch files; nil content deletes.
func writerCLI(files map[string]*string) func([]string, string, io.Reader, io.Writer, io.Writer) int {
	return func(_ []string, dir string, _ io.Reader, _, _ io.Writer) int {
		for name, content := range files {
			p := filepath.Join(dir, name)
			if content == nil {
				_ = os.Remove(p)
				continue
			}
			if err := os.WriteFile(p, []byte(*content), 0o644); err != nil {
				return 70
			}
		}
		return 0
	}
}

func strptr(s string) *string { return &s }

func TestRunCLITargetsUnchanged(t *testing.T) {
	declared := "targets_unchanged: [a.json, b.json]"
	t.Run("both untouched", func(t *testing.T) {
		f := setupEngine(t, "cli/multi", multiTargetFiles(declared), Binding{RunCLI: (&cliStub{}).run})
		wantOutcome(t, f.run(SeamCLI), f.Case, SeamCLI, StatusPass)
	})
	t.Run("one modified", func(t *testing.T) {
		f := setupEngine(t, "cli/multi", multiTargetFiles(declared), Binding{
			RunCLI: writerCLI(map[string]*string{"b.json": strptr("{\"b\": 9}\n")}),
		})
		out := f.run(SeamCLI)
		wantOutcome(t, out, f.Case, SeamCLI, StatusFail, "target b.json was modified by a failed apply")
		if strings.Contains(out.Detail, "target a.json was modified") {
			t.Errorf("the untouched target must not be reported: %q", out.Detail)
		}
	})
	t.Run("both modified are both reported", func(t *testing.T) {
		f := setupEngine(t, "cli/multi", multiTargetFiles(declared), Binding{
			RunCLI: writerCLI(map[string]*string{"a.json": strptr("x\n"), "b.json": strptr("y\n")}),
		})
		out := f.run(SeamCLI)
		wantOutcome(t, out, f.Case, SeamCLI, StatusFail,
			"target a.json was modified", "target b.json was modified")
	})
	t.Run("deleted target", func(t *testing.T) {
		f := setupEngine(t, "cli/multi", multiTargetFiles(declared), Binding{
			RunCLI: writerCLI(map[string]*string{"a.json": nil}),
		})
		wantOutcome(t, f.run(SeamCLI), f.Case, SeamCLI, StatusFail, "reading a.json:")
	})
	t.Run("names a file the case does not ship", func(t *testing.T) {
		f := setupEngine(t, "cli/multi", multiTargetFiles("targets_unchanged: [ghost.json]"), Binding{RunCLI: (&cliStub{}).run})
		wantOutcome(t, f.run(SeamCLI), f.Case, SeamCLI, StatusCorpusError,
			"targets_unchanged names missing file ghost.json")
	})
}

func TestRunCLIExpectedTargets(t *testing.T) {
	declared := "expected_targets: {a.json: expected-a.json, b.json: expected-b.json}"
	t.Run("both rewritten as expected", func(t *testing.T) {
		f := setupEngine(t, "cli/multi", multiTargetFiles(declared), Binding{
			RunCLI: writerCLI(map[string]*string{
				"a.json": strptr("{\"a\": 2}\n"), "b.json": strptr("{\"b\": 2}\n"),
			}),
		})
		wantOutcome(t, f.run(SeamCLI), f.Case, SeamCLI, StatusPass)
	})
	t.Run("one target wrong", func(t *testing.T) {
		f := setupEngine(t, "cli/multi", multiTargetFiles(declared), Binding{
			RunCLI: writerCLI(map[string]*string{
				"a.json": strptr("{\"a\": 2}\n"), "b.json": strptr("{\"b\": 3}\n"),
			}),
		})
		out := f.run(SeamCLI)
		wantOutcome(t, out, f.Case, SeamCLI, StatusFail, "post-run b.json != expected fixture:")
		if strings.Contains(out.Detail, "post-run a.json") {
			t.Errorf("the conformant target must not be reported: %q", out.Detail)
		}
	})
	t.Run("both wrong, reported in sorted order", func(t *testing.T) {
		f := setupEngine(t, "cli/multi", multiTargetFiles(declared), Binding{
			RunCLI: writerCLI(map[string]*string{"a.json": strptr("nope\n"), "b.json": strptr("nope\n")}),
		})
		out := f.run(SeamCLI)
		ai, bi := strings.Index(out.Detail, "post-run a.json"), strings.Index(out.Detail, "post-run b.json")
		if ai < 0 || bi < 0 || ai > bi {
			t.Errorf("both targets must be reported, a.json first: %q", out.Detail)
		}
	})
	t.Run("deleted target", func(t *testing.T) {
		f := setupEngine(t, "cli/multi", multiTargetFiles("expected_targets: {a.json: expected-a.json}"), Binding{
			RunCLI: writerCLI(map[string]*string{"a.json": nil}),
		})
		wantOutcome(t, f.run(SeamCLI), f.Case, SeamCLI, StatusFail, "reading post-run target a.json:")
	})
	t.Run("missing expected fixture", func(t *testing.T) {
		f := setupEngine(t, "cli/multi", multiTargetFiles("expected_targets: {a.json: ghost.json}"), Binding{
			RunCLI: (&cliStub{}).run,
		})
		wantOutcome(t, f.run(SeamCLI), f.Case, SeamCLI, StatusCorpusError, "missing expected fixture ghost.json")
	})
}

// TestRunCLINoFilesCreated: "no --record, no record" — hew does not litter.
func TestRunCLINoFilesCreated(t *testing.T) {
	t.Run("nothing created passes", func(t *testing.T) {
		f := setupEngine(t, "cli/multi", multiTargetFiles(`no_files_created: ["*.hewr"]`), Binding{RunCLI: (&cliStub{}).run})
		wantOutcome(t, f.run(SeamCLI), f.Case, SeamCLI, StatusPass)
	})
	t.Run("a forbidden file is reported", func(t *testing.T) {
		f := setupEngine(t, "cli/multi", multiTargetFiles(`no_files_created: ["*.hewr"]`), Binding{
			RunCLI: writerCLI(map[string]*string{"apply.hewr": strptr("record\n")}),
		})
		wantOutcome(t, f.run(SeamCLI), f.Case, SeamCLI, StatusFail,
			"file created but no_files_created forbids it: apply.hewr")
	})
	t.Run("a pre-existing file matching the glob is not a creation", func(t *testing.T) {
		files := multiTargetFiles(`no_files_created: ["*.json"]`)
		f := setupEngine(t, "cli/multi", files, Binding{RunCLI: (&cliStub{}).run})
		wantOutcome(t, f.run(SeamCLI), f.Case, SeamCLI, StatusPass)
	})
	t.Run("rewriting a pre-existing file is not a creation", func(t *testing.T) {
		f := setupEngine(t, "cli/multi", multiTargetFiles(`no_files_created: ["*.json"]`), Binding{
			RunCLI: writerCLI(map[string]*string{"a.json": strptr("{\"a\": 2}\n")}),
		})
		wantOutcome(t, f.run(SeamCLI), f.Case, SeamCLI, StatusPass)
	})
	t.Run("bad glob is a corpus error", func(t *testing.T) {
		f := setupEngine(t, "cli/multi", multiTargetFiles(`no_files_created: ["["]`), Binding{RunCLI: (&cliStub{}).run})
		wantOutcome(t, f.run(SeamCLI), f.Case, SeamCLI, StatusCorpusError, "bad no_files_created glob [")
	})
	t.Run("every forbidden file is reported", func(t *testing.T) {
		f := setupEngine(t, "cli/multi", multiTargetFiles(`no_files_created: ["*.hewr", "*.bak"]`), Binding{
			RunCLI: writerCLI(map[string]*string{"a.hewr": strptr("r\n"), "a.bak": strptr("b\n")}),
		})
		wantOutcome(t, f.run(SeamCLI), f.Case, SeamCLI, StatusFail,
			"forbids it: a.hewr", "forbids it: a.bak")
	})
}

// TestRunCLIExpectedRecordIsALoudGap: until M10 recomputes digests, a case
// declaring expected_record must fail rather than pass vacuously.
func TestRunCLIExpectedRecordIsALoudGap(t *testing.T) {
	f := setupEngine(t, "cli/multi", multiTargetFiles("expected_record: record.json"), Binding{RunCLI: (&cliStub{}).run})
	wantOutcome(t, f.run(SeamCLI), f.Case, SeamCLI, StatusFail,
		"expected_record assertion not implemented (spec §9.7); M10")
}

func TestSortedKeys(t *testing.T) {
	tests := []struct {
		name string
		in   map[string]string
		want []string
	}{
		{"nil", nil, []string{}},
		{"one", map[string]string{"a": "1"}, []string{"a"}},
		{"sorted", map[string]string{"b": "2", "a": "1", "c": "3"}, []string{"a", "b", "c"}},
		{"numeric-ish", map[string]string{"t10": "", "t2": ""}, []string{"t10", "t2"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := sortedKeys(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("sortedKeys(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// --- cross-seam plumbing -------------------------------------------------

// TestRunSeamIndependence is runner obligation 2: each declared seam is its
// own execution, on its own scratch copy.
func TestRunSeamIndependence(t *testing.T) {
	files := applyCaseFiles("[parse, apply-ir, e2e]")
	files["transforms.hewt"] = "IR:add-key\n"
	dirs := map[Seam]string{}
	f := setupEngine(t, "json/apply-case", files, Binding{
		ParseToHewt: stubParse, CanonHewt: stubCanon,
		ApplyHewt:  func([]byte, []byte, string) ([]byte, error) { return []byte("{\"a\": 2}\n"), nil },
		ApplyPatch: func([]byte, []byte, string) ([]byte, error) { return []byte("{\"a\": 2}\n"), nil },
	})
	scratchRoot := t.TempDir()
	var last string
	f.Eng.Scratch = func() (string, error) {
		d, err := os.MkdirTemp(scratchRoot, "run-*")
		last = d
		return d, err
	}
	for _, seam := range f.Case.SortedSeams() {
		out := f.run(seam)
		wantOutcome(t, out, f.Case, seam, StatusPass)
		dirs[seam] = last
	}
	if dirs[SeamParse] == dirs[SeamApplyIR] || dirs[SeamApplyIR] == dirs[SeamE2E] {
		t.Errorf("each seam must get a fresh scratch dir, got %v", dirs)
	}
}

func TestStatusString(t *testing.T) {
	tests := []struct {
		status Status
		want   string
	}{
		{StatusPass, "pass"},
		{StatusFail, "fail"},
		{StatusSkip, "skip"},
		{StatusCorpusError, "corpus-error"},
		{Status(9), "status(9)"},
		{Status(-1), "status(-1)"},
	}
	for _, tc := range tests {
		if got := tc.status.String(); got != tc.want {
			t.Errorf("Status(%d).String() = %q, want %q", int(tc.status), got, tc.want)
		}
	}
}

func TestStatusIotaOrder(t *testing.T) {
	for i, s := range []Status{StatusPass, StatusFail, StatusSkip, StatusCorpusError} {
		if int(s) != i {
			t.Errorf("%s = %d, want %d", s, int(s), i)
		}
	}
	// StatusPass must be the zero value: a zero Outcome is a pass nowhere else.
	var zero Status
	if zero != StatusPass {
		t.Error("StatusPass must be the zero Status")
	}
}
