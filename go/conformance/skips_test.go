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
// P3 state: the notation parser and renderer cover every non-markdown
// fragment syntax (§8.1-§8.5), and the JSON (§8.1), JSONC (§8.2), YAML
// (§8.3), TOML (§8.4) and HCL (§8.5) appliers are bound, as are Resolve
// (§9.2) and --ops/--record (§9.7). Remaining: the differ (P4), and
// git source resolution.
var skipRules = []harness.SkipRule{
	{Case: "markdown/*", Seam: "*", Reason: "deferred: Markdown backend gated on spec §8.7/O29 evaluation (severable family)"},

	// The round-trip cases exercise parse -> render -> re-parse -> apply
	// without touching the differ at all (engine.runApply derives the IR
	// from expected.hew via ParseToHewt for a Roundtrip case, never via
	// DiffToHew); only their diff seam needs the differ.
	{Case: "json/roundtrip-basic", Seam: "diff", Reason: "M11: differ not yet implemented (P4)"},
	{Case: "jsonc/roundtrip-basic", Seam: "diff", Reason: "M11: differ not yet implemented (P4)"},
	{Case: "yaml/roundtrip-basic", Seam: "diff", Reason: "M11: differ not yet implemented (P4)"},

	// git-anchor source resolution (Appendix A.7) and the differ are both P4.
	{Case: "cli/diff-git-anchor", Seam: "cli", Reason: "M11: differ / git source resolution not yet implemented (P4)"},

	// TOML and HCL apply for real now (§8.4's surface duality, §8.5's
	// splice + modified-body alignment); only their diff seams still wait
	// on the differ.
	{Case: "toml/*", Seam: "diff", Reason: "M11: differ not yet implemented (P4)"},
	{Case: "hcl/*", Seam: "diff", Reason: "M11: differ not yet implemented (P4)"},
}
