package toml

import (
	hew "github.com/benjaminabbitt/hew/go"
)

// init registers the TOML binding (Appendix A.6, O35).
// Every binding registers itself the same way BY DESIGN; that shape IS the
// registry contract. Four registrations that look alike is the contract
// holding rather than duplication to factor out, and there is nothing to
// factor into: each supplies its own format id and its own functions.
// reprise:ignore
func init() {
	hew.Register(hew.FormatTOML, hew.Binding{
		Applier:       Apply,
		Differ:        DiffTree,
		Document:      Document,
		EmptyDocument: []byte(""),
		Detect: hew.DetectRule{
			Extensions: []string{".toml"},
		},
		// `surface:` is TOML's qualifier (§8.4's dotted-key / table-header
		// duality, §9.6). Same treatment as YAML's `anchor:` — ownership moves
		// here, spelling and enforcement do not (O48, tension 1).
		Qualifiers: []string{"surface"},
	})
}
