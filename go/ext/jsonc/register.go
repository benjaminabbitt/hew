package jsonc

import (
	hew "github.com/benjaminabbitt/hew/go"
)

// init registers the JSONC binding (Appendix A.6, O35).
func init() {
	hew.Register(hew.FormatJSONC, hew.Binding{
		Applier:       Apply,
		Differ:        DiffTree,
		Document:      Document,
		EmptyDocument: []byte("{}\n"),
		Detect: hew.DetectRule{
			Extensions: []string{".jsonc"},
			// §8.0's well-known names: files that are JSONC by convention
			// despite ending .json. This list is binding DATA and not spec
			// (O4) — which is the whole point of it living here, where a
			// convention that shifts costs a release and not a revision.
			WellKnownNames: []string{
				"settings.json",
				"tasks.json",
				"launch.json",
				"tsconfig.json",
				".mcp.json",
			},
		},
	})
}
