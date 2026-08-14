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

	// O41's quoted-key rules and O44's reserved-token rule died here when the
	// quoted segment landed: json/quoted-key-scoped, json/diff-scoped-key and
	// json/reserved-match-operator run whole, and json/quoted-key-digits runs
	// at every seam but one.
	//
	// THIS RULE IS NOT A MISSING FEATURE — it is a CORPUS DEFECT, and it is
	// recorded here because a work package may not edit corpus/. The quoted-key
	// behaviour the case exists for is implemented and green at its apply-ir
	// and e2e seams; what its transforms fixture pins besides is an op ORDER no
	// other case in the corpus produces:
	//
	//	body:  - "8080"  /  + "8080"  /  ctx "8443"
	//	fixture: test 8080, replace 8080, test 8443     (interleaved)
	//	§9.1:    test 8080, test 8443, replace 8080     (step 2, then step 5)
	//
	// §9.1 lowers in phases — step 2 emits EVERY context and `-` line's test in
	// body order, steps 4 and 5 then emit the removes and adds/replaces — and
	// three passing cases pin those phases against the same shape:
	// hcl/repeated-label-ordinal (a `-`/`+` pair followed by a context line,
	// fixture: test, test, replace — the exact contradiction),
	// json/array-remove-element (test, test, test, remove) and json/add-key
	// (test, test, add). Implementing the fixture's order turns those three
	// red, so the two readings cannot both be conformant and this fixture is
	// the outlier: it was written with the ruling and has never been executed.
	// The fix is a two-record reorder in
	// corpus/json/quoted-key-digits/transforms.hewt, and it belongs to whoever
	// owns the corpus.
	{Case: "json/quoted-key-digits", Seam: "parse", Reason: "corpus defect (not a gap): the fixture interleaves test/replace, contradicting §9.1's phases and hcl/repeated-label-ordinal, json/add-key, json/array-remove-element — see the note above"},
}
