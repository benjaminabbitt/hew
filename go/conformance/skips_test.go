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
// What is built: the notation parser and renderer, format-agnostic and
// complete over every non-Markdown fragment syntax §8 defines (JSON/JSONC
// §8.1-§8.2, YAML §8.3, TOML §8.4, HCL §8.5), plus ONE applier — JSON (§8.1)
// — and a CLI `apply` that dispatches to it. So no parse or render seam is
// skipped outside markdown/*. Every other format's applier, the differ (P4),
// git source resolution, and the resolved-op-list projection (Resolve, §9.2 —
// needed by --ops and by --record's resolved transforms) are out of scope and
// skipped explicitly below, never silently.
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

	// jsonc has no rules left at all: hewjsonc (§8.2) applies, and the
	// notation side parses and renders §8.2's comment nodes for real, with
	// per-projection comment addresses (§4.5b).

	// Every yaml case that declares parse runs for real (roundtrip-basic's
	// multi-line block-style elements, set-scalar's comment nodes, and
	// pragma-idempotent-file's file-level `idempotent:` pragma, ruling O3).
	// What the family still lacks is the hewyaml applier.
	{Case: "yaml/*", Seam: "apply-ir", Reason: "P3: hewyaml applier not yet implemented"},
	{Case: "yaml/*", Seam: "e2e", Reason: "P3: hewyaml applier not yet implemented"},

	// TOML and HCL parse and render for real (§8.4 table headers and dotted
	// keys, §8.5 attributes/blocks/labels, §7.2 ordinals); what neither
	// format has yet is an applier, a differ, or a CLI backend.
	{Case: "toml/*", Seam: "apply-ir", Reason: "P3: hewtoml applier not yet implemented"},
	{Case: "toml/*", Seam: "e2e", Reason: "P3: hewtoml applier not yet implemented"},
	{Case: "toml/*", Seam: "diff", Reason: "M11: differ not yet implemented (P4)"},
	{Case: "hcl/*", Seam: "apply-ir", Reason: "P3: hewhcl applier not yet implemented"},
	{Case: "hcl/*", Seam: "e2e", Reason: "P3: hewhcl applier not yet implemented"},
	{Case: "hcl/*", Seam: "diff", Reason: "M11: differ not yet implemented (P4)"},
}
