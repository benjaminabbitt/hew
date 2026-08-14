package conformance

import "github.com/ctxloom/hew/internal/harness"

// skipRules is the milestone skip table. Every entry is a recorded reason
// satisfying the "no case is skipped without a recorded skip reason"
// acceptance criterion while a component is unbuilt. Rules are matched
// first-hit; TestCorpusSkips fails on any rule that matched nothing, so this
// table can only shrink truthfully as milestones land. The end-state gate
// (`just corpus-go-strict`, HEW_CORPUS_NO_SKIPS=1) turns every match into a
// failure.
//
// The markdown family's rule is permanent-until-O29: the spec itself (§8.7)
// gates that backend on an evaluation that has not been run, and declares the
// family severable.
var skipRules = []harness.SkipRule{
	{Case: "markdown/*", Seam: "*", Reason: "deferred: Markdown backend gated on spec §8.7/O29 evaluation (severable family)"},
	{Case: "*/*", Seam: "apply-ir", Reason: "M4-M8: appliers not yet implemented"},
	{Case: "*/*", Seam: "e2e", Reason: "M4-M8: appliers not yet implemented"},
	{Case: "*/*", Seam: "render", Reason: "M9: renderer not yet implemented"},
	{Case: "*/*", Seam: "cli", Reason: "M10: CLI not yet implemented"},
	{Case: "*/*", Seam: "diff", Reason: "M11: differ not yet implemented"},
}
