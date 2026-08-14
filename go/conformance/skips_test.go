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
// P2 shipped the notation parser and renderer (format-agnostic) and the JSON
// applier (§8.1); P3 adds the YAML applier (§8.3) and the CLI's dispatch to
// it. The remaining formats' appliers, the differ (P4), git source
// resolution, and the resolved-op-list projection (Resolve, §9.2 — needed by
// --ops and by --record's resolved transforms) are out of scope and skipped
// explicitly below, never silently.
var skipRules = []harness.SkipRule{
	{Case: "markdown/*", Seam: "*", Reason: "deferred: Markdown backend gated on spec §8.7/O29 evaluation (severable family)"},

	// The round-trip cases exercise parse -> render -> re-parse -> apply
	// without touching the differ at all (engine.runApply derives the IR
	// from expected.hew via ParseToHewt for a Roundtrip case, never via
	// DiffToHew); only their diff seam needs the differ.
	{Case: "json/roundtrip-basic", Seam: "diff", Reason: "M11: differ not yet implemented (P4)"},
	{Case: "jsonc/roundtrip-basic", Seam: "diff", Reason: "M11: differ not yet implemented (P4)"},
	{Case: "yaml/roundtrip-basic", Seam: "diff", Reason: "M11: differ not yet implemented (P4)"},

	// jsonc/roundtrip-basic's apply-ir/e2e/cli still need a JSONC-specific
	// (comment-aware, §8.2) applier this slice does not ship — only plain
	// JSON is in scope for P2's "one applier" slice.
	{Case: "jsonc/roundtrip-basic", Seam: "apply-ir", Reason: "P3: hewjsonc applier not yet implemented"},
	{Case: "jsonc/roundtrip-basic", Seam: "e2e", Reason: "P3: hewjsonc applier not yet implemented"},

	// --ops and --record's resolved transforms both need Resolve (§9.2,
	// Appendix A.1's abstract -> RFC 6901 projection against a specific
	// target), not implemented in P2.
	{Case: "cli/apply-ops-output", Seam: "cli", Reason: "P3: Resolve (§9.2) not implemented; --ops prints the resolved op list"},
	{Case: "cli/apply-record", Seam: "cli", Reason: "P3: Resolve (§9.2) not implemented; the record's targets[].transforms is the resolved form"},

	// git-anchor source resolution (Appendix A.7) and the differ are both P4.
	{Case: "cli/diff-git-anchor", Seam: "cli", Reason: "M11: differ / git source resolution not yet implemented (P4)"},

	// Every other jsonc case: parse/render are format-agnostic and largely
	// work (see jsonc/roundtrip-basic passing for real), but this slice has
	// not verified them against jsonc's comment-anchoring corpus cases
	// specifically (§8.2's leading/trailing/free comment rules interact
	// with the parser's comment-node handling in ways P2 did not exercise
	// case by case) and ships no jsonc applier for apply-ir/e2e/cli at all.
	{Case: "jsonc/*", Seam: "apply-ir", Reason: "P3: hewjsonc applier not yet implemented"},
	{Case: "jsonc/*", Seam: "e2e", Reason: "P3: hewjsonc applier not yet implemented"},
	{Case: "jsonc/*", Seam: "parse", Reason: "P3: not verified beyond jsonc/roundtrip-basic against §8.2's comment-anchoring cases"},
	{Case: "jsonc/*", Seam: "render", Reason: "P3: not verified beyond jsonc/roundtrip-basic"},

	// The yaml family runs for real: P3 ships hewyaml (§8.3), and every
	// yaml/* apply-ir and e2e seam — plus the four yaml-target cli cases —
	// is bound. Only yaml/roundtrip-basic's diff seam is still skipped,
	// above, with the rest of the differ.

	// TOML and HCL bodies use native "key = value" / block syntax the
	// parser's fragment reader (YAML/JSON-shaped, colon syntax) does not
	// parse at all, and neither format has an applier, differ, or CLI
	// backend in P2.
	{Case: "toml/*", Seam: "*", Reason: "P3: hewtoml backend and fragment syntax (dotted keys, table headers, §8.4) not implemented"},
	{Case: "hcl/*", Seam: "*", Reason: "P3: hewhcl backend and fragment syntax (attribute/block bodies, §8.5) not implemented"},
}
