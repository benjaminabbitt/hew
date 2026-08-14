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

	// O38 — `hew diff` of identical inputs emits a preamble-only patch, and a
	// patch whose file section has no hunks applies as a no-op. Today the
	// differ writes zero bytes for an empty result (hewcli/diff.go) and the
	// parser refuses a hunkless file section (parse.go finishSection), which
	// is the pair of behaviours §10.2's amendment replaces.
	{Case: "cli/diff-empty-output", Seam: "cli", Reason: "P5: ratified, implementation pending — O38 (diff of identical inputs emits a preamble-only patch, not zero bytes)"},
	{Case: "cli/apply-no-hunks-noop", Seam: "cli", Reason: "P5: ratified, implementation pending — O38 (a file section with no hunks applies as a no-op, exit 0; the parser still refuses it as HEW001)"},

	// O39 — the `--- ` line names the OLD side. The corpus's own diff seam
	// already passes the old label (harness engine, §9.4-R7), so the six
	// roundtrip cases need no rule; the CLI still labels with the new side
	// (hewcli/diff.go's `target := newLabel`), which only a case with two
	// differently-named descriptors can see.
	{Case: "cli/diff-old-target", Seam: "cli", Reason: "P5: ratified, implementation pending — O39 (hew diff stamps the OLD side's label; the CLI still stamps the new side)"},

	// O40 — `hew apply --reversal`. The flag does not exist yet, so the run
	// fails as a usage error before it reaches the reversal artifact at all.
	{Case: "cli/apply-reversal", Seam: "cli", Reason: "P5: ratified, implementation pending — O40 (hew apply --reversal writes diff(after→before) as a real .hew)"},

	// O37 — a pinned applied_at. The harness carries the case's env: block to
	// the RunCLI seam; the CLI reads no environment yet, so the record still
	// carries the wall clock. See newBinding's RunCLI hook, which drops env
	// on the floor and says so.
	{Case: "cli/record-pinned-time", Seam: "cli", Reason: "P5: ratified, implementation pending — O37 (HEW_APPLIED_AT pins the record's applied_at; the CLI reads no environment)"},

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
