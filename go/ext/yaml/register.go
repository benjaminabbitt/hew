package yaml

import (
	hew "github.com/benjaminabbitt/hew/go"
)

// init registers the YAML binding (Appendix A.6, O35).
func init() {
	hew.Register(hew.FormatYAML, hew.Binding{
		Applier:  Apply,
		Differ:   DiffTree,
		Document: Document,
		Detect: hew.DetectRule{
			Extensions: []string{".yaml", ".yml"},
		},
		// `anchor:` is YAML's qualifier (§7.3, §9.6) and this is where that
		// ownership is recorded (O48). Only the ownership moved: the key's
		// spelling and its position in the canonical serialization stay in
		// §9.6, because the corpus pins those bytes and an opaque
		// extension-owned bag could not be canonicalized deterministically or
		// round-tripped through RT2. Enforcement is unchanged too — a
		// qualifier no format declares is HEW001 at parse, and one this
		// applier cannot honour is HEW020 at apply (§9.3).
		Qualifiers: []string{"anchor"},
	})
}
