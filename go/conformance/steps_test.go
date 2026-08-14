package conformance

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"

	"github.com/hew-format/hew/internal/harness"
)

func readCaseFile(c *harness.Case, name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(c.Dir, name))
}

// corpusWorld is one scenario's state. Steps SELECT cases and run seams
// through the shared harness.Engine — they filter and aggregate, and never
// re-implement seam logic (that would be the parallel-runner mistake the
// engine exists to prevent).
type corpusWorld struct {
	eng      *harness.Engine
	cases    []*harness.Case
	selected []*harness.Case
	outcomes []harness.Outcome
}

func (w *corpusWorld) run(c *harness.Case, seam harness.Seam) {
	w.outcomes = append(w.outcomes, w.eng.RunSeam(c, seam))
}

// failures returns every non-pass, non-skip outcome rendered for a step error.
func (w *corpusWorld) failures() []string {
	var out []string
	for _, o := range w.outcomes {
		if o.Status == harness.StatusFail || o.Status == harness.StatusCorpusError {
			out = append(out, fmt.Sprintf("%s/%s [%s]: %s", o.Case, o.Seam, o.Status, o.Detail))
		}
	}
	return out
}

func (w *corpusWorld) noFailures() error {
	if f := w.failures(); len(f) > 0 {
		return fmt.Errorf("%d nonconformant seam run(s):\n%s", len(f), strings.Join(f, "\n"))
	}
	return nil
}

func (w *corpusWorld) declares(c *harness.Case, seam harness.Seam) bool {
	for _, s := range c.Seams {
		if s == seam {
			return true
		}
	}
	return false
}

// --- Givens -----------------------------------------------------------------

func (w *corpusWorld) givenCorpus(dir string) error {
	if base := filepath.Base(w.eng.CorpusDir); base != dir {
		return fmt.Errorf("feature expects corpus at %q, engine is bound to %q", dir, base)
	}
	if len(gCorpusErrs) > 0 {
		msgs := make([]string, len(gCorpusErrs))
		for i, e := range gCorpusErrs {
			msgs[i] = e.Error()
		}
		return fmt.Errorf("corpus has structural errors:\n%s", strings.Join(msgs, "\n"))
	}
	w.selected = w.cases
	return nil
}

func (w *corpusWorld) givenTransformsFixtureCases() error {
	w.selected = nil
	for _, c := range w.cases {
		if c.TransformsFile != "" || c.Roundtrip {
			w.selected = append(w.selected, c)
		}
	}
	if len(w.selected) == 0 {
		return fmt.Errorf("no corpus case carries a transforms fixture")
	}
	return nil
}

func (w *corpusWorld) givenRoundTripCases() error {
	w.selected = nil
	for _, c := range w.cases {
		if c.Roundtrip {
			w.selected = append(w.selected, c)
		}
	}
	if len(w.selected) == 0 {
		return fmt.Errorf("no corpus round-trip cases found")
	}
	return nil
}

func (w *corpusWorld) givenCLICases() error {
	w.selected = nil
	for _, c := range w.cases {
		if c.Kind == "cli" {
			w.selected = append(w.selected, c)
		}
	}
	if len(w.selected) == 0 {
		return fmt.Errorf("no corpus CLI cases found")
	}
	return nil
}

func (w *corpusWorld) givenTransformsCLICase() error {
	w.selected = nil
	for _, c := range w.cases {
		if c.Kind != "cli" {
			continue
		}
		for _, arg := range c.Argv {
			if arg == "--transforms" {
				w.selected = append(w.selected, c)
			}
		}
	}
	// The escape-hatch scenario wants the apply case, not the usage-error
	// case: keep only exit-0 invocations.
	kept := w.selected[:0]
	for _, c := range w.selected {
		if c.Exit != nil && *c.Exit == 0 {
			kept = append(kept, c)
		}
	}
	w.selected = kept
	if len(w.selected) == 0 {
		return fmt.Errorf("no CLI case invokes apply with a transforms file and exit 0")
	}
	return nil
}

// --- Whens ------------------------------------------------------------------

func (w *corpusWorld) whenApplyCasesRun() error {
	for _, c := range w.cases {
		if c.Kind != "ok" {
			continue
		}
		for _, seam := range []harness.Seam{harness.SeamApplyIR, harness.SeamE2E} {
			if w.declares(c, seam) {
				w.run(c, seam)
			}
		}
	}
	if len(w.outcomes) == 0 {
		return fmt.Errorf("no apply-family runs selected")
	}
	return nil
}

func (w *corpusWorld) whenErrorCasesRun() error {
	for _, c := range w.cases {
		if c.Kind != "error" {
			continue
		}
		for _, seam := range c.SortedSeams() {
			w.run(c, seam)
		}
	}
	if len(w.outcomes) == 0 {
		return fmt.Errorf("no error cases selected")
	}
	return nil
}

func (w *corpusWorld) whenToleranceCasesRun() error {
	for _, c := range w.cases {
		if ok, _ := path.Match("*/tolerance-*", c.Rel); !ok {
			continue
		}
		for _, seam := range c.SortedSeams() {
			w.run(c, seam)
		}
	}
	if len(w.outcomes) == 0 {
		return fmt.Errorf("no */tolerance-* cases found")
	}
	return nil
}

func (w *corpusWorld) whenPatchesParsed() error {
	for _, c := range w.selected {
		if w.declares(c, harness.SeamParse) {
			w.run(c, harness.SeamParse)
		}
	}
	if len(w.outcomes) == 0 {
		return fmt.Errorf("no parse-seam runs selected")
	}
	return nil
}

func (w *corpusWorld) whenTransformsApplied() error {
	for _, c := range w.selected {
		if w.declares(c, harness.SeamApplyIR) {
			w.run(c, harness.SeamApplyIR)
		}
	}
	if len(w.outcomes) == 0 {
		return fmt.Errorf("no apply-ir runs selected")
	}
	return nil
}

// whenRoundTripChain runs the literal RT1 chain the scenario narrates:
// diff -> render -> re-parse -> apply == new. The per-seam independence of
// the same fixtures is covered by TestCorpus; this is the one deliberately
// chained execution, because the scenario text says so.
func (w *corpusWorld) whenRoundTripChain() error {
	for _, c := range w.selected {
		// The chain spans four components; if ANY of its seams is under a
		// recorded skip, the whole chain is skipped with that reason.
		if reason, skipped := w.chainSkip(c); skipped {
			w.outcomes = append(w.outcomes, harness.Outcome{Case: c.Rel, Seam: "rt1-chain", Status: harness.StatusSkip, Detail: reason})
			continue
		}
		w.outcomes = append(w.outcomes, w.runRT1(c))
	}
	return nil
}

func (w *corpusWorld) chainSkip(c *harness.Case) (string, bool) {
	for _, seam := range []harness.Seam{harness.SeamDiff, harness.SeamRender, harness.SeamParse, harness.SeamApplyIR} {
		if reason, ok := w.eng.Skips.Lookup(c.Rel, seam); ok {
			return reason, true
		}
	}
	return "", false
}

func (w *corpusWorld) runRT1(c *harness.Case) harness.Outcome {
	fail := func(msg string) harness.Outcome {
		return harness.Outcome{Case: c.Rel, Seam: "rt1-chain", Status: harness.StatusFail, Detail: msg}
	}
	b := w.eng.Bind
	if b.DiffToHew == nil || b.ParseToHewt == nil || b.ApplyHewt == nil {
		return fail("rt1 chain unbound (diff/parse/apply hooks nil) and no skip rule matches")
	}
	read := func(name string) ([]byte, error) {
		return readCaseFile(c, name)
	}
	old, err := read(c.OldFile)
	if err != nil {
		return fail(err.Error())
	}
	new_, err := read(c.NewFile)
	if err != nil {
		return fail(err.Error())
	}
	hewText, err := b.DiffToHew(old, new_, c.Format)
	if err != nil {
		return fail("diff: " + err.Error())
	}
	hewt, err := b.ParseToHewt(hewText)
	if err != nil {
		return fail("re-parse of diffed notation: " + err.Error())
	}
	got, err := b.ApplyHewt(hewt, old, c.Format)
	if err != nil {
		return fail("apply: " + err.Error())
	}
	if d := harness.DiffBytes(new_, got); d != "" {
		return fail("RT1 violated: apply(parse(diff(old,new)), old) != new\n" + d)
	}
	return harness.Outcome{Case: c.Rel, Seam: "rt1-chain", Status: harness.StatusPass}
}

func (w *corpusWorld) whenCLICasesRun() error {
	for _, c := range w.selected {
		w.run(c, harness.SeamCLI)
	}
	if len(w.outcomes) == 0 {
		return fmt.Errorf("no CLI runs selected")
	}
	return nil
}

func (w *corpusWorld) whenTransformsCLIRuns() error {
	for _, c := range w.selected {
		for _, seam := range c.SortedSeams() {
			w.run(c, seam)
		}
	}
	return nil
}

// --- Thens ------------------------------------------------------------------

func (w *corpusWorld) thenNoUnexplainedSkips() error {
	for _, o := range w.outcomes {
		if o.Status == harness.StatusSkip && strings.TrimSpace(o.Detail) == "" {
			return fmt.Errorf("%s/%s skipped without a recorded reason", o.Case, o.Seam)
		}
	}
	return nil
}

func InitializeScenario(sc *godog.ScenarioContext) {
	w := &corpusWorld{}
	sc.Before(func(ctx context.Context, scn *godog.Scenario) (context.Context, error) {
		initCorpus()
		if gInitErr != nil {
			return ctx, gInitErr
		}
		w.eng, w.cases = gEngine, gCases
		w.selected, w.outcomes = nil, nil
		return ctx, nil
	})

	sc.Given(`^the conformance corpus at "([^"]*)"$`, w.givenCorpus)
	sc.Given(`^every corpus case that carries a transforms fixture$`, w.givenTransformsFixtureCases)
	sc.Given(`^every corpus round-trip case$`, w.givenRoundTripCases)
	sc.Given(`^the corpus CLI cases$`, w.givenCLICases)
	sc.Given(`^a corpus CLI case invoking apply with a transforms file$`, w.givenTransformsCLICase)

	sc.When(`^each apply case's patch is applied to its input document$`, w.whenApplyCasesRun)
	sc.When(`^each error case's patch is applied to its input document$`, w.whenErrorCasesRun)
	sc.When(`^each tolerance case's patch is applied to its drifted input$`, w.whenToleranceCasesRun)
	sc.When(`^the case's patch is parsed$`, w.whenPatchesParsed)
	sc.When(`^the fixture's transforms are applied to the input document$`, w.whenTransformsApplied)
	sc.When(`^the old and new documents are diffed, the transforms rendered to notation, the notation re-parsed, and the result applied to the old document$`, w.whenRoundTripChain)
	sc.When(`^each case's documented invocation runs$`, w.whenCLICasesRun)
	sc.When(`^the invocation runs$`, w.whenTransformsCLIRuns)

	sc.Then(`^the result is byte-identical to the case's expected output$`, w.noFailures)
	sc.Then(`^no case is skipped without a recorded skip reason$`, w.thenNoUnexplainedSkips)
	sc.Then(`^the implementation reports the case's declared error code$`, w.noFailures)
	sc.Then(`^the error names the seam declared by the case$`, w.noFailures)
	sc.Then(`^the target file is left byte-identical to its input$`, w.noFailures)
	sc.Then(`^the resulting transform list equals the fixture$`, w.noFailures)
	sc.Then(`^the final document is byte-identical to the new document$`, w.noFailures)
	sc.Then(`^exit code 0 means the patch applied$`, w.noFailures)
	sc.Then(`^exit code 1 means the patch did not apply and nothing was modified$`, w.noFailures)
	sc.Then(`^exit code 2 means trouble$`, w.noFailures)
	sc.Then(`^stdout and stderr match the case's declared contracts$`, w.noFailures)
	sc.Then(`^the result equals applying the equivalent notation patch$`, w.noFailures)
}
