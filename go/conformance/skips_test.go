package conformance

import "github.com/hew-format/hew/internal/harness"

// skipRules is the milestone skip table (spec §13.7). Every entry is a
// recorded reason satisfying "no case is skipped without a recorded skip
// reason" while a component is unbuilt. Rules are matched first-hit, so a
// case-specific override MUST come before the family-wide rule it carves an
// exception out of. TestCorpusSkips fails on any rule that matched nothing,
// so this table can only shrink truthfully as milestones land. The
// end-state gate (`just corpus-go-strict`, HEW_CORPUS_NO_SKIPS=1) turns
// every match into a failure.
//
// P4 state: the notation parser and renderer cover every non-markdown
// fragment syntax (§8.1-§8.5); the JSON (§8.1), JSONC (§8.2) and YAML (§8.3)
// appliers are bound, as are Resolve (§9.2), --ops/--record (§9.7), the
// differ (§9.4) over those same three formats, and git source resolution
// (§9.5, Appendix A.7). Remaining: the TOML and HCL appliers, and with them
// those two formats' diff trees.
var skipRules = []harness.SkipRule{
	{Case: "markdown/*", Seam: "*", Reason: "deferred: Markdown backend gated on spec §8.7/O29 evaluation (severable family)"},

	// TOML and HCL parse and render for real (§8.4 table headers and dotted
	// keys, §8.5 attributes/blocks/labels, §7.2 ordinals); what neither
	// format has yet is a document model, and without one it has neither an
	// applier nor a differ — hew.DiffTrees is format-agnostic, but it has to
	// be handed a tree, and only a binding can build one.
	{Case: "toml/*", Seam: "apply-ir", Reason: "P3: hewtoml applier not yet implemented"},
	{Case: "toml/*", Seam: "e2e", Reason: "P3: hewtoml applier not yet implemented"},
	{Case: "toml/*", Seam: "diff", Reason: "P3: hewtoml has no document model yet, so no diff tree to hand the differ"},
	{Case: "hcl/*", Seam: "apply-ir", Reason: "P3: hewhcl applier not yet implemented"},
	{Case: "hcl/*", Seam: "e2e", Reason: "P3: hewhcl applier not yet implemented"},
	{Case: "hcl/*", Seam: "diff", Reason: "P3: hewhcl has no document model yet, so no diff tree to hand the differ"},
}
