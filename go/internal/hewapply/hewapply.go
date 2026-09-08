// Package hewapply is the apply loop every binding runs.
//
// Applying a transform list is one sequence, and it is the same sequence for
// every format: refuse the qualifiers this binding does not implement, then for
// each transform in order — reparse the current bytes, evaluate an assert,
// skip a hint, plan an edit, splice it. None of that is a format's own
// business. What IS the format's own business is parsing the document and
// deciding what a transform means against it, which is what Binding supplies.
//
// The sequence living once matters more than the lines it saves. Its rules are
// exactly the ones that must not differ between bindings and would not fail
// loudly if they did: that the document is REPARSED between transforms (§4.5e
// reads advisories against what the next transform will meet), that a hint
// never edits and never asserts, and that an empty plan is a no-op rather than
// an error. A binding that quietly stopped reparsing, or that treated a hint as
// an assertion, would pass its own tests and disagree with every other format.
package hewapply

import (
	hew "github.com/benjaminabbitt/hew/go"
	"github.com/benjaminabbitt/hew/go/internal/hewerr"
	"github.com/benjaminabbitt/hew/go/internal/hewsplice"
)

// RunContext is the per-transform state the loop threads into a binding's run.
type RunContext struct {
	// Target is the file the patch names, for diagnostics.
	Target string
	// All is the whole transform list, which a binding reads to find the
	// PAIRED WRITE at a path — the write that decides whether a failed assert
	// is drift or "already applied" (§10.6).
	All []hew.Transform
	// Converged spans the WHOLE apply, not one transform: whether a path's
	// before-image assert was tolerated as already-applied has to be visible
	// to that path's own write, wherever in the list it falls. The loop owns
	// it for that reason; a binding with no such rule may ignore it.
	Converged map[string]bool
	// Adv is this transform's advisory, already migrated (§4.5e) so the
	// patch's own earlier edits are compensated before it is read.
	Adv hew.Advisory
}

// Run is one transform's evaluation against a freshly parsed document.
type Run interface {
	// EvalTest evaluates an assert, returning the failure to report.
	EvalTest(t hew.Transform) error
	// PlanOne resolves a mutation to the byte edits that carry it out. No
	// edits is a no-op, not a failure — a converged transform plans nothing.
	PlanOne(t hew.Transform) ([]hewsplice.Edit, error)
}

// Binding is what one format supplies to the loop.
type Binding interface {
	// FormatName names the format in the parse diagnostic ("TOML", "YAML").
	FormatName() string
	// Unsupported refuses a qualifier this binding does not implement, and is
	// asked of every transform BEFORE any is applied. §9.3 is explicit that
	// ignoring one is non-conformant rather than lenient, and refusing up
	// front is what stops a list applying halfway and then refusing.
	Unsupported(target string, t hew.Transform) error
	// NewRun parses src and prepares the run for one transform. It is called
	// once PER TRANSFORM: the document is reparsed between them, so each reads
	// the bytes its predecessors produced.
	NewRun(src []byte, ctx RunContext) (Run, error)
}

// Apply runs tl against target and returns the new bytes.
func Apply(target []byte, tl hew.TransformList, b Binding) ([]byte, error) {
	// Pass 0, over the list alone: a property of the transforms, not of the
	// document, so it is decided before the document is read at all.
	for _, t := range tl.Transform {
		if err := b.Unsupported(tl.Target, t); err != nil {
			return nil, err
		}
	}

	converged := map[string]bool{}
	cur := target
	migrated := hew.Migrate(tl.Transform)
	for i, t := range tl.Transform {
		run, err := b.NewRun(cur, RunContext{
			Target: tl.Target, All: tl.Transform, Converged: converged, Adv: migrated[i],
		})
		if err != nil {
			return nil, &hewerr.Error{Code: hewerr.CodeTargetParse, Component: hewerr.ComponentApplier,
				Target: tl.Target, Detail: "target does not parse as " + b.FormatName() + ": " + err.Error()}
		}
		switch t.Op {
		case hew.OpTest:
			if err := run.EvalTest(t); err != nil {
				return nil, err
			}
		case hew.OpHint:
			// The non-asserting hint channel: no edit, no assertion. A hint
			// can never fail a match, so it cannot reach either arm above.
			continue
		default:
			es, err := run.PlanOne(t)
			if err != nil {
				return nil, err
			}
			if len(es) == 0 {
				continue
			}
			if cur, err = hewsplice.Apply(cur, es); err != nil {
				return nil, err
			}
		}
	}
	return cur, nil
}
