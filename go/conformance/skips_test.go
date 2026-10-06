package conformance

import "github.com/benjaminabbitt/hew/go/internal/harness"

// skipRules is the milestone skip table (spec §13.7). Every entry is a
// recorded reason satisfying "no case is skipped without a recorded skip
// reason" while a component is unbuilt. Rules are matched first-hit, so a
// case-specific override MUST come before the family-wide rule it carves an
// exception out of. TestCorpusSkips fails on any rule that matched nothing,
// so this table can only shrink truthfully as milestones land. The
// end-state gate (`just corpus-go-strict`, HEW_CORPUS_NO_SKIPS=1) turns
// every match into a failure.
//
// P5 state: the notation parser and renderer cover every non-markdown
// fragment syntax (§8.1-§8.5); the JSON (§8.1), JSONC (§8.2), YAML (§8.3),
// TOML (§8.4) applier is bound, as are Resolve (§9.2),
// --ops/--record (§9.7), the differ (§9.4) over every non-markdown format,
// and git source resolution (§9.5, Appendix A.7).
//
// An entry is one of two kinds, and the difference matters when reading the
// table. A DEFERRAL is gated on an open spec question — the markdown rule
// waits on O29 and will be deleted or the family removed when §8.7 is
// evaluated. A PROMISE OUTSTANDING means the ruling landed, the corpus case
// landed with it, and the code has not — it names the ruling that decided
// it, so the table reads as a work list rather than as a set of tests that
// happen to fail. Every one of them dies the moment its behaviour
// lands, because a rule that matches nothing fails the build.
var skipRules = []harness.SkipRule{
	{Case: "markdown/*", Seam: "*", Reason: "deferred: Markdown backend gated on spec §8.7/O29 evaluation (severable family)"},

	// O37, O38, O39 and O40 stood here and are now LIVE: the five cli cases
	// they named — cli/diff-empty-output, cli/apply-no-hunks-noop,
	// cli/diff-old-target, cli/apply-reversal, cli/record-pinned-time — run
	// unskipped against hewfs (Appendix A.8) and the CLI's `--reversal` and
	// environment plumbing. Their rules were deleted BEFORE the code was
	// written, which is O50(a): a work package that implements first and
	// deletes its rules afterwards wrote its acceptance test knowing the
	// answer.

	// O41's quoted-key rules and O44's reserved-token rule died here when the
	// quoted segment landed: json/quoted-key-scoped, json/diff-scoped-key,
	// json/reserved-match-operator and json/quoted-key-digits run whole.
}
