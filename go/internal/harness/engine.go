package harness

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

// Binding wires the implementation under test into the engine. Every hook
// speaks canonical .hewt bytes at the IR boundary, pinning the IR exactly
// where the corpus does. A nil hook for a declared seam is a FAILURE unless a
// skip rule covers it — that is the milestone mechanism.
type Binding struct {
	// ParseToHewt compiles .hew notation to canonical .hewt bytes (parse seam).
	ParseToHewt func(patch []byte) ([]byte, error)
	// CanonHewt re-canonicalizes .hewt bytes (unmarshal→marshal), so
	// hand-authored corpus fixtures compare against parser output at the
	// value level while the comparison itself stays byte-exact.
	CanonHewt func(hewt []byte) ([]byte, error)
	// ApplyHewt applies a .hewt transform list to target bytes (apply-ir seam).
	ApplyHewt func(hewt, target []byte, format string) ([]byte, error)
	// ApplyPatch applies .hew notation to target bytes (e2e seam).
	ApplyPatch func(patch, target []byte, format string) ([]byte, error)
	// RenderHew renders .hewt bytes to .hew notation (render seam, RT2).
	RenderHew func(hewt []byte) ([]byte, error)
	// DiffToHew diffs two documents to .hew notation (diff seam). target is
	// the new side's file name, which the produced patch carries in its
	// "--- <target>" header — a diff says "apply this to get here", and the
	// file it names is the new one.
	DiffToHew func(old, new []byte, format, target string) ([]byte, error)
	// RunCLI runs the hew CLI in-process: argv (without argv0), a working
	// directory all relative paths resolve against, and explicit streams.
	RunCLI func(argv []string, dir string, stdin io.Reader, stdout, stderr io.Writer) int
}

// Status classifies one seam run.
type Status int

const (
	StatusPass Status = iota
	StatusFail
	StatusSkip
	StatusCorpusError
)

func (s Status) String() string {
	switch s {
	case StatusPass:
		return "pass"
	case StatusFail:
		return "fail"
	case StatusSkip:
		return "skip"
	case StatusCorpusError:
		return "corpus-error"
	}
	return fmt.Sprintf("status(%d)", int(s))
}

// Outcome is one seam run's result. Detail carries the first-divergence
// report, the collected error-contract mismatches, or the skip reason.
type Outcome struct {
	Case   string
	Seam   Seam
	Status Status
	Detail string
}

// Engine runs corpus seams through a Binding. Scratch supplies a fresh
// directory per seam run (the go-test frontend passes t.TempDir).
type Engine struct {
	CorpusDir string
	Bind      Binding
	Skips     *SkipRegistry
	Scratch   func() (string, error)
}

func (e *Engine) outcome(c *Case, s Seam, st Status, detail string) Outcome {
	return Outcome{Case: c.Rel, Seam: s, Status: st, Detail: detail}
}

// RunSeam executes one declared seam of one case in a scratch copy.
func (e *Engine) RunSeam(c *Case, seam Seam) Outcome {
	if reason, ok := e.Skips.Lookup(c.Rel, seam); ok {
		if e.Skips.NoSkips {
			return e.outcome(c, seam, StatusFail, "skips disallowed (HEW_CORPUS_NO_SKIPS=1); rule reason: "+reason)
		}
		return e.outcome(c, seam, StatusSkip, reason)
	}
	scratch, err := e.Scratch()
	if err != nil {
		return e.outcome(c, seam, StatusFail, "scratch: "+err.Error())
	}
	if err := CopyCase(c.Dir, scratch); err != nil {
		return e.outcome(c, seam, StatusCorpusError, err.Error())
	}
	snap, err := Snapshot(scratch)
	if err != nil {
		return e.outcome(c, seam, StatusFail, "snapshot: "+err.Error())
	}

	read := func(name string) ([]byte, bool) { b, ok := snap[name]; return b, ok }
	mustRead := func(name string) ([]byte, error) {
		if b, ok := read(name); ok {
			return b, nil
		}
		return nil, fmt.Errorf("corpus error: %s missing %s", c.Rel, name)
	}

	switch seam {
	case SeamParse:
		return e.runParse(c, seam, mustRead)
	case SeamApplyIR:
		return e.runApply(c, seam, scratch, snap, true)
	case SeamE2E:
		return e.runApply(c, seam, scratch, snap, false)
	case SeamRender:
		return e.runRender(c, seam, mustRead)
	case SeamDiff:
		return e.runDiff(c, seam, mustRead)
	case SeamCLI:
		return e.runCLI(c, seam, scratch, snap)
	}
	return e.outcome(c, seam, StatusCorpusError, fmt.Sprintf("unknown seam %q", seam))
}

func (e *Engine) unbound(c *Case, s Seam, hook string) Outcome {
	return e.outcome(c, s, StatusFail, fmt.Sprintf("seam %s unbound (%s hook is nil) and no skip rule matches", s, hook))
}

func (e *Engine) runParse(c *Case, seam Seam, mustRead func(string) ([]byte, error)) Outcome {
	if e.Bind.ParseToHewt == nil {
		return e.unbound(c, seam, "ParseToHewt")
	}
	patchName := c.PatchFile
	if c.Roundtrip {
		patchName = c.ExpectedHewFile
	}
	patch, err := mustRead(patchName)
	if err != nil {
		return e.outcome(c, seam, StatusCorpusError, err.Error())
	}
	got, perr := e.Bind.ParseToHewt(patch)

	switch {
	case c.Kind == "error":
		if probs := CheckError(perr, &c.Manifest); len(probs) > 0 {
			return e.outcome(c, seam, StatusFail, strings.Join(probs, "\n"))
		}
		return e.outcome(c, seam, StatusPass, "")
	case c.Kind == "cli":
		// Quirk adapter: a cli-kind case with a bonus parse seam and no
		// error: field (cli/empty-patch-exit-2) expects parse failure with
		// the HEW-code embedded in stderr_contains.
		wantCode, ok := c.StderrCode()
		if !ok {
			return e.outcome(c, seam, StatusCorpusError, "cli-kind parse seam with no HEW code in stderr_contains")
		}
		if perr == nil {
			return e.outcome(c, seam, StatusFail, fmt.Sprintf("expected parse failure %s, got success", wantCode))
		}
		he, isHew := hewerr.As(perr)
		if !isHew {
			return e.outcome(c, seam, StatusFail, "error is not a *hewerr.Error: "+perr.Error())
		}
		if string(he.Code) != wantCode {
			return e.outcome(c, seam, StatusFail, fmt.Sprintf("code: want %s, got %s", wantCode, he.Code))
		}
		return e.outcome(c, seam, StatusPass, "")
	default: // ok
		if perr != nil {
			return e.outcome(c, seam, StatusFail, "parse failed: "+perr.Error())
		}
		if c.Roundtrip || c.TransformsFile == "" {
			return e.outcome(c, seam, StatusPass, "") // no pinned IR; success is the assertion
		}
		if e.Bind.CanonHewt == nil {
			return e.unbound(c, seam, "CanonHewt")
		}
		fixture, err := mustRead(c.TransformsFile)
		if err != nil {
			return e.outcome(c, seam, StatusCorpusError, err.Error())
		}
		want, cerr := e.Bind.CanonHewt(fixture)
		if cerr != nil {
			return e.outcome(c, seam, StatusFail, "transforms fixture does not canonicalize: "+cerr.Error())
		}
		if d := DiffBytes(want, got); d != "" {
			return e.outcome(c, seam, StatusFail, "parsed IR != pinned transforms fixture (both canonicalized)\n"+d)
		}
		return e.outcome(c, seam, StatusPass, "")
	}
}

// runApply covers apply-ir (viaIR=true) and e2e (viaIR=false); the two seams
// share their target/expected plumbing but MUST stay independent executions
// (runner obligation 2) — this shares code, not results.
func (e *Engine) runApply(c *Case, seam Seam, scratch string, snap map[string][]byte, viaIR bool) Outcome {
	targetName, expectedName := c.TargetFile, c.ExpectedFile
	if c.Roundtrip {
		targetName, expectedName = c.OldFile, c.NewFile
	}
	target, ok := snap[targetName]
	if !ok {
		return e.outcome(c, seam, StatusCorpusError, "missing target "+targetName)
	}

	var got []byte
	var aerr error
	if viaIR {
		if e.Bind.ApplyHewt == nil {
			return e.unbound(c, seam, "ApplyHewt")
		}
		var hewt []byte
		if c.Roundtrip {
			if e.Bind.ParseToHewt == nil {
				return e.unbound(c, seam, "ParseToHewt")
			}
			expectedHew := snap[c.ExpectedHewFile]
			var perr error
			hewt, perr = e.Bind.ParseToHewt(expectedHew)
			if perr != nil {
				return e.outcome(c, seam, StatusFail, "deriving IR from expected.hew: "+perr.Error())
			}
		} else {
			hewt, ok = snap[c.TransformsFile]
			if !ok {
				return e.outcome(c, seam, StatusCorpusError, "missing transforms fixture")
			}
		}
		got, aerr = e.Bind.ApplyHewt(hewt, target, c.Format)
	} else {
		if e.Bind.ApplyPatch == nil {
			return e.unbound(c, seam, "ApplyPatch")
		}
		patchName := c.PatchFile
		if c.Roundtrip {
			patchName = c.ExpectedHewFile
		}
		patch, ok := snap[patchName]
		if !ok {
			return e.outcome(c, seam, StatusCorpusError, "missing patch "+patchName)
		}
		got, aerr = e.Bind.ApplyPatch(patch, target, c.Format)
	}

	if c.Kind == "error" {
		var probs []string
		if p := CheckError(aerr, &c.Manifest); len(p) > 0 {
			probs = p
		}
		if got != nil {
			probs = append(probs, "applier returned non-nil bytes alongside an error (all-or-nothing violated)")
		}
		if d := e.checkUnchanged(scratch, snap); d != "" {
			probs = append(probs, d)
		}
		if len(probs) > 0 {
			return e.outcome(c, seam, StatusFail, strings.Join(probs, "\n"))
		}
		return e.outcome(c, seam, StatusPass, "")
	}

	if aerr != nil {
		return e.outcome(c, seam, StatusFail, "apply failed: "+aerr.Error())
	}
	want, ok := snap[expectedName]
	if !ok {
		return e.outcome(c, seam, StatusCorpusError, "missing expected "+expectedName)
	}
	if d := DiffBytes(want, got); d != "" {
		return e.outcome(c, seam, StatusFail, d)
	}
	return e.outcome(c, seam, StatusPass, "")
}

func (e *Engine) runRender(c *Case, seam Seam, mustRead func(string) ([]byte, error)) Outcome {
	if e.Bind.RenderHew == nil {
		return e.unbound(c, seam, "RenderHew")
	}
	if e.Bind.ParseToHewt == nil {
		return e.unbound(c, seam, "ParseToHewt")
	}
	// RT2: hewt -> .hew -> hewt must be the identity on the canonical IR.
	var base []byte
	if c.Roundtrip {
		expectedHew, err := mustRead(c.ExpectedHewFile)
		if err != nil {
			return e.outcome(c, seam, StatusCorpusError, err.Error())
		}
		var perr error
		base, perr = e.Bind.ParseToHewt(expectedHew)
		if perr != nil {
			return e.outcome(c, seam, StatusFail, "deriving IR from expected.hew: "+perr.Error())
		}
	} else {
		if e.Bind.CanonHewt == nil {
			return e.unbound(c, seam, "CanonHewt")
		}
		fixture, err := mustRead(c.TransformsFile)
		if err != nil {
			return e.outcome(c, seam, StatusCorpusError, err.Error())
		}
		var cerr error
		base, cerr = e.Bind.CanonHewt(fixture)
		if cerr != nil {
			return e.outcome(c, seam, StatusFail, "transforms fixture does not canonicalize: "+cerr.Error())
		}
	}
	hewText, rerr := e.Bind.RenderHew(base)
	if rerr != nil {
		return e.outcome(c, seam, StatusFail, "render failed: "+rerr.Error())
	}
	re, perr := e.Bind.ParseToHewt(hewText)
	if perr != nil {
		return e.outcome(c, seam, StatusFail, "re-parse of rendered notation failed: "+perr.Error())
	}
	if d := DiffBytes(base, re); d != "" {
		return e.outcome(c, seam, StatusFail, "RT2 violated: parse(render(ir)) != ir\n"+d)
	}
	return e.outcome(c, seam, StatusPass, "")
}

func (e *Engine) runDiff(c *Case, seam Seam, mustRead func(string) ([]byte, error)) Outcome {
	if e.Bind.DiffToHew == nil {
		return e.unbound(c, seam, "DiffToHew")
	}
	old, err := mustRead(c.OldFile)
	if err != nil {
		return e.outcome(c, seam, StatusCorpusError, err.Error())
	}
	new_, err := mustRead(c.NewFile)
	if err != nil {
		return e.outcome(c, seam, StatusCorpusError, err.Error())
	}
	want, err := mustRead(c.ExpectedHewFile)
	if err != nil {
		return e.outcome(c, seam, StatusCorpusError, err.Error())
	}
	got, derr := e.Bind.DiffToHew(old, new_, c.Format, c.NewFile)
	if derr != nil {
		return e.outcome(c, seam, StatusFail, "diff failed: "+derr.Error())
	}
	if d := DiffBytes(want, got); d != "" {
		return e.outcome(c, seam, StatusFail, d)
	}
	return e.outcome(c, seam, StatusPass, "")
}

func (e *Engine) runCLI(c *Case, seam Seam, scratch string, snap map[string][]byte) Outcome {
	if e.Bind.RunCLI == nil {
		return e.unbound(c, seam, "RunCLI")
	}
	if err := BuildFixture(&c.Manifest, scratch); err != nil {
		return e.outcome(c, seam, StatusFail, err.Error())
	}
	var stdout, stderr bytes.Buffer
	exit := e.Bind.RunCLI(c.Argv, scratch, strings.NewReader(""), &stdout, &stderr)

	var probs []string
	if c.Exit != nil && exit != *c.Exit {
		probs = append(probs, fmt.Sprintf("exit: want %d, got %d (stderr: %s)", *c.Exit, exit, stderr.String()))
	}
	if c.Stdout != nil {
		var want []byte
		if *c.Stdout != "" {
			b, ok := snap[*c.Stdout]
			if !ok {
				return e.outcome(c, seam, StatusCorpusError, "missing stdout fixture "+*c.Stdout)
			}
			want = b
		}
		if d := DiffBytes(want, stdout.Bytes()); d != "" {
			probs = append(probs, "stdout mismatch:\n"+d)
		}
	}
	for _, wantSub := range c.StderrContains {
		if !strings.Contains(stderr.String(), wantSub) {
			probs = append(probs, fmt.Sprintf("stderr missing %q (stderr: %q)", wantSub, stderr.String()))
		}
	}
	if c.TargetUnchanged {
		if d := e.checkUnchanged(scratch, snap); d != "" {
			probs = append(probs, d)
		}
	}
	// Multi-target cases name their targets explicitly (§10.5, ruling O12).
	for _, name := range c.TargetsUnchanged {
		want, ok := snap[name]
		if !ok {
			return e.outcome(c, seam, StatusCorpusError, "targets_unchanged names missing file "+name)
		}
		got, err := os.ReadFile(filepath.Join(scratch, name))
		if err != nil {
			probs = append(probs, "reading "+name+": "+err.Error())
		} else if !bytes.Equal(want, got) {
			probs = append(probs, fmt.Sprintf("target %s was modified by a failed apply:\n%s", name, DiffBytes(want, got)))
		}
	}
	for _, name := range sortedKeys(c.ExpectedTargets) {
		want, ok := snap[c.ExpectedTargets[name]]
		if !ok {
			return e.outcome(c, seam, StatusCorpusError, "missing expected fixture "+c.ExpectedTargets[name])
		}
		got, err := os.ReadFile(filepath.Join(scratch, name))
		if err != nil {
			probs = append(probs, "reading post-run target "+name+": "+err.Error())
		} else if d := DiffBytes(want, got); d != "" {
			probs = append(probs, "post-run "+name+" != expected fixture:\n"+d)
		}
	}
	// "No --record, no record": hew does not litter (§9.7).
	for _, pattern := range c.NoFilesCreated {
		hits, err := filepath.Glob(filepath.Join(scratch, pattern))
		if err != nil {
			return e.outcome(c, seam, StatusCorpusError, "bad no_files_created glob "+pattern)
		}
		for _, hit := range hits {
			if _, pre := snap[filepath.Base(hit)]; !pre {
				probs = append(probs, "file created but no_files_created forbids it: "+filepath.Base(hit))
			}
		}
	}
	if c.ExpectedRecord != "" {
		recordFile := recordArgValue(c.Argv, "--record")
		if recordFile == "" {
			return e.outcome(c, seam, StatusCorpusError, "expected_record set but argv has no --record flag")
		}
		wantFixture, ok := snap[c.ExpectedRecord]
		if !ok {
			return e.outcome(c, seam, StatusCorpusError, "missing expected_record fixture "+c.ExpectedRecord)
		}
		gotBytes, err := os.ReadFile(filepath.Join(scratch, recordFile))
		if err != nil {
			probs = append(probs, "reading record file "+recordFile+": "+err.Error())
		} else {
			fx := RecordFixtures{
				Pre: func(name string) ([]byte, bool) { b, ok := snap[name]; return b, ok },
				Post: func(name string) ([]byte, bool) {
					b, rerr := os.ReadFile(filepath.Join(scratch, name))
					return b, rerr == nil
				},
			}
			probs = append(probs, CheckRecord(wantFixture, gotBytes, c.RecordDigestFields, fx)...)
		}
	}
	if c.Expected != "" {
		want, ok := snap[c.Expected]
		if !ok {
			return e.outcome(c, seam, StatusCorpusError, "missing expected fixture "+c.Expected)
		}
		if c.TargetFile == "" {
			return e.outcome(c, seam, StatusCorpusError, "expected: declared but no target.* file to compare")
		}
		got, err := os.ReadFile(filepath.Join(scratch, c.TargetFile))
		if err != nil {
			probs = append(probs, "reading post-run target: "+err.Error())
		} else if d := DiffBytes(want, got); d != "" {
			probs = append(probs, "post-run target != expected fixture:\n"+d)
		}
	}
	if len(probs) > 0 {
		return e.outcome(c, seam, StatusFail, strings.Join(probs, "\n"))
	}
	return e.outcome(c, seam, StatusPass, "")
}

// checkUnchanged asserts the scratch target file still holds its pre-run
// bytes (runner obligation 4's unchanged-target clause). For pure
// bytes-in/bytes-out library seams this is trivially true today; it stays on
// uniformly to guard any future file-writing path.
func (e *Engine) checkUnchanged(scratch string, snap map[string][]byte) string {
	for name, want := range snap {
		if !strings.HasPrefix(name, "target.") && name != "config.yaml" {
			continue
		}
		got, err := os.ReadFile(filepath.Join(scratch, name))
		if err != nil {
			return "unchanged-target check: " + err.Error()
		}
		if !bytes.Equal(want, got) {
			return fmt.Sprintf("target %s was modified by a failed apply:\n%s", name, DiffBytes(want, got))
		}
	}
	return ""
}

// sortedKeys gives a map-driven assertion a deterministic report order.
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
