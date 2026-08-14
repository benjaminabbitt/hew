package conformance

import "github.com/hew-format/hew/internal/harness"

// newBinding wires the implementation under test into the harness. Hooks are
// nil until their milestone lands; a nil hook behind a declared seam FAILS
// unless the skip table covers it, so wiring a hook and deleting its skip
// rules must happen together.
func newBinding() harness.Binding {
	return harness.Binding{
		// M3: ParseToHewt
		// M2: CanonHewt
		// M4+: ApplyHewt, ApplyPatch
		// M9: RenderHew
		// M10: RunCLI
		// M11: DiffToHew
	}
}
