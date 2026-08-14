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
// TOML (§8.4) and HCL (§8.5) appliers are bound, as are Resolve (§9.2),
// --ops/--record (§9.7), the differ (§9.4) over every non-markdown format,
// and git source resolution (§9.5, Appendix A.7).
//
// Two kinds of entry live here, and the difference matters when reading the
// table. The markdown rule is a DEFERRAL: it is gated on an open spec
// question (O29) and will be deleted or the family removed when §8.7 is
// evaluated. The P5 rules are PROMISES OUTSTANDING: the ruling landed, the
// corpus case landed with it, and the code has not — each names the ruling
// that decided it, so the table reads as a work list rather than as a set of
// tests that happen to fail. Every one of them dies the moment its behaviour
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

	// O41 — the quoted-key segment, and the canonical-rendering rule that makes
	// String()/ParsePath a bijection. Unlike every other rule in this table,
	// these cover a LIVE DEFECT and not just an unbuilt feature: path.go has no
	// quoted-key form at all (a quoted segment is unconditionally a label), and
	// escapeKey escapes only ~ / =, so a key whose SHAPE collides with another
	// segment form has no spelling that survives a render/reparse round trip.
	// json/diff-scoped-key is the defect's producer end — diff.go builds
	// SegKey straight from document keys — and is why these cases declare both
	// the producing and the consuming seams.
	{Case: "json/quoted-key-scoped", Seam: "*", Reason: "P5: ratified, implementation pending — O41 (quoted-key segment; \"@scope/pkg\" currently reparses as a marker)"},
	{Case: "json/quoted-key-digits", Seam: "*", Reason: "P5: ratified, implementation pending — O41 (quoted-key segment; a digit-only key currently reparses as an index)"},
	{Case: "json/diff-scoped-key", Seam: "*", Reason: "P5: ratified, implementation pending — O41 (LIVE DEFECT: the differ emits /@scope~1pkg, which reparses as a marker)"},

	// O44 — reserved tokens. `count>=5` parses today as a match on a field
	// named "count>", so the parser has to start refusing it before O6's
	// operators can ever be added compatibly.
	{Case: "json/reserved-match-operator", Seam: "*", Reason: "P5: ratified, implementation pending — O44 (a key-match field ending < > ! is reserved for O6's operators)"},
}
