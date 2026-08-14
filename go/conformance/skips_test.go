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
// P2 ships: the notation parser and renderer (format-agnostic), and ONE
// applier — JSON (§8.1) — plus a CLI `apply` that dispatches to it. Every
// other format's applier, the differ (P4), git source resolution, and the
// resolved-op-list projection (Resolve, §9.2 — needed by --ops and by
// --record's resolved transforms) are out of scope and skipped explicitly
// below, never silently.
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

	// Every other cli case names a non-JSON target format, so it needs that
	// format's applier — not shipped in P2. apply-transforms also declares
	// a bare apply-ir seam alongside cli, for the same yaml target.
	{Case: "cli/apply-in-place", Seam: "cli", Reason: "P3: hewyaml applier not yet implemented"},
	{Case: "cli/apply-stdout", Seam: "cli", Reason: "P3: hewyaml applier not yet implemented"},
	{Case: "cli/apply-transforms", Seam: "*", Reason: "P3: hewyaml applier not yet implemented"},
	{Case: "cli/stale-exit-1", Seam: "cli", Reason: "P3: hewyaml applier not yet implemented"},

	// jsonc's apply-ir/e2e run for real: hewjsonc (§8.2) ships. Its parse and
	// render seams stay skipped — the notation side has not been verified
	// against jsonc's comment-anchoring cases seam by seam (§8.2's
	// leading/trailing/free rules interact with the parser's comment-node
	// handling in ways P2 did not exercise case by case), even though the
	// e2e seam necessarily exercises parsing end to end.
	{Case: "jsonc/*", Seam: "parse", Reason: "P3: not verified beyond jsonc/roundtrip-basic against §8.2's comment-anchoring cases"},
	{Case: "jsonc/*", Seam: "render", Reason: "P3: not verified beyond jsonc/roundtrip-basic"},

	// yaml/roundtrip-basic's parse/render/apply-ir/e2e are verified and run
	// for real above (only its diff seam is skipped); every other yaml case
	// needs either the hewyaml applier (not shipped) or exercises YAML
	// grammar this parser has not been verified against case by case
	// (aliases/anchors §8.3, multi-line block-style nested elements — see
	// the parser's classifyMember doc comment).
	{Case: "yaml/*", Seam: "apply-ir", Reason: "P3: hewyaml applier not yet implemented"},
	{Case: "yaml/*", Seam: "e2e", Reason: "P3: hewyaml applier not yet implemented"},
	// Only three yaml cases besides roundtrip-basic declare a parse or
	// render seam at all; both are pinning something beyond the plain
	// mirror grammar this parser targets (the idempotent PRAGMA's
	// file-wide effect, and a bare scalar replace whose fixture this
	// implementation has not been checked against) — named individually
	// rather than a family-wide glob, so roundtrip-basic's parse/render
	// (verified, run for real above) is never accidentally caught by it.
	{Case: "yaml/pragma-idempotent-file", Seam: "parse", Reason: "P3: not verified against the idempotent-pragma parse fixture"},
	{Case: "yaml/set-scalar", Seam: "parse", Reason: "P3: not verified against this fixture"},

	// TOML and HCL bodies use native "key = value" / block syntax the
	// parser's fragment reader (YAML/JSON-shaped, colon syntax) does not
	// parse at all, and neither format has an applier, differ, or CLI
	// backend in P2.
	{Case: "toml/*", Seam: "*", Reason: "P3: hewtoml backend and fragment syntax (dotted keys, table headers, §8.4) not implemented"},
	{Case: "hcl/*", Seam: "*", Reason: "P3: hewhcl backend and fragment syntax (attribute/block bodies, §8.5) not implemented"},
}
