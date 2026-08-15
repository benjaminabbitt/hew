package conformance

import (
	"fmt"
	"io"

	"github.com/benjaminabbitt/hew/go"
	_ "github.com/benjaminabbitt/hew/go/ext/all"
	"github.com/benjaminabbitt/hew/go/hewdiff"
	"github.com/benjaminabbitt/hew/go/internal/harness"
	"github.com/benjaminabbitt/hew/go/internal/hewcli"
	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

// applyByFormat applies through whichever binding the registry holds for the
// case's format (Appendix A.6), and fails loud (HEW021) when there is none.
// "No extension claims this format" and "the extension that does ships no
// applier" are one answer here because they are one fact for the caller —
// nothing linked into this binary can apply it. Markdown is the second kind
// while §8.7's evaluation is open.
//
// The blank ext/all import above is what makes any binding available at all:
// the conformance runner is a program like any other, and what it can do is
// what it linked (O35). This function was a switch over five packages before
// the registry landed, and a sixth binding could have existed without the
// corpus ever reaching it.
func applyByFormat(target []byte, tl hew.TransformList, format string) ([]byte, error) {
	if format != "" {
		tl.Format = hew.FormatID(format)
	}
	b, ok := hew.Lookup(tl.Format)
	if !ok || b.Applier == nil {
		return nil, &hewerr.Error{Code: hewerr.CodeUnsupportedFormat, Component: hewerr.ComponentApplier,
			Target: tl.Target, Detail: fmt.Sprintf("no binding for format %q (P3)", tl.Format)}
	}
	return b.Applier(target, tl)
}

// newBinding wires the implementation under test into the harness. Hooks
// are nil until their milestone lands; a nil hook behind a declared seam
// FAILS unless the skip table covers it, so wiring a hook and deleting its
// skip rules must happen together.
func newBinding() harness.Binding {
	return harness.Binding{
		ParseToHewt: func(patch []byte) ([]byte, error) {
			tls, err := hew.Parse(patch)
			if err != nil {
				return nil, err
			}
			return hew.MarshalTransformStream(tls)
		},
		CanonHewt: hew.CanonicalizeTransforms,
		ApplyHewt: func(hewt, target []byte, format string) ([]byte, error) {
			tl, err := hew.UnmarshalTransforms(hewt)
			if err != nil {
				return nil, err
			}
			return applyByFormat(target, tl, format)
		},
		ApplyPatch: func(patch, target []byte, format string) ([]byte, error) {
			tls, err := hew.Parse(patch)
			if err != nil {
				return nil, err
			}
			if len(tls) != 1 {
				return nil, fmt.Errorf("hew: e2e seam expects exactly 1 file section, got %d", len(tls))
			}
			return applyByFormat(target, tls[0], format)
		},
		RenderHew: func(hewt []byte) ([]byte, error) {
			tl, err := hew.UnmarshalTransforms(hewt)
			if err != nil {
				return nil, err
			}
			return hew.Render(tl, hew.RenderOptions{Context: 1})
		},
		// DiffToHew composes the two halves the diff seam pins: the differ
		// produces the abstract list (§9.4) and the renderer writes it as
		// notation (§9.2's one-way street from IR to text). The context
		// radius is the spec's default of 1 and the fragments are spelled in
		// the target format's own syntax, which is what §5's "parsed by the
		// target format's fragment parser" licenses and what the corpus's
		// expected.hew files show. target is the OLD side (§9.4-R7): the
		// differ has no opinion, it stamps the label it is handed, and which
		// label that is belongs to the caller — here, the engine.
		DiffToHew: func(old, new []byte, format, target string) ([]byte, error) {
			tl, err := hewdiff.Diff(old, new, hew.FormatID(format), hew.DiffOptions{
				Target:  target,
				Context: hew.ContextDefault,
			})
			if err != nil {
				return nil, err
			}
			return hew.Render(tl, hew.RenderOptions{Context: hew.ContextDefault, Fragment: hew.FragmentNative})
		},
		// The case's env: block goes straight through to the CLI, which takes
		// its environment as data rather than reading the process's (O37,
		// Appendix B.1). That is what lets cli/record-pinned-time pin
		// applied_at without the runner mutating global state — and the reason
		// the two variables the manifest may name are the two the spec
		// declares environment-readable, checked at manifest validation.
		RunCLI: func(argv []string, dir string, env map[string]string,
			stdin io.Reader, stdout, stderr io.Writer) int {
			return hewcli.Run(argv, dir, env, stdin, stdout, stderr)
		},
	}
}
