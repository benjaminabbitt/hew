package conformance

import (
	"fmt"

	"github.com/hew-format/hew"
	"github.com/hew-format/hew/hewhcl"
	"github.com/hew-format/hew/hewjson"
	"github.com/hew-format/hew/hewjsonc"
	"github.com/hew-format/hew/hewyaml"
	"github.com/hew-format/hew/internal/harness"
	"github.com/hew-format/hew/internal/hewcli"
	"github.com/hew-format/hew/internal/hewerr"
)

// applyByFormat dispatches to the format bindings this slice ships (§8.1's
// JSON, §8.2's JSONC, §8.3's YAML, §8.5's HCL) and fails loud (HEW021) for anything else,
// matching the spec's own registration model (Appendix A.6) without the
// registry indirection a handful of bindings doesn't need yet.
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
		RunCLI: hewcli.Run,
		// DiffToHew: P4, not implemented.
	}
}
