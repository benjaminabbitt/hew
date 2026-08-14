package hcl

import (
	hew "github.com/benjaminabbitt/hew/go"
)

// KindBlock is HCL's node kind for a block (§8.5, OP-28). It is declared HERE
// rather than in the core's NodeKind enum because a block is not a universal
// shape: map, seq and scalar are what every tree-shaped format has, and §8.8
// moved everything else to the extension that means it. The `.hewt` spelling —
// `kind: "block"` — is unchanged.
const KindBlock hew.NodeKind = "block"

// init registers the HCL binding (Appendix A.6, O35).
func init() {
	hew.Register(hew.FormatHCL, hew.Binding{
		Applier: Apply,
		Differ:  DiffTree,
		Detect: hew.DetectRule{
			// §8.0's shipped defaults. `.pkr.hcl` needs no entry of its own:
			// its extension is `.hcl`. `.tf.json` is deliberately absent — it
			// is JSON, and its extension says so.
			Extensions: []string{".tf", ".hcl", ".tfvars", ".nomad"},
		},
		Kinds: []hew.NodeKind{KindBlock},
	})
}
