package conformance

import (
	"github.com/ctxloom/hew"
	"github.com/ctxloom/hew/internal/harness"
)

// newBinding wires the implementation under test into the harness. Hooks are
// nil until their milestone lands; a nil hook behind a declared seam FAILS
// unless the skip table covers it, so wiring a hook and deleting its skip
// rules must happen together.
func newBinding() harness.Binding {
	return harness.Binding{
		CanonHewt:   hew.CanonicalizeTransforms, // M2
		ParseToHewt: parseToHewt,                // M3
		// M4+: ApplyHewt, ApplyPatch
		// M9: RenderHew
		// M10: RunCLI
		// M11: DiffToHew
	}
}

// parseToHewt is the parse seam: .hew notation in, canonical .hewt bytes out
// (spec §9.1). A single-target patch serializes as one document; a
// multi-target patch as the §9.6 multi-document stream, one document per file
// section, in file order.
func parseToHewt(patch []byte) ([]byte, error) {
	p, err := hew.Parse(patch)
	if err != nil {
		return nil, err
	}
	files := p.Files()
	if len(files) == 1 {
		return hew.MarshalTransforms(files[0].Transforms())
	}
	tls := make([]hew.TransformList, 0, len(files))
	for _, f := range files {
		tls = append(tls, f.Transforms())
	}
	return hew.MarshalTransformStream(tls)
}
