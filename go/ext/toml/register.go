package toml

import (
	hew "github.com/benjaminabbitt/hew/go"
)

// init registers the TOML binding (Appendix A.6, O35).
func init() {
	hew.Register(hew.FormatTOML, hew.Binding{
		Applier: Apply,
		Differ:  DiffTree,
		Detect: hew.DetectRule{
			Extensions: []string{".toml"},
		},
		// `surface:` is TOML's qualifier (§8.4's dotted-key / table-header
		// duality, §9.6). Same treatment as YAML's `anchor:` — ownership moves
		// here, spelling and enforcement do not (O48, tension 1).
		Qualifiers: []string{"surface"},
	})
}
