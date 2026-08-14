# hew conformance corpus — v0

**This directory is the standard.** Where `docs/design/hew-spec.md` and these cases disagree,
the corpus wins and the spec has a bug. The Go implementation and the later Rust port are
conformant exactly insofar as they pass this directory — that is the single decision that
makes "port to Rust" a port rather than a rewrite-and-diverge.

Nothing here is executed yet. There is no parser, no applier, no differ and no CLI. These are
the fixtures those components will be built against, and they are reviewable on their own.

---

## What a case is

One directory per case, named `<format>/<case-name>/`. Every case carries a `case.yaml`
manifest and the fixtures its declared seams need.

```
corpus/
  README.md
  json/      jsonc/     yaml/     toml/     hcl/     markdown/     cli/
```

The corpus lives at the **repository root**, not under `tests/` — it is consumed by every
implementation as a peer, including the Rust port, and `tests/` would imply Go-test-only
ownership (spec O15).

## The five seams

A case declares which seams it pins. **A runner must run each declared seam separately and
assert only that seam's output.** An end-to-end-only corpus cannot tell a parser bug from an
applier bug, and those are written by different people in different languages.

| Seam | Input → output | Isolates |
|---|---|---|
| `parse` | `patch.hew` → `transforms.hewt` | The notation. No target file, no backend. A Rust port passes every `parse` case before it has a single format binding. |
| `apply-ir` | `transforms.hewt` + `target.*` → `expected.*` | The applier: format mechanics and byte preservation, notation entirely out of the picture. |
| `e2e` | `patch.hew` + `target.*` → `expected.*` | The composition — what a user experiences. |
| `render` | `transforms.hewt` → `.hew` → `transforms.hewt` | The renderer, via IR identity (RT2). |
| `diff` | `old.*` + `new.*` → `expected.hew` | The differ's determinism and context radius (spec §9.4). |
| `cli` | `argv` → exit code + streams | The CLI contract. |

A directory carrying `patch.hew`, `transforms.hewt`, `target.*` and `expected.*` pins the
first three seams from one set of fixtures.

## Files

| File | Present when | Contents |
|---|---|---|
| `case.yaml` | always | The manifest. |
| `patch.hew` | all seams but `apply-ir` and `diff` | The patch. |
| `transforms.hewt` | `parse`, `apply-ir`, `render` | The canonical transform list — expected output of the parser *and* input to the applier. |
| `target.<ext>` | `apply-ir`, `e2e` | The input document, byte-exact. |
| `expected.<ext>` | success cases | The expected output, compared **byte for byte**. |
| `expected-ops.json` | optional | The *resolved* RFC 6901 op list, for interop pinning. |
| `old.*` / `new.*` / `expected.hew` | `diff` | The differ's inputs and output. `expected.hew`'s `--- ` line names **`old.*`** — the patch applies to old (spec §9.4-R7, ruling O39). |

## Manifest fields

```yaml
name: yaml/keyed-array-add     # must equal the directory path
seams: [parse, apply-ir, e2e]
kind: ok                       # ok | error | cli
format: yaml
ops: [OP-16]                   # operations-catalog entries this case pins (spec §11)
why: |                         # what rule this case exists to pin, in spec terms
  ...
spec: "§6.2, §11 OP-16"
```

Error cases add `error` (the code), `error_seam` (which component must raise it),
`error_path`, `patch_line`, and `message_contains`. **`error_seam` is itself an assertion:** a
`HEW001` that surfaces at `e2e` instead of `parse` means the parser deferred work it should
have refused.

CLI cases add `argv`, `exit`, and optionally `stdout` / `stderr_contains` /
`target_unchanged` / `expected` / `expected_targets` / `targets_unchanged` /
`no_files_created` / `expected_record` / `record_digest_fields` / `requires` / `env`.

**`env:`** pins the environment of the run, and is deliberately restricted to the two
variables the spec names as environment-readable — `HEW_APPLIED_AT` and `SOURCE_DATE_EPOCH`
(spec §9.7, ruling O37). Anything else is a corpus error: a corpus that could reach any
environment variable could pin behaviour the spec never promised. A case that pins the clock
this way also inverts the record rule below.

**`expected_record` / `record_digest_fields`.** A record fixture cannot pin a digest or a wall
clock, and the runner does not ignore either — it *recomputes* each named digest from the
case's own fixture bytes and requires the record to have got it right, and it requires
`applied_at` to be present and RFC 3339 UTC. The one exception is a case that sets
`HEW_APPLIED_AT` through `env:`: there the fixture pins `applied_at` and a different instant
is a failure, because that is the assertion.

## Runner obligations

1. Copy the case directory to scratch before running — in-place cases mutate.
2. Run each declared seam independently. Collapsing seams is non-conformant.
3. Compare bytes **exactly**: no normalization, no trailing-newline tolerance, no whitespace
   folding. Byte preservation is the contract; a lenient comparison silently retires it.
4. For `error` cases, assert the code, the seam, the path, and every `message_contains`
   substring — **and** that the target is byte-unchanged.
5. An unknown seam or kind, or a missing declared file, is a **corpus error**, not a skip.
   Silent skips are how a conformance suite lies.
6. Report catalog coverage: every operation marked `v0` in spec §11 must appear in at least
   one case's `ops:` list. All 40 are covered today; a new v0 operation with no case is a gap
   the runner must name.

## The skip registry

An implementation under construction cannot pass a corpus that pins components it has not
built. The dishonest answer is to run a subset. The honest one is a **skip registry**, and a
conformant runner must carry one with three properties:

1. Every skip is a rule with a **recorded reason** — a milestone that has not landed, or an
   open spec question. No unexplained skips.
2. **A rule that matches nothing fails the build.** The table can only shrink truthfully; when
   a milestone lands, its rule stops matching and must be deleted.
3. **`HEW_CORPUS_NO_SKIPS=1` disallows the registry entirely** — every case a rule would have
   skipped instead fails. This is the end-state gate, and an implementation is conformant when
   it passes under it (`just corpus-go-strict`).

Ratified-but-unbuilt behaviour rides the same mechanism. When a ruling lands before its
implementation, the case is added **first** and a rule records that it is red because the
ruling is newer than the code — reason text naming the ruling, so the registry reads as a list
of promises outstanding rather than a list of tests that happen to fail. The two ratchets do
the rest.

The `markdown/*` rule is the one entry expected to outlive the milestones: it is gated on
spec §8.7 / O29, not on work in progress.

## Acceptance criteria and the quality bar

`features/` expresses these obligations as language-agnostic Cucumber criteria, bound by the
Go implementation with godog at `go/conformance` (`TestFeatures`, `just accept-go`). They are
not a second corpus — they are this corpus's obligations written as criteria, with these cases
as their examples.

**Mutation testing is the bar for corpus quality itself.** `just mutate-go-acceptance` runs
the corpus and acceptance suites as mutant killers. A surviving mutant is a **corpus gap** — a
behaviour the standard claims to pin and does not — and the fix belongs here, not in the
implementation's own tests.

## Two case families that are not about one operation

**Tolerance** (`*/tolerance-*`) — one per format family, pinning spec §6.4's table: keys
reordered, keyed-array elements reordered, reformatting, unrelated edits. These are the cases
that show why hew is not `patch(1)` with a structural parser: they *survive*, and under
`patch(1)` every one of them would break a hunk.

**Round trip** (`*/roundtrip-basic`) — one per implement format, pinning
`apply(parse(render(diff(old, new))), old) == new` byte for byte. A failure anywhere in the
four components fails this, which is why the seam decomposition above matters: RT1 says
*something* is wrong, the seams say *what*. A format may carry a second round-trip case where
one address shape is worth pinning end to end on its own — `hcl/two-label-roundtrip` is the
`resource "type" "name"` tuple (§4.3), whose anchor the differ has to spell in full.

## Severability

`markdown/` is severable. Markdown's place in the implement tier is deferred to the evaluation
in spec §8.7 (open question O29) — it may be better served by plain `patch(1)`, since
reorder-blindness is hew's largest win over `patch(1)` and is worth approximately nothing for
prose, where order *is* the content. No case outside `markdown/` depends on a Markdown
fixture, so dropping the dialect is the removal of one directory.

## Two fixtures that are not files

- `cli/diff-git-anchor` needs a git repository. **No `.git` directory is committed** — a
  committed repo is unreviewable and unportable. The case's `fixture:` field states the
  commands the runner runs in the scratch copy to build one.
- `cli/apply-in-place` and friends mutate their target; obligation 1 covers them.

## Counts

| Directory | Cases |
|---|---|
| `json/` | 22 |
| `jsonc/` | 5 |
| `yaml/` | 25 |
| `toml/` | 8 |
| `hcl/` | 13 |
| `markdown/` | 7 (severable) |
| `cli/` | 17 |
| **total** | **97** |

Three families pin the human's 2026-08-14 rulings specifically: the reapply pair
(`yaml/reapply-not-idempotent`, `json/reapply-add-exists`) and the pragma pair
(`yaml/pragma-idempotent-file`, `yaml/pragma-strict-override`) for O3; the multi-target pair
(`cli/multi-target-atomic`, `cli/multi-target-commit`) for O12, differing in one byte of input
so they isolate the all-or-nothing rule; and `cli/apply-record` +
`cli/apply-no-record-by-default` for O14.
