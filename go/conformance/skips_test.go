package conformance

import "github.com/benjaminabbitt/hew/internal/harness"

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
// fragment syntax (§8.1-§8.5); the JSON (§8.1), JSONC (§8.2), YAML (§8.3),
// TOML (§8.4) and HCL (§8.5) appliers are bound, as are Resolve (§9.2),
// --ops/--record (§9.7), the differ (§9.4) over every non-markdown format,
// and git source resolution (§9.5, Appendix A.7). Nothing outside the
// Markdown family is skipped.
var skipRules = []harness.SkipRule{
	{Case: "markdown/*", Seam: "*", Reason: "deferred: Markdown backend gated on spec §8.7/O29 evaluation (severable family)"},
}
