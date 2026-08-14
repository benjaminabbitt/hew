package conformance

import (
	"fmt"

	"github.com/benjaminabbitt/hew"
	"github.com/benjaminabbitt/hew/hewdiff"
	"github.com/benjaminabbitt/hew/hewhcl"
	"github.com/benjaminabbitt/hew/hewjson"
	"github.com/benjaminabbitt/hew/hewjsonc"
	"github.com/benjaminabbitt/hew/hewtoml"
	"github.com/benjaminabbitt/hew/hewyaml"
	"github.com/benjaminabbitt/hew/internal/harness"
	"github.com/benjaminabbitt/hew/internal/hewcli"
	"github.com/benjaminabbitt/hew/internal/hewerr"
)

// applyByFormat dispatches to the format bindings this slice ships (§8.1's
// JSON, §8.2's JSONC, §8.3's YAML, §8.4's TOML, §8.5's HCL) and fails loud
// (HEW021) for anything else, matching the spec's own registration model
// (Appendix A.6) without the registry indirection a handful of bindings
// doesn't need yet.
func applyByFormat(target []byte, tl hew.TransformList, format string) ([]byte, error) {
	if format != "" {
		tl.Format = hew.FormatID(format)
	}
	switch tl.Format {
	case hew.FormatJSON:
		return hewjson.Apply(target, tl)
	case hew.FormatJSONC:
		return hewjsonc.Apply(target, tl)
	case hew.FormatYAML:
		return hewyaml.Apply(target, tl)
	case hew.FormatTOML:
		return hewtoml.Apply(target, tl)
	case hew.FormatHCL:
		return hewhcl.Apply(target, tl)
	default:
		return nil, &hewerr.Error{Code: hewerr.CodeUnsupportedFormat, Component: hewerr.ComponentApplier,
			Target: tl.Target, Detail: fmt.Sprintf("no binding for format %q (P3)", tl.Format)}
	}
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
			return hew.Render(tl, hew.RenderOptions{Preamble: true, Context: 1})
		},
		// DiffToHew composes the two halves the diff seam pins: the differ
		// produces the abstract list (§9.4) and the renderer writes it as
		// notation (§9.2's one-way street from IR to text). The context
		// radius is the spec's default of 1 and the fragments are spelled in
		// the target format's own syntax, which is what §5's "parsed by the
		// target format's fragment parser" licenses and what the corpus's
		// expected.hew files show.
		DiffToHew: func(old, new []byte, format, target string) ([]byte, error) {
			tl, err := hewdiff.Diff(old, new, hew.FormatID(format), hew.DiffOptions{
				Target:  target,
				Context: hew.ContextDefault,
			})
			if err != nil {
				return nil, err
			}
			return hew.Render(tl, hew.RenderOptions{Preamble: true, Context: hew.ContextDefault, Fragment: hew.FragmentNative})
		},
		RunCLI: hewcli.Run,
	}
}
