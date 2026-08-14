# Hew: a structured patch format — specification, v0 draft

**Status: DRAFT for human review (P1 gate).** Nothing here is implemented. No parser,
no backend, no CLI exists. This document plus `corpus/` is the deliverable.

**Name (human, 2026-08-14, final): the tool and the standard are `hew`.** Binary `hew`,
extension `.hew`, transform lists `.hewt`, CLI verbs `hew apply` and `hew diff`. *Structured
patch* remains the generic descriptive phrase for what hew is; **hew** is the proper noun.
This supersedes the working name "Structured Patch (SP)" used while the workstream was scoped
in `ugly-icy-squid/structured-patch-standard.plan.md`; the error-code prefix moved with it
(`SP010` → `HEW010`). Formats implemented: JSON,
JSONC, YAML, TOML, Markdown, HCL. Formats documented only: INI-family, dotenv, XML (§12).

**Notation (human, 2026-08-14, superseding the earlier hybrid ruling): Hew v0 is one grammar.**
A shape-mirroring body with in-place `+`/`-` margins and match/assert annotations. There is
**no op-list escape hatch** — not fenced, not severable, not present. The two cases the hatch
was going to carry are absorbed into the mirror grammar itself: HCL repeated-label blocks are
selected with an ordinal annotation (§7.2, the survey's design-A rendering made normative),
and assert-only patches are annotation-only mirrors (§7.4). The operations the mirror grammar
still cannot say — node moves and copies — are written as a **transform list** (§9.6), the
IR's canonical serialization, which had to exist for the corpus and for RFC 6902 interop
anyway and is therefore also an accepted input. **Appendix C** is the honest inventory of that
boundary. There is no third surface and none is reserved.

**Architecture (human, 2026-08-14): four components around one IR.** Parser (`.hew` → IR) and
renderer (IR → `.hew`) are notation-side inverses; differ (two sources → IR) and applier
(IR + file → file) are format-side inverses. The IR — the **transform list** — is the internal
boundary, the interop surface, and what the corpus pins (§9, §13.1).

**Derivation order.** The operations catalog (§11) is a survey of every verb in the prior art
plus ctxloom's own eight mechanisms; the catalog's *IR record* rows define the transform-list
schema (§9.6); the schema defines the Go signatures (Appendix A). Never the reverse — a future
catalog entry extends the IR by construction.

**Reduction.** The survey is exhaustive; the normative set is not. 52 catalogued operations
reduce to **five IR primitives** (`test`, `add`, `remove`, `replace`, `copy`) plus addressing
and a minimal qualifier set — *the IR is essentially ASM*. Everything else is either sugar the
parser compiles away or an explicit `OUT` with a reason (§11.10).

**Tolerance.** `patch(1)`'s fuzz is the wrong benchmark. Hew addresses nodes by path, so
reordered keys, reordered keyed arrays, reformatting, and unrelated edits are **invisible** to
a patch — while value drift, missing nodes and ambiguous matches fail loudly and by name. The
normative table is §6.4.

**The corpus is the standard.** Where this prose and `corpus/` disagree, the corpus
wins, and the disagreement is a spec bug to be fixed here. The Go implementation (P2/P3) and
the later Rust port (P6) are conformant exactly insofar as they pass the same corpus.

---

## Table of contents

1. [Why Hew exists, and the one property everything else serves](#1-why-hew-exists)
2. [File format: preamble, file sections, hunks](#2-file-format)
3. [Margins: the six column-1 characters](#3-margins)
4. [Hew paths: the address grammar](#4-hew-paths)
5. [Hunk semantics: the two projections](#5-hunk-semantics-the-two-projections)
6. [Context, position, exhaustiveness — and the tolerance model](#6-context-position-and-exhaustiveness)
7. [Annotations: `?` assertions and `!` directives](#7-annotations)
8. [Per-format binding: what a body line *is*](#8-per-format-binding)
9. [The transform list: Hew's IR, and the four components around it](#9-the-transform-list)
10. [Error taxonomy](#10-error-taxonomy)
11. [**Operations catalog** — every surveyed verb, in or out, and why](#11-operations-catalog)
12. [Documented-only formats](#12-documented-only-formats)
13. [The conformance corpus: three seams and two round-trip identities](#13-the-conformance-corpus)
14. [Appendix A — the Go API surface, including the document API](#appendix-a--the-go-api-surface)
15. [Appendix B — the CLI surface](#appendix-b--the-cli-surface)
16. [Appendix C — operations the mirror grammar cannot express](#appendix-c--operations-the-mirror-grammar-cannot-express-non-normative)
17. [Decisions and residual open questions](#decisions-and-residual-open-questions)

---

## 1. Why Hew exists

`patch(1)` won over `ed` scripts because a reviewer can read one hunk and see both the change
and its surroundings without mentally executing anything. Every structured-data patch format
in the survey lost that property: op-lists (RFC 6902, go-patch, RFC 5261, jd) read as
instructions, and shape-mirroring overlays (RFC 7386, strategic merge, spruce, CUE, jsonnet)
either restate whole arrays to change one element or cannot express deletion at all.
Coccinelle's SmPL is the only surveyed notation that shape-mirrors *and* keeps `-`/`+` margins
in place — and it silently no-ops when its match fails.

Hew is that shape-mirroring-with-margins notation, generalized off C and onto config trees,
with SmPL's one gap closed. **The governing property, from which the rest of this spec is
derived:**

> A hunk that does not match its target is an **error with a named cause and a location**.
> Never a no-op. Never a best-effort apply. Never a fuzzy line-offset guess.

That is ctxloom's silent-no-op discipline (exit 0, success message, zero bytes written)
applied to a file format. Three concrete consequences, stated up front because they surprise
people who expect `patch(1)`:

- **No fuzz factor.** Hew has no analogue of `patch --fuzz`. Context either matches or the
  apply fails.
- **Atomic per target file.** Either every hunk against a file applies or none does. There
  are no `.rej` files and no partially-patched output. (§10.5)
- **Already-applied is a failure, not a success.** **Ruled (human, 2026-08-14): strict is the
  default.** An unannotated hunk has `patch(1)` semantics — re-applying it fails loudly
  (`HEW014` / `HEW011`, §10.6). `! idempotent` (§7.5) opts a hunk into convergence, and the
  `idempotent:` preamble pragma (§2.1) sets it patch-wide for a generating tool. **The
  rationale is the discipline itself: loud failure is the default because it is the property
  the format exists to provide; convergence is a legitimate need and is therefore a *visible
  choice*, present in the patch text where a reviewer sees it.** A tool that wants convergent
  patches says so once, in the preamble, and the reader knows before the first hunk.

**And the second property, from the v0 notation ruling:** there is exactly one way to write a
patch. A reader of a `.hew` file never has to learn a second grammar, and a reviewer never has
to ask why *this* edit was written as instructions when *that* one was written as shape. The
price is paid in Appendix C, honestly and in one place.

---

## 2. File format

A hew file is UTF-8 text, LF-terminated lines. A trailing newline on the last line is
optional. The file is **line-oriented at the top level**: the first character of every line
is structural.

```
patchfile   := preamble filesection+
preamble    := ( comment | blank | directive )*
directive   := key ":" WSP value LF
filesection := targetline ( hunk | comment | blank )+
targetline  := "--- " path [ WSP attrlist ] LF
hunk        := hunkheader bodyline*
hunkheader  := "@@ " address " @@" [ WSP attrlist ] LF
bodyline    := margin WSP text LF | comment | blank
comment     := "#" [ WSP text ] LF
attrlist    := attr ( WSP attr )*
attr        := key "=" value

WSP         := one space character (0x20)
LF          := one line feed (0x0A)
```

### 2.1 Preamble

```
# ctxloom v0.7 → v0.8 settings migration
hew: 1
format: yaml
```

- `hew:` — **required**, must be the first non-comment, non-blank line. Value is the format
  version integer. This document specifies `1`. A reader seeing an unknown version MUST fail
  with `HEW002` rather than attempt a best-effort parse.
- `format:` — optional default format for every file section that does not declare its own.
- `idempotent:` — optional boolean pragma, default `false`. **`idempotent: true` applies
  `! idempotent` (§7.5) to every hunk in the file.** A single hunk opts back out with
  `! strict` (§7.5). This exists so that a *generating* tool — ctxloom's own managed-file
  writers, every one of which is idempotent by construction — can emit convergent patches
  without stamping a directive onto each hunk, while the choice stays visible in one place
  at the top of the file rather than being a property of the applier's invocation.

```
# ctxloom-generated; safe to re-apply
hew: 1
idempotent: true
format: json
```

No other preamble keys are defined in v0. An unknown preamble key is `HEW001` (fail loud;
forward-compatible ignoring is deliberately not offered — see [O9](#decisions-and-residual-open-questions)).

### 2.2 File sections — the `---` target line

Borrowed verbatim from unified diff's visual grammar, and from kustomize's lesson that a
patch document needs to say *which* thing it patches:

```
--- .claude/settings.json format=jsonc
--- config.yaml
--- pyproject.toml
```

- The path is relative to the apply root (the CLI's `--root`, default: the process working
  directory). Absolute paths are legal; `..` traversal above the root is `HEW003`.
- `format=` is optional. Resolution order: `format=` attr → preamble `format:` → extension
  inference (§8.0) → `HEW021 unsupported-format`.
- A path containing a space must be quoted: `--- "my config.yaml"`.
- **Multiple file sections may name the same path.** Their hunks are merged and the atomicity
  rule (§10.5) covers the union.

Because a target line's first character is `-` and a hunk body line's first character is a
margin *followed by a space*, `--- ` (three dashes, space) can never be confused with a
removal line: a removal line is `- ` (one dash, one space) and its text would have to begin
`-- `. Removing a target line's worth of literal text is expressed as `- -- foo` with no
ambiguity for the parser.

### 2.3 Hunks and anchors

```
@@ /server @@
@@ /mcpServers/name=github @@
@@ / @@
@@ /provider/"aws" @@
```

A hunk header names the **anchor**: the Hew path (§4) of the node the body mirrors. `@@ / @@`
anchors at the document root.

The anchor node must exist and must resolve to exactly one node, or the hunk fails
(`HEW013 no-match` / `HEW012 ambiguous-match`) — with two exceptions: a trailing `?` on the last
path segment (§4.4) permits the anchor to be created, and a `! match ord=` first body line
(§7.2) selects among same-tuple siblings.

**Body indentation is relative to the anchor.** The body is written as though the anchor's
subtree were the whole document: the anchor's own container delimiters (`{`/`}` in JSON, the
`server:` key line in YAML, the `[table]` header in TOML, the `provider "aws" {` line in HCL,
the `## Heading` line in Markdown) are **not** written in the body. Only its children are.

```
@@ /server @@
  port: 8080
- timeout: 30
+ timeout: 60
```

is the same patch as

```
@@ / @@
  server:
    port: 8080
-   timeout: 30
+   timeout: 60
```

Choose the anchor that makes the hunk read best. A deeper anchor means less context to write;
a shallower anchor means more visible surroundings. Same trade unified diff makes with
`-U`/`--unified=N`, made per hunk instead of per file.

---

## 3. Margins

Column 1 is the margin. Column 2 is a mandatory single space. The body text begins at column 3.

| Margin | Name | Meaning |
|---|---|---|
| `` ` ` `` (space) | **context** | This node must exist and be equal. It is not modified. It pins insertion position (§6.2). |
| `-` | **remove** | This node must exist and be equal. It is deleted. |
| `+` | **add** | This node is created. It must not already exist (unless `! idempotent`). |
| `?` | **assert** | An annotation line carrying an assertion (§7.1, §7.4). Not part of either projection. |
| `!` | **directive** | An annotation line changing how application works (§7.2, §7.3, §7.5). Not part of either projection. |
| `#` | **comment** | A hew comment. Ignored entirely. Not part of either projection. |

A completely blank line (zero characters, or whitespace only) is **insignificant**: it is a
visual separator and is ignored. To express a blank line in the *target*, see §8.6 — only
Markdown has a body-line notion of blank, and there it is structural rather than written.

A line whose column 1 is none of the six margin characters is `HEW001 parse-error`. There is
no "loose" mode.

**Target comments are ordinary body text.** A `#` comment in a YAML/TOML/HCL target, or a
`//` comment in JSONC, is written as context/add/remove like any other line:

```
@@ /server @@
  # ports below 1024 need CAP_NET_BIND_SERVICE
  port: 8080
+ # bumped for the slow upstream (ctxloom, 2026-08)
- timeout: 30
+ timeout: 60
```

The Hew margin `#` and the target's `#` never collide because the target's lives at column 3.

---

## 4. Hew paths

A hew path addresses a node. It is **RFC 6901 JSON Pointer plus four extensions**, chosen so
that the common config edit is expressible without positional indices.

```
hewpath  := "/" | ( "/" segment )+
segment  := key | quoted | index | "-" | keymatch | label | heading | block | marker | comment
quoted   := '"' ( char | '\"' | '\\' )* '"'
```

There is **no ordinal segment**. Selecting among same-shaped siblings that a path cannot
distinguish is an annotation (§7.2), not an address — so that a path is always a statement
about identity and never about position in the file.

**A quoted segment is the literal form**, and it is what makes the grammar closed: every other
segment form is recognized by its shape, so a key whose text happens to have one of those
shapes needs a spelling that says "this is literal text, not a form". §4.1 gives the rule and
[O41](#p5--the-api-ratification-2026-08-14) gives the reasoning. Quoting is **container-kind
resolved**: against an HCL block set a quoted segment is a label (§4.3, unchanged), against a
mapping it is a key. The two contexts are disjoint — a container is one or the other — so one
spelling serves both without ambiguity.

**Path splitting is quote-aware.** A `/` inside a quoted segment is literal, so
`/dependencies/"@scope/pkg"` is two segments, not three. (Splitting on `/` before honouring
quotes is the defect this rule exists to prevent, and it is one an implementation reaches for
first.)

### 4.1 Key and index segments (RFC 6901, with two stated differences)

```
/server/timeout          object key "timeout" under object key "server"
/tags/0                  element 0 of the sequence "tags"
/tags/-                  the append position of "tags" (RFC 6901's literal "-")
```

Escapes: `~0` = `~`, `~1` = `/`, **and Hew adds `~2` = `=`** so that an object key containing
a literal `=` cannot be mistaken for a key-match segment. (Extending RFC 6901's escape set is
a compatibility decision — [O5](#ratified-by-the-coordinator-2026-08-14).)

**Two differences from RFC 6901 at the root, stated because the heading used to claim there
were none.** RFC 6901 spells the whole document `""` and spells *the member whose key is the
empty string* `/`. Hew spells the document root `/`, which it must — a hew path is written on
a `@@` line and in a `--- ` target's neighbourhood, where an empty token is unreadable and
unreviewable. The consequence is that **RFC 6901's empty-key member has no bare hew spelling
at all**, which is one of the holes the quoted form closes: it is written `/""`.

#### The quoted-key form

**Ratified ([O41](#p5--the-api-ratification-2026-08-14)).** A double-quoted segment is a
**literal**. Against a mapping it is an object key; against an HCL block set it is a label
(§4.3, unchanged). Escapes inside the quotes are `\"` and `\\`; `~0`/`~1`/`~2` are neither
needed nor interpreted there, because nothing inside quotes is being disambiguated.

```
/dependencies/"@scope/pkg"       the key "@scope/pkg" — NOT a marker segment
/versions/"8080"                 the key "8080" — NOT an index
/flags/"-"                       the key "-" — NOT the append position
/paths/"*"                       the key "*" — a real tsconfig key (§4.7)
/x/""                            the empty-string key (RFC 6901's "/")
/x/"code:0"                      the key "code:0" — NOT a Markdown block ordinal
/x/"a?"                          the key "a?" — NOT an optional segment
```

**The canonical-rendering rule, and it is normative.** An implementation that renders a path
back to text MUST emit the quoted form for any key whose bare spelling would not reparse as
the same segment. Concretely, a key is rendered quoted when it is empty, or is entirely
digits, or is exactly `-`, or begins with `@`, `#` or `"`, or has the `<blockkind>:<n>` shape
of §4.5, or has the `#<n>`/`#t` shape of §4.5b, or ends with `?`, or ends with a `[<n>]`
ordinal, or would otherwise be recognized as another segment form. Everything else renders
bare, so ordinary paths are unchanged and stay readable.

> **`String()` and `ParsePath` are a bijection on keys.** Rendering a path and reparsing it
> MUST yield the identical path, for every key any target document can contain.

This is not a style rule. The `.hewt` serialization (§9.6) stores every address as this text,
and the differ builds key segments straight from the target document's own keys — so without
the rule, a `package.json` with a scoped dependency produces a transform list whose addresses
silently mean something else. That is the failure this ruling closes, and
[O41](#p5--the-api-ratification-2026-08-14) records that it is a live defect, not a
hypothetical one.

### 4.2 Key-match segments — `name=value`

Adopted from go-patch, whose `/instance_groups/name=zookeeper/instances` idiom is the survey's
only proven answer to keyed-array addressing:

```
/mcpServers/name=github            the element of "mcpServers" whose "name" field == "github"
/tool/x/id="a b"                   quoted value, for values containing spaces or "="
/servers/enabled=true              non-string scalars compare after format-native decoding
```

- The container must be a sequence, **or an HCL set of same-`(type, labels)` blocks** — see
  below. The field must be a direct child of each element.
- **Exactly one element must match**, or `HEW012 ambiguous-match` / `HEW013 no-match`. This is
  the loud-staleness rule applied to addressing: an array that grew a duplicate key is a
  drifted array and Hew says so by name.
- Values are compared **after decoding**: `port=8080` matches the number `8080` and the string
  `"8080"` is written `port="8080"`. Booleans and null are bare tokens `true`/`false`/`null`.
- Only equality is defined in v0. No regex, no substring, no numeric comparison
  ([O6](#ratified-by-the-coordinator-2026-08-14)).
- A field name may itself be quoted (§4.1) when its bare spelling would not survive:
  `/deps/"@scope/pkg"=1.0.0`.

**Rendering a match value is subject to the same bijection rule as a key**
([O42](#p5--the-api-ratification-2026-08-14)). The decoded-comparison rule above means the
*spelling* of a value carries its type, so an implementation MUST force-quote any string
scalar whose bare rendering would not reparse to an identical scalar — anything that would
read back as a number, a boolean, `null`, or an empty token. A programmatically-built
`name=8080` that meant the **string** `"8080"` and reparses as the **number** `8080` addresses
a different element, or none; there is no error, only the wrong node. The differ already
sidesteps this by quoting every string it emits, which is the fix generalized.

**Key-match over HCL block sets** ([O45](#p5--the-api-ratification-2026-08-14)). §6.4.3 rule 1
recommends key-match addressing as the mitigation for repeated HCL blocks — the construct
[O25](#residual--genuinely-open) names as the one place hew can silently patch the wrong node
— and until now §4.2 restricted key-match to sequences, so the recommended remedy had no
spelling. A set of blocks sharing a `(type, labels)` tuple is therefore an addressable
container for key-match, matched on a direct attribute:

```
/resource/"aws_instance"/name="web"      among identically-labelled blocks, the one whose
                                          name attribute is "web"
```

The uniqueness rule is unchanged (`HEW012`/`HEW013`), and the effect is to **strictly reduce**
how often an ordinal annotation is the only option: an ordinal remains legal, and remains the
admission §4.3 says it is, but it stops being forced wherever the blocks differ in any
attribute. Implementation pending.

**The empty-field form — `/tags/=gamma`.** With no field name, the segment matches the
element that *is* the value, addressing scalar sequences by content rather than by index:

```
/tags/=gamma                       the element of "tags" equal to "gamma"
/permissions/deny/="Bash(curl *)"  quoted, for values with spaces
```

Same uniqueness rule: zero matches is `HEW013`, more than one is `HEW012` — a duplicated scalar
in a list is drift, and Hew names it instead of picking the first. This is the address the
differ prefers for primitive lists (§9.4-R4) and the one that makes a removal survive
reordering (OP-15, adopted from strategic merge's `$deleteFromPrimitiveList`).

### 4.3 Label segments — HCL blocks

An HCL block is keyed by a tuple of (block type, ordered labels), not by a name. Labels are
written as **quoted segments** following the block-type segment:

```
/provider/"aws"                    block: provider "aws" { ... }
/resource/"aws_instance"/"web"     block: resource "aws_instance" "web" { ... }
/terraform                         block: terraform { ... }  (no labels)
/provider/"aws"/region             the region attribute inside that block
```

A quoted segment is a label; an unquoted segment is an attribute name or a nested block type.
That is the whole disambiguation rule, and it works because HCL attribute names are bare
identifiers.

This is unchanged by [O41](#p5--the-api-ratification-2026-08-14)'s quoted-key form, and the
two do not collide: a quoted segment resolves **by the kind of container it is applied to**,
which the resolver knows at every step. Against a block set it is a label, here; against a
mapping it is a key, §4.1. O5's original rationale — "quoting the whole segment collides with
the label syntax HCL needs, which would make the address grammar ambiguous" — assumed the two
uses could meet, and they cannot: a container is a block set or a mapping, never both. O5's
*decision* stands (the `~2` escape is kept, and it is what keeps ordinary keys unquoted); only
that one clause of its reasoning is overturned.

**A repeated `(type, labels)` tuple is `HEW012 ambiguous-match` unless the hunk carries an
ordinal annotation** (§7.2). The path stays an identity statement; the ordinal is a separate,
visible admission that identity was insufficient here.

### 4.4 Optional segments — trailing `?`

A trailing `?` on the **last** segment means "match it, or create it":

```
/mcpServers/name=ctxloom?          the ctxloom element, inserted if absent
/server/tls?                       the tls object, created if absent
```

Legal only on the last segment, and only on a hunk **anchor** (never inside `? expect`). Its
effect: `HEW013 no-match` at that segment becomes a creation instead of an error. Creation
inserts at the end of the container unless the body's context pins a position (§6.2).

### 4.5 Heading, block, and marker segments — Markdown

```
/# Getting started                       the h1 section with that exact heading text
/# Getting started/## Install            the h2 "Install" nested inside it
/# Install/code:0                        the first fenced code block in that section
/# Install/list:0                        the first list block
/# Install/para:1                        the second paragraph block
/@ctxloom:context                        a ctxloom managed-marker block (§8.6)
```

A heading segment is the literal heading marker plus a single space plus the exact heading
text. `/` inside heading text escapes as `~1`. Duplicate headings at the same level under the
same parent are `HEW012 ambiguous-match`.

Block segments are `<kind>:<ordinal>` where kind ∈ `para | code | list | table | quote | html`
and the ordinal counts **within that kind, within that section**. Markdown blocks have no
keys of any sort, so this is the one place a path carries a number that is not an array
index; see [O7](#decisions-and-residual-open-questions).

### 4.5b Comment segments

Comments are nodes in JSONC, YAML, TOML and HCL (§8), so they need addresses. Two forms:

```
/server/#0                 the first standalone comment node inside "server"
/server/#2                 the third
/server/timeout/#t         the trailing comment on the "timeout" member
```

`#<n>` is kind-scoped within the container, exactly like a Markdown block ordinal. `#t` is the
trailing comment attached to the preceding member. Comment addresses are what let a comment be
removed or replaced as a node (OP-32, OP-33) and are why the mirror grammar's
comment-attachment spelling (OP-31) needs no IR qualifier of its own — it desugars into an
`add` at a comment address.

**The ordinal is a selector on a `test`, `remove` or `replace`, and a POSITION on an `add`**
([O47](#p5--the-api-ratification-2026-08-14)). This is the one place the two readings of a
number diverge, and the spec previously left it to be guessed. On an `add`, `#<n>` names the
index the new comment **takes**, and `#-` appends — the same append spelling §4.1 gives a
sequence, for the same reason. Existing comments at that index and after shift down; the
placement is otherwise governed by `before:`/`after:` exactly as any other add.

A worked example, with the container's comments numbered before and after:

```jsonc
// before                       addresses          // after `add` at /server/#1
{                                                  {
  // pinned by ops     -> /server/#0                 // pinned by ops     -> #0
  // do not reformat   -> /server/#1                 // reviewed 2026-08  -> #1  <- added
  "timeout": 30 // sec -> /server/timeout/#t         // do not reformat   -> #2  <- shifted
}                                                  }
```

`/server/#1` on a `remove` deletes "do not reformat". `/server/#1` on an `add` inserts *above*
it. Both are the same address; the op decides whether the number selects or positions, which
is what makes `#-` worth having — an append needs no such decision. `#t` is never an `add`
position (a member has at most one trailing comment); an `add` at `#t` where one already
exists is `HEW014`, and `! upsert` replaces it.

### 4.6 Relative paths in annotations

Inside a hunk, an annotation's path may begin with `.` meaning "relative to the enclosing
hunk's anchor":

```
@@ /server @@
? expect ./port = 8080
```

### 4.7 Reserved in v0

**Ratified ([O44](#p5--the-api-ratification-2026-08-14)).** Two spellings are `HEW001` in v0
even though nothing yet uses them, because they are the spellings the extensions this spec
already names would have to take:

| Reserved | Reserved for | Literal spelling |
|---|---|---|
| A key-match **field** ending `<`, `>` or `!` | Comparison operators, the [O6](#ratified-by-the-coordinator-2026-08-14) extension | `/x/"count>"=5` |
| A bare `*` segment | A wildcard segment | `/paths/"*"` |

The first is not speculative tidiness: `count>=5` **parses today** as a match on a field named
`count>` against the value `5`, which is a working address that a later `>=` operator would
silently reinterpret. Refusing it now costs a field name nobody has, and not refusing it costs
a v1 that cannot add operators without breaking v0 patches.

The second is the same trade with a real literal behind it — `*` is a genuine key in a
`tsconfig.json` `paths` map — which is exactly why this reservation is affordable only now
that [O41](#p5--the-api-ratification-2026-08-14) gives every literal a spelling.

**The quoted form is the permanent escape hatch**: any token this spec reserves, now or later,
remains addressable as a literal by quoting it, so a reservation can never make a real
document unpatchable.

---

## 5. Hunk semantics: the two projections

This is the normative core of Hew, and it is what makes the notation implementable once per
format instead of once per operation.

**Every hunk body defines exactly two documents:**

- the **before-image** = every context and `-` line, margins stripped;
- the **after-image** = every context and `+` line, margins stripped.

Annotation and comment lines are in neither. Both images are parsed by the target format's
**fragment parser** as a fragment of the same node kind as the anchor.

Application is then defined in three steps:

1. **Match.** The before-image must match the target subtree at the anchor, under the
   matching rules of §6.1. Failure is `HEW010 stale-target`, naming the first mismatching Hew
   path and the patch line number.
2. **Diff.** The before-image and after-image are diffed at the *node* level, producing an
   ordered RFC 6902 op list (§9).
3. **Apply.** The op list is handed to the format backend, which mutates the target
   byte-preservingly and re-serializes.

Worked example. Target `config.yaml`:

```yaml
name: myapp
version: 1.2.0
server:
  host: localhost
  port: 8080
  timeout: 30
tags:
  - alpha
  - beta
mcpServers:
  - name: filesystem
    command: npx
    args: ["-y", "@modelcontextprotocol/server-filesystem"]
  - name: github
    command: npx
    args: ["-y", "@modelcontextprotocol/server-github"]
```

The survey's canonical four-operation change-set, as one Hew file:

```
hew: 1

--- config.yaml format=yaml

@@ /server @@
- host: localhost
  port: 8080
- timeout: 30
+ timeout: 60

@@ /tags @@
  - beta
+ - gamma

@@ /mcpServers @@
  - name: github
+ - name: ctxloom
+   command: ctxloom
+   args: [mcp]
```

Its projections, hunk by hunk:

| Hunk | before-image | after-image |
|---|---|---|
| `/server` | `{host: localhost, port: 8080, timeout: 30}` | `{port: 8080, timeout: 60}` |
| `/tags` | `[beta]` | `[beta, gamma]` |
| `/mcpServers` | `[{name: github}]` | `[{name: github}, {name: ctxloom, …}]` |

And the derived op list (§9):

```json
[
  { "op": "test",    "path": "/server/host",    "value": "localhost" },
  { "op": "test",    "path": "/server/port",    "value": 8080 },
  { "op": "test",    "path": "/server/timeout", "value": 30 },
  { "op": "remove",  "path": "/server/host" },
  { "op": "replace", "path": "/server/timeout", "value": 60 },
  { "op": "test",    "path": "/tags/1",         "value": "beta" },
  { "op": "add",     "path": "/tags/2",         "value": "gamma" },
  { "op": "test",    "path": "/mcpServers/1/name", "value": "github" },
  { "op": "add",     "path": "/mcpServers/2",   "value": { "name": "ctxloom", "command": "ctxloom", "args": ["mcp"] } }
]
```

Note what the `test` ops are: **every context line and every removal becomes an assertion.**
That is where loud staleness lives in the lowered form, and it is why a hew patch is
down-compilable to 6902 without losing its safety property.

### 5.1 Partial elements in a match

In the `/mcpServers` hunk above, the context element is written as `- name: github` — one
field, not the whole element. Object-valued context is matched as a **subset** by default
(§6.1): the listed fields must be present and equal, unlisted fields are neither required nor
forbidden. This is what lets a keyed-array context line be one line long instead of eight,
and it is the single largest readability win Hew has over RFC 7386 Merge Patch, which must
restate every untouched element of a touched array.

---

## 6. Context, position, and exhaustiveness

### 6.1 Matching is partial by default

| Node kind in the before-image | Match rule |
|---|---|
| Mapping / object | **Subset.** Every listed key must exist with an equal value. Unlisted keys are ignored. |
| Sequence / array | **Ordered subsequence.** Listed elements must appear in the target in the listed relative order. Unlisted elements may appear before, between, or after. |
| Scalar | **Exact**, after format-native decoding (`8080` == `8080`, `8080` != `"8080"`). |
| Comment (JSONC/YAML/TOML/HCL) | **Exact text**, after stripping the comment marker and one leading space. |
| Markdown block | **Exact source bytes** of the block, after trailing-whitespace normalization. |

`? exhaustive` (§7.1) upgrades subset to exact-set and subsequence to exact-sequence for the
container it governs. Use it when "nothing else was added here" is part of what you are
asserting — the case Merge Patch handles by accident and expensively, and Hew handles on
purpose and cheaply.

### 6.2 Insertion position comes from surrounding context

An added node is inserted **relative to the context lines around it in the hunk body**:

- If a context (or `-`) sibling precedes the `+` run, insert immediately after that sibling.
- Otherwise, if a context sibling follows the `+` run, insert immediately before it.
- Otherwise (no sibling context at all in this container), insert at the **end** of the
  container.

This is unified diff's rule, and it is the reason context lines earn their keystrokes:

```
@@ /server @@
  host: localhost
+ tls: true
  port: 8080
```

places `tls` between `host` and `port` — something no op-list notation can express without a
numeric index, and no merge-patch notation can express at all.

For mappings, position is a *formatting* property, not a semantic one; a backend whose format
has no stable key order (none of ours — all six preserve source order) may ignore it. For
sequences it is semantic.

### 6.3 Equality and formatting

Matching compares **values**, not bytes: `port: 8080`, `port:  8080`, and `"port": 8080` all
match the number 8080. Quoting style, flow-vs-block YAML style, and whitespace inside a line
are not matched.

Conversely, **application preserves the target's bytes everywhere it did not have to change
them.** An unchanged sibling keeps its exact original bytes, comments, blank lines, and
quoting. A changed scalar keeps the *container's* formatting and adopts the patch's rendering
for the new value. This is the byte-preservation contract every backend must meet, and the
corpus enforces it by byte-exact comparison of expected output (§13.4).

### 6.4 The tolerance model — what drift a patch survives

Normative, and the section to read if you are coming from `patch(1)`.

**`patch(1)`'s fuzz is the wrong benchmark.** Fuzz relocates a hunk *textually*: it searches
nearby line offsets and, with `--fuzz`, drops context lines until something matches. It
tolerates the drift it can find by scanning, and it silently mis-applies when the scan lands
somewhere plausible but wrong. Hew has no fuzz (§1) — and yet it tolerates *more* real-world
drift than fuzz does, because it does not match by position at all.

> **Hew addresses nodes by path. Reordering is invisible to a path.** A user who reorganized
> their `settings.json` has not broken any Hew patch against it. Under `patch(1)` the same
> reorganization breaks every hunk.

#### 6.4.1 Map and object asserts are order-insensitive

A mirror context line for a sibling key compiles to a `test` on that key's **presence and
value** (§9.0) — never on its position. This is normative for every mapping-shaped node in
all six formats: JSON/JSONC objects, YAML mappings, TOML tables, HCL bodies, and the child
sets of Markdown sections.

```
@@ /server @@
  host: localhost
- timeout: 30
+ timeout: 60
```

applies unchanged whether the target reads `host, port, timeout`, `timeout, port, host`, or
has gained six new keys in between. The one place order re-enters is **insertion position**
(§6.2), and that is an instruction about where to *write*, not a condition on what must be
true — a `+` whose context has moved is placed relative to where that context now is.

#### 6.4.2 Keyed arrays are addressed by identity

`/mcpServers/name=github` (§4.2) and `/tags/=gamma` survive reordering of the sequence, because
neither address mentions a position. This is why §9.4-R4 requires the differ to *prefer*
identity addressing, and it is the strategic-merge lesson made general: SMP's merge-by-key
behavior was the right idea trapped behind a schema registry, and Hew's answer is to put the
key in the address where the patch itself declares it.

**"A usable key"**, normatively, for both the differ and a human author:

1. The field is present on **every** element of the sequence, and
2. its value is a **scalar** (not a map or sequence), and
3. the values are **unique** across the sequence.

If no field satisfies all three, the sequence has no identity and Hew addresses it positionally
— honestly, and with the consequences in §6.4.3.

#### 6.4.3 The two order-sensitive spots, named

| Spot | Why | Mitigation |
|---|---|---|
| **Plain positional arrays** (`/tags/0`) | The elements have no identity; position is genuinely all there is. | Prefer the `=value` address (§4.2) for scalar lists — `/tags/=gamma` is identity addressing for a list that has no key field. Only fall back to `/tags/0` when the list has duplicates. |
| **HCL repeated-label blocks** (`! match ord=1`) | Two `provider "aws"` blocks are indistinguishable by path; an inserted earlier sibling shifts every later ordinal. | §7.2, tightened below. |

**Normative mitigation for ordinals** (strengthening §7.2, and adopting ytt's
`overlay.subset()` idiom): an ordinal is the **last resort**, not the first tool.

1. If the block has a distinguishing child attribute, **address it by that attribute
   instead** of by ordinal — a hew path may descend into the block and assert
   (`? expect ./alias = "east"`), and, since
   [O45](#p5--the-api-ratification-2026-08-14), may *select* the block by that attribute:
   `/provider/"aws"/alias="east"`. Until O45 this rule recommended a mitigation that §4.2 did
   not actually offer a spelling for, which is why the ruling extends key-match to
   same-`(type, labels)` block sets. Implementation pending.
2. **An ordinal-addressed transform MUST carry at least one distinguishing assert** — a
   context line or a `? expect` on a child that differs between the same-label siblings. A
   patch that carries `! match ord=` with no distinguishing assert is `HEW001`. The reason is
   the whole tolerance model in one sentence: *if the ordinal shifts, the patch must fail
   loudly rather than silently edit the wrong block.*
3. If the siblings are genuinely indistinguishable in every child, the ordinal stands alone —
   and that is the one construct in Hew that can silently patch the wrong node. The corpus
   pins the diagnostic, and [O25](#residual--genuinely-open) asks whether such a patch
   should be refused outright.

#### 6.4.4 Two different things called "move"

Spec language keeps these apart, because conflating them is how a reader concludes Hew is
either too strict or too loose:

| Term | Meaning | Hew's stance |
|---|---|---|
| **Target drift** | The *user* reordered or relocated nodes in the file since the patch was written. | **Tolerated, and invisible** — node addressing does not see it (§6.4.1, §6.4.2). |
| **Move as an operation** | The *patch* relocates a node from one path to another (OP-21). | Not expressible in the mirror grammar; written as a transform list (§9.6, Appendix C.1). |

"This patch survives a move" means the first. "This patch performs a move" means the second.

#### 6.4.5 The tolerance table

Normative. Each row is corpus-case material (§13), one per format family.

| Target changed how | Hew's response | Why |
|---|---|---|
| Keys reordered within a map | **Survives** | Path addressing; no positional assert (§6.4.1) |
| Keyed-array elements reordered | **Survives** | Identity addressing (§6.4.2) |
| Whitespace, indentation, line wrapping changed | **Survives** | Matching compares values, not bytes (§6.3) |
| Quoting style changed (`'x'` → `"x"`, bare → quoted) | **Survives** | Same |
| YAML block ↔ flow style changed | **Survives** | Same |
| Comments added, edited, or removed elsewhere | **Survives** | Comments are nodes; unasserted nodes are unconstrained (§6.1) |
| Unrelated keys added anywhere | **Survives** | Subset matching (§6.1) — unless `? exhaustive` was asserted, which is the point of asserting it |
| Unrelated keys removed elsewhere | **Survives** | Same |
| An asserted node's **value** changed | **`HEW010` stale-target** | The assert is the contract |
| An addressed node is **missing** | **`HEW013` no-match** | Never a silent no-op (§1) |
| A key-match or heading now matches **two** nodes | **`HEW012` ambiguous-match** | Drift Hew will not resolve by guessing |
| The patch was **already applied** | **`HEW014`/`HEW011`** (§10.6), unless `! idempotent` or the `idempotent:` pragma (§7.5) | Ruled O3: strict default |
| Plain-array elements reordered | **`HEW010`** if a positional address was used | The one honest failure; use `=value` addressing |
| An earlier same-label HCL block was inserted | **`HEW010`/`HEW011`** via the required distinguishing assert (§6.4.3) | Loud, not silent |

---

## 7. Annotations

Annotation lines carry margin `?` (assertion — can fail) or `!` (directive — changes how
application works, cannot itself fail). They are the second half of the notation: the mirror
body says *what the shape is*, and the annotations say *what must be true about it* and *which
of several identical-looking nodes is meant*.

**Attachment.** An annotation's text is written at the indentation of the body lines it sits
among. Annotations fall into three attachment classes:

| Class | Directives | Attaches to |
|---|---|---|
| **Free-standing** | `? expect`, `? absent`, `? count`, `? kind` | Nothing — they carry their own path. |
| **Container-scoped** | `? exhaustive`, `! surface` | The container whose children are at this indentation (the anchor, for top-level body lines). |
| **Line-scoped** | `! match`, `! anchor`, `! optional`, `! idempotent`, `! strict`, `! upsert`, `! default` | The immediately following body line; or the **anchor** if the annotation is the first body line of the hunk. |

A line-scoped annotation not followed by a body line, and not first in the hunk, is `HEW001`.

### 7.1 `?` assertions

| Directive | Meaning | Failure |
|---|---|---|
| `? expect <hewpath> = <value>` | The node exists and equals the value. Does not modify. | `HEW011` |
| `? absent <hewpath>` | The node does not exist. | `HEW011` |
| `? exhaustive` | The listed children are the *complete* child set of the governed container. | `HEW011` |
| `? count <hewpath> = <n>` | The container has exactly `n` children. | `HEW011` |
| `? kind <hewpath> = <k>` | The node's kind is `k` ∈ `map\|seq\|scalar\|block\|section`. | `HEW011` |

```
@@ /server @@
? expect /version = 1.2.0
? absent /server/tls
- timeout: 30
+ timeout: 60
```

Container-scoped `? exhaustive`, shown at both levels so the attachment rule is concrete:

```
@@ / @@
? exhaustive
  server:
? exhaustive
    port: 8080
```

The first asserts `server` is the document's only top-level key; the second asserts `port` is
`server`'s only key.

### 7.2 `! match` — ordinal selection among identical siblings

This is the notation's answer to HCL's repeated-label case, promoted from the survey's
design-A rendering into normative grammar. Target:

```hcl
provider "aws" {
  region  = "us-west-1"
  profile = "default"
}

provider "aws" {
  alias  = "east"
  region = "us-east-1"
}
```

`/provider/"aws"` names two nodes, so it is `HEW012` on its own. The ordinal is written as an
annotation, in place, beside the block it selects:

```
--- main.tf format=hcl

@@ / @@
! match label=["aws"] ord=0
  provider "aws" {
-   region  = "us-west-1"
+   region  = "us-west-2"
    profile = "default"
  }
! match label=["aws"] ord=1
  provider "aws" {
    alias  = "east"
    region = "us-east-1"
+   profile = "ctxloom"
  }
```

Or, hunk-anchored, using the first-body-line form:

```
@@ /provider/"aws" @@
! match ord=1
  alias = "east"
+ profile = "ctxloom"
```

Grammar: `! match [label=[<label>, …]] ord=<n>`.

- `ord` is **required**, 0-based, and counts same-`(type, labels)` siblings in source order.
- `label=[…]` is **optional and redundant by design**: when present it is checked against the
  selected block's actual labels and a mismatch is `HEW011`. It exists because a bare `ord=1`
  is unreadable in a long file, and because a redundant assertion is the cheapest possible
  guard on a fragile selector.
- An ordinal is only legal where the path is genuinely ambiguous. `! match` on a path that
  resolves to exactly one node is `HEW001` — an unnecessary ordinal is a latent misapply
  waiting for the file to grow a sibling, and Hew refuses it rather than tolerating it.
- **Every hunk using `! match ord=` MUST carry at least one distinguishing assert** — a
  context line or a `? expect` on a child that differs between the same-label siblings. A
  hunk with `! match ord=` and no distinguishing assert is `HEW001`. The `alias = "east"`
  context line above is what makes the second example safe: if a third `provider "aws"` block
  is inserted earlier, the ordinal still selects index 1 but the context no longer matches,
  and the apply fails by name instead of editing the wrong provider. See §6.4.3 for the
  tolerance rationale, and prefer addressing by the distinguishing attribute outright when one
  exists.

### 7.3 `! anchor` and `! surface`

Format-specific directives, specified with their wrinkles: `! anchor rewrite|fork` in §8.3
(YAML aliases), `! surface table|dotted` in §8.4 (TOML duality).

### 7.4 Assert-only hunks

A hunk whose body contains context and `?` lines but **no `+` or `-` lines** is a legal
assert-only hunk. It changes nothing, contributes only `test` ops (§9), and fails loudly if
its assertions do not hold. This is how Hew expresses "check that the world is what I think it
is" — a precondition patch, a drift check in CI, a guard shipped alongside a migration:

```
hew: 1

--- .claude/settings.json format=jsonc

@@ /permissions @@
? exhaustive
  "deny": ["Bash(rm -rf *)"]
  "allow": ["Bash(git *)"]

@@ / @@
? absent /env/ANTHROPIC_API_KEY
? kind /permissions = map
```

Note that the mirror body is still a mirror — the shape is what supplies the context — so an
assert-only patch reads exactly like the patches around it. It has no second grammar.

### 7.5 `! idempotent` and `! strict`

**Ruled (human, 2026-08-14): strict is the default and convergence is opt-in.** Three
spellings of one boolean, in precedence order — hunk directive beats file pragma beats the
strict default:

| Spelling | Scope | Effect |
|---|---|---|
| *(nothing)* | default | Strict. Re-applying fails loudly (§10.6). |
| `idempotent: true` | preamble (§2.1) | Every hunk in the file is convergent. |
| `! idempotent` | hunk or line | This hunk (or line) is convergent. |
| `! strict` | hunk or line | This hunk is strict, overriding a file pragma. |

`! idempotent` attached to a hunk (as the first body line) or to a single `+`/`-` line
changes the failure rule to:

> If the before-image does not match **but the after-image does**, the hunk is satisfied and
> contributes zero ops.

It does not weaken anything else: a *partially* applied state — where neither image matches —
is still `HEW010 stale-target`, which is exactly the dangerous case a naive "just merge it"
tool papers over.

This is the directive ctxloom's own managed-file writers (M1–M8 in the config-patching
review) will reach for immediately, since every one of them is idempotent by construction —
which is exactly why the *file pragma* exists rather than only the per-hunk form.

**Why strict remains the default even though ctxloom's own writers want the opposite.** A
convergent-by-default format cannot tell "this patch has already been applied" from "this
patch was written against a file that has since drifted into looking applied". The first is
benign; the second is the misapply this format was built to prevent, and only the strict
default makes them distinguishable. Making convergence the default would optimize for the
generating tool at the expense of the hand-authored patch — and the hand-authored patch, read
in a pull request, is the artifact the whole notation exists for.

### 7.6 `! optional`

Attached to a `-` line: the removal is satisfied whether or not the node exists. Discouraged —
it disables loud staleness for that line, which is the property the whole format exists to
provide. A conformant linter should warn on every use, and the Hew comment above it should say
why. (OP-06.)

### 7.7 `! upsert` and `! default` — the two add-semantics variants

Attached to a `+` line. They exist because the operations sweep (§11) found three distinct
"add" semantics in the surveyed systems, and a single `+` margin can only mean one of them:

| Directive | Node already present → | Catalog |
|---|---|---|
| *(none)* | `HEW014 already-exists` — the strict default | OP-02 |
| `! upsert` | replaced, whatever it held | OP-03 |
| `! default` | **left alone**, zero ops, exit 0 | OP-04 |

```
@@ /server @@
# seed a timeout only if the user has not chosen one
! default
+ timeout: 30
```

`! upsert` is the one mapping write that asserts nothing about the prior state, so it cannot
detect drift; use it only where ctxloom owns the key outright (`agent.InstallMCPServerJSON`'s
exact case). `! default` is its opposite and is safe by construction.

---

## 8. Per-format binding

A format binding must answer exactly four questions. Everything else in this spec is
format-agnostic.

1. **Detection** — which files am I? (§8.0)
2. **Fragment parsing** — given body text and an anchor node kind, what tree does it denote?
3. **Node identity** — what are this format's node kinds, and what does "equal" mean?
4. **Byte-preserving application** — given an op list, how do I edit the source?

### 8.0 Detection

| Format | Extensions | Notes |
|---|---|---|
| `json` | `.json` | Also the default for `.json` files known to forbid comments (`package.json`). |
| `jsonc` | `.jsonc`, well-known names | `settings.json`, `tasks.json`, `launch.json`, `tsconfig.json`, `.mcp.json` are JSONC by convention despite the extension. The well-known-name list is data, not spec — [O4](#decisions-and-residual-open-questions). |
| `yaml` | `.yaml`, `.yml` | |
| `toml` | `.toml` | |
| `hcl` | `.tf`, `.hcl`, `.tfvars`, `.nomad`, `.pkr.hcl` | `.tf.json` is `json`, not `hcl`. |
| `markdown` | `.md`, `.markdown` | |

Explicit `format=` on the target line always wins. Ambiguity without an explicit declaration
is `HEW021 unsupported-format` — Hew never sniffs content to guess.

**The table above is a non-normative record of the shipped defaults**
([O4](#ratified-by-the-coordinator-2026-08-14), finished by
[O48](#p5--the-format-isolation-audit-2026-08-14)). What is normative is the *mechanism*: each
format extension carries its own `DetectRule` (Appendix A.6), detection reads the **name** and
never the content, and an explicit `format=` always wins. A hard-coded name list in a standard
ages badly; one in an extension's detection table is a default.

### 8.1 JSON

Node kinds: object, array, string, number, boolean, null.

**Body text is written without the anchor's own delimiters** (§2.3) and is read by a
*tolerant* member-list reader:

- **Trailing and separating commas are optional and ignored.** `port: 8080` and
  `"port": 8080,` are the same body line. The backend emits correct separators.
- **Keys may be written bare** if they are valid JSON identifiers: `port: 8080` ≡
  `"port": 8080`. A key needing quotes must be quoted.
- Nested values on one line carry their own braces/brackets normally: `+ args: ["mcp"]`.

```
--- .mcp.json format=json

@@ /mcpServers @@
  "ctxloom": { "command": "ctxloom" }
+ "taskloom": {
+   "command": "taskloom",
+   "args": ["mcp"]
+ }
```

Byte preservation: JSON has no comments, but it has indentation, key order, and **numeric
literal form**. A backend MUST NOT round-trip numbers through float64 (the `MCPFileConfig`
lesson: foreign large integers must be re-emitted as their original bytes). Untouched members
keep their exact source bytes.

### 8.2 JSONC — comment anchoring

Everything from §8.1, plus comments as first-class nodes.

**The anchoring rule** (this is the wrinkle, made normative):

- A comment line (`//` or `/* */`) **immediately preceding** a member, with no blank line
  between, is that member's **leading comment** and moves/deletes with it.
- A comment on the **same line as** a member, after it, is that member's **trailing comment**
  and moves/deletes with it.
- A comment separated from the next member by a blank line, or standing at the end of a
  container, is a **free comment** bound to the *container* at its position. It is never
  moved or deleted by an operation on a sibling.

Consequences the corpus pins:

```
--- .claude/settings.json format=jsonc

@@ /permissions @@
  // ctxloom-managed — do not edit
- "deny": ["Bash(rm -rf *)"]
+ "deny": ["Bash(rm -rf *)", "Bash(curl *)"]
```

Removing a member with a leading comment removes both. To keep the comment, make it a context
line (as above) — the comment is context, the member changes, the comment survives. To remove
a member *and* keep its leading comment, promote the comment to free by adding a blank line
first, which is itself an edit.

### 8.3 YAML — anchors, aliases, merge keys

Node kinds: mapping, sequence, scalar, comment, anchor, alias, document.

Style is preserved: a block mapping stays a block mapping, a flow sequence stays flow. An
added node adopts the style of its siblings, or the patch's own rendering if it has none.

**The anchor/alias wrinkle, made normative.** One authored node can be referenced from many
tree locations. An edit whose path resolves *through or at* an alias site is:

- `HEW040 anchor-ambiguity` by default. Hew will not guess whether you meant to change every
  use site or just this one.
- `! anchor rewrite` — the edit is applied to the **anchor definition**. Every alias site
  observes it. The patch is asserting that shared-value semantics are intended.
- `! anchor fork` — the alias at this site is **materialized** into an independent concrete
  node carrying the anchor's current value, and the edit is applied to that copy. Other alias
  sites are unaffected.

```yaml
# target
defaults: &defaults
  timeout: 30
  retries: 3
service_a:
  <<: *defaults
  port: 8080
service_b:
  <<: *defaults
  port: 8081
```

```
@@ /service_a @@
! anchor fork
- timeout: 30
+ timeout: 60
```

yields `service_a` with its own explicit `timeout: 60` (the merge key stays, the key is
shadowed), while `service_b` still sees 30. With `! anchor rewrite`, `defaults.timeout`
becomes 60 and both services see it.

**Merge keys (`<<:`) specifically:** a key that a mapping only has *via* a merge is **not
present at that site**. Removing it with `-` is `HEW013 no-match`, not a silent success — you
cannot delete an inherited key, only shadow it. The error message names the anchor the key
came from.

### 8.4 TOML — dotted-key / table-header duality

`a.b.c = 1`, `[a.b]` + `c = 1`, and `[a]` + `b.c = 1` denote the same tree with three
different surface forms.

**Normative rules:**

1. An edit to a path that **already exists** is applied at whichever surface form the target
   actually uses. Hew never adds a second surface for an existing path.
2. If a path is defined at **two** surfaces in the same document (which TOML forbids but real
   files occasionally contain), that is `HEW041 surface-ambiguity`.
3. A **creation** adopts the surface of the nearest existing ancestor: creating `/a/b/c` where
   `[a.b]` exists appends `c = …` to that table; where only `a.b = {}` exists (inline table)
   it edits the inline table; where nothing exists it creates a `[a.b]` table header at the
   end of the document.
4. `! surface table` / `! surface dotted` overrides rule 3 for a creation. It is **not**
   permitted to rewrite an existing path's surface — surface migration is not a patch
   operation in v0 ([O10](#decisions-and-residual-open-questions)).

```
--- ~/.codex/config.toml format=toml

@@ /mcp_servers @@
? absent /mcp_servers/taskloom
! surface table
+ [mcp_servers.taskloom]
+ command = "taskloom"
+ args = ["mcp"]
```

Note the body writes the `[mcp_servers.taskloom]` header even though §2.3 says the anchor's
own delimiters are omitted — because here the header belongs to the *added child*, not to the
anchor `/mcp_servers`. Array-of-tables (`[[x]]`) children are addressed as sequence elements
and support key-match (`/tool/plugins/name=foo`).

### 8.5 HCL — attributes, blocks, labels

Node kinds: body, attribute, block, expression, comment.

- An **attribute** body line contains a top-level `=`: `region = "us-west-1"`.
- A **block** body line ends in `{` and its body is written indented, closing with `}`:

```
--- main.tf format=hcl

@@ /terraform @@
  required_version = ">= 1.6"
+ required_providers {
+   aws = {
+     source  = "hashicorp/aws"
+     version = "~> 5.0"
+   }
+ }
```

- **Expressions are compared as source text**, normalized for whitespace only. Hew does not
  evaluate HCL: `"${var.x}"` and `var.x` are different values even where HCL would agree.
  This keeps the format binding honest about what it can prove.
- Alignment: `hclwrite` re-aligns `=` within a body. Hew adopts the backend's alignment on any
  body it modified, and leaves untouched bodies byte-identical. The corpus pins this.
- **Repeated `(type, labels)` tuples**: `HEW012 ambiguous-match` unless a `! match ord=`
  annotation (§7.2) selects one. This is the case the notation was checked against in the
  survey's HCL section, and the check is now the grammar.

### 8.6 Markdown — sections and blocks

Markdown is the one format where Hew is not addressing a keyed tree, and it gets a dialect
rather than a variant.

- The document is a tree of **sections** (by ATX heading level) containing **blocks**
  (paragraph, fenced code, list, table, blockquote, HTML). Setext headings are normalized to
  their ATX level for addressing but preserved byte-for-byte if untouched.
- **A body line's margin applies to its whole line, but matching and ops are per block.**
  Every line of a multi-line block carries the same margin. A block whose lines carry mixed
  margins is `HEW001`. To change one line of a paragraph, remove the paragraph and add the
  replacement — Markdown has no sub-block addressing in v0.
- **Blank lines are structural, not written.** The patch does not carry blank lines between
  blocks; the backend emits exactly one blank line between blocks it inserts and preserves the
  target's existing separation elsewhere.

```
--- README.md format=markdown

@@ /# ctxloom/## Install @@
  Install with:
- ```sh
- go install github.com/ctxloom/ctxloom@v0.6.0
- ```
+ ```sh
+ go install github.com/ctxloom/ctxloom@v0.7.0
+ ```
```

**Managed-marker blocks.** ctxloom's own in-file ownership markers (M2:
`<!-- ctxloom:context:begin (managed — do not edit between markers) -->` …
`<!-- ctxloom:context:end -->`) are addressable as a single node:

```
@@ /@ctxloom:context @@
! idempotent
- (the previous managed body, as context/removal lines)
+ (the new managed body)
```

The `@<name>` segment addresses the region between an HTML comment pair whose begin marker
matches `<!-- <name>:begin` and end marker `<!-- <name>:end`. The markers themselves are never
part of the node's value, so a patch replacing the region cannot destroy them. An unclosed
begin marker is `HEW002 target-parse-error` — refuse, do not repair. This is the direct Hew
expression of `agent.WriteManagedContext`, and it is why Markdown is in the implement tier at
all.

### 8.7 Markdown: Hew dialect vs unified diff — evaluation plan

**Markdown's place in the implement tier is not settled** (human, 2026-08-14). It may be
better served by plain `patch(1)`. The dialect above stays in the draft as designed; this
section is the plan for deciding, and it runs **after** the spec is complete, before any
Markdown backend is built.

#### 8.7.1 Why this is a real question and not a formality

The tolerance model (§6.4) is where Hew earns its keep, and **its central asymmetry runs
against Markdown**:

| | Keyed trees (JSON/JSONC/YAML/TOML/HCL) | Prose (Markdown) |
|---|---|---|
| Is sibling order meaningful? | **No.** Reordering `settings.json` changes nothing. | **Yes.** Paragraph order *is* the document. |
| So reorder-blindness is… | the single largest win over `patch(1)` | **worth approximately nothing** |
| Is there a stable identity per node? | Yes — keys, `name=` fields, labels | **No.** A paragraph's only identity is its text and its position — which is exactly what a unified-diff hunk already matches on |
| Is `patch(1)` at home? | No — it cannot see that two keys are siblings | **Yes.** Line-oriented prose is its native material |

Put bluntly: Hew's Markdown dialect addresses blocks by *kind-scoped ordinal within a section*
(§4.5), which is a positional address dressed up as a structural one. `patch(1)` addresses
lines by position with fuzz. Both are positional. One of them already exists, is universally
installed, has thirty years of tooling, and is what every reviewer already reads.

#### 8.7.2 What the evaluation must do

Not a discussion — an **analysis and simulation**, in the style of the notation survey that
produced this spec: render the same realistic scenarios in both notations, side by side, and
score where each fails.

**Scenarios (all drawn from ctxloom's real managed-Markdown surfaces, not invented):**

1. **Managed-block replacement** — `CLAUDE.md` / `AGENTS.md` with a
   `<!-- ctxloom:context:begin -->` … `end` region rewritten wholesale (M2's actual job,
   OP-45). Both notations attempt it.
2. **User edits surrounding prose** — the same replacement, but the user has rewritten three
   paragraphs above and added a section below since the patch was authored.
3. **Block moved within the file** — the user relocated the managed block (or a section) to a
   different position in the document. This is the scenario the tolerance model claims as Hew's
   win; the evaluation must measure whether it *is* one for prose.
4. **Concurrent prose edits adjacent to the block** — a paragraph immediately above the
   managed region changed in the same window. `patch(1)`'s fuzz behavior here is the thing to
   observe: does it apply, mis-apply, or reject?
5. **Section addressed by heading, heading text edited** — Hew's `/## Install` address breaks;
   `patch(1)`'s context breaks too. Which fails more usefully?
6. **A section gains a duplicate heading** — Hew raises `HEW012`; `patch(1)` picks by position.

**Scoring, per scenario, per notation:**

| Criterion | Question |
|---|---|
| Applies correctly? | Does the intended edit land? |
| Fails loudly when it should? | Or does it apply somewhere plausible-but-wrong (`patch(1)`'s fuzz failure mode) or silently no-op? |
| Survives the drift? | Per §6.4's table, adapted to prose |
| Reviewable? | Is the patch legible in a PR — the criterion that started this whole workstream |
| Authoring cost | Hand-writing the patch; and, for the differ, generation cost |
| Implementation cost | A Markdown block model with byte preservation is the most expensive of the six backends — there is no `hclwrite` equivalent to lean on |

#### 8.7.3 Possible outcomes

1. **Keep the dialect** — the evaluation finds managed-block addressing (OP-45) and
   heading-path addressing genuinely beat unified diff for ctxloom's surfaces.
2. **Drop Markdown from the implement tier** — it moves to §12 (documented-only) with a
   normative "use `patch(1)`" note, and `hew` gains no Markdown backend.
3. **Narrow to the managed-marker case only** — Hew implements *only* `/@name` marker regions
   (OP-45) and nothing else, which is the one Markdown operation with a real structural
   address and the one `patch(1)` handles worst. This is the outcome to beat: it is a small
   backend with a clear win, and it is what ctxloom actually needs.

#### 8.7.4 Severability

The dialect is already structurally severable and must stay that way while the question is
open:

- All Markdown corpus cases live in **`corpus/markdown/`** and nowhere else, so a
  drop is `git rm -r` on one directory plus §8.6 and §4.5.
- **No rule in §§1–7 or §9–§11 depends on Markdown.** The block and heading segments (§4.5)
  and the comment/section node kinds are the only cross-references, and each is guarded by a
  format check.
- Markdown is the only format whose `NodeKind` set includes `KindSection`; nothing else reads
  it.

**[O48](#p5--the-format-isolation-audit-2026-08-14) strengthens this from an audit into a
deletion.** With `SegHeading`, `SegBlock`, `SegMarker`, `BlockKind` and `KindSection` relocated
to `ext/markdown` (§8.8), the core retains **no Markdown vocabulary at all** — the two
cross-references named above stop existing rather than staying guarded. Dropping the family
becomes removing `ext/markdown/` and `corpus/markdown/`, and severability stops being a claim
a reader has to verify.

Tracked as [O29](#residual--genuinely-open).

### 8.8 Format isolation — what the core is allowed to know

**Ruled (human, 2026-08-14), [O48](#p5--the-format-isolation-audit-2026-08-14): format-specific
constructs in the core are held to an absolute minimum, every one is challenged, and whatever
survives as genuinely format-specific lives in a per-format extension package** — layout
`ext/<format>`, not in the core package and not in the core grammar.

The premise is the one §9 already states and this section enforces: the core is a notation, an
IR and an execution model. A format is a *plugin*. Every format-specific token the core
grammar knows is a token the Rust port must reimplement, a token an INI or XML extension
(§12) cannot introduce without a spec revision, and — for Markdown specifically — a token that
makes [O29](#residual--genuinely-open)'s "severable" claim weaker than it sounds.

#### The design direction

**The core grammar defines only universal lexical shapes.** Five, and they are the ones every
tree-shaped format has: `key`, `index`, `-` (append), `key-match`, and the quoted segment
(§4.1). Everything else is a **sigil-or-shape segment whose interpretation is supplied by the
registered extension**:

```
segment := key | quoted | index | "-" | keymatch | extension-claimed
```

An extension registers the segment shapes it claims; the core lexes a segment into
`(raw token)` and offers it to the active format's extension before defaulting to `key`. The
quoted segment already works exactly this way and is the proof the mechanism is sound: one
lexical form, interpreted as a label against an HCL block set and as a key against a mapping,
resolved by the container the resolver is already standing on (§4.1, O41).

**Format-specific qualifiers move behind an extension-owned mechanism, and the wire format does
not move with them.** `anchor:` and `surface:` keep the `.hewt` spelling §9.6 defines, byte for
byte. What changes is *who owns them*: the extension declares the qualifier keys it
understands and validates their values, so an unknown qualifier is `HEW001` at parse
([O9](#ratified-by-the-coordinator-2026-08-14)'s fail-loud) and a known-but-unsupported one is
`HEW020` at apply (§9.3, unchanged).

#### The audit

Every format-specific element currently in the core, with a verdict.

| Element | Format(s) | Verdict | Rationale |
|---|---|---|---|
| `SegKey`, `SegIndex`, `SegAppend`, `SegMatch` | all | **keep in core** | Genuinely universal: every format in the implement tier and both documented-only families (§12) have keyed containers and ordered containers. RFC 6901 is the evidence — these are the shapes a format-neutral pointer standard needed. |
| Quoted segment (§4.1) | all | **keep in core** | A *literal* is universal; what a literal means is not. The core lexes it; the container's kind decides label-or-key, and the container comes from the extension. This is the pattern every other row below is measured against. |
| `SegLabel` | HCL | **restructure** | There is no such thing as a label segment. There is a *quoted segment* resolved against a block set, which is the same lexical form as a quoted key resolved against a mapping. The distinct kind disappears from the core; `ext/hcl` supplies "quoted, against a block set, means label". §4.3's text and every existing path spelling are unchanged. |
| `SegHeading`, `SegBlock`, `SegMarker` | Markdown | **relocate to `ext/markdown`** | The clearest case in the audit. `# text`, `code:0` and `@name` are prose-document vocabulary with no meaning in any config format, and they are three of the four things §8.7.4 has to name when it argues Markdown is severable. Moving them means **the core loses all Markdown vocabulary**, and O29's severability stops being a claim the reader has to audit — dropping the family becomes deleting a directory. |
| `BlockKind` (para/code/list/table/quote/html) | Markdown | **relocate to `ext/markdown`** | A closed enum of prose block types in a config-patching core. It exists solely to give `SegBlock` its kinds and moves with it. |
| `SegComment`, `CommentValue` | JSONC, YAML, TOML, HCL (not JSON) | **restructure** | The awkward one, and the audit should say so rather than round it to a neat answer: it is neither universal nor single-format. It is **capability**-scoped — "does this format have comment nodes" — which JSON answers no and §8.1 already enforces as `HEW020`. Ruling: the `#` form leaves the core segment vocabulary like the others, and the four comment-capable extensions share one `ext/comment` helper. Shared code, not core vocabulary — the distinction the whole ruling turns on. |
| `NodeKind.KindBlock`, `KindSection` | HCL, Markdown | **restructure** | `KindMap`/`KindSeq`/`KindScalar` are universal and stay. The other two become **extension-declared kind names**: `? kind` (OP-28) carries a string, and the active extension validates it against the set it declares. An unknown kind is `HEW001`, exactly as today; the `.hewt` spelling is unchanged. |
| `Transform.Anchor` | YAML | **restructure — ownership moves, spelling does not** | The `anchor:` key stays exactly as §9.6 defines it, because §9.6 is normative and the corpus pins those bytes. `ext/yaml` declares and validates it. See the tension below. |
| `Transform.Surface` | TOML | **restructure — same** | Same treatment, `ext/toml`. |
| `FormatID.Valid()`'s six-format switch | all | **restructure** | A live defect found by this audit, not just a layering complaint: `Valid()` hardcodes the six v0 formats, so a correctly-registered seventh extension is rejected by the *parser* before any binding is consulted. Validity must be a registry lookup (A.6), which is what makes §12's "documented-only" families addable without touching the core. |
| §8.0's detection table | all | **relocate to the extensions** | Already half-ruled by [O4](#ratified-by-the-coordinator-2026-08-14) ("the mechanism is normative; the list is binding data") and [O35](#p5--the-api-ratification-2026-08-14) (each binding carries its `DetectRule`). This ruling finishes it: §8.0's table becomes a **non-normative record of the shipped defaults**, and the normative statement is the mechanism plus "detection reads the name, never the content". |
| Binding packages `hewjson`, `hewjsonc`, `hewyaml`, `hewtoml`, `hewhcl`, `hewmarkdown` | each | **relocate: rename to `ext/<format>`** | The isolation should be visible in the import path. `github.com/benjaminabbitt/hew/go/ext/yaml` says what `hewyaml` only implies, and a reviewer scanning imports can see at a glance whether the core has grown a format dependency. **This is a breaking rename**, landing with the rest of the P5 implementation; consumers pinned at `v0.1.0` are unaffected until they upgrade. |

#### The tensions, recorded rather than forced

Two places where a construct cannot move cleanly, and the honest answer is to say where the
line fell and why.

1. **`Transform`'s serialized fields cannot leave the one IR.** §9 is built on a single
   `TransformList` that the parser, renderer, differ and every applier all speak, and §9.6
   pins its serialization byte-for-byte. A truly extension-owned qualifier would be an opaque
   bag the core cannot validate, cannot canonicalize deterministically, and cannot round-trip
   through RT2 — which would trade a layering win for a conformance loss. So the ruling stops
   at **ownership**: the field's *meaning and validation* belong to `ext/yaml` and `ext/toml`,
   its *spelling and ordering* stay in §9.6. The core still knows two format-specific key
   names. That is the residue, and it is deliberate.

2. **Path parsing becomes format-aware, which trades against §9's "the parser knows no format
   mechanics".** An extension-claimed segment form can only be resolved once the active format
   is known — which it is, from the `--- ` target line (§2.2), before any hunk is read. The
   line this ruling draws: the parser performs no format **mechanics** — it opens no target,
   parses no document, and does no I/O, all of which §9 actually requires — but it does
   consult a registered extension's **segment grammar**, which is data of the same kind as
   §8.0's detection rules. A patch whose format is unknown at parse time already cannot be
   lowered ([O9](#ratified-by-the-coordinator-2026-08-14), `HEW021`), so nothing new becomes
   unparseable.

---

## 9. The transform list

**Architecture ruling (human, 2026-08-14).** Hew is four components around one intermediate
representation. The IR is the **transform list**: an ordered list of node addresses, RFC
6902-modeled operations, and assertions. It is simultaneously the internal boundary, the
interop surface, and the thing the corpus pins.

```
        NOTATION SIDE                    IR                    FORMAT SIDE
                                 ┌─────────────────┐
   .hew text ──[ parser ]───────► │                 │ ──[ applier ]──► patched bytes
                                 │  transform list │       (per format)
   .hew text ◄──[ renderer ]───── │   addresses     │ ◄──[ differ ]──── (old, new) bytes
                                 │   ops (6902)    │       (per format)
                                 │   assertions    │
                                 └─────────────────┘
```

- **Parser** (`.hew` → IR). Owns the notation. **Never touches a target file, never opens
  anything, knows no format mechanics.** Its output is fully determined by the patch text.
- **Applier** (IR + target bytes → patched bytes). One implementation per format, each
  wrapping that format's byte-preserving editor library (`tailscale/hujson`,
  `pelletier/go-toml/v2` `unstable/edit`, `hashicorp/hcl/v2/hclwrite`, `yaml.v3` node surgery,
  a Markdown block editor). **Never sees a margin, a hunk, or an annotation.**
- **Differ** (old bytes, new bytes → IR). One implementation per format, parsing both sides
  with the *same* library its applier uses, computing a structural diff into the IR. P4 work;
  §9.4 specifies its requirements now because they constrain the IR's design.
- **Renderer** (IR → `.hew`). Notation side, format-agnostic like the parser. Writes the mirror
  grammar back out, generating context lines from the assertions in the IR.

Parser and renderer are notation-side inverses; differ and applier are format-side inverses.
The four-way closure is what makes the round-trip identity in §13.5 a meaningful conformance
test rather than a slogan.

**The IR has a canonical serialized form (§9.6), and that form is also an accepted input.**
It had to exist anyway — the corpus pins the parser by comparing against it, and it is the
RFC 6902 interop surface. Making it readable *in* costs nothing and resolves the
escape-hatch question permanently: an edit the mirror grammar cannot express (a move, a copy)
is written as a transform list directly. **This is not a second notation the standard
optimizes for humans.** It is machine-first, it is where a tool emits and a tool consumes, and
the `.hew` mirror grammar remains the only authoring surface designed to be read in a pull
request.

### 9.0 Context lines are not decoration — they compile into assertions

This is the load-bearing consequence of the pipeline, and it is why the parser can be
format-agnostic while the format still fails loudly on drift.

> **Every context line and every `-` line in the mirror body compiles into a `test`
> transform in the IR.** Nothing about "loud staleness" lives in the applier. The applier is
> a dumb executor of a transform list; the reason a drifted target fails is that the list it
> was handed *begins with assertions the parser derived from the shape the author wrote*.

A reviewer reading a hunk sees context lines and understands them as "the surroundings". The
machine reads the same lines and understands them as "these must hold". They are the same
lines. That equivalence is the whole design:

```
@@ /server @@
  port: 8080          →  { test,    /server/port,    8080 }
- timeout: 30         →  { test,    /server/timeout, 30 } then { replace, /server/timeout, 60 }
+ timeout: 60
```

Two practical consequences the corpus pins:

1. **Writing more context makes a patch stricter, not merely more readable.** `-U3` in
   unified diff buys reviewability; context radius in Hew buys reviewability *and* drift
   detection, from the same keystrokes.
2. **An applier that ignores `test` transforms is not merely lax — it is non-conformant**,
   and the corpus's `apply-error` cases catch it, because those cases fail at the IR level
   before any byte is touched.

### 9.1 Lowering algorithm (parser → transform list)

Purely textual. No target is read. For each hunk, in file order:

1. Take the anchor path verbatim, attaching any `! match ord=` as a **selector** on the last
   segment. Emit no transform for this step.
2. For each context and `-` line, in body order, emit a `test` transform at that node's Hew
   path with its before-image value. For a subset-matched object context line, emit one
   `test` per listed field, not one for the object.
3. For each `? ` assertion, emit the corresponding transform: `expect` → `test`; `absent` →
   `test` with `absent: true`; `exhaustive` → `test` with `exhaustive: true`; `count`/`kind` →
   `test` with the corresponding qualifier. (These qualifiers are Hew extensions to 6902's
   `test`; a strict-6902 consumer will reject them, which is correct — it cannot honor them.)
4. For each `-` line whose key does not also appear as a `+` line at the same position, emit
   `remove`.
5. For each `+` line whose key also appears as a `-` line at the same position, emit
   `replace`. Otherwise emit `add`, with the insertion position carried as a **relative
   placement** (`after: <sibling path>` / `before: <sibling path>` / `end`) derived from
   §6.2 — *not* as a numeric index, since the parser has no target to count against.
6. `!` directives emit no transform of their own. They ride the affected transform as
   qualifiers (`anchor: fork`, `surface: table`, `idempotent: true`, `optional: true`).

**The parser never emits `move` or `copy`.** The mirror grammar compiles to four of RFC
6902's six operations. The IR itself carries all six, and the missing two are reachable by
authoring a transform list directly (§9.6) — which is what Appendix C now points at instead
of a future spec revision.

### 9.2 Two forms of the transform list

The IR exists in two forms, and conflating them is the mistake this section exists to prevent.

| Form | Paths | Placement | Produced by | Consumed by |
|---|---|---|---|---|
| **Abstract** | Hew paths, key-match and selectors intact | relative (`after:`/`before:`/`end`) | parser (§9.1), differ (§9.4) | applier, renderer |
| **Resolved** | RFC 6901 pointers, indices concrete | numeric indices | `Lower(ir, doc)` against a specific target | interop consumers, `hew apply --ops`, corpus `expected-ops.json` |

Only the **abstract** form is the pipeline's IR. It is target-independent, which is exactly
what lets the parser never open a file and the renderer never need one.

The **resolved** form is a projection for interop and for corpus assertions: key-match
segments (`/mcpServers/name=github`) become indices (`/mcpServers/1`) against *this* target,
and relative placements become array indices. It is lossy in one direction — anchor/alias
directives, surface directives, ordinal selectors, comment nodes, and Markdown block
structure have no RFC 6902 representation at all and are consumed during resolution.

Therefore: **the resolved op list is a derived artifact, not a serialization of the patch.**
`hew apply --ops` prints it; `hew` cannot read it back, and v0 defines no way to author one.

### 9.3 What the applier is, and is not

An applier receives `(abstract transform list, target bytes)` and returns `(patched bytes)`.
It resolves paths against the document it just parsed, evaluates every `test` transform
before performing any mutation, and either produces a fully patched document or an error with
nothing written (§10.5).

An applier **must not**: see the `.hew` text, know what a margin is, apply a fuzz factor,
reorder transforms, skip a `test` it does not understand, or emit a partial document. An
applier that encounters a transform qualifier it does not implement must fail `HEW020`, never
ignore it — ignoring an `anchor: fork` qualifier silently produces exactly the wrong edit.

### 9.4 Diff generation requirements (P4 — specified now because the IR depends on them)

The differ is not implemented in v0. Its requirements are normative anyway, because a
transform list that a differ cannot produce is an IR that only half exists.

**Inputs are pure content.** The core differ signature takes two byte sources of the same
format. It has **zero git awareness** — no repository, no revision, no subprocess. Descriptor
resolution (`HEAD:config.yaml`) is a CLI-boundary concern (§9.5, Appendix A.7), and keeping it
out of the core is load-bearing: it is what keeps the library embeddable and the Rust port's
dependency list short.

**R1 — Determinism.** The same `(old, new, options)` triple MUST produce a byte-identical
`.hew` file, on every run, in every implementation. This is what makes a diff case pinnable in
the corpus at all. Concretely it requires: a specified sequence-diff algorithm (v0: Myers over
node equality, ties broken toward earlier deletion), key-order-preserving traversal for
mappings, deterministic anchor selection (R3), and deterministic value rendering (R5).

**R2 — Context radius, the `-U3` analog.** The renderer includes unchanged siblings around
each changed run. The unit is **siblings within the anchored container**, not lines.

- **Default: 1.** One unchanged sibling before and one after each changed run.
- `--context=N` sets it; `--context=0` emits margins only; `--context=all` emits every sibling
  of every touched container (which, combined with `? exhaustive`, is the strictest patch Hew
  can express).
- **Identity lines are exempt from the radius and always emitted.** The key field of a touched
  keyed-array element (`- name: github`) is not context — it is the address, rendered as a
  line. Suppressing it at `--context=0` would make the hunk unaddressable.
- Two changed runs whose context windows overlap or abut are coalesced into one hunk, exactly
  as unified diff coalesces hunks.

Because context lines compile into assertions (§9.0), the radius is a **strictness dial, not
a verbosity dial** — a fact the CLI's help text must state, since users will otherwise reach
for `--context=0` to make patches smaller and quietly disable drift detection.

**R3 — Anchor selection.** The anchor of a hunk is the deepest container that contains every
changed node in that hunk. Deterministic, and it produces the shallowest bodies.

**R4 — Address preference.** For sequences, the differ MUST prefer a key-match segment
(`/mcpServers/name=github`) over a positional index when the sequence has a usable identity
field, because a positional address drifts the moment the user reorders the list. A field is
usable when it is present on every element, scalar, and unique across the sequence. Candidate
fields are tried in order: `name`, `id`, `key`, then any single field satisfying the
condition. If more than one field qualifies and none is a candidate, the differ uses indices
and emits a hew comment saying so. See [O18](#decisions-and-residual-open-questions) — a hardcoded
candidate list in a standard is exactly the kind of thing that ages badly.

**R5 — Rendering values.** An added node is rendered from the *new* document's own source
bytes, re-indented to the hunk body, so that a diff-then-apply round trip preserves the
author's formatting rather than re-emitting canonical form.

**R6 — Inexpressible changes.** If the structural diff contains a change Hew v0 cannot express
(Appendix C), the differ fails with `HEW020` naming the change. It MUST NOT silently degrade —
a differ that emits delete-and-add for a detected move without saying so would make Appendix
C.1's honest limitation into a silent data-shape change. (Note the asymmetry with the *apply*
side, which does not detect moves at all: [O16](#decisions-and-residual-open-questions).)

**R7 — The produced list's target is the OLD side** (ratified
[O39](#p5--the-api-ratification-2026-08-14)). A differ performs no I/O and has no names of its
own, so the label is the caller's to supply — but *which* label is not the caller's to choose:
the patch applies to `old`, so `TransformList.Target` is old's label and the rendered `--- `
line names old. Appendix B.2.1 states the two corollaries a CLI needs (a git anchor renders as
its path component; a stdin side has no name). §13.3's `diff` seam is run with `old.<ext>` as
the label for the same reason, which is also what makes a `diff` case's `expected.hew` directly
usable as the `e2e` input of the same case — the round-trip identity RT1 asserts.

**R8 — Two identical inputs produce a preamble-only patch, not an empty one** (ratified
[O38](#p5--the-api-ratification-2026-08-14)). The differ returns a `TransformList` with a
target and zero transforms; the renderer writes the preamble and the `--- ` line and stops.
§10.2 is amended so that apply accepts it as a no-op, which is what makes `diff` and `apply`
composable when the answer is "no change". See Appendix B.2.2.

### 9.5 Source descriptors (CLI layer only)

The CLI resolves a **source descriptor** to bytes before calling the core differ:

| Descriptor | Meaning |
|---|---|
| `path/to/file` | Working-tree file. |
| `-` | Standard input. Legal at most once per invocation. |
| `REV:path` | A git anchor, following git's own `<tree-ish>:<path>` convention: `HEAD:config.yaml`, `main:config.yaml`, `abc1234:sub/dir/config.yaml`. |

Canonical invocation: `hew diff HEAD:config.yaml config.yaml` — "what have I changed since the
last commit, as a structured patch".

**Git anchors are resolved by invoking git plumbing as a subprocess** (`git cat-file blob
<rev>:<path>`, or `git show <rev>:<path>`), never by linking a git library. Rationale: the
resolver is a hundred lines in any language, the subprocess boundary is the same in Go and
Rust, and linking libgit2 or go-git into a patch tool to read one blob is a dependency the
core spent §9.4-R0 avoiding. If `git` is not on `PATH`, a descriptor containing `:` is a
usage error (`exit 2`), never a silent fallback to treating it as a filename.

A literal path containing `:` is disambiguated with `./` (`./weird:name.yaml`), which is
git's own rule.

### 9.6 Canonical serialization of the transform list — `.hewt`

Normative. The IR's serialized form is a **YAML document** (and therefore also readable as
JSON, since JSON is a YAML subset — one reader serves both). Extension `.hewt`, media type
`application/vnd.ctxloom.hew-transforms+yaml`. Name and extension flagged as
[O21](#decisions-and-residual-open-questions).

```yaml
hew-transforms: 1
target: config.yaml
format: yaml
transforms:
  - op: test
    path: /server/timeout
    value: 30
  - op: replace
    path: /server/timeout
    value: 60
  - op: test
    path: /mcpServers/name=github/command
    value: npx
  - op: add
    path: /mcpServers
    after: /mcpServers/name=github
    value:
      name: ctxloom
      command: ctxloom
      args: [mcp]
```

**Document keys**

| Key | Required | Meaning |
|---|---|---|
| `hew-transforms` | yes | Version integer. `1` here. Must be the first key. |
| `target` | yes | Target path, same semantics as a `--- ` line (§2.2). |
| `format` | no | Same resolution order as §2.2. |
| `transforms` | yes | Ordered sequence. **May be empty**: a document with a target and no transforms is the IR of a no-op patch, and it applies as one (§10.2 as amended by [O38](#p5--the-api-ratification-2026-08-14)). The key itself is still required — omitting it is `HEW001`, because "no transforms" must be *said*, not inferred from a missing key. |

A multi-target transform list is a multi-document YAML stream (`---`-separated), one document
per target, applied in order under §10.5's per-file atomicity.

**Transform record — the reduced core**

The IR is **ASM**: few primitives, stable, composable. Richness lives in the notation and in
the compiler (the parser), never in the IR. The derivation that produced this set is §11.10.

**Five operations.** RFC 6902's six, minus `move`, which is a lossless composition
(`copy` + `remove`) and therefore desugars.

| `op` | Meaning | Why it cannot be composed away |
|---|---|---|
| `test` | Assert. | The only assertion primitive; every context line compiles here (§9.0). |
| `add` | Create a node. | The only creation primitive. |
| `remove` | Delete a node. | The only deletion primitive. |
| `replace` | Swap a node's value **in place**. | `remove` + `add` provably loses the node's attached comments (§8.2) and re-derives its position. Not the same operation. |
| `copy` | Create a node from the value at another **path in the target**. | The only primitive that takes its value *by reference*. An `add` would have to restate the value, which requires reading the target and would break the IR's target-independence. |

**Fields.**

| Field | Applies to | Meaning |
|---|---|---|
| `op` | all | One of the five above. |
| `path` | all | Hew path (§4), abstract form. **All addressing richness lives here** — key-match, `=value`, labels, headings, blocks, comments, markers, `[n]` ordinal selectors. |
| `from` | `copy` | Source Hew path. |
| `value` | `add`, `replace`, `test` | The value, in YAML. |
| `before` / `after` | `add`, `copy` | Placement (§6.2). Mutually exclusive; absence means append at end. |
| `on_conflict` | `add` | `fail` (default, OP-02) \| `keep` (OP-04) \| `replace` (OP-03). |
| `absent` | `test` | Assert non-existence. |
| `count` | `test` | Assert child count. |
| `kind` | `test` | Assert node kind. |
| `anchor` | any | `rewrite` \| `fork` — YAML alias policy (§8.3). |
| `surface` | `add` | `table` \| `dotted` — TOML placement (§8.4). |
| `optional` | `remove`, `test` | Tolerate absence (§7.6). |
| `idempotent` | `add`, `remove`, `replace` | Tolerate an already-applied state (§7.5). |
| `line` | any | Provenance into the `.hew` file. **Emitted, ignored on input.** |

Exactly one of `value` / `absent` / `count` / `kind` on a `test`. `on_conflict` is the only
conditional on `add`; `optional` and `idempotent` are the two tolerance flags, and there are
no others.

**What is NOT in the record, and where it went.** These were in earlier drafts and were
removed by the reduction; each is now produced by the parser as a composition:

| Removed | Desugars to |
|---|---|
| `exhaustive` | `test`+`count` on the container, plus one `test`+`value` per listed child (OP-26). |
| `comment: {leading, trailing}` | A separate `add` of a comment node at a comment address (§4.5b), placed with `before`/`after` (OP-31). |
| `ord` | A `[n]` selector on the path's last segment — addressing, not operation (OP-37). |
| `labels` | Nothing. The labels are already the path's label segments; the parser checks the redundant `label=[…]` spelling and drops it. |
| `move` | `copy` + `remove` (OP-21). |

An unknown `op` or an unknown field is `HEW001`. Fields whose combination is meaningless
(`value` with `absent`, `from` on `add`) are `HEW001`.

**This table is not an independent design.** It is the union of the **IR record** rows of the
operations catalog (§11), rendered as data — the ordering ruling for this workstream is that
the surveyed catalog fixes the vocabulary and the IR schema falls out of it, never the
reverse. Concretely: `on_conflict` exists because the sweep found ytt's `missing_ok`,
jsonnet's absent-field merge and ctxloom's M5 install all needing add-semantics variants
(OP-02/03/04); `comment` exists because OP-30/OP-31 found a capability *no surveyed patch
format has* and ctxloom's own managed writers require. **A future catalog entry that needs a
new qualifier extends this schema by construction**, and a qualifier with no catalog entry is
a spec bug.

**Move and copy.** These are the operations the mirror grammar cannot express, and this is
where they live. A move is written as its two core records — which is also exactly what the
`.hew` mirror grammar could never have expressed as one gesture:

```yaml
hew-transforms: 1
target: config.yaml
format: yaml
transforms:
  - op: test
    path: /server/host
    value: localhost
  - op: copy
    from: /server/host
    path: /network/host
  - op: remove
    path: /server/host
```

An applier MUST implement all five core operations, and `copy` MUST preserve the source
subtree's bytes and attached comments where its format's editor library allows. That
requirement is what makes the copy-then-remove pair a *move* rather than a transcription, and
it is why reaching for the IR form beats writing delete-and-add in the mirror grammar.

A reader who prefers the word may write `op: move` with `from:`/`path:`: it is accepted on
input as sugar and is normalized to the two core records on the way in. It never appears in an
emitted transform list, and the corpus pins that.

**What the serialized IR is not.** It is not a review artifact. A pull request containing a
`.hewt` file where a `.hew` file would do is a review-quality regression, and a project may
lint for it. It is not a *lossless* record of a `.hew` file either: Hew comments, hunk
boundaries, and the author's chosen anchors are notation-side and do not survive the round
trip (§13.5 pins `render → parse == identity on the IR`, not `parse → render == identity on
the text`).

### 9.7 The application record

**Ruled (human, 2026-08-14): v0 specifies the record; `hew revert` and ledger integration are
future work built on it.** The record is the durable answer to "what did this tool do to my
file", and it is the artifact that makes withdrawal possible later without designing
withdrawal now.

`hew apply --record <path>` writes one after a successful apply. It is `.hewt`-shaped — same
YAML document model, same transform vocabulary — because the thing worth recording *is* a
transform list, and inventing a second schema for it would be exactly the duplication §11.10
spent the reduction avoiding.

```yaml
hew-record: 1
applied_at: 2026-08-14T09:31:07Z
patch:
  source: migrate-0.8.hew
  digest: sha256:9f2c…                 # of the patch bytes, as read
targets:
  - target: .mcp.json
    format: json
    before: sha256:4ab1…               # target bytes as read
    after:  sha256:7d30…               # target bytes as written
    committed: true
    transforms:                        # the RESOLVED list, exactly as executed
      - op: test
        path: /mcpServers/ctxloom/command
        value: ctxloom
      - op: add
        path: /mcpServers/taskloom
        value: {command: taskloom, args: [mcp]}
  - target: .claude/settings.json
    format: jsonc
    before: sha256:11cc…
    after:  sha256:2e88…
    committed: true
```

**Fields**

| Key | Meaning |
|---|---|
| `hew-record` | Version integer. `1` here. Must be first. |
| `applied_at` | RFC 3339 UTC. **Pinnable** — see below. |
| `patch.source` | The patch's path or `-`. Informational. |
| `patch.digest` | `sha256:` of the patch bytes **as read**, so a record can be tied to the exact patch that produced it. |
| `targets[].before` / `.after` | `sha256:` of the target bytes as read and as written. **These are what make the record verifiable**: a later tool can tell whether the file still holds what hew left, or whether a human has edited it since. |
| `targets[].committed` | `true` once the rename succeeded. Present and `false` only in the crash-prefix case (§10.5) if a record is written incrementally. |
| `targets[].transforms` | The **resolved** transform list (§9.2) actually executed — indices concrete, key-matches resolved. Not the abstract form: the record states what happened to *this* file, not what the patch said in general. |

**`applied_at` is pinnable, and a pinned record is reproducible** (ratified
[O37](#p5--the-api-ratification-2026-08-14)). Every other field of a record is a function of
its inputs: the digests are of bytes, the transforms are of the patch and the target. The
timestamp was the one field that made an otherwise deterministic artifact differ on every run,
which is a problem for exactly the callers most likely to keep records — a build that commits
them gets a diff for free, and a test that pins one cannot.

hew therefore honours the reproducible-build convention rather than inventing one, in this
precedence:

1. The library caller's explicit value (`WriteOptions.AppliedAt`, Appendix A.8).
2. `HEW_APPLIED_AT` — an RFC 3339 timestamp.
3. `SOURCE_DATE_EPOCH` — seconds since the Unix epoch, the cross-ecosystem spelling that
   reproducible-builds tooling already sets.
4. The system clock.

The chosen instant is normalized to RFC 3339 **UTC** and written byte-exactly, so two applies
of the same patch against the same target with the same pin produce byte-identical records. A
malformed `HEW_APPLIED_AT` or `SOURCE_DATE_EPOCH` is a usage error (exit 2), never a silent
fallback to the clock: a pin that quietly does not pin is worse than no pin, because the
artifact still *looks* reproducible.

**What the record is for, and what it is not.**

- It **is** an audit trail: what changed, in what order, to what bytes, from which patch.
- It **is** the input a future `hew revert` would invert. Recording the resolved list and both
  digests is precisely what an inverter needs; recording the abstract list would not be.
- It **is** the shape of an ownership record. ctxloom's `config-write` (review B2,
  `distinct-bullpen`) cannot withdraw what it added because nothing recorded that ctxloom
  added it. A record whose `transforms` are `add`s against a foreign file *is* that missing
  statement of ownership.
- It is **not** a lock, a ledger, or a claim of exclusivity. It records; it does not reserve.
- It is **not** written by default. No `--record`, no record — hew does not litter.

**Future work, named and deliberately not designed here:**

- `hew revert <record.hewt>` — invert the resolved transforms, guarded by the `after` digests
  so a file edited since is refused rather than clobbered. The inversion rules (what is the
  inverse of an `add` with `on_conflict: keep`?) are a design task, not an implementation
  detail, and they are not in v0. **The reversal patch (`hew apply --reversal`,
  [O40](#p5--the-api-ratification-2026-08-14)) is the half of this that v0 does ship**, and it
  is deliberately the half that needs no inversion rules at all: it diffs the after-image back
  to the before-image, both of which existed, rather than reasoning backwards from an op. It
  does not subsume `hew revert` — a reversal patch must be kept, while a record can be found
  later — but it means "undo what hew just did" has an answer today.
- **Ledger integration.** ctxloom's `ledger.Ledger` records *names* it owns per surface; a hew
  record records *transforms* it applied per target. The former is a set to reconcile, the
  latter a history to invert. Whether one subsumes the other is a P5 question, and answering
  it early would bind the standard to one host project's ownership model — the thing the
  spec-first structure exists to avoid.


---

## 10. Error taxonomy

Every failure is one of these codes. Every error message carries: the code, the Hew path in the
target, the patch file line number, and — where applicable — the expected and actual values.

| Code | Name | Raised when |
|---|---|---|
| `HEW001` | `parse-error` | The `.hew` file is malformed: bad margin, bad path, unknown directive, mixed margins in a Markdown block, `! match` on an unambiguous path, an unattached line-scoped annotation. |
| `HEW002` | `target-parse-error` | The target file will not parse in its declared format, or an unclosed managed marker, or an unknown `hew:` version. **Nothing is written.** (`agent.RefuseCorrupt`'s stance, as a spec rule.) |
| `HEW003` | `target-path-error` | The target path escapes the apply root, or is not a regular file. |
| `HEW010` | `stale-target` | A context or `-` line's node is absent or unequal. The characteristic drift error. |
| `HEW011` | `assertion-failed` | A `? expect` / `? absent` / `? exhaustive` / `? count` / `? kind`, or a `! match label=` cross-check, did not hold. |
| `HEW012` | `ambiguous-match` | A key-match, HCL label tuple, or Markdown heading selected more than one node and no ordinal annotation resolved it. |
| `HEW013` | `no-match` | A required node does not exist: a `-` line's node, an anchor without `?`, an `ord=` beyond the sibling count, a merge-key-inherited key. |
| `HEW014` | `already-exists` | A `+` line's node already exists and the hunk is not `! idempotent`. |
| `HEW020` | `inexpressible` | The requested edit cannot be represented in Hew v0 or in the target format — a node move or copy (Appendix C), a comment node in JSON, a sub-block Markdown edit, a TOML surface migration. The message names Appendix C's condition for a spec revision. |
| `HEW021` | `unsupported-format` | No binding for the declared or inferred format. |
| `HEW030` | `conflict` | Two hunks in one apply touch overlapping nodes with incompatible ops. |
| `HEW040` | `anchor-ambiguity` | A YAML path resolves through or at an alias with no `! anchor` directive. |
| `HEW041` | `surface-ambiguity` | A TOML path is defined at two surfaces. |

### 10.1 What is deliberately *not* an error

- Extra unlisted siblings (§6.1) — that is what `? exhaustive` is for.
- A hunk that produces zero ops because the after-image equals the before-image. This is a
  legal no-op hunk (an assert-only hunk, §7.4); the file is unchanged and the exit code is 0.

### 10.2 What is deliberately an error even though other tools tolerate it

- Line-offset drift. There is no fuzz.
- An already-applied patch (`HEW010`), absent `! idempotent`.
- **An empty patch file: `HEW001`.** (`cli.runConfigWrite`'s refuse-empty-patch rule, promoted
  to the format.) See the amendment below for exactly where this line falls.
- A hunk that matched nothing. SmPL's silent context-mode no-op is the single behavior this
  format was designed to not have.

**Amended by [O38](#p5--the-api-ratification-2026-08-14): a file section with no hunks is a
no-op, not an error.** The bullet above originally read "an empty patch file, *or a file
section with no hunks*". The second clause is withdrawn, and the line now falls here:

| Patch text | Result |
|---|---|
| Zero bytes, or whitespace only | `HEW001` — no `hew: 1`, so not a hew file at all |
| Anything without a leading `hew: 1` (§2.1) | `HEW001` — same reason |
| `hew: 1` and **no file section** | `HEW001` — a patch that names no target says nothing about any file |
| `hew: 1` + a `--- ` target line + **no hunks** | **applies as a no-op. Exit 0, target byte-unchanged, no write.** |

The distinction is *did the author say which file this is about*. A patch with a file section
and no hunks is a complete, well-formed statement — "here is a target, and there is nothing to
change about it" — and it is precisely the artifact `hew diff` produces for two identical
inputs (Appendix B.2.2). A patch with no file section is an unfinished one, and refusing it
keeps the rule this bullet was written for: a generating tool that emitted nothing must not be
able to pass that off as a successful apply.

Note what does *not* change. A no-op patch writes no file (there is nothing to write), takes
no lock the applier would not otherwise take, and produces an application record with an empty
`transforms` list if `--record` is given — a record that truthfully says nothing happened.
`before` and `after` are equal digests, which is exactly what a verifier should see.

### 10.3 Message shape

```
hew: config.yaml:/server/timeout: HEW010 stale-target
  patch.hew:9: expected 30
  config.yaml:6:  found 45
```

Human-readable diagnostics go to **stderr**; `--format-out json` emits one JSON object per
error on stdout. (ctxloom's diagnostic channel split, applied to the CLI.)

**A `HEW013` from a key-match that hit nothing MUST name its nearest miss, with the miss's
type** ([O46](#p5--the-api-ratification-2026-08-14)). Because §4.2 compares after decoding, a
match can fail for a reason invisible in the address — the element is there and its field
differs only in scalar *type* — and "no element matched" sends the author looking for a
missing element that is in front of them:

```
hew: package.json:/deps/version=1.0: HEW013 no-match
  patch.hew:4: no element of /deps has version=1.0 (number)
  package.json:9: 1 element has version="1.0" (string) — quote the value to match a string
```

### 10.4 First error wins

Application stops at the first error. There is no "collect all failures" mode in v0
([O11](#decisions-and-residual-open-questions)).

### 10.6 Already-applied: which code, and why it is not `HEW010`

A hunk fails in one of two distinguishable ways, and hew names them differently because the
remedies differ:

| Situation | Code | What the author should do |
|---|---|---|
| **The after-image holds in full** — the patch has already been applied | `HEW014 already-exists` when the hunk's failing line is a `+` whose node now exists; `HEW011 assertion-failed` when a `-` or context assert fails while the after-image otherwise holds | Nothing, or add `! idempotent` / the `idempotent:` pragma if re-running is expected |
| **Neither image holds** — the target drifted somewhere else | `HEW010 stale-target` | Re-author the patch against the current file |

The distinction is load-bearing. "Already applied" and "drifted" look identical to a tool that
only checks whether the before-image matched, and conflating them is how a convergent tool
talks itself into re-applying against a file it does not understand. An implementation MUST
evaluate the after-image before choosing the code.

### 10.5 Atomicity

Per target file, all-or-nothing. A file is written only if every hunk against it matched and
applied. There is no `.rej` file, no partial output, no backup file — the write goes through
an atomic temp-and-rename, and a failed apply leaves the target byte-identical. (This is
`iox.WriteFileAtomicFs`'s contract and the deliberate no-backup ruling, restated as a format
property so the Rust port inherits it.)

**Ruled (human, 2026-08-14): across multiple targets, all-or-nothing too.** A multi-target
apply is two phases:

1. **Stage.** Every file section is parsed, matched and applied *in memory*, producing the
   complete result bytes for every target. Any failure anywhere aborts here, and **not one
   byte has been written.**
2. **Commit.** Only once every section has staged successfully are the results written, each
   through the atomic temp-and-rename of phase 1.

This is what exit 1 already promised — "nothing happened, and here is why" (Appendix B.3). A
patch touching `.mcp.json` and `settings.json` cannot half-apply, which matters precisely
because keeping several engines' configs consistent is hew's motivating use case: a
half-applied cross-engine patch leaves the engines disagreeing, which is worse than leaving
them all stale.

**The honest residual: a crash during the commit phase can leave a prefix.** Once staging has
succeeded, hew renames the staged results one at a time. A power loss or `SIGKILL` between the
first and last rename leaves the earlier targets written and the later ones not. hew does not
journal, does not write a two-phase-commit log, and does not attempt recovery — those are
filesystem-transaction machinery, and a patch tool that ships half of one is more dangerous
than a patch tool that ships none.

What hew does instead: the commit window is as small as it can be (pure renames, no parsing,
no matching, no I/O beyond the rename itself), and the **application record** (§9.7) names
every target that was committed, so the state after a crash is *discoverable* rather than
merely unknown. A caller that needs true multi-file transactionality needs a filesystem or a
VCS that provides it; hew's contract is that every failure it can detect leaves everything
untouched, and that the one failure it cannot detect is recorded rather than hidden.

---

## 11. Operations catalog

**This section is derived by survey, not invented.** Its input is the operation vocabulary of
every system in `patch-notation-survey.md` — RFC 6902, RFC 7386, Kubernetes strategic merge,
ytt overlays, go-patch/yaml-patch, jd, kustomize, RFC 5261 XML Patch, spruce, CUE, jsonnet,
Coccinelle — plus the eight mechanisms ctxloom actually runs today (M1–M8 in
`config-patching-review.md`), plus the format-specific necessities each of the six implement
formats imposes.

**The completeness contract:** for any operation any of those systems can perform, a reader
can find it here and determine whether Hew v0 does it, how it is written, or why it does not.
Deferral is a legitimate answer. Omission is a spec bug.

### 11.0 How to read this catalog

**Read this section before §9.6.** The drafting order is normative for the workstream: the
survey fixes the operation vocabulary here, the catalog's **IR record** rows collectively
*define* the transform-list schema, and §9.6's canonical serialization is this catalog
rendered as data. The IR was not designed and then documented; it was derived. A future
addition to this catalog is an IR extension by construction.

The IR has **six operations** (`add`, `remove`, `replace`, `move`, `copy`, `test`) — RFC
6902's set, unchanged, because the survey found it to be the emergent cross-tool consensus.
Everything below is a *user-level* operation, expressed as one of those six plus path features
(§4) and qualifiers (§9.6). There is no seventh op and v0 adds none: the sweep produced 52
distinct user-level operations and every one of them landed on the six, which is itself the
strongest available evidence that 6902's vocabulary is the right skeleton.

Each entry states:

- **Status** — `v0` (normative and corpus-covered), `deferred` (named, not in v0, with the
  condition for adding it), `rejected` (deliberately never, with the reason).
- **Disp** — the disposition against the reduced core (§11.10):
  - `CORE` — reaches the IR as one core op plus addressing and core qualifiers.
  - `SUGAR` — a notation spelling that the **parser desugars** into a composition of core
    records. **The IR never carries it.** Richness in the compiler, not the ASM.
  - `OUT` — not in the normative set: rejected or deferred, with the reason stated.
- **Sources** — where in the survey the operation was found.
- **Absent/empty behavior** — the silent-no-op discipline, per operation: what happens when
  the target node is missing, or the container is empty. Every `v0` entry answers this.
- **Mirror** — the `.hew` rendering, or `IR-only` when the mirror grammar cannot express it.
- **IR** — the transform record.
- **Formats** — applicability across the six implement formats.
- **Errors** — the named failures.
- **Corpus** — the case(s) that pin it.

Format column key: `✓` supported · `—` not applicable to this format's data model ·
`✗` explicitly unsupported (raises `HEW020`).

### 11.1 Source sweep — where every surveyed verb landed

| Surveyed system | Its vocabulary | Landed as |
|---|---|---|
| **RFC 6902** | `add`, `remove`, `replace`, `move`, `copy`, `test` | The IR's six ops verbatim. OP-01–OP-06, OP-21, OP-22, OP-24. |
| **RFC 6901** | `-` append token, `~0`/`~1` escapes | §4.1, OP-11. |
| **RFC 7386 merge patch** | implicit set; `null` = delete; whole-array replace | OP-01, OP-05, OP-08. `null`-as-delete **rejected** (OP-10). |
| **K8s strategic merge** | `$patch: delete`, `$patch: replace`, `$patch: merge`, merge-key list semantics, `$setElementOrder`, `$deleteFromPrimitiveList` | OP-05, OP-08, OP-09 (rejected), OP-16, OP-19 (deferred), OP-15. |
| **ytt overlay** | `@overlay/match` (`by=`, `expects=`, `missing_ok=`), `@overlay/remove`, `@overlay/replace`, `@overlay/insert before=/after=`, `@overlay/append`, `@overlay/assert`, `@overlay/replace via=λ` | §4.2 + §7.2 (`by=`/`ord=`), OP-05, OP-01, OP-13, OP-11, OP-24–OP-28, OP-27 (`expects=`), OP-04 (`missing_ok=`), OP-29 (rejected: `via=λ`). |
| **go-patch / yaml-patch** | `type: replace/remove`, `path` with `name=value`, trailing `?` | §4.2, §4.4, OP-01, OP-05, OP-16. |
| **jd** | path-scoped hunks, `-`/`+` values, set/multiset modes | The margin grammar itself; OP-20 (rejected: set semantics). |
| **kustomize** | target selector; `patchesStrategicMerge`; `patchesJson6902`; `replacements` (field→field) | §2.2 `--- ` target line; OP-23. |
| **RFC 5261 XML Patch** | `<add>` with `pos="before"\|"after"\|"prepend"`, `type="@attr"`, `<replace>`, `<remove ws=>` | OP-12, OP-13; attribute addressing sketched in §12.3. |
| **spruce** | `(( delete ))`, `(( append ))`, `(( prepend ))`, `(( insert after/before ))`, `(( merge on <key> ))` | OP-05, OP-11, OP-12, OP-13, OP-16. |
| **CUE** | unification; **no delete** | Not an op vocabulary. Its missing delete is why Hew is not a unification language. |
| **jsonnet** | `+:` deep merge, `::` hide-from-output | OP-09 (rejected), OP-05. |
| **Coccinelle SmPL** | `-`/`+` margins in-shape, metavariables | §3 margins; metavariables not adopted (no pattern variables in v0 — OP-29). |
| **ctxloom M1 ledger** | record-owned-names, withdraw-then-re-add | OP-51 (out of scope: ownership, [O14](#decisions-and-residual-open-questions)). |
| **ctxloom M2 managed section** | splice block, strip section, remove file when empty, never create absent file | OP-45, OP-49 (deferred), OP-48 (deferred). |
| **ctxloom M3 structural merge** | remove-owned-by-name then add current set | OP-16, OP-17. |
| **ctxloom M4 package files** | render-then-swap, delete stale tracked files, empty-render guard | File-level; OP-52, OP-49. |
| **ctxloom M5 byte-level MCP** | install/uninstall one server, installed? | OP-16, OP-17, OP-24. |
| **ctxloom M6 `config-write`** | deep merge, arrays replace wholesale, refuse empty patch | OP-09 (rejected), OP-08, §10.2. |
| **ctxloom M7 `config.yaml`** | node-tree patch preserving unchanged sections, drop removed keys, append new keys sorted | OP-01, OP-05, OP-02 — and it is the closest existing thing to a hew applier. |
| **ctxloom M8 gitignore** | append-only idempotent block, content-match idempotency | OP-50 (deferred: needs a line-oriented text binding). |

---

### 11.2 Mapping and key operations

#### OP-01 `set-scalar` — replace the value of an existing key
**Status** v0 · **Disp** `CORE` — `replace` · **Sources** 6902 `replace`, 7386, ytt default, go-patch, jd, M3/M6/M7
**Absent/empty** Key absent → `HEW013`. Never creates. Value equal to the target already → the
`-` line still matches, the op is a no-op replace, exit 0.
**Mirror**
```
@@ /server @@
- timeout: 30
+ timeout: 60
```
**IR** `{op: test, path: /server/timeout, value: 30}` then `{op: replace, path: /server/timeout, value: 60}`
**Formats** json ✓ · jsonc ✓ · yaml ✓ · toml ✓ · hcl ✓ · markdown — (no keys)
**Errors** `HEW010` `HEW013` · **Corpus** `json/set-scalar`, `yaml/set-scalar`, `toml/set-scalar-dotted`, `hcl/set-attribute`

#### OP-02 `add-key` — create a key that must not exist
**Status** v0 · **Disp** `CORE` — `add` · **Sources** 6902 `add`, ytt `@overlay/match missing_ok=True` + insert
**Absent/empty** Key already present → `HEW014 already-exists`. This is the strict default:
adding over an existing key is a drift signal, not a convenience.
**Mirror**
```
@@ /server @@
  port: 8080
+ tls: true
```
**IR** `{op: add, path: /server/tls, value: true, after: /server/port}`
**Formats** json ✓ · jsonc ✓ · yaml ✓ · toml ✓ · hcl ✓ · markdown —
**Errors** `HEW014` · **Corpus** `json/add-key`, `hcl/add-attribute`

#### OP-03 `upsert-key` — add, or replace whatever is there
**Status** v0 · **Disp** `CORE` — `add` + `on_conflict: replace` · **Sources** go-patch trailing `?`, ytt `missing_ok`, M5 install
**Absent/empty** Absent → created. Present → replaced regardless of current value. **This
operation deliberately does not assert the prior state**, so it is the one mapping write that
cannot detect drift; use it only where ctxloom owns the key outright (M5's exact case).
**Mirror** — `! upsert` (§7.7), line-scoped:
```
@@ /mcp_servers @@
! upsert
+ taskloom = { command = "taskloom" }
```
**IR** `{op: add, path: /mcp_servers/taskloom, on_conflict: replace, value: {...}}`
**Formats** json ✓ · jsonc ✓ · yaml ✓ · toml ✓ · hcl ✓ · markdown —
**Errors** — (this op has no failure of its own) · **Corpus** `toml/upsert-key`

#### OP-04 `default-key` — add only if absent, leave an existing value alone
**Status** v0 · **Disp** `CORE` — `add` + `on_conflict: keep` · **Sources** ytt `missing_ok=True` without replace; jsonnet `+:` for absent fields
**Absent/empty** Absent → created. Present → **untouched**, zero ops, exit 0. The
"defaulting" operation: seeding a config key without stomping a user's choice.
**Mirror** — `! default`, line-scoped:
```
@@ /server @@
! default
+ timeout: 30
```
**IR** `{op: add, path: /server/timeout, on_conflict: keep, value: 30}`
**Formats** json ✓ · jsonc ✓ · yaml ✓ · toml ✓ · hcl ✓ · markdown —
**Errors** — · **Corpus** `yaml/default-key-present`, `yaml/default-key-absent`

#### OP-05 `remove-key`
**Status** v0 · **Disp** `CORE` — `remove` · **Sources** 6902 `remove`, 7386 `null`, SMP `$patch: delete`, ytt
`@overlay/remove`, spruce `(( delete ))`, jsonnet `::`, M3/M7
**Absent/empty** Absent → `HEW013`. Present but unequal to the `-` line's value → `HEW010`.
Removing the last child leaves an empty container; it does **not** cascade-delete the parent
(see OP-49 for the file-level analogue ctxloom's M2 performs).
**Mirror**
```
@@ /server @@
- host: localhost
```
**IR** `{op: test, path: /server/host, value: localhost}` then `{op: remove, path: /server/host}`
**Formats** json ✓ · jsonc ✓ (with its leading comment, §8.2) · yaml ✓ · toml ✓ · hcl ✓ · markdown —
**Errors** `HEW010` `HEW013` · **Corpus** `json/delete-key`, `jsonc/delete-key-with-comment`

#### OP-06 `remove-key-if-present`
**Status** v0, **discouraged** · **Disp** `CORE` — `remove` + `optional` · **Sources** ytt `expects="0+"`, patch(1) tolerance
**Absent/empty** Absent → no-op, exit 0. **This is the only construct in Hew that can silently
do nothing**, which is why §7.6 requires a justifying comment and a linter warning.
**Mirror**
```
@@ /server @@
# legacy key, may already be gone on fresh installs
! optional
- deprecated_flag: true
```
**IR** `{op: remove, path: /server/deprecated_flag, optional: true}`
**Formats** all six as OP-05 · **Errors** — · **Corpus** `yaml/remove-optional-absent`

#### OP-07 `rename-key`
**Status** v0, **IR-only** · **Disp** `SUGAR` — → `copy` + `remove` · **Sources** 6902 `move` within a container
**Absent/empty** Source absent → `HEW013`. Destination present → `HEW014`.
**Mirror** IR-only. The mirror form (`- old: v` / `+ new: v`) is a delete-and-add and loses
the node's comments and source bytes — see [O16](#decisions-and-residual-open-questions).
**IR** `{op: copy, from: /server/timeout, path: /server/timeout_seconds}` then `{op: remove, path: /server/timeout}`
**Formats** json ✓ · jsonc ✓ · yaml ✓ · toml ✓ · hcl ✓ · markdown —
**Errors** `HEW013` `HEW014` · **Corpus** `yaml/ir-rename-key`

#### OP-08 `replace-container-wholesale`
**Status** v0 · **Disp** `CORE` — `replace` at the container path · **Sources** 7386 (its only array mode), SMP `$patch: replace`, M6 (arrays
replace wholesale)
**Absent/empty** Container absent → `HEW013`. Replacing with an empty container is legal and
**not** treated as a no-op — an explicit empty is a real value, and Hew will not second-guess
it the way a merge tool would.
**Mirror** anchor the hunk at the container's parent and mark the whole value:
```
@@ /server @@
- tags: [alpha, beta]
+ tags: []
```
**IR** `{op: replace, path: /server/tags, value: []}`
**Formats** json ✓ · jsonc ✓ · yaml ✓ · toml ✓ · hcl ✓ · markdown ✓ (a section's body)
**Errors** `HEW010` `HEW013` · **Corpus** `json/replace-array-wholesale`

#### OP-09 `deep-merge-container`
**Status** **rejected** · **Disp** `OUT` · **Sources** 7386, SMP `$patch: merge`, jsonnet `+:`, spruce, M6
**Why not.** A deep merge is exactly the operation whose result you cannot read off the patch
— the survey's central finding, and ctxloom's own `config-write` (M6) is the local proof: its
deep merge has no ownership record, replaces arrays wholesale without saying so, and cannot be
withdrawn. Hew's position is that a merge is a *set of explicit ops*, and the differ (§9.4) is
how you get that set without typing it. Reopening this would reintroduce the "reads as an
overlay, applies as something else" gap that motivated the format.
**Migration for M6** — `config-write`'s patch object becomes a generated transform list.

#### OP-10 `null-as-delete`
**Status** **rejected** · **Disp** `OUT` · **Sources** RFC 7386, jsonnet `::`
**Why not.** `null` is a legal JSON/YAML *value*. A format in which setting a key to null and
deleting a key are the same keystroke cannot express "set this to null", and every 7386
consumer has this bug. Hew has an explicit `-` margin and an explicit `remove` op; `+ x: null`
sets null.

---

### 11.3 Sequence operations

#### OP-11 `append-element`
**Status** v0 · **Disp** `CORE` — `add`, placement omitted · **Sources** 6901 `-` token, ytt `@overlay/append`, spruce `(( append ))`, jsonnet `+:`
**Absent/empty** Sequence absent → `HEW013` (use OP-03 to create it). Empty sequence → appends
as the only element, legal.
**Mirror** — a `+` line with no following context sibling:
```
@@ /tags @@
  - beta
+ - gamma
```
**IR** `{op: add, path: /tags, after: /tags/=beta, value: gamma}` (or `end` with no context)
**Formats** json ✓ · jsonc ✓ · yaml ✓ · toml ✓ (array + array-of-tables) · hcl ✓ (tuple exprs) · markdown ✓ (list blocks)
**Errors** `HEW013` · **Corpus** `yaml/list-append`, `json/array-append`

#### OP-12 `prepend-element`
**Status** v0 · **Disp** `CORE` — `add` + `before` · **Sources** spruce `(( prepend ))`, RFC 5261 `pos="prepend"`
**Absent/empty** As OP-11.
**Mirror** — a `+` line whose only sibling context follows it:
```
@@ /tags @@
+ - aardvark
  - alpha
```
**IR** `{op: add, path: /tags, before: /tags/=alpha, value: aardvark}`
**Formats** as OP-11 · **Errors** `HEW013` · **Corpus** `yaml/list-prepend`

#### OP-13 `insert-before` / `insert-after`
**Status** v0 · **Disp** `CORE` — `add` + `before`/`after` · **Sources** ytt `@overlay/insert before=/after=`, spruce `(( insert after ))`, RFC 5261 `pos=`
**Absent/empty** The reference sibling must exist and match, or `HEW010`. This is the operation
that makes context lines load-bearing rather than decorative (§9.0).
**Mirror** — position falls out of the surrounding context (§6.2), with no keyword at all:
```
@@ /tags @@
  - alpha
+ - alpha2
  - beta
```
**IR** `{op: add, path: /tags, after: /tags/=alpha, value: alpha2}`
**Formats** as OP-11 · **Errors** `HEW010` `HEW013` · **Corpus** `yaml/list-insert-middle`

#### OP-14 `remove-element-by-index`
**Status** v0 · **Disp** `CORE` — `remove`, index address · **Sources** 6902 (its only array removal), jd
**Absent/empty** Index out of range → `HEW013`. The element's value must match the `-` line.
**Mirror**
```
@@ /tags @@
  - alpha
- - beta
```
**IR** `{op: test, path: /tags/1, value: beta}` then `{op: remove, path: /tags/1}`
**Formats** as OP-11 · **Errors** `HEW010` `HEW013` · **Corpus** `json/array-remove-element`

#### OP-15 `remove-element-by-value` (primitive lists)
**Status** v0 · **Disp** `CORE` — `remove`, `=value` address · **Sources** SMP `$deleteFromPrimitiveList`
**Absent/empty** No element equals the value → `HEW013`. More than one equal element →
`HEW012` (a duplicated scalar in a list is drift; Hew names it rather than picking the first).
**Mirror** identical to OP-14 — the author writes `- - beta` and the *parser* chooses a
value-match address for scalar sequences rather than an index, so the patch survives
reordering.
**IR** `{op: remove, path: /tags/=beta}` — the `=value` form of §4.2 with an empty field name,
meaning "the element that equals this".
**Formats** json ✓ · jsonc ✓ · yaml ✓ · toml ✓ · hcl ✓ · markdown — · **Errors** `HEW012` `HEW013`
**Corpus** `yaml/list-remove-by-value`, `yaml/list-remove-by-value-duplicate` (error)

#### OP-16 `add-or-replace-keyed-element` — the headline operation
**Status** v0 · **Disp** `CORE` — `add` + `on_conflict`, key-match address · **Sources** go-patch `name=value` + `?`, SMP merge-key semantics, spruce
`(( merge on ))`, ytt `overlay.subset()`, **and ctxloom M3/M5** (every engine's MCP-server
registration is exactly this)
**Absent/empty** With `?` on the anchor: absent → inserted at the position the body's context
implies. Without `?`: absent → `HEW013`. Two elements with the same key → `HEW012`.
**Mirror**
```
@@ /mcpServers @@
  - name: github
+ - name: ctxloom
+   command: ctxloom
+   args: [mcp]
```
**IR** `{op: add, path: /mcpServers, after: /mcpServers/name=github, value: {...}}`
**Formats** json ✓ · jsonc ✓ · yaml ✓ · toml ✓ (`[[x]]`) · hcl ✓ (blocks by label) · markdown —
**Errors** `HEW012` `HEW013` `HEW014` · **Corpus** `yaml/keyed-array-add`, `json/keyed-array-add`, `toml/array-of-tables-add`

#### OP-17 `remove-keyed-element`
**Status** v0 · **Disp** `CORE` — `remove`, key-match address · **Sources** SMP `$patch: delete` with merge key, M3 `removeManagedMCP`
**Absent/empty** Absent → `HEW013`; ambiguous → `HEW012`.
**Mirror**
```
@@ /mcpServers @@
- - name: legacy
```
**IR** `{op: remove, path: /mcpServers/name=legacy}`
**Formats** as OP-16 · **Errors** `HEW012` `HEW013` · **Corpus** `json/keyed-array-remove`

#### OP-18 `patch-inside-keyed-element`
**Status** v0 · **Disp** `CORE` — any core op, key-match address · **Sources** go-patch `/instance_groups/name=zookeeper/instances`
**Absent/empty** As OP-16 for the selector; then per the inner operation.
**Mirror** — anchor the hunk at the element:
```
@@ /mcpServers/name=github @@
  command: npx
+ env:
+   GITHUB_TOKEN: ${GITHUB_TOKEN}
```
**IR** `{op: add, path: /mcpServers/name=github/env, value: {...}}`
**Formats** as OP-16 · **Errors** `HEW010` `HEW012` `HEW013` · **Corpus** `yaml/keyed-element-inner-add`

#### OP-19 `reorder-sequence` / `set-element-order`
**Status** **deferred** · **Disp** `OUT` · **Sources** SMP `$setElementOrder`
**Why not in v0.** No mechanism in M1–M8 reorders a list; ctxloom's own ledger sorts on
render (M1) so ordering is derived, not patched. Expressible today as a series of IR `move`
ops, verbosely. **Condition to add:** a named case where the order of a config list is
semantically load-bearing *and* changes independently of its contents.

#### OP-20 `set` / `multiset` sequence semantics
**Status** **rejected** · **Disp** `OUT` · **Sources** jd's `-set`/`-mset` modes
**Why not.** Treating a sequence as unordered changes what "the same document" means, and it
would silently make OP-12/OP-13 meaningless. Hew sequences are ordered. A user who wants set
semantics wants `? exhaustive` plus key-match addressing, which Hew has.

---

### 11.4 Move and copy

#### OP-21 `move-node`
**Status** v0, **IR-only** · **Disp** `SUGAR` — → `copy` + `remove` · **Sources** RFC 6902 `move`
**Absent/empty** Source absent → `HEW013`. Destination present → `HEW014`. Destination inside
the source subtree → `HEW001` (6902's own prohibition).
**Mirror** IR-only. §9.6 is the authoring surface; Appendix C.1 is the rationale.
**IR** `{op: copy, from: /server/host, path: /network/host}` then `{op: remove, path: /server/host}`
**Formats** json ✓ · jsonc ✓ (comments travel) · yaml ✓ · toml ✓ · hcl ✓ · markdown ✓ (a block or section)
**Errors** `HEW001` `HEW013` `HEW014` · **Corpus** `yaml/ir-move-node`, `markdown/ir-move-section`

#### OP-22 `copy-node`
**Status** v0, **IR-only** · **Disp** `CORE` — `copy` · **Sources** RFC 6902 `copy`
**Absent/empty** As OP-21, minus the containment prohibition.
**IR** `{op: copy, from: /defaults, path: /service_c}`
**Formats** as OP-21 · **Errors** `HEW013` `HEW014` · **Corpus** `json/ir-copy-node`

#### OP-23 `copy-value-between-fields`
**Status** v0, **IR-only** · **Disp** `CORE` — `copy` · **Sources** kustomize `replacements`
Same record as OP-22 with a scalar source. Cataloged separately because kustomize treats it
as a distinct feature and a reader coming from kustomize will look for it by that name.
**Corpus** covered by `json/ir-copy-node`.

---

### 11.5 Assertions

#### OP-24 `test-value`
**Status** v0 · **Disp** `CORE` — `test` + `value` · **Sources** 6902 `test`, ytt `@overlay/assert`, CUE's unification conflict
**Absent/empty** Node absent → `HEW011` (distinct from `HEW013`: an assertion that a node holds
a value fails *as an assertion* when it is missing).
**Mirror** a context line, or `? expect`. **Both compile to the same transform** (§9.0).
**IR** `{op: test, path: /version, value: 1.2.0}`
**Formats** all six · **Errors** `HEW011` · **Corpus** `yaml/assert-expect-ok`, `yaml/assert-expect-fail`

#### OP-25 `assert-absent`
**Status** v0 · **Disp** `CORE` — `test` + `absent` · **Sources** ytt `expects="0"`, no 6902 equivalent (a real gap in 6902)
**IR** `{op: test, path: /env/API_KEY, absent: true}` · **Mirror** `? absent /env/API_KEY`
**Formats** all six · **Errors** `HEW011` · **Corpus** `jsonc/assert-absent-fail`

#### OP-26 `assert-exhaustive`
**Status** v0 · **Disp** `SUGAR` — → `test`+`count` and one `test`+`value` per listed child · **Sources** implicit in 7386/SMP whole-array replace; explicit nowhere
**Semantics** The listed children are the container's complete child set (§6.1). **This is the
operation that makes Merge Patch's expensive accident cheap and deliberate.**
**IR** desugars to `{op: test, path: /permissions, count: 2}` plus one `{op: test, …, value: …}` per listed child · **Mirror** `? exhaustive`
**Formats** json ✓ · jsonc ✓ · yaml ✓ · toml ✓ · hcl ✓ · markdown ✓ (a section's block list)
**Errors** `HEW011` · **Corpus** `json/assert-exhaustive-fail`

#### OP-27 `assert-count`
**Status** v0 · **Disp** `CORE` — `test` + `count` · **Sources** ytt `expects="1"`, `expects="0+"`, `expects="1+"`
**IR** `{op: test, path: /mcpServers, count: 3}` · **Mirror** `? count /mcpServers = 3`
**Formats** all six · **Errors** `HEW011` · **Corpus** `yaml/assert-count-fail`

#### OP-28 `assert-kind`
**Status** v0 · **Disp** `CORE` — `test` + `kind` · **Sources** no direct source; derived from `agent.InstallMCPServerJSON`'s
measured refusal of an `mcpServers` key "present but of the wrong type" (and B6, the codex
twin that lacks the guard)
**IR** `{op: test, path: /mcpServers, kind: map}` · **Mirror** `? kind /mcpServers = map`
**Formats** all six · **Errors** `HEW011` · **Corpus** `json/assert-kind-fail`

#### OP-29 `computed` / `pattern` assertions
**Status** **rejected** · **Disp** `OUT` · **Sources** ytt `@overlay/replace via=lambda`, Coccinelle
metavariables, jq/starlark expressions in adjacent tools
**Why not.** A patch format with an expression language is a template engine, and a `.hew` file
that computes its own values is no longer readable as "the document with a diff on it" — the
one property §1 exists to protect. Equality and existence only.

---

### 11.6 Comment operations

Comments are nodes in JSONC, YAML, TOML and HCL, and Hew treats them as such. **Decision (this
spec, resolving the brief's open point): a patch CAN carry a comment for a node it adds.**
Two forms, both v0.

#### OP-30 `add-comment-node` — a standalone comment
**Status** v0 · **Disp** `CORE` — `add` at a comment address · **Sources** none of the surveyed patch formats can do this at all; it is
required by ctxloom's own managed-marker practice (M1/M2 both write explanatory comments)
**Absent/empty** Position follows §6.2 like any other node.
**Mirror**
```
@@ /server @@
+ # ctxloom-managed — regenerate with `ctxloom apply`
  timeout: 60
```
**IR** `{op: add, path: /server/#0, before: /server/timeout, value: {comment: "ctxloom-managed — …"}}`
**Formats** json ✗ (`HEW020`) · jsonc ✓ · yaml ✓ · toml ✓ · hcl ✓ · markdown ✓ (HTML comment block)
**Errors** `HEW020` · **Corpus** `toml/add-comment-line`, `json/comment-inexpressible` (error)

#### OP-31 `attach-comment-to-added-node`
**Status** v0 · **Disp** `SUGAR` — → two `add` records (comment node, then member) · **Sources** §8.2's anchoring rule, applied to added nodes
**Semantics** A `+` comment line immediately preceding a `+` member becomes that member's
**leading** comment and travels with it (moves on OP-21, deletes on OP-05). This is the
mirror form; the IR carries it as a qualifier so an applier need not reconstruct adjacency.
**Mirror**
```
@@ /mcp_servers @@
+ # added by taskloom manage install
+ [mcp_servers.taskloom]
+ command = "taskloom"
```
**IR** desugars to `{op: add, path: /mcp_servers/#0, value: {comment: "added by taskloom manage install"}, before: /mcp_servers/taskloom}` plus `{op: add, path: /mcp_servers/taskloom, value: {...}}`
**Formats** json ✗ · jsonc ✓ · yaml ✓ · toml ✓ · hcl ✓ · markdown —
**Errors** `HEW020` · **Corpus** `jsonc/add-with-leading-comment`

#### OP-32 `remove-comment` · #### OP-33 `replace-comment-text`
**Status** v0 · **Disp** `CORE` — `remove`, comment address · Comments are matched by exact text (§6.1) and removed/replaced like any node.
**Mirror** `- # old note` / `+ # new note`
**IR** `{op: replace, path: <comment path>, value: {comment: "new note"}}`
**Formats** as OP-30 · **Errors** `HEW010` `HEW020` · **Corpus** `yaml/replace-comment`

---

### 11.7 Format-specific operations

#### OP-34 `hcl-block-create` · #### OP-35 `hcl-block-remove`
**Status** v0 · **Disp** `CORE` — `add`, label address · **Sources** HCL's block/attribute duality (§8.5); `hclwrite`'s DOM API
**Absent/empty** Create over an existing identical tuple → `HEW014` unless the intent is a
second sibling, which requires `! match ord=` on neither line (a genuinely new sibling is an
`add` at the body level, and the corpus pins that it does not accidentally target the
existing one).
**Mirror** a `+`/`-` run covering the whole block including its braces (§8.5).
**IR** `{op: add, path: /provider/"aws", value: {region: "us-west-1"}}` — the label is in the address, never in the value
**Formats** hcl ✓ only · **Errors** `HEW012` `HEW014` · **Corpus** `hcl/add-block`, `hcl/remove-block`

#### OP-36 `hcl-block-relabel`
**Status** v0, **IR-only** · **Disp** `SUGAR` — → `copy` + `remove` · A relabel is a `move` between label tuples (OP-21).
**IR** `{op: copy, from: /provider/"aws", path: /provider/"aws-legacy"}` then `{op: remove, path: /provider/"aws"}`
**Errors** `HEW012` `HEW013` `HEW014` · **Corpus** `hcl/ir-relabel-block`

#### OP-37 `select-repeated-block` (ordinal selector)
**Status** v0 · **Disp** `CORE` — addressing mode, not an op · Not an operation but a **selector**, cataloged because the survey's HCL check
treated it as a missing capability. §7.2 `! match [label=[…]] ord=<n>`.
**Formats** hcl ✓ · others — (no format else permits repeated identical keys)
**Errors** `HEW001` (unnecessary ordinal) `HEW011` (label cross-check) `HEW012` `HEW013`
**Corpus** `hcl/repeated-label-ordinal`, `hcl/repeated-label-ambiguous` (error), `hcl/ordinal-context-guard` (error)

#### OP-38 `toml-surface-placement-on-add`
**Status** v0 · **Disp** `CORE` — `surface` qualifier · **Sources** the TOML dotted/table wrinkle (§8.4)
**Semantics** `! surface table|dotted` chooses the surface form for a **creation** only.
**IR** `{op: add, path: /a/b/c, value: 1, surface: table}`
**Formats** toml ✓ only · **Errors** `HEW041` · **Corpus** `toml/surface-directive-table`, `toml/surface-ambiguous` (error)

#### OP-39 `toml-surface-migration`
**Status** **deferred** · **Disp** `OUT` · Rewriting `[a.b]` ↔ `a.b = {…}` (§8.4 rule 4, Appendix C.4).
`HEW020` today. **Condition to add:** a named case where the surface itself, not the value, is
what needs to change — most plausibly a formatter, which is a different tool.

#### OP-40 `yaml-anchor-rewrite` · #### OP-41 `yaml-alias-fork`
**Status** v0 · **Disp** `CORE` — `anchor: rewrite` qualifier · **Sources** the YAML anchor wrinkle (§8.3); no surveyed format addresses it
**Absent/empty** Editing at an alias site with neither directive → `HEW040`, always. There is
no default, deliberately: both answers are destructive in one direction.
**IR** `{op: replace, path: /service_a/timeout, value: 60, anchor: fork}` — one core op, one core qualifier
**Formats** yaml ✓ only · **Errors** `HEW040` `HEW013` (merge-inherited key)
**Corpus** `yaml/anchor-rewrite`, `yaml/alias-fork`, `yaml/alias-ambiguous` (error), `yaml/merge-key-remove` (error)

#### OP-42 `md-replace-block` · #### OP-43 `md-insert-section` · #### OP-44 `md-remove-section`
**Status** v0 · **Disp** `CORE` — `replace`, block address · **Sources** ctxloom's own Markdown surfaces (CLAUDE.md, AGENTS.md, steering files)
**Absent/empty** Section absent → `HEW013`; duplicate heading → `HEW012`. Inserting a section
uses §6.2 placement against sibling sections.
**Mirror** per §8.6 — whole-block margins.
**IR** `{op: replace, path: "/# ctxloom/## Install/code:0", value: {...}}`
**Formats** markdown ✓ only · **Errors** `HEW010` `HEW012` `HEW013`
**Corpus** `markdown/replace-code-block`, `markdown/insert-section-after`, `markdown/remove-section`, `markdown/duplicate-heading` (error)

#### OP-45 `md-replace-managed-region`
**Status** v0 · **Disp** `CORE` — `replace` + `idempotent`, marker address · **Sources** ctxloom M2 verbatim (`agent.WriteManagedContext`)
**Absent/empty** Begin marker present without end → `HEW002` (refuse, never repair). Region
absent entirely → `HEW013`; **creating** the region is `! upsert` on the marker path. The
markers are never part of the node's value, so no operation can destroy them (§8.6).
**IR** `{op: replace, path: /@ctxloom:context, value: {...}, idempotent: true}`
**Formats** markdown ✓ only · **Errors** `HEW002` `HEW013` · **Corpus** `markdown/managed-region-replace`

#### OP-46 `md-sub-block-edit`
**Status** **rejected** · **Disp** `OUT` · Editing one line of a paragraph (Appendix C.3). `HEW020`.
**Why not.** Sub-block addressing means addressing prose by offset, which is the exact
fragility structural patching exists to escape.

#### OP-47 `md-heading-level-change`
**Status** **deferred** · **Disp** `OUT` · Promoting `##` to `#` restructures the section tree; expressible
today only as remove + add of the whole section. **Condition to add:** a named case.

---

### 11.8 File-level operations (from ctxloom M1–M8)

These are operations ctxloom's real mechanisms perform that are **not node operations**. They
are cataloged because omitting them would make the catalog look complete while leaving the
mechanisms Hew is meant to serve unaccounted for.

| # | Operation | Source | Status | Where it lives instead |
|---|---|---|---|---|
| OP-48 | `create-file-if-absent` | M2 (never creates), M4 (does create) | **deferred** | The CLI's `--create` would own it. The IR is per-target and assumes the target exists. Named because M2's "an absent file is never created" is a *deliberate* behavior Hew must be able to express. |
| OP-49 | `delete-file-when-empty` | M2 (removes the file when nothing user-authored remains), M4 (deletes stale tracked files) | **deferred** | Same. Note this is the one place ctxloom's own discipline requires a file-level effect *derived from* a node-level result. [O22](#residual--genuinely-open). |
| OP-50 | `append-only-idempotent-block` | M8 (`.gitignore`) | **deferred** | Needs a line-oriented `text` binding, which is not one of the six. Expressible as OP-45 once such a binding exists. |
| OP-51 | `ledger-recorded-withdrawal` | M1 (`.ctxloom-managed`) | **out of scope** | Ownership records are not a patch operation. But see [O14](#decisions-and-residual-open-questions): an applied `.hew` file is itself a candidate ownership record, which would make `hew revert` the withdrawal story. |
| OP-52 | `whole-file-replace` | M4, kiro's dedicated files | v0 | `{op: replace, path: /, value: {...}}` — a replace at the root anchor. Legal and boring; listed so nobody invents a file-level op for it. |

---

### 11.9 Catalog summary

| Status | Count | Entries |
|---|---|---|
| **v0-normative** | 36 | OP-01–08, 11–18, 21–28, 30–38, 40–45, 52 |
| **deferred** | 6 | OP-19, 39, 47, 48, 49, 50 |
| **rejected** | 5 | OP-09, 10, 20, 29, 46 |
| **out of scope** | 1 | OP-51 |

| Disposition | Count | Entries |
|---|---|---|
| **CORE** | 31 | everything v0 except the five below |
| **SUGAR** (parser desugars; never in the IR) | 5 | OP-07 rename, OP-21 move, OP-26 exhaustive, OP-31 comment-attachment, OP-36 relabel |
| **OUT** | 12 | the deferred, rejected and out-of-scope rows above |

Every rejected entry names the property it would break. Every deferred entry names the
condition that would revive it. That is what "exhaustive" is for: a future reader arguing for
`$setElementOrder` should be arguing against OP-19's stated condition, not rediscovering the
question.

### 11.10 The reduced core — how 52 operations became five

**The rule (human, 2026-08-14): the survey is exhaustive; the normative set is not.** The IR
reduces to the smallest orthogonal core that comfortably describes every `v0` row. No
duplication. No sugar that could be spelled another way. *The IR is essentially ASM* — few
primitives, stable, composable — and every bit of richness lives in the notation and the
parser that compiles it.

**The promotion test.** A surveyed verb becomes a core op **only if** no composition of
existing core ops plus addressing produces the same observable result. "Observable" includes
byte preservation and attached comments, because those are contract in this format (§6.3).

**What survived, and what it displaced:**

| Core op | Displaces (surveyed verbs that compile to it) |
|---|---|
| `test` | 6902 `test`; ytt `@overlay/assert` and every `expects=`; CUE's unification conflict; `? expect`/`? absent`/`? count`/`? kind`/`? exhaustive`; **every context line in the mirror grammar** |
| `add` | 6902 `add`; 7386 implicit set; ytt `@overlay/append` / `insert before=` / `insert after=` / `missing_ok`; spruce `(( append ))` / `(( prepend ))` / `(( insert after ))`; RFC 5261 `pos="prepend"/"before"/"after"`; jsonnet `+:`; M5 install |
| `remove` | 6902 `remove`; 7386 `null`; SMP `$patch: delete` and `$deleteFromPrimitiveList`; ytt `@overlay/remove`; spruce `(( delete ))`; jsonnet `::`; M3 withdraw |
| `replace` | 6902 `replace`; SMP `$patch: replace`; ytt `@overlay/replace`; M1/M7 rewrite |
| `copy` | 6902 `copy`; kustomize `replacements`; **and half of `move`** |

**The four reductions, each with its proof obligation:**

1. **`move` → `copy` + `remove`.** Observably identical: `copy` is defined to carry the source
   node's bytes and attached comments, so removing the source afterwards leaves exactly what a
   `move` would. 6902's six become five. `op: move` is accepted on input and normalized away.
2. **`exhaustive` → `count` + per-child `test`.** "These are all the children" is exactly "the
   child count is N" conjoined with "each of these N is present and equal", both of which are
   already core. The qualifier bought nothing but a shorter record.
3. **`comment: {leading, trailing}` → an `add` at a comment address.** This one required a
   *change elsewhere*: comments needed addresses (§4.5b) — which they needed anyway for OP-32
   and OP-33. Once they had them, the qualifier was redundant. This is the reduction working
   as intended: it forced the node model to become uniform instead of letting a qualifier
   paper over a gap.
4. **`ord` / `labels` → addressing.** An ordinal is an addressing mode, not an operation. It
   belongs on the path in the IR, exactly as it belongs *off* the path in the notation (§4.3,
   §7.2). The notation keeps ordinals visible as annotations because a human should see the
   fragility; the ASM puts them in the address because that is what they are.

**The case examined hardest: ordering-sensitive inserts (OP-11, OP-12, OP-13).**

The temptation is four ops — `append`, `prepend`, `insert-before`, `insert-after` — because
four surveyed systems spell them that way (ytt, spruce, RFC 5261, and 6901's `-` token). They
compile to **one** core op with a placement qualifier:

| Surveyed spelling | Core form |
|---|---|
| ytt `@overlay/append`, spruce `(( append ))`, 6901 `/tags/-` | `add`, no placement (default: end) |
| spruce `(( prepend ))`, RFC 5261 `pos="prepend"` | `add`, `before: <first sibling>` |
| ytt `insert before=`, RFC 5261 `pos="before"` | `add`, `before: <sibling>` |
| ytt `insert after=`, spruce `(( insert after ))`, RFC 5261 `pos="after"` | `add`, `after: <sibling>` |

**Could placement itself be reduced further — to the path?** This was the closest call in the
reduction. `add path: /tags/-` (append) and `add path: /tags/0` (prepend) are pure addressing
and need no qualifier at all. But an identity-based insert — "after the element named
github" — has no path spelling that is not an index, and an index is precisely the fragility
§6.4 exists to avoid. So placement stays, as **two fields expressing one concept**, and
`before`/`after` cannot collapse into one: `before: X` is not `after: <predecessor of X>`
when X is first, and naming a predecessor requires knowing one exists.

**The overlap that was kept deliberately.** `replace` and `add`+`on_conflict: replace` produce
the same bytes when the node exists. They are not duplicates: `replace` **requires** the node
(`HEW013` if absent), `add`+`on_conflict: replace` does not. The difference is a precondition,
which is orthogonal to the effect — see [O26](#decisions-and-residual-open-questions), because a
reviewer could reasonably call this the one piece of redundancy that survived.

---

## 12. Documented-only formats

Named by the spec, with an addressing sketch, so that a future binding does not have to
redesign the address grammar. **No corpus cases, no v0 implementation.**

### 12.1 INI-family

There is no INI standard, and "INI" bundles at least three incompatible dialects. A binding
must therefore be **dialect-parameterized**, not universal:

| Dialect | Example | Addressing sketch |
|---|---|---|
| git-style | `[remote "origin"]` | Quoted subsection = a **label segment** (§4.3), reusing HCL's rule: `/remote/"origin"/url`. |
| systemd-style | `[Service]` with repeatable keys | `/Service/ExecStart` addresses a *sequence* when the key repeats; a repeated key that the patch addresses as a scalar is `HEW012`. |
| Java `.properties` / `.npmrc` | `a.b.c=1`, flat | **Flat, not a tree.** `/a.b.c` is one segment. The `~1` escape is mandatory for any `/` in a key. A binding MUST NOT split on `.`. |

The dialect is a required binding parameter (`format=ini dialect=git`), never inferred.

### 12.2 dotenv

Flat `KEY=VALUE`, no sections, no nesting. `/DATABASE_URL` is the whole address grammar. The
measured gap is on the *write* side (`joho/godotenv` re-serializes and does not claim comment
preservation), so a binding needs its own line-oriented editor — which is why this is
document-only despite being the simplest model surveyed.

### 12.3 XML

Node kinds: element, attribute, text, comment, processing instruction. Addressing sketch: Hew
paths map onto a **restricted XPath subset** — `/config/server/@port` for attributes,
`/config/servers/server/name="ctxloom"` reusing the key-match segment for XPath's predicate
form. RFC 5261 is prior art for the op side and should be the down-compilation target instead
of 6902 for this format. Mixed content (text interleaved with elements) has no
shape-mirroring answer and is the reason XML is document-only.

---

## 13. The conformance corpus

`corpus/` is the normative artifact. The Go implementation and the Rust port run the
same directory. **The corpus pins the pipeline at three independent seams, not just
end-to-end** — an end-to-end-only corpus cannot tell a parser bug from an applier bug, and the
two are written by different people in different languages.

### 13.1 The three seams

| Seam | Input → output | What it isolates |
|---|---|---|
| **`parse`** | `patch.hew` → transform list | The notation. Format-agnostic; needs no target file and no backend. A Rust port can pass every `parse` case before a single backend exists. |
| **`apply-ir`** | `transforms.hewt` + `target.*` → `expected.*` | The applier. Format mechanics and byte preservation, with the notation entirely out of the picture. |
| **`e2e`** | `patch.hew` + `target.*` → `expected.*` | The composition, which is what a user experiences. |

A case directory carrying `patch.hew`, `transforms.hewt`, `target.*` and `expected.*` pins all
three from one set of fixtures, and the runner asserts each independently. This is why
`transforms.hewt` (§9.6) is a corpus file and not an implementation detail — and why
`hew apply --transforms` (Appendix B) gets conformance coverage for free: it is the
`apply-ir` seam with a CLI in front of it.

Two further seams exist for the P4 differ:

| Seam | Input → output | What it isolates |
|---|---|---|
| **`render`** | transform list → `.hew` → transform list | The renderer, via IR identity (§13.5). |
| **`diff`** | `old.*` + `new.*` → `expected.hew` | The differ's determinism and context radius (§9.4). |

### 13.2 Layout

```
corpus/                        (repo root — ratified O15)
  README.md                    how a runner consumes this directory
  json/<case>/
  jsonc/<case>/
  yaml/<case>/
  toml/<case>/
  hcl/<case>/
  markdown/<case>/
  cli/<case>/
```

One directory per case. The directory name is the case name.

**`markdown/` is severable.** Markdown's place in the implement tier is deferred to the
evaluation in §8.7; no case outside `markdown/` may depend on a Markdown fixture, so dropping
the dialect is the removal of one directory.

### 13.3 Case files

| File | Present when | Contents |
|---|---|---|
| `case.yaml` | always | The manifest (§13.4). |
| `patch.hew` | every seam except `apply-ir` and `diff` | The patch. |
| `transforms.hewt` | `parse`, `apply-ir`, `render` seams | The canonical transform list (§9.6) — expected output of the parser *and* input to the applier. |
| `target.<ext>` | `apply-ir`, `e2e` seams | The input document, byte-exact. |
| `expected.<ext>` | success cases | The expected output, **compared byte for byte**. |
| `expected-ops.json` | optional | The **resolved** RFC 6901 op list (§9.2), for interop pinning. |
| `old.<ext>` / `new.<ext>` | `diff` seam | The two inputs. |
| `expected.hew` | `diff` seam | The differ+renderer's expected output, byte-exact (§9.4-R1). Its `--- ` line names **`old.<ext>`** (§9.4-R7): the patch applies to old, and the `apply-ir`/`e2e` seams of a round-trip case apply it to exactly that file. |
| `stdout.txt` / `stderr.txt` | `cli` cases, optional | Expected streams. |

### 13.4 `case.yaml`

```yaml
name: yaml/keyed-array-add
seams: [parse, apply-ir, e2e]
kind: ok                  # ok | error | cli
format: yaml
ops: [OP-16]              # catalog entries this case pins (§11)
why: |
  One-paragraph statement of the rule this case pins, in spec terms, with the
  section number. A case whose `why` does not name a spec rule is a bug.
spec: "§6.2, §11 OP-16"
```

An error case names the seam the failure belongs to, which is itself an assertion — a
`HEW001` that surfaces at the `e2e` seam instead of the `parse` seam means the parser is
deferring work it should have refused:

```yaml
name: yaml/stale-scalar
seams: [e2e]
kind: error
format: yaml
error: HEW010
error_seam: apply-ir
error_path: /server/timeout
patch_line: 6
message_contains: ["stale-target", "30"]
why: A drifted scalar under a `-` margin fails by name and location, never silently.
spec: "§5, §10"
```

```yaml
name: cli/stale-exit-1
seams: [cli]
kind: cli
argv: ["apply", "--in-place", "patch.hew"]
exit: 1
stderr_contains: ["HEW010"]
target_unchanged: true
why: A failed apply leaves the target byte-identical and exits 1, patch(1)-style.
spec: "§10.5, Appendix B"
```

**`env:` — the pinned-environment block.** A `cli` case may declare an environment for the
run. It exists for exactly one thing today, and the restriction is the point: **`env:` may
only set inputs the spec names as environment-readable**, which is `HEW_APPLIED_AT` and
`SOURCE_DATE_EPOCH` (§9.7, Appendix B.1) and nothing else. A corpus that could reach any
environment variable would be a corpus that could pin behaviour the spec never promised.

```yaml
name: cli/record-pinned-time
seams: [cli]
kind: cli
argv: ["apply", "-i", "--record", "out.hewt", "patch.hew"]
env:
  HEW_APPLIED_AT: "2026-08-14T09:31:07Z"
exit: 0
expected_record: expected-record.hewt   # which now MAY pin applied_at
```

A record fixture normally must *not* pin `applied_at` — a wall clock cannot be pinned, and a
fixture that tries is itself the error. A case that pins the clock through `env:` inverts that
rule for itself: `applied_at` becomes an ordinary compared field, and a byte-inexact match is a
failure. That inversion is the whole assertion — "pinned" means pinned.

**Reversal-patch fields.** A `cli` case exercising `--reversal`
([O40](#p5--the-api-ratification-2026-08-14)) asserts the artifact the same way every other
produced file is asserted, with `expected_targets` naming the reversal file and the fixture it
must equal byte-for-byte. No new mechanism is needed and none is added: the reversal patch is
just a file the run wrote.

The *second* half of the claim — that applying the reversal restores the original — is
**stated in the case's `why` and not asserted by the manifest**, because the manifest's `argv`
is one invocation and this needs two. When the corpus grows a multi-invocation `cli` form, that
assertion moves into the manifest; until then, RT1 already pins the identity it depends on
(`apply(parse(render(diff(a, b))), a) == b`, §13.5) over every implement format, and the
reversal patch is that same composition with the arguments exchanged. Saying so in prose is
honest; asserting it with machinery that does not exist would not be.

### 13.5 The round-trip identities

Two identities close the four-component architecture (§9). They are stated as normative
conformance requirements, and the corpus carries one instance per implement format.

**RT1 — full round trip.** For any `(old, new)` pair of the same format:

```
apply( parse( render( diff(old, new) ) ), old )  ==  new
```

Byte-for-byte. This is the single strongest statement the standard makes: the notation can
express what the differ finds, the renderer writes it faithfully, the parser reads back what
was written, and the applier reproduces the target exactly. A failure anywhere in the four
components fails RT1, which is why the three-seam decomposition above matters — RT1 tells you
*that* something is wrong, the seams tell you *what*.

**RT2 — notation round trip on the IR.**

```
parse( render( ir ) )  ==  ir
```

Note the direction. `render → parse` is an identity **on the IR**; `parse → render` is *not*
an identity on the text, and the corpus must not assert that it is: Hew comments, hunk
boundaries, chosen anchors and context radius are notation-side authorial choices that the IR
does not carry (§9.6).

Corpus cases: `<format>/roundtrip-basic/` for each of the six implement formats, carrying
`old.*`, `new.*`, `expected.hew`, and `case.yaml` with `seams: [diff, render, parse, apply-ir, e2e]`.

### 13.6 Runner contract

A conformant runner MUST:

1. Copy the case directory to a scratch location (cases with `--in-place` mutate).
2. For each declared seam, run **only** that seam and assert only its output. A runner that
   collapses `parse` and `e2e` into one execution is not conformant — it cannot report which
   component failed, which is the whole point.
3. For byte comparisons, compare **exactly**: no normalization, no trailing-newline tolerance,
   no whitespace folding.
4. For `error` cases: assert the exact `error` code, the exact `error_path`, the declared
   `error_seam`, and all `message_contains` substrings; assert the target is byte-unchanged.
5. For `cli` cases: run the binary with `argv` relative to the case directory; assert exit
   code and stream contents.
6. Report an unknown `seam` or `kind`, or a missing declared file, as a **corpus error**, not
   a skip. Silent skips are how a conformance suite lies.
7. Report the catalog coverage: every `OP-nn` in §11 marked `v0` must appear in at least one
   case's `ops:` list. An uncovered v0 operation is a corpus gap, and the runner names it.

### 13.7 The skip registry, and the gate that retires it

An implementation under construction cannot pass a corpus that pins components it has not
built. The dishonest answer is to run a subset; the honest one is a **skip registry** — and
the conventions below are the ones the human set directly in the standalone repository's
`justfile`, recorded here so that the Rust port inherits them rather than reinventing them.

**A conformant runner MUST carry a skip registry with these properties:**

1. **Every skip is a rule with a recorded reason.** A rule names a case glob, a seam, and a
   sentence saying *why* — a milestone that has not landed, or a spec question that is open.
   No unexplained skips, ever.
2. **A rule that matches nothing is a failure.** The registry can only shrink truthfully: when
   a milestone lands and its rule stops matching, the build breaks until the rule is deleted.
   This is the property that stops a skip table from becoming a graveyard.
3. **The strict gate turns every match into a failure.** With `HEW_CORPUS_NO_SKIPS=1` set, the
   registry is disallowed: any case a rule would have skipped instead fails. This is the
   end-state conformance gate, and an implementation is conformant when it passes under it.

The Go implementation binds these as `just corpus-go` (registry honoured) and
`just corpus-go-strict` (`HEW_CORPUS_NO_SKIPS=1`). The `markdown/*` rule is the one entry
expected to outlive the *milestones*: it is gated on [O29](#residual--genuinely-open), not on
work in progress, and it will be deleted or the family removed when §8.7 is evaluated.

**Ratified-but-unbuilt behaviour is carried the same way, and this is the mechanism that makes
a spec-first ratification honest.** When a ruling lands before its implementation — O37's
pinned `applied_at`, O38's no-op patch, O39's diff target, O40's reversal patch — the corpus
grows the case *first*, and a skip rule records that the case is red because the ruling is
newer than the code. The rule's reason must name the ruling, so the registry reads as a list
of promises outstanding rather than a list of tests that happen to fail:

```
{Case: "cli/apply-reversal", Seam: "cli",
 Reason: "P5: ratified, implementation pending — O40 (hew apply --reversal)"}
```

The two ratchets do the rest: the rule dies the moment the behaviour lands (a rule matching
nothing is a build failure), and the strict gate reports the whole outstanding set at once.

**The pending cases are the acceptance tests, and implementation MUST proceed test-first**
([O50](#p5--the-filesystem-surface-and-tdd-discipline-2026-08-14)). This is the discipline P0
through P4 ran under; writing it down makes it reviewable rather than remembered.

1. **A work package begins by deleting its skip rules.** That is the red step, and it is not
   optional or reordered: the corpus case was written when the ruling landed, so the failing
   test already exists and the first commit of an implementation makes it *run*. A package
   that implements first and deletes rules afterwards has written its acceptance test knowing
   the answer.
2. **Layer-level unit tests are written red/green alongside**, not backfilled. The document API
   (A.0), the registry (A.6), `hewfs` (A.8) and the reversal patch each get their own suite.
   Those suites use **`afero.MemMapFs`**, and a tmpdir-based test where an in-memory
   filesystem would serve is a defect — it is slower, it leaks state between runs, and it
   tests the operating system rather than the code.
3. **The established mutation gates apply**: **≥85% on the core, ≥75% on the format
   extensions**, run as `just mutate-go` (`gremlins`, `--timeout-coefficient 30`, unit-test
   killers only — the inner loop).
4. **The acceptance-mutation gate runs once at P5 completion**: `just mutate-go-acceptance`
   (`--coverpkg --integration`). §13.8 already states why this is the one that matters — it
   measures whether **the corpus itself** detects a defect, and a surviving mutant is a corpus
   gap to be fixed in `corpus/`, not in an implementation's own tests.

### 13.8 Acceptance criteria and the quality bar

**The corpus states the obligations; `features/` states them as acceptance criteria.** A
Cucumber feature set at the repository root expresses what any implementation must satisfy —
language-agnostic, so the Go and Rust implementations are held to the same text. The Go
binding runs them with godog at `go/conformance`, entry point `TestFeatures`
(`just accept-go`). The features are not a second corpus: they are the corpus's obligations
written as criteria, with the corpus cases as their examples.

**Mutation testing is the conformance-quality bar.** A corpus can be passed by an
implementation whose assertions do not actually check anything, and the way that is caught is
to mutate the implementation and confirm the suite notices. Two levels, both bound in the
justfile:

| Level | What kills the mutants | When |
|---|---|---|
| `just mutate-go` | Unit tests only; slow suites skipped | Inner loop, per change |
| `just mutate-go-acceptance` | The corpus and acceptance suites as killers | Milestone exit gate |

The second is the one that matters for this standard: it measures whether **the corpus itself**
detects an implementation defect. A mutant that survives `mutate-go-acceptance` is a corpus
gap — a behaviour the standard claims to pin and does not — and the fix belongs in `corpus/`,
not in the implementation's own tests.

---

## Appendix A — the Go API surface

**Ratification status.** This appendix was checkpoint-1 material — signatures for human review,
not code. **It is now ratified**, in two halves that must be read differently:

- **Shipped and normative.** A.1–A.10 describe the surface the Go implementation actually
  exports. Where the implementation deviated from the checkpoint-1 draft, the deviation is
  ratified in place with an O-number and the draft text is corrected, not preserved: a spec
  that describes signatures nobody wrote is worse than no spec.
- **Ratified and pending.** A.0 (the document API), A.6's registry, A.8's `hewfs`, and the
  `--reversal` / pinned-`applied_at` surfaces are decided but **not implemented**. The
  conformance corpus carries their cases and the Go runner's skip registry (§13.7) records
  each as pending with the ruling that decided it. A rule that stops matching is deleted;
  that is how this half becomes the first half.

**Package path:** `github.com/benjaminabbitt/hew/go`, with **format extensions at
`.../ext/json`, `ext/jsonc`, `ext/yaml`, `ext/toml`, `ext/hcl`, `ext/markdown`**
([O48](#p5--the-format-isolation-audit-2026-08-14) — the isolation is visible in the import
path, and a reviewer scanning imports can see whether the core has grown a format dependency),
the differ front end at `hewdiff`, and the CLI at `cmd/hew`. **The core package imports no host
project** — not ctxloom, not anything else: it is a standard's reference implementation and a
Rust port's peer. Filesystem and git integration live in `hewfs` and the CLI's `hewsource`,
never in the core. (This supersedes the draft's `github.com/ctxloom/ctxloom/internal/hew` path,
which named the extraction that has since happened.)

**The `ext/<format>` layout is a breaking rename** of the shipped `hewjson`/`hewyaml`/… packages
and lands with the rest of the P5 implementation. Consumers pinned at `v0.1.0` are unaffected
until they upgrade.

**The four components map to four entry points**, and the type that connects them is
`TransformList`:

| Component | Entry point | Side |
|---|---|---|
| Parser | `hew.Parse([]byte) ([]TransformList, error)` | notation, format-agnostic |
| Renderer | `hew.Render(TransformList, RenderOptions) ([]byte, error)` | notation, format-agnostic |
| Applier | one `Apply(target []byte, tl TransformList)` per format package | format |
| Differ | one `DiffTree(src []byte)` per format package, driven by `hewdiff.Diff` | format |

**And one entry point above all four**, for the caller who has a file rather than a patch:
`hew.Open` (A.0). It is the surface a host program uses; the four components are what it is
built out of.

### A.0 The document API — editing a file without writing a patch

**Ratified (human, 2026-08-14), [O34](#p5--the-api-ratification-2026-08-14).** A host program
that wants to change a config file does not want to build a `[]Transform` by hand. Today it
does exactly that — ctxloom's `config-write` hand-rolls a hundred lines of `hew.Transform{Op:
hew.OpAdd, Path: prefix.Append(hew.Segment{Kind: hew.SegKey, Name: k}), …}`, plus its own
record marshaller, plus its own atomic write — and every line of it is this appendix's
absence, not the caller's invention. A.0 is the surface that replaces it.

The API is one sentence of Go per edit, and it is built on three rules, each of which is a
restatement of a rule the format already has.

**Rule 1 — format appears at the open boundary and nowhere else.** §8.0 is normative here:
detection is from the **name**, never from the content, and an explicit override is legal only
where a patch would carry `format=`. The name is what every constructor takes — a path, a
handle's `Name()`, or an explicit label for bare bytes — and the override is what `As`
supplies. After the document is open, no method mentions a format again, ever.

**The constructor family is three functions**, ratified as
[O49](#p5--the-filesystem-surface-and-tdd-discipline-2026-08-14), and the filesystem type
throughout is `afero.Fs`:

```go
import "github.com/spf13/afero"

// Open reads path from fsys and returns a document bound to the format §8.0
// detects FROM THE PATH. Content is never sniffed (§8.0). A path whose format
// cannot be determined is HEW021 — fixable at this call site, and only here.
func Open(fsys afero.Fs, path string, opts ...OpenOption) (*Doc, error)

// OpenFile adopts an ALREADY-OPEN handle. Detection reads f.Name(); a handle
// with no usable name takes hew.As, exactly as an ambiguous path does.
//
// The caller owns the handle: hew does not open it, does not lock it, and
// NEVER CLOSES A HANDLE IT DID NOT OPEN. This is the constructor for a program
// that has already taken a lock, already resolved a symlink, or already
// stat-ed and validated the file it is about to edit — which is what a careful
// config writer does, and what it must not be made to redo.
func OpenFile(f afero.File, opts ...OpenOption) (*Doc, error)

// OpenBytes is the same for content already in hand. name is still required
// and still the only thing detection reads: bytes have no name of their own,
// and inventing one from their shape is the sniffing §8.0 forbids.
func OpenBytes(name string, src []byte, opts ...OpenOption) (*Doc, error)

// As overrides detection, and is the exact analogue of a target line's
// `format=` attribute (§2.2) — the one place the spec already lets a human
// out-vote the extension.
func As(format FormatID) OpenOption
```

```go
doc, err := hew.Open(fsys, ".claude/settings.json")              // jsonc, by well-known name
doc, err := hew.OpenFile(f)                                      // an FD the caller already holds
doc, err := hew.OpenBytes("config", src, hew.As(hew.FormatTOML)) // no extension: say so
```

**Rule 2 — addressing is the §4 path, and there is only the one language.** A document is
addressed with the same string a hunk anchor is addressed with, so the thing a reviewer reads
in a `.hew` file and the thing a programmer types in Go are the same artifact:

```go
// At takes a LITERAL path, optionally with typed holes. A bad path, or a hole
// count that does not match the arguments, is HEW001 at this call.
func (d *Doc) At(path string, fill ...Segment) *Sel
func (d *Doc) AtPath(p Path) *Sel      // the pre-parsed form
```

```go
doc.At("/mcpServers/name=github/command").Replace("npx")
```

**Values that come from variables go in through typed holes, never through the string**
([O43](#p5--the-api-ratification-2026-08-14)). `{}` in the path is a placeholder filled by one
`Segment` argument, positionally:

```go
doc.At("/mcpServers/{}/command", hew.MatchKey("name", name)).Replace(cmd)
doc.At("/dependencies/{}", hew.Key(pkg)).Set(version)
```

**The unit of substitution is a typed segment, and this is the whole point.** A segment knows
whether it is a key, a label, a match field or a match value, so it knows how to render itself
under §4.1's canonical rule and §4.2's value-typing rule. A *string* knows none of that, which
is why the two obvious alternatives are recorded as **rejected**:

- **printf-style character-level escaping** (`hew.Escapef("/deps/%s", pkg)`) — rejected. An
  escaper receives a raw string and cannot know whether it is standing in for a key, a label,
  a match field or a match value, and those escape differently (`~2` matters in a field and
  not in a quoted key; a string value must force-quote, §4.2). An escaper that cannot tell
  them apart escapes for the wrong one silently.
- **detecting concatenation and warning** — rejected. It cannot be done without false
  positives on legitimately-computed literal paths, and a warning that fires on correct code
  trains the reader to route around it, which is worse than no warning.

**String concatenation into `At` is a defect**, and the spec says so plainly rather than
guarding against it: `At("/deps/" + pkg)` is broken for `@scope/pkg`, for `8080`, and for `-`,
in the silent way §4.1's bijection rule exists to eliminate. Use a hole.

Paths that are computed wholesale — built in a loop, or stored — use the typed segment
constructors directly, thin named wrappers over the `Segment` literals A.1 already exports:

```go
func Key(name string) Segment                        // §4.1
func Index(i int) Segment                            // §4.1
func Append() Segment                                // §4.1's "-"
func Label(s string) Segment                         // §4.3
func Heading(level int, text string) Segment         // §4.5
func Block(kind BlockKind, ord int) Segment          // §4.5
func Marker(name string) Segment                     // §4.5
func Comment(ord int) Segment                        // §4.5b
func TrailingComment() Segment                       // §4.5b's "#t"
func Optional(s Segment) Segment                     // §4.4's trailing `?`

// Key-match, with the comparison's TYPE visible at the construction site
// (O42). The plain spelling takes a string and produces a quoted string
// scalar, because a string is what a caller almost always has and a silent
// re-decode to a number is the failure §4.2 names.
func MatchKey(field, value string) Segment           // §4.2  name="value"
func MatchKeyNumber(field, literal string) Segment   // §4.2  name=8080
func MatchKeyBool(field string, v bool) Segment      // §4.2  name=true
func MatchKeyNull(field string) Segment              // §4.2  name=null
func MatchValue(value string) Segment                // §4.2  ="value"
func MatchValueNumber(literal string) Segment        // §4.2  =8080

p := hew.NewPath(hew.Key("mcpServers"), hew.MatchKey("name", "github"), hew.Key("command"))
doc.AtPath(p).Replace("npx")
```

`MatchKey(field, value string)` deliberately does **not** take an `any` and guess. A
`MatchKey("port", "8080")` that inferred "this looks numeric" would address a different node
than the one the caller named, with no error anywhere; and the number case is rare enough that
spelling it `MatchKeyNumber` costs one word and buys a reader who can see, at the call site,
which comparison is being made.

**There is deliberately no navigation vocabulary** — no `doc.Key("mcpServers").Match("name",
"github")` method chain. It would be a second address language, differing from the first in
spelling but not in meaning, and a reviewer diffing the Go against the `.hew` would have to
translate between them. One language, two encodings (text and typed), and `AtPath(ParsePath(s))`
is the identity.

**Rule 3 — reads become asserts.** This is the rule that makes the fluent API *the same thing*
as a patch rather than a convenience wrapper over the IR.

> **Because the document is open, an operation that names an existing node records the value
> it found as a `test` transform beside the write it performs.** `Replace(v)` on a node
> currently holding `u` compiles to `{test, p, u}` then `{replace, p, v}` — which is §9.1's
> lowering, with the before-image taken from the document instead of from a `-` line.

A patch derived this way therefore carries the same loud staleness a hand-written hunk carries:
re-applying it against a file that has since drifted fails `HEW010`, and re-applying it against
a file already holding `v` fails `HEW014`/`HEW011` per §10.6. The builder never produces a
transform an author could not have written; **its output is ordinary IR and is indistinguishable
from parsed-patch IR**, which is the property that keeps the corpus's `apply-ir` seam meaningful
for both producers.

The two documented *unasserted* forms are the catalog's own, and they are spelled as themselves
so that "I did not assert the prior state" is visible in the code exactly as `! upsert` and
`! default` make it visible in a patch (§7.7):

| Method | Records | Catalog |
|---|---|---|
| `Replace(v)` | `test`(current) + `replace` | OP-01 |
| `Set(v)` | `add` with `on_conflict: replace` — **no assert** | OP-03 (`! upsert`) |
| `Default(v)` | `add` with `on_conflict: keep` — no assert, no write if present | OP-04 (`! default`) |
| `Add(v)` | `add`, `on_conflict: fail` | OP-02 |
| `Remove()` | `test`(current) + `remove` | OP-05 |

`Set` is the one mapping write that asserts nothing about the prior state and therefore cannot
detect drift — §7.7's warning applies verbatim, and a linter that warns on `! upsert` should
warn on `Set` for the same reason.

**The surface**

```go
type Doc struct{ /* unexported */ }

func (d *Doc) Name() string
func (d *Doc) Format() FormatID

type Sel struct{ /* unexported */ }

// Writes. Each returns the Sel so qualifiers chain.
func (s *Sel) Replace(v any) *Sel   // OP-01: asserted
func (s *Sel) Set(v any) *Sel       // OP-03: upsert, unasserted
func (s *Sel) Default(v any) *Sel   // OP-04: defaulting, unasserted
func (s *Sel) Add(v any) *Sel       // OP-02: must not exist
func (s *Sel) Remove() *Sel         // OP-05

// Placement for Add (OP-11 … OP-13). Abstract sibling addresses, never
// indices — the same relative placement §9.1 step 5 emits.
func (s *Sel) After(sibling string) *Sel
func (s *Sel) Before(sibling string) *Sel

// Assertions. Each is an assert-only transform (§7.4) and writes nothing.
func (s *Sel) Assert(v any) *Sel        // OP-24 `? expect`
func (s *Sel) AssertAbsent() *Sel       // OP-25 `? absent`
func (s *Sel) AssertCount(n int) *Sel   // OP-27 `? count`
func (s *Sel) AssertKind(k NodeKind) *Sel // OP-28 `? kind`
func (s *Sel) AssertExhaustive() *Sel   // OP-26 `? exhaustive`

// Qualifiers, in the vocabulary §7 already defines. They ride the transforms
// this Sel has recorded, exactly as a `!` directive rides a hunk's.
func (s *Sel) Optional() *Sel                 // §7.6
func (s *Sel) Idempotent() *Sel               // §7.5
func (s *Sel) Anchor(mode AnchorMode) *Sel    // §8.3, OP-40 / OP-41
func (s *Sel) Surface(sf Surface) *Sel        // §8.4, OP-38
```

**Terminals.** A `Doc` is not an editor of bytes; it is a recorder of transforms with a target
to resolve them against. Nothing has happened until a terminal is called, and each terminal
names exactly which artifact it produces:

```go
// Transforms is the IR (§9.2 abstract form). Everything else is derived from it.
func (d *Doc) Transforms() (TransformList, error)

// RenderPatch writes a reviewable .hew file. Because the document is open,
// the renderer has real siblings to draw context from, so the patch carries
// genuine context lines at the §9.4-R2 radius — the same artifact `hew diff`
// produces, from the same renderer.
func (d *Doc) RenderPatch(opt RenderOptions) ([]byte, error)

// Bytes applies the recorded transforms to the opened content and returns the
// patched bytes. All-or-nothing (§10.5): on any error the result is nil.
func (d *Doc) Bytes() ([]byte, error)

// Write commits Bytes() back THROUGH THE SAME afero.Fs OR afero.File the
// document was opened from, via §10.5's temp-and-rename. A Doc from
// OpenBytes has no destination and returns an error naming that rather than
// guessing one.
func (d *Doc) Write(opt ...WriteOption) error
```

The terminal set is the honest statement of what this API is: **a patch producer that happens
to be able to apply its own output.** `RenderPatch` is not a debugging aid — it is how a
program that edits a user's file leaves behind a reviewable statement of what it did, in the
same notation a human would have written by hand.

**The atomicity caveat, stated rather than implied.** §10.5 requires an atomic temp-and-rename,
and `Write` performs one — but **through an arbitrary `afero.Fs` the guarantee is best-effort,
because the rename semantics belong to the backend, not to hew.** On `afero.OsFs` a rename
within one filesystem is the atomic operation §10.5 assumes. On `MemMapFs` it is atomic in the
trivial sense that nothing else is running. On a backend layered over object storage or a
network filesystem, "rename" may be copy-then-delete, which has a window §10.5's contract does
not survive. hew cannot detect this and does not pretend to: a caller whose backend does not
provide atomic rename does not get atomic writes, and needs to know that about its own
filesystem. Everything §10.5 promises about *detectable* failures still holds on every backend
— a failed apply writes nothing at all, because staging happens entirely in memory.

### A.1 The IR — `TransformList`

```go
package hew

// Version is the .hew notation version; TransformsVersion is the IR serialization version.
const (
    Version           = 1
    TransformsVersion = 1
)

// TransformList is the IR: the boundary between the notation side and the format side,
// the interop surface, and the unit the corpus pins. Its record shape is the union of
// the "IR record" rows of the operations catalog (spec §11).
type TransformList struct {
    Target    string      // target path, as declared by "--- " or "target:"
    Format    FormatID    // "" = infer
    Transform []Transform
}

// MarshalTransforms writes the canonical .hewt serialization (spec §9.6).
func MarshalTransforms(tl TransformList) ([]byte, error)

// UnmarshalTransforms reads a hand-authored or generated .hewt document. This is the
// input path behind `hew apply --transforms`, and the reason moves and copies need no
// notation surface.
func UnmarshalTransforms(src []byte) (TransformList, error)

// Transform is one record of the reduced core (spec §11.10). Five ops, one address,
// and the minimum qualifier set. Sugar (move, exhaustive, comment attachment, ord)
// is desugared by the parser and never reaches this type.
type Transform struct {
    Op    OpKind
    Path  Path  // abstract Hew path; ALL addressing richness lives here
    From  Path  // OpCopy only
    Value Value // OpAdd, OpReplace, OpTest

    // Placement (OP-11 … OP-13). Abstract, never a numeric index: the parser has no
    // target to count against. Mutually exclusive; both zero means append at end.
    Before Path
    After  Path

    // Exactly one of these five selects the assertion mode on OpTest.
    // Value (above) | Absent | Count | NodeKind | Exhaustive
    Absent     bool
    Count      *int
    NodeKind   *NodeKind
    Exhaustive bool // OP-26; always paired with Count (§9.1 step 3)

    OnConflict OnConflict // OpAdd only: OP-02 / OP-03 / OP-04
    Anchor     AnchorMode // OP-40 / OP-41, YAML alias policy
    Surface    Surface    // OP-38, TOML placement

    // The two tolerance flags, and there are no others.
    Optional   bool // OP-06 — legal on remove and test
    Idempotent bool // §7.5 — legal on test, add, remove and replace (O32)

    // Provenance. None of it is serialized, none of it is compared by Equal:
    // it exists so a diagnostic can point at the line a reviewer must fix.
    PatchLine  int  // the line this transform was lowered from
    AnchorPath Path // the `@@ … @@` address of the hunk it came from (O31)
    AnchorLine int  // the line that address is written on (O31)
}

type OpKind string

const (
    OpTest    OpKind = "test"
    OpAdd     OpKind = "add"
    OpRemove  OpKind = "remove"
    OpReplace OpKind = "replace"
    OpCopy    OpKind = "copy"
    // There is no OpMove: "move" is accepted on input and normalized to
    // OpCopy + OpRemove (spec §11.10 reduction 1).
)

type OnConflict string

const (
    ConflictFail    OnConflict = "fail"    // default, OP-02
    ConflictKeep    OnConflict = "keep"    // OP-04, defaulting
    ConflictReplace OnConflict = "replace" // OP-03, upsert
)

type AnchorMode string // "", "rewrite", "fork"
type Surface    string // "", "table", "dotted"

// Resolve projects the abstract list onto the RFC 6901 form (spec §9.2) against a
// specific document: key-match segments become indices, placements become indices,
// format-specific qualifiers are consumed. Lossy by design; interop only.
func Resolve(tl TransformList, doc Document) ([]ResolvedOp, error)

type ResolvedOp struct {
    Op    OpKind
    Path  string // RFC 6901 pointer
    From  string // OpCopy only
    Value Value

    // Every assertion mode OpTest can carry, carried through unchanged.
    // NodeKind is here for the reason Exhaustive always was (O33): the
    // resolved list is what `--record` embeds (§9.7), and a record that
    // drops an assertion the applier really evaluated UNDER-REPORTS what
    // happened to the file. A record must not under-report.
    Absent     bool
    Count      *int
    NodeKind   *NodeKind
    Exhaustive bool
}
```

**Provenance, and why it is on the record rather than beside it** ([O31](#p5--the-api-ratification-2026-08-14)).
`AnchorPath`/`AnchorLine` exist because a resolution failure *inside* an anchor is the
**anchor's** failure: when `@@ /provider/"google" @@` matches two blocks, the reviewer must be
sent to the `@@` line, not to the first context line that happened to ask a question. Two
corpus cases pin exactly that reporting (`hcl/repeated-label-ambiguous`,
`markdown/duplicate-heading`), and a transform that does not know which hunk it came from
cannot produce it. The fields are provenance in the strict sense — not serialized to `.hewt`
(§9.6 already rules `line` emitted-and-ignored), not compared by `Equal`, and therefore
invisible to every corpus byte comparison.

**`! idempotent` on a `test`** ([O32](#p5--the-api-ratification-2026-08-14)). §7.5's rule is
stated over the *hunk*: "if the before-image does not match but the after-image does, the hunk
is satisfied". A hunk lowers to a before-image `test` **and** a write, so the tolerance has to
reach both records — an `Idempotent` flag that rode only the write would leave the `test` it
was lowered beside failing loudly, and the hunk could never converge. Hence the two-grant
form: `Idempotent` is legal on `test`, `add`, `remove` and `replace`. `copy` is the one
exclusion, because a copy asserts nothing about its destination and so has nothing to tolerate.

### A.2 Parser — notation → IR

```go
// Parse reads a hew patch document and returns one TransformList per file
// section (§2.2), in file order. It performs NO I/O, opens no target, and
// knows no format mechanics: its output is fully determined by the patch text.
func Parse(src []byte) ([]TransformList, error)
```

**Ratified deviation ([O30](#p5--the-api-ratification-2026-08-14)): the draft's `*Patch`,
`*FileSection` and `*Hunk` types are dropped, and `Parse` returns the IR directly.** The draft
placed three object types between the caller and the thing the caller wanted, and every one of
their accessors was either a field of `TransformList` already (`Target`, `Format`) or a
question no consumer asked. `Patch.Version()` reports a number the parser has already refused
to proceed without (§2.1). `[]TransformList` is what the parser produces, what the applier
takes, and what §13.1's `parse` seam pins — so it is what `Parse` returns.

**The one capability this drops, named honestly: hunk introspection.** `FileSection.Hunks()`
and `Hunk.AssertOnly()` were the surface a **linter** would use — the tool §7.6 asks for when
it says "a conformant linter should warn on every use" of `! optional`, and the tool
[O17](#ratified-by-the-coordinator-2026-08-14) promised. Lowering is lossy in exactly that
direction: the IR knows a transform is `Optional`, but not which hunk it shared a body with,
nor whether that hunk wrote anything. `Transform.AnchorPath`/`AnchorLine` recover the hunk
*address* (A.1), which is enough for diagnostics and not enough for a linter.

**This is recorded as deferred, not resolved.** A linter is not in v0, and when one is
specified, the shape it needs — a hunk-level view retained alongside the IR — is an additive
parser output, not a change to what `Parse` returns. Nothing here forecloses it.

### A.3 Renderer — IR → notation

```go
// Render writes a transform list back out as .hew mirror-grammar text.
// Format-agnostic, like the parser. Deterministic: same input, same bytes (§9.4-R1).
func Render(tl TransformList, opt RenderOptions) ([]byte, error)

type RenderOptions struct {
    Context   int  // sibling radius, spec §9.4-R2. Default 1. -1 = all siblings.
    Preamble  bool // emit "hew: 1"; false when appending to an existing document
    Comment   string
}

// RenderErr reports a transform the mirror grammar cannot express (OP-21, OP-22).
// Callers that must round-trip such a list write it with MarshalTransforms instead.
var ErrInexpressible = errors.New("hew: transform not expressible in mirror grammar")
```

### A.4 Applier — IR + target bytes → patched bytes

```go
// Applier is a format binding's apply half. It never sees .hew text, a margin, a
// hunk, or an annotation.
type Applier interface {
    ID() FormatID

    // ParseDocument parses a whole target file. It must refuse a document it cannot
    // fully represent rather than dropping what it did not understand.
    ParseDocument(src []byte) (Document, error)

    // Apply evaluates every OpTest before mutating anything, then performs the
    // remaining transforms in order, and returns the re-serialized bytes.
    // All-or-nothing: on any error the returned bytes are nil.
    Apply(src []byte, tl TransformList) ([]byte, error)

    // Supports reports whether this binding implements a transform's op and
    // qualifiers. An unsupported qualifier must surface as HEW020, never be ignored.
    Supports(t Transform) error
}

// Document is a parsed target, exposed for Resolve and for diagnostics.
type Document interface {
    Resolve(p Path) ([]Node, error) // 0 results is not an error here; the engine names it
    Bytes() ([]byte, error)         // exact input bytes for an unmodified document
}

type Node interface {
    Path() Path
    Kind() NodeKind
    Value() (Value, error)
    Source() []byte // exact bytes, for diagnostics
    Line() int
}

type NodeKind int

const (
    KindMap NodeKind = iota
    KindSeq
    KindScalar
    KindComment
    KindBlock   // HCL block, Markdown block
    KindSection // Markdown section
)
```

### A.5 Differ — two content sources → IR

```go
// Differ is a format binding's diff half (P4). Its inputs are PURE CONTENT: it has
// no notion of a filesystem, a repository, or a revision. Descriptor resolution is a
// CLI concern (A.7) — keeping it out of the core is what keeps the library embeddable
// and the Rust port's dependency list short.
type Differ interface {
    ID() FormatID

    // Diff computes the structural difference. Deterministic (§9.4-R1): the same
    // (old, new, opt) triple yields the same TransformList in every implementation.
    Diff(old, new []byte, opt DiffOptions) (TransformList, error)
}

type DiffOptions struct {
    // KeyFields are the candidate identity fields for keyed-array addressing,
    // tried in order (§9.4-R4). Empty means {"name", "id", "key"}.
    KeyFields []string

    // Context is the sibling radius (§9.4-R2). Zero means the default of 1;
    // ContextNone (-U0) and ContextAll (-U all) spell the two ends.
    Context int

    // Target is stamped into the produced TransformList; it is a label, not a path
    // the differ reads. It is the OLD side's label (O39): the patch applies to old.
    Target string

    // Note receives the remarks §9.4-R4 requires when the differ falls back to
    // index addressing. The CLI renders them as the patch's leading `#` comment;
    // a nil Note discards them.
    Note func(string)
}
```

### A.6 Registration and detection

```go
type FormatID string

const (
    FormatJSON     FormatID = "json"
    FormatJSONC    FormatID = "jsonc"
    FormatYAML     FormatID = "yaml"
    FormatTOML     FormatID = "toml"
    FormatHCL      FormatID = "hcl"
    FormatMarkdown FormatID = "markdown"
)

// A Binding is a format's three halves plus its detection rule: the applier,
// the differ, and the read-only Document view Resolve (§9.2) and the document
// API (A.0) project against.
type Binding struct {
    Applier  Applier
    Differ   Differ
    Document func(name string, src []byte) (Document, error)
    Detect   DetectRule
}

type DetectRule struct {
    Extensions     []string
    WellKnownNames []string // O4: binding DATA, not spec
}

func Register(id FormatID, b Binding)
func Lookup(id FormatID) (Binding, bool)

// DetectFormat implements §8.0 over the registered bindings' DetectRules. It
// reads the NAME and never the content. Two bindings claiming one name, or no
// binding claiming it, is HEW021 — the caller's cue to say `format=` (or
// hew.As, A.0), which is the only override §8.0 allows.
func DetectFormat(filename string) (FormatID, bool)
```

**Ratified ([O35](#p5--the-api-ratification-2026-08-14)): A.6 becomes real, and bindings
self-register from `init()` on import.** The draft specified a registry and the implementation
grew a `switch` on `FormatID` in three places instead (the corpus binding, the CLI's applier
dispatch, the CLI's document dispatch), which is how the JSON-only `documentFor` gap survived
unnoticed. A registry with one entry point makes "which formats can this build actually do"
answerable, and answerable is what a `--format` error message needs.

```go
package yaml   // ext/yaml; likewise ext/json, ext/jsonc, ext/toml, ext/hcl, ext/markdown

func init() { hew.Register(hew.FormatYAML, hew.Binding{ /* … */ }) }

// Document is the extension's read-only view, exported so a caller that
// already knows the format can skip detection.
func Document(name string, src []byte) (hew.Document, error)
```

```go
import _ "github.com/benjaminabbitt/hew/go/ext/all"   // every v0 extension
import _ "github.com/benjaminabbitt/hew/go/ext/json"  // or exactly the ones you want
```

A `Binding` also carries what [O48](#p5--the-format-isolation-audit-2026-08-14) moved out of
the core: the segment forms this format claims (§8.8), the node-kind names it declares, and
the transform qualifier keys it owns. Registration is therefore the single place a format
announces everything the core would otherwise have had to know about it.

**Precedent, and the alternative that was rejected.** Import-for-effect registration is Go's
own answer to this problem — `image/png`, `database/sql`, `net/http/pprof` — and it is the one
that lets a program pay for the formats it uses: a tool that only edits `.json` should not link
an HCL parser. `ext/all` exists so the common case is still one import.

The alternative — **explicit registration**, `hew.Register(hew.FormatYAML, yaml.Binding())`
at program start — was considered and **rejected**. It reads better in isolation, and it fails
in the only way that matters: a program that forgets one line gets `HEW021 unsupported-format`
for a `.yaml` file it can plainly see, at runtime, from a library that was linked and ready.
Import-for-effect makes the dependency and the capability the same fact. The cost is real and
stated: an unused blank import is invisible to `goimports`, and a linker that could have
dropped a binding cannot.

### A.7 Source resolution — CLI boundary only

```go
package hewsource // cmd/hew's helper; NOT imported by internal/hew

// Resolver turns a descriptor into bytes. The interface is deliberately tiny so the
// core never grows a dependency on it and a test can substitute a map.
type Resolver interface {
    // Resolve accepts: "path/to/file", "-" (stdin), or "REV:path" (git anchor,
    // git's own <tree-ish>:<path> convention). A literal path containing ":" is
    // disambiguated with a leading "./", as in git.
    Resolve(descriptor string) (content []byte, label string, err error)
}

// NewGitResolver resolves REV:path by invoking git plumbing as a SUBPROCESS
// (`git cat-file blob <rev>:<path>`). It never links a git library. If git is not on
// PATH, a descriptor containing ":" is a usage error, never a silent fallback to
// treating it as a filename.
func NewGitResolver(fsys afero.Fs, workdir string, stdin io.Reader) Resolver
```

### A.8 `hewfs` — the filesystem boundary

**Ratified ([O36](#p5--the-api-ratification-2026-08-14)): `hewfs` imports no host project.**
The draft called this section "the ctxloom adapter" and spelled its contract in ctxloom's own
vocabulary — `agent.WithFileLock`, `iox.WriteFileAtomicFs`. **That text is superseded.** hew is
a standalone standard with a Rust port as a peer implementation; a package in its reference
implementation that names one host project's locking helper is not a boundary, it is a
dependency pointing the wrong way. What §10.5 actually requires is *atomic temp-and-rename and
no backup file*, which is a property, not a helper — and a host that wants its own locking
wraps `hewfs`, exactly as ctxloom's `config-write` already wraps its writes in
`agent.WithFileLock` from the outside.

**`afero.Fs` is the filesystem type here and in A.0** ([O49](#p5--the-filesystem-surface-and-tdd-discipline-2026-08-14)),
which is the lineage this appendix already had: A.7's `NewGitResolver(fsys afero.Fs, …)` took
one from the first draft, and the P5 ratification restores it consistently rather than
introducing it.

```go
package hewfs // imports: stdlib, afero, and hew. Nothing else.

// ApplyFile applies every file section of a parsed patch, honoring §10.5:
// every section stages in memory, and the commit phase runs only if all
// staged successfully. There is no .rej file, no partial output, and NO
// BACKUP FILE — the write is a temp-and-rename, and a failed apply leaves
// every target byte-identical. (Rename atomicity is the afero backend's;
// see A.0's caveat.)
func ApplyFile(fsys afero.Fs, root string, tls []hew.TransformList, opt WriteOptions) ([]FileResult, error)

// ApplyTransforms is the same path for a hand-authored or generated .hewt
// document — the `hew apply --transforms` entry point, and the seam the
// corpus pins as `apply-ir`.
func ApplyTransforms(fsys afero.Fs, root string, tls []hew.TransformList, opt WriteOptions) ([]FileResult, error)

type WriteOptions struct {
    DryRun bool
    Format hew.FormatID // override detection for every target (§8.0)

    // RecordPath, if set, writes the §9.7 application record there.
    RecordPath string

    // AppliedAt pins the record's applied_at (§9.7, O37). Zero means "read
    // the environment, else now".
    AppliedAt time.Time

    // ReversalPath, if set, writes the reversal patch there after a
    // successful mutation (O40). Opt-in always; empty writes nothing.
    ReversalPath string
}

// Record is the application record (spec §9.7): what was executed, against which
// bytes. It is the input a future `hew revert` inverts, and the shape of the
// ownership record a host project's config writer otherwise lacks.
type Record struct {
    Version   int
    AppliedAt time.Time
    Patch     RecordPatch
    Targets   []RecordTarget
}

type RecordPatch struct{ Source, Digest string }

type RecordTarget struct {
    Target     string
    Format     FormatID
    Before     string // "sha256:..." of the bytes as read
    After      string // "sha256:..." of the bytes as written
    Committed  bool
    Transforms []hew.ResolvedOp
}

func MarshalRecord(r Record) ([]byte, error)
func UnmarshalRecord(src []byte) (Record, error)

type FileResult struct {
    Target   string
    Changed  bool
    Written  bool
    Reversal string // path of the reversal patch written, or "" (O40)
    Ops      []hew.Transform
}
```

**The reversal patch** ([O40](#p5--the-api-ratification-2026-08-14)). With `ReversalPath` set,
a successful mutation also writes `diff(after → before)` — a real `.hew` file, with real
context lines at the §9.4-R2 radius, produced by the same differ and renderer `hew diff` uses.
It is not a backup and it does not weaken §10.5's no-backup rule: a backup is a copy of a file,
opaque and whole; a reversal patch is a **statement of what to undo**, reviewable in a pull
request, and refusing to apply if the file has drifted since. Applying it *is* the undo:

```
hew apply --reversal config.yaml.undo.hew migrate.hew   # forward, keeping the way back
hew apply config.yaml.undo.hew                          # back
```

This is the concrete half of [O14](#ruled-by-the-human-2026-08-14)'s deferred `hew revert`,
and it is deliberately the *cheap* half: it needs no inversion rules (what is the inverse of an
`add` with `on_conflict: keep`?), because it inverts nothing — it diffs two byte images that
both existed. `hew revert <record.hewt>`, which must answer the inversion question, stays
future work.

**Pinned `applied_at`** ([O37](#p5--the-api-ratification-2026-08-14)). §9.7's record carries a
wall clock, which makes an otherwise deterministic artifact unreproducible: two identical
applies produce two different records, and a build that commits its records has a diff on every
run. hew therefore honors the reproducible-build convention rather than inventing one:

| Source | Precedence |
|---|---|
| `WriteOptions.AppliedAt` (library) / no CLI flag | 1 — an explicit caller wins |
| `HEW_APPLIED_AT`, an RFC 3339 timestamp | 2 |
| `SOURCE_DATE_EPOCH`, seconds since the Unix epoch | 3 — the cross-ecosystem convention |
| the system clock | 4 — the default |

The value is normalized to RFC 3339 UTC and written **byte-exactly** as `applied_at`, so a
record built twice from the same inputs and the same pin is the same file. A malformed
`HEW_APPLIED_AT` or `SOURCE_DATE_EPOCH` is a usage error (exit 2), never a silent fallback to
the clock — a pin that quietly does not pin is worse than no pin.

### A.9 Errors — attached to the component that raises them

```go
type Code string

const (
    // Parser layer — raised by Parse/UnmarshalTransforms, before any target is opened.
    CodeParse             Code = "HEW001"
    CodeUnsupportedFormat Code = "HEW021"

    // Resolver / IO layer — raised by hewfs and hewsource.
    CodeTargetParse Code = "HEW002"
    CodeTargetPath  Code = "HEW003"

    // Applier layer — raised while evaluating a transform list against a document.
    CodeStaleTarget      Code = "HEW010"
    CodeAssertionFailed  Code = "HEW011"
    CodeAmbiguousMatch   Code = "HEW012"
    CodeNoMatch          Code = "HEW013"
    CodeAlreadyExists    Code = "HEW014"
    CodeInexpressible    Code = "HEW020"
    CodeConflict         Code = "HEW030"
    CodeAnchorAmbiguity  Code = "HEW040"
    CodeSurfaceAmbiguity Code = "HEW041"
)

// Component is asserted by the corpus (`error_seam`): a code surfacing from the
// wrong component means that component is deferring work it should have refused.
// Spelled "Component" rather than the draft's "Layer" because it names one of the
// four components of §9 plus the two boundaries around them, and the corpus's own
// error_seam vocabulary already says "parser", "applier", "differ".
type Component int

const (
    ComponentParser Component = iota
    ComponentResolver
    ComponentApplier
    ComponentDiffer
    ComponentRenderer
    ComponentCLI
)

// Error is the one error type Hew returns. Every field is part of the contract the
// corpus asserts on.
type Error struct {
    Code       Code
    Component  Component
    Target     string
    PatchFile  string // the .hew file, filled in at the CLI boundary (§10.3)
    Path       string
    PatchLine  int
    TargetLine int
    Want, Got  string
    Detail     string
}

func (e *Error) Error() string
func As(err error) (*Error, bool)
```

### A.10 Corpus runner (test-only)

The corpus is data; this is the Go runner. It is a **library** — nothing in it touches
`*testing.T` — so the `go test` frontend and the godog acceptance suite (§13.8) are two thin
frontends over one engine, and the two cannot disagree about what a seam means.

```go
package harness

type Seam string

const (
    SeamParse   Seam = "parse"    // patch.hew                -> transforms.hewt
    SeamApplyIR Seam = "apply-ir" // transforms.hewt + target -> expected
    SeamE2E     Seam = "e2e"      // patch.hew + target       -> expected
    SeamRender  Seam = "render"   // transforms.hewt -> .hew -> transforms.hewt (RT2)
    SeamDiff    Seam = "diff"     // old + new                -> expected.hew
    SeamCLI     Seam = "cli"      // argv                     -> exit code + streams
)

// Binding wires the implementation under test in. Every hook speaks canonical
// .hewt bytes at the IR boundary, pinning the IR exactly where the corpus does.
// A nil hook behind a DECLARED seam is a failure unless a skip rule covers it —
// that is the milestone mechanism, and it is why wiring a hook and deleting its
// skip rule must happen in the same change.
type Binding struct {
    ParseToHewt func(patch []byte) ([]byte, error)
    CanonHewt   func(hewt []byte) ([]byte, error)
    ApplyHewt   func(hewt, target []byte, format string) ([]byte, error)
    ApplyPatch  func(patch, target []byte, format string) ([]byte, error)
    RenderHew   func(hewt []byte) ([]byte, error)

    // DiffToHew's `target` is the OLD side's file name (O39): the produced
    // patch applies to old, so that is the label its `--- ` line carries.
    DiffToHew func(old, new []byte, format, target string) ([]byte, error)

    // RunCLI runs the CLI in-process. env carries the case manifest's `env:`
    // block (§13.4) — the seam through which a case pins HEW_APPLIED_AT (O37).
    RunCLI func(argv []string, dir string, env map[string]string,
        stdin io.Reader, stdout, stderr io.Writer) int
}

func Discover(corpusDir string) ([]*Case, []error)
func (e *Engine) RunSeam(c *Case, seam Seam) Outcome

// The skip registry of §13.7, with both ratchets: Unused() names rules that
// matched nothing (dead rules must be deleted), and NoSkips turns every match
// into a failure (HEW_CORPUS_NO_SKIPS=1, the end-state gate).
func NewSkipRegistry(rules []SkipRule, noSkips bool) *SkipRegistry
func (r *SkipRegistry) Unused() []SkipRule

// ComputeCoverage names every v0 catalog operation with no case (§13.6 rule 7).
func ComputeCoverage(catalog Catalog, cases []*Case) Coverage
```

---

## Appendix B — the CLI surface

**Shape: standalone binary `hew`**, ratified as [O1](#ratified-by-the-coordinator-2026-08-14)
and settled by construction — hew is its own repository with `go/` and `rust/` trees. (Family
precedent: `ltk`, `taskloom`, `harp`.)

### B.1 `hew apply`

```
hew apply [flags] <patch.hew>...
hew apply -                                    read the patch from stdin
hew apply --transforms <file.hewt>...           apply a transform list directly
```

| Flag | Meaning |
|---|---|
| `-t, --target FILE` | Override the patch's `--- ` target. Legal only with exactly one file section. |
| `-i, --in-place` | Write the result back to the target. Default when the patch declares a target and no `-o` is given. |
| `-o, --output FILE` | Write the result here instead. Requires a single file section. `-o -` writes to stdout. |
| `-R, --root DIR` | Resolve target paths under DIR. Default: cwd. |
| `--transforms FILE` | Read a canonical transform list (§9.6) instead of `.hew` notation. **This is the authoring path for moves and copies** (Appendix C) and the only way to reach OP-21/OP-22/OP-07/OP-36. Mutually exclusive with positional `.hew` arguments. Flag name at [O21](#decisions-and-residual-open-questions). |
| `--record FILE` | Write an application record (§9.7) after a successful apply: the resolved transforms actually executed, plus before/after digests per target. Not written by default. |
| `--reversal [FILE]` | After a successful mutation, **also** write the reversal patch — `diff(after → before)` as a real `.hew` file with real context lines. Opt-in always; no flag, no file. With no value the name is derived from the target as `<target>.undo.hew`; with several targets, one file per target. Applying it is the undo ([O40](#p5--the-api-ratification-2026-08-14)). |
| `--dry-run` | Do everything including matching; write nothing; exit as if written. |
| `--ops` | Print the **resolved** RFC 6901 op list (§9.2) to stdout and write nothing. |
| `--transforms-out FILE` | Write the **abstract** transform list (§9.6) and write no target. The parser seam, exposed. |
| `--format FMT` | Override format detection for every target. |
| `--format-out json` | Machine-readable diagnostics and results on stdout. |
| `-q, --quiet` | Suppress the per-file success lines. |

**Environment.** `hew apply` reads exactly two environment variables, both governing the
record's `applied_at` (§9.7, [O37](#p5--the-api-ratification-2026-08-14)): `HEW_APPLIED_AT`
(RFC 3339) and, if that is unset, `SOURCE_DATE_EPOCH` (Unix seconds). Either one malformed is
exit 2. Nothing else about hew's behaviour is reachable through the environment — a patch tool
whose *effect* depends on invisible input is the thing this format exists to refuse; a
timestamp is metadata about the run, not part of it.

### B.2 `hew diff` (P4)

```
hew diff [flags] <old> <new>
hew diff HEAD:config.yaml config.yaml          the canonical invocation
hew diff HEAD~3:.mcp.json .mcp.json
hew diff old.toml -                            new side from stdin
```

Both arguments are **source descriptors** (§9.5), not file paths: a working-tree path, `-`
for stdin, or a `REV:path` git anchor following git's own `<tree-ish>:<path>` convention. Git
anchors are resolved by invoking git plumbing as a subprocess; the library core has no git
awareness at all.

#### B.2.1 Which side the `--- ` line names

**Ratified ([O39](#p5--the-api-ratification-2026-08-14)): `hew diff old new` stamps the OLD
side's label.** The `--- ` line names the file the patch **applies to**, and a patch applies to
`old` — this is `patch(1)`'s own convention (`--- old`, `+++ new`), and it is what RT1 already
says in symbols: `apply(parse(render(diff(old, new))), old) == new`. The patch is a function
*from* old; naming its result would name a file the applier will never open.

Two corollaries, both stated so no implementation has to guess:

- **A git anchor renders as its path component.** `hew diff HEAD:config.yaml config.yaml`
  stamps `--- config.yaml`, not `--- HEAD:config.yaml`. A `--- ` line is a *target path* (§2.2,
  one literal path per section, [O13](#ratified-by-the-coordinator-2026-08-14)); a revision is
  not a path an applier can open, and writing one there would produce a patch that cannot
  apply anywhere. The revision is provenance, and provenance belongs in the patch's leading
  `#` comment if it belongs anywhere.
- **A stdin old side has no name to give**, so the new side's label is used, and if that is
  also stdin the invocation is a usage error — a patch with no target is not a patch (§2.2).

#### B.2.2 Two identical inputs

**Ratified ([O38](#p5--the-api-ratification-2026-08-14)): `hew diff` of identical inputs emits
a preamble-only patch and exits 0.** The output is exactly:

```
hew: 1

--- config.yaml format=yaml
```

— a well-formed `.hew` file with a preamble, one file section, and zero hunks. Not an empty
file, and not nothing at all.

The reason is composition. `hew diff a b > p.hew && hew apply p.hew` must not be a shell
pipeline that breaks when the answer is "no change": emitting zero bytes produces a file that
`hew apply` then refuses as `HEW001` (§10.2), turning "nothing changed" into an error one
command later — the exact silent-mode-flip that a tool with an honest exit contract must not
have. §10.2 is amended in the same ruling so that the artifact `diff` produces is one `apply`
accepts, which is the only relationship between the two verbs that can be called a round trip.

| Flag | Meaning |
|---|---|
| `-U, --context N` | Sibling context radius (§9.4-R2). Default `1`. `all` emits every sibling. **This is a strictness dial, not a verbosity dial** — context lines compile into assertions (§9.0), so a smaller radius makes the patch *weaker*, and the help text says so. |
| `--format FMT` | Override format detection. Both sides must be the same format. |
| `--key-fields a,b,c` | Candidate identity fields for keyed-array addressing (§9.4-R4). Default `name,id,key`. |
| `--transforms-out FILE` | Emit the transform list instead of `.hew` notation. |
| `-o, --output FILE` | Write the patch here. Default stdout. |

### B.3 Exit codes

patch(1)-shaped, three states, no more:

| Code | Meaning |
|---|---|
| `0` | Every hunk applied. Files written (or, with `--dry-run`, would be). For `diff`: a patch was produced, and it may be empty. |
| `1` | The patch did not apply: `HEW010`–`HEW041`. **No file was modified** (§10.5). |
| `2` | Trouble: usage error, unreadable patch (`HEW001`), unreadable/unparseable target (`HEW002`/`HEW003`), unresolvable descriptor, `git` absent for a git anchor, unsupported format (`HEW021`), I/O failure. |

Note the deliberate difference from `patch(1)`: exit 1 there means "some hunks failed and
`.rej` files were written". Here it means "nothing happened, and here is why". A caller that
treats nonzero as "unknown state" is being unnecessarily defensive; Hew's contract is that
nonzero means unchanged.

Stdout carries results (`--ops`, `--transforms-out`, `-o -`, `--format-out json`, `hew diff`).
Stderr carries human diagnostics. Never both on stdout.

---

## Appendix C — operations the mirror grammar cannot express (non-normative)

Hew v0 is one human-authoring notation, and one notation cannot express everything. This
appendix names what the mirror grammar cannot say — and, since the transform-list IR is itself
an accepted input (§9.6), **what to write instead**. The contingency costs no new surface,
now or ever: the IR had to be specified for the corpus and for interop regardless.

**Nothing here is a future spec revision.** Everything below is doable today.

### C.1 Node move (OP-21) and rename (OP-07)

Relocating a node while preserving its identity. Shape-mirroring has no notation for "this
node, but over there": the two locations are in two different regions of the document, and a
margin marks a line, not a correspondence between two lines far apart.

**Write instead:**

```yaml
hew-transforms: 1
target: config.yaml
format: yaml
transforms:
  - op: test
    path: /server/host
    value: localhost
  - op: move
    from: /server/host
    path: /network/host
```

`hew apply --transforms move.hewt`. The applier preserves the subtree's source bytes and its
attached comments (§9.6), which is exactly what the mirror-grammar substitute cannot do.

**The mirror-grammar substitute is legal and lossy.** A `-` at the old path and a `+` at the
new path applies fine — it just means delete-and-add. Comments attached to the removed node
are lost, byte-exact formatting is lost, and a large subtree must be restated in full. Hew does
**not** detect the pattern and does not warn: see [O16](#decisions-and-residual-open-questions) for
why that was decided rather than assumed.

**Why the mirror grammar was not extended instead.** The config-patching review inventoried
eight mechanisms across five engines and **not one performs a move**. A move is what a schema
migration does, and ctxloom's schema migrations are Go code with a version gate. Paying for
cross-hunk correspondence syntax in the human notation, to serve an operation the human
notation's users do not perform, is the trade this design declines.

### C.2 Node copy (OP-22, OP-23)

Same reasoning, same remedy: `{op: copy, from: …, path: …}` in a `.hewt`. The mirror-grammar
substitute — restating the value in full under `+` — is a *transcription*, and it drifts the
moment either side changes.

### C.3 Sub-block Markdown editing (OP-46)

Changing one line of a paragraph without restating the paragraph (§8.6). **Rejected outright,
not deferred**: Markdown blocks are the addressing unit and there is no path to a sentence.
The IR cannot express it either. `HEW020`.

### C.4 TOML surface migration (OP-39)

Rewriting `[a.b]` into `a.b = {…}` or vice versa (§8.4 rule 4). Deferred, and **not** reachable
via the IR — Hew edits values at whichever surface exists; restructuring surfaces is a
formatter's job. `HEW020`, and see [O10](#decisions-and-residual-open-questions).

### C.5 Cross-file operations

Moving a key from one file to another, or asserting a relationship between two targets. Each
transform list names one target (§9.6) and each file section is independent (§10.5). This is
the largest of the five and the one most likely to be requested first, since ctxloom's real
job is keeping five engines' configs consistent. Not reachable via the IR today;
[O12](#decisions-and-residual-open-questions) and [O13](#decisions-and-residual-open-questions) are its
prerequisites.

---

## Decisions and residual open questions

Every fork resolved by judgment while drafting was listed here for ratification. **The
coordinator ratified 26 of them on 2026-08-14**, and the human ruled the headline questions
directly — four at drafting time, and eleven more (O30–O40) when Appendix A's proposed API was
ratified against the shipped implementation. What remains open is four items, listed last —
each genuinely needs evidence that does not exist yet.

### Ruled by the human, 2026-08-14

| # | Question | Ruling |
|---|---|---|
| **O3** | Is re-applying a patch an error? | **Strict default.** An unannotated hunk has `patch(1)` semantics and re-applying fails loudly (`HEW014`/`HEW011`, §10.6). `! idempotent` opts a hunk into convergence; the `idempotent:` preamble pragma (§2.1) sets it patch-wide for a generating tool; `! strict` opts a single hunk back out. Loud failure is the core discipline; convergence is a **visible choice** in the patch text, never a property of how the applier was invoked. |
| **O12** | Multi-file atomicity. | **All-or-nothing across targets** (§10.5). Every section stages in memory; the commit phase runs only if all staged successfully. Any detectable failure leaves every target byte-identical — which is what exit 1 already promised. The crash-mid-commit prefix is documented as the honest residual, with the application record (§9.7) making the resulting state discoverable rather than unknown. |
| **O14** | Where does the ledger meet hew? | **Spec the application record, defer revert** (§9.7). `hew apply --record <path>` emits a `.hewt`-shaped record: the resolved transforms actually executed plus before/after digests per target. `hew revert` and ledger integration are named as future work built on the record, not v0 — answering them now would bind the standard to one host project's ownership model. |
| **O20** | Git anchors via subprocess, not a linked library. | **Subprocess** (§9.5, Appendix A.7). Ruled directly; the cost — no `REV:path` in a container without `git`, surfacing as a usage error — is accepted and stated. |

### Ratified by the coordinator, 2026-08-14

Each was a stated lean or provisional decision in the draft; each is now decided, with the
reason that decided it.

| # | Question | Decision and rationale |
|---|---|---|
| **O1** | Standalone `hew` binary vs `ctxloom patch` subcommand. | **Standalone.** Settled by construction: the human created `~/workspace/hew` as its own repository with `go/` and `rust/` trees and a corpus-driven justfile. A standard intended to outlive its host project cannot ship as that host's subcommand. |
| **O2** | Doc filename convention. | **`docs/hew-spec.md`** at the standalone repo root. Settled by the same repository. This is a spec, not a design note, so it does not take the `*.design.md` convention of ctxloom's `docs/design/`. |
| **O4** | JSONC-by-well-known-name. | **The mechanism is normative; the list is binding data.** §8.0 specifies that a binding carries a `DetectRule` with extensions and well-known names; the names themselves ship with the binding and are always overridable by an explicit `format=`. A hard-coded name list in the *standard* would age badly; one in an implementation's detection table is just a default. |
| **O5** | Extending RFC 6901's escape set with `~2` for `=`. | **Adopted.** One character, unambiguous, and it keeps segments readable. The alternative — quoting the whole segment — collides with the label-segment syntax HCL needs (§4.3), which would make the address grammar ambiguous exactly where it must not be. |
| **O6** | Key-match comparison operators. | **Equality only in v0.** go-patch, the one proven prior art for this idiom, has only equality. Regex or multi-field match is a compatible future extension to the segment grammar; shipping it now would mean designing a match language against zero measured demand. |
| **O7** | Markdown kind-scoped block ordinals (`code:0`) in a path. | **Allowed, and scoped to Markdown's fate.** Markdown has no keys at all, so the dialect is unusable without them. Revisited only if [O29](#residual--genuinely-open) keeps Markdown in the implement tier; if Markdown drops, this question drops with it. |
| **O8** | `! match ord=` on an unambiguous path. | **`HEW001`.** Consistent with §6.4.3's MUST: an unnecessary ordinal is a latent misapply waiting for the file to grow a sibling. A patch that breaks when the file *stops* being ambiguous is a patch that told you something changed, which is the contract. |
| **O9** | Unknown preamble keys and unknown `!` directives. | **Hard failure `HEW001`.** Fail-loud is the project's discipline and the format's whole premise. The version field carries the forward-compat burden deliberately: a reader that silently ignores what it does not understand is a reader that silently misapplies. |
| **O10** | TOML surface migration (`[a.b]` ↔ `a.b = {…}`). | **Not a patch operation** (`HEW020`, §8.4 rule 4, Appendix C.4). Restructuring surfaces without changing values is a formatter's job. hew edits at whichever surface exists and never adds a second one. |
| **O11** | Collect-all-failures mode. | **First error wins for apply** (§10.4). A later `--all-errors` *diagnostic* flag is compatible and would not weaken the apply contract, since apply is all-or-nothing regardless; it is simply not v0. |
| **O13** | Globs or multiple paths on `--- ` target lines. | **No — one literal path per section.** With O12's cross-target atomicity, listing targets explicitly is cheap and the patch says exactly what it touches. A glob makes the set of affected files depend on the filesystem at apply time, which is precisely the kind of invisible input this format refuses elsewhere. |
| **O15** | Corpus location. | **`corpus/` at the standalone repo root.** Settled by the human's repository; `tests/` would have implied Go-test-only ownership of an artifact the Rust port consumes as a peer. |
| **O16** | Is delete-and-add acceptable semantics for a move? | **Yes, undetected.** hew does not pattern-match a `-`/`+` pair into a move: the detection is a heuristic, a false positive would block a legitimate delete-and-add, and hew cannot know what the author meant. The cost is named in Appendix C.1 — lost comments, lost byte-exact formatting, full restatement — and the remedy is the transform list, which is one flag away. |
| **O17** | Should `! optional` exist at all? | **Yes, with a linter warning** (§7.6). Real files have genuinely conditional content. It is the one construct that reintroduces the silent no-op, so it is discouraged in the spec, warned on by a conformant linter, and pinned by a corpus case so the warning has something to fire on. |
| **O18** | Differ identity-field candidates. | **Same treatment as O4:** the *preference rule* is normative (§9.4-R4 — present on every element, scalar, unique), the candidate list is binding data with a `--key-fields` override. The rule is what conformance turns on; the list is a default. |
| **O19** | Default context radius = 1 sibling. | **1.** The smallest radius that still pins insertion position (§6.2), which is the property context must carry. Unified diff's 3 is a line-oriented number with no structural meaning here. Because context compiles into assertions (§9.0), a larger default would silently make every generated patch stricter than its author asked for; `-U` raises it deliberately. |
| **O21** | Name and extension of the serialized IR. | **`.hewt`, `hew apply --transforms`.** The extension mirrors `.hew` so the pair reads as one family; the flag names the concept rather than the file type. Sniffing the positional argument was rejected — two input grammars with invisible precedence is how a tool starts guessing. |
| **O23** | Comment attachment on added nodes. | **Yes** (OP-30, OP-31), `HEW020` for JSON which has no comments. No surveyed patch format can do this, and ctxloom's own managed writers require it — every managed region carries an explanatory header. The implementation cost lands on the appliers, which is the right place for it. |
| **O25** | *(moved to residual — see below)* | |
| **O26** | `replace` vs `add`+`on_conflict: replace` overlap. | **Both kept.** They differ in *precondition*, not effect: `replace` requires the node (`HEW013` if absent), `add` does not. A precondition is orthogonal to an effect, so this is not the duplication the reduction forbids. Removing `replace` would make the most common operation a two-record composition. |
| **O27** | Two tolerance flags rather than one. | **Both kept.** `optional` tolerates an absent node; `idempotent` tolerates an already-applied state. Different conditions, and a single `tolerate:` enum would spell them less obviously while saving one field. |
| **O28** | Comment addresses (`/x/#0`, `/x/timeout/#t`). | **Kept.** They had to exist for OP-32/OP-33 regardless, and their existence is what let the comment-attachment qualifier reduce away entirely (§11.10). The positional-drift hazard is real and bounded: a comment address is only used by a patch that is editing that comment, and such a patch asserts the comment's text, so a shifted ordinal fails loudly rather than editing the wrong comment. |
| **O22, O24, O29** | *(residual — see below)* | |

### P5 — the API ratification, 2026-08-14

Appendix A was checkpoint-1 material: signatures for review. The implementation of P1–P4
answered some of its questions by construction and contradicted it in four places, and the
adoption slice (ctxloom's `config-write`, which builds `[]Transform` by hand because nothing
better exists) named the surface that was missing. **The human ruled all eleven on 2026-08-14.**

O30–O33 ratify deviations the implementation already made; O34–O37 and O40 decide surfaces
that are specified here and **not yet built**; O38 and O39 amend behaviour that is built.

| # | Question | Ruling |
|---|---|---|
| **O30** | `Parse` returns `*Patch` with a `FileSection`/`Hunk` object graph, or the IR directly? | **The IR directly: `Parse([]byte) ([]TransformList, error)`** (A.2). The three object types stood between the caller and the only thing every caller wanted, and their accessors were either fields of `TransformList` already or questions nobody asked. The one capability this drops is **hunk introspection**, which a linter ([O17](#ratified-by-the-coordinator-2026-08-14), §7.6) would need — recorded as deferred, and reachable later as an additive parser output rather than a change to `Parse`. |
| **O31** | May a `Transform` carry provenance the IR does not serialize? | **Yes — `PatchLine`, `AnchorPath`, `AnchorLine`** (A.1). A resolution failure inside an anchor is the *anchor's* failure and must be reported at the `@@` line, which a transform that does not know its hunk cannot do. Non-serialized, not compared by `Equal`, therefore invisible to every corpus byte comparison — provenance in the strict sense, not content. |
| **O32** | Is `! idempotent` legal on a `test`? | **Yes** (A.1, §7.5). §7.5's rule is stated over the *hunk*, and a hunk lowers to a before-image `test` **and** a write; a tolerance riding only the write would leave the paired `test` failing loudly and convergence unreachable. `copy` is the one exclusion — it asserts nothing about its destination. |
| **O33** | Does `ResolvedOp` carry every assertion mode, or only the ones RFC 6902 has? | **Every one — `Exhaustive` and `NodeKind` included** (A.1). The resolved list is what `--record` embeds (§9.7), and a record that drops an assertion the applier really evaluated under-reports what happened to the file. A record must not under-report. |
| **O34** | What does a program that wants to edit a config file call? | **A fluent document API — `hew.Open` → `.At(path)` → operation → terminal** (A.0). Three rules, each a restatement of one the format already has: format appears only at the open boundary and is detected from the **name** (§8.0, never sniffed, overridable by `hew.As` exactly where a patch would say `format=`); addressing is the **§4 path**, one language in two encodings, with typed segment constructors for computed paths and deliberately no navigation method-chain; and **reads become asserts** — `Replace` records `test`(current) + `replace`, which is §9.1's lowering with the before-image taken from the open document. `Set`/`Default` are the documented unasserted forms (OP-03/OP-04). The builder's output is ordinary IR, indistinguishable from parsed-patch IR, and `RenderPatch` turns any edit into a reviewable `.hew`. |
| **O35** | Registry, or a `switch` per call site? | **Registry, with bindings self-registering from `init()` on import** (A.6). Import-for-effect is Go's own answer (`image/png`, `database/sql`) and makes "linked" and "capable" the same fact, so a JSON-only tool links no HCL parser; `hewall` keeps the common case to one import. **Explicit registration was considered and rejected**: it reads better in isolation and fails by giving `HEW021` for a plainly-visible `.yaml`, at runtime, from a library that was linked and ready. Cost stated: an unused blank import is invisible to `goimports`. |
| **O36** | What may `hewfs` import? | **Nothing from any host project** (A.8). The draft spelled §10.5 in ctxloom's vocabulary (`agent.WithFileLock`, `iox.WriteFileAtomicFs`); that text is superseded. §10.5 requires a *property* — atomic temp-and-rename, no backup file — and a host that wants its own locking wraps `hewfs` from outside, as `config-write` already does. |
| **O37** | Is `applied_at` a wall clock, or pinnable? | **Pinnable: `HEW_APPLIED_AT`, then `SOURCE_DATE_EPOCH`, then the clock** (§9.7, A.8). Every other field of a record is a function of its inputs; the timestamp was the one thing making a deterministic artifact differ per run, which breaks the callers most likely to keep records. A malformed pin is exit 2, never a silent fallback — a pin that quietly does not pin is worse than none, because the artifact still looks reproducible. |
| **O38** | What does `hew diff` emit for identical inputs, and what does `hew apply` do with it? | **A preamble-only patch, which applies as a no-op** (B.2.2, §9.4-R8, §10.2 amended). Emitting zero bytes produces a file `apply` then refuses as `HEW001`, turning "nothing changed" into an error one command later. §10.2's line moves to *did the author say which file this is about*: `hew: 1` + a `--- ` line + no hunks is a complete statement and exits 0; zero bytes, or a preamble with no file section, stays `HEW001`. |
| **O39** | Which side does `hew diff old new` stamp on the `--- ` line? | **The OLD side** (B.2.1, §9.4-R7). `patch(1)`'s convention, and what RT1 already says in symbols — the patch is a function *from* old, so naming its result would name a file the applier never opens. Corollaries ruled here so nothing guesses: a git anchor renders as its **path component** (`HEAD:config.yaml` → `--- config.yaml`), because a `--- ` line is a target path ([O13](#ratified-by-the-coordinator-2026-08-14)) and a revision is not one; a stdin old side falls back to the new side's label, and both-stdin is a usage error. |
| **O40** | How does a caller undo an apply, given `hew revert` is deferred? | **`hew apply --reversal [FILE]`** (A.8, B.1). On successful mutation, also write `diff(after → before)` as a real `.hew` with real context lines — default `<target>.undo.hew`, opt-in always. The reversal artifact **is** the revert story: applying it is the undo. It is deliberately the cheap half of [O14](#ruled-by-the-human-2026-08-14) — it needs no inversion rules because it inverts nothing, it diffs two byte images that both existed. It is not a backup and does not weaken §10.5: a backup is an opaque copy, a reversal patch is a reviewable statement that refuses to apply to a file that has drifted. |

### P5 — the addressing-language review, 2026-08-14

A critical review of §4 ran in parallel with the API ratification and found one **live
defect** and six gaps. **The human ruled all seven.** O41 is not a design improvement — it
fixes addresses that are wrong on disk today.

| # | Question | Ruling |
|---|---|---|
| **O41** | `Path.String()` is not injective: a key like `@scope/pkg` renders `/@scope~1pkg` and reparses as a **marker**; a digit-only key reparses as an **index**; `-` as **append**; a trailing `?` flips a match into create-if-absent; an empty key vanishes into the root. | **A quoted-key segment form, plus a normative canonical-rendering rule** (§4, §4.1). A quoted segment resolves **by container kind** — block set → label (§4.3, unchanged), mapping → key — and `String()` MUST emit it for any key whose bare spelling would not reparse as the same segment. **`String()`↔`ParsePath` becomes a stated bijection.** This overturns one clause of [O5](#ratified-by-the-coordinator-2026-08-14)'s reasoning ("quoting collides with the label syntax") and not its decision: the two contexts are disjoint, because a container is a block set or a mapping and never both, and the resolver knows which at every step. **This is a live defect, not a hypothesis**: the differ builds key segments straight from the target's own keys and `.hewt` stores every address as this text, so a `package.json` with a scoped dependency produces a transform list whose addresses already mean something else. Also corrected here: §4.1's "RFC 6901, unchanged" was false at the root — hew spells the document `/` where RFC 6901 spells it `""`, and RFC's empty-key member had no hew spelling at all until this form. |
| **O42** | `Scalar.pathString()` quotes only when `Quoted` is set, so a programmatic string scalar `"8080"` renders `name=8080` and re-decodes as a **number**. | **Force-quote any string scalar whose bare rendering would not reparse identically** (§4.2) — the same bijection rule as O41, one level down. And in the API (A.0), `MatchKey(field, value string)` always produces a quoted string scalar, with `MatchKeyNumber`/`MatchKeyBool`/`MatchKeyNull` for typed comparisons, so **the comparison's type is visible at the construction site** rather than inferred from a value's shape. The differ already dodges this by quoting every string it emits, which is the fix generalized rather than invented. |
| **O43** | How does a caller get a runtime value into a path? | **Typed holes: `doc.At("/servers/{}", hew.MatchKey("name", v))`** (A.0). The unit of substitution is a **typed segment**, never an escaped string, because a segment knows whether it is a key, a label, a field or a value and a raw string does not. String concatenation into `At` is documented as a **defect**, not guarded against. Recorded **rejected**: printf-style character-level escaping (an escaper cannot know which of the four things its argument stands for, and they escape differently), and concatenation-detection heuristics (false positives on legitimately-computed paths train the reader to route around the warning). |
| **O44** | Should v0 reserve the tokens its own named extensions would need? | **Yes, two** (§4.7). A key-match **field** ending `<`, `>` or `!` is `HEW001`, reserved for [O6](#ratified-by-the-coordinator-2026-08-14)'s comparison operators — `count>=5` **parses today** as a match on a field named `count>`, a working address a later `>=` would silently reinterpret. A bare `*` segment is `HEW001`, reserved for a wildcard. Both are affordable **only because O41 gives every literal a spelling**: `*` is a real key in `tsconfig.json`, written `/paths/"*"`. The quoted form is named as the permanent escape hatch for any token this spec reserves later, so a reservation can never make a real document unpatchable. |
| **O45** | §6.4.3 rule 1 recommends key-match addressing as the mitigation for repeated HCL blocks, but §4.2 restricted key-match to sequences and no binding implements it over block sets. | **Extend §4.2 to same-`(type, labels)` block sets** — `/resource/"aws_instance"/name="web"`. The spec was recommending a remedy for its own most dangerous construct ([O25](#residual--genuinely-open)) without providing a spelling for it. Extending strictly *reduces* ordinal usage: the ordinal stays legal and stays the visible admission §4.3 says it is, but stops being forced wherever the blocks differ in any attribute. Implementation pending. |
| **O46** | A key-match that hits nothing reports "no match", which sends the author looking for an element that is in front of them. | **`HEW013` MUST name the nearest miss and its type** (§10.3) — `1 element has version="1.0" (string) — quote the value to match a string`. Because §4.2 compares after decoding, a match can fail for a reason that is invisible in the address, and the remedy (quote the value) is not guessable from "no match". |
| **O47** | At a comment address, is `#<n>` on an `add` a selector or a position? | **A selector on `test`/`remove`/`replace`; a POSITION on `add`, with `#-` to append** (§4.5b, with the per-projection worked example the section lacked). The two readings genuinely diverge only here, and leaving it to be guessed means two conformant implementations insert in different places. `#t` is never an `add` position — a member has one trailing comment, so an `add` where one exists is `HEW014` and `! upsert` replaces it. |

### P5 — the format-isolation audit, 2026-08-14

**Ruled by the human, 2026-08-14.** One ruling, and it is architectural rather than a point
fix — it changes where a whole class of construct lives.

| # | Question | Ruling |
|---|---|---|
| **O48** | How much may the core know about any particular format? | **As close to nothing as the design allows, every construct challenged, and whatever genuinely survives as format-specific lives in `ext/<format>`** (§8.8, with the full audit table). The core grammar keeps five universal lexical shapes — key, index, append, key-match, quoted — and everything else becomes an **extension-claimed segment form** whose interpretation the registered format supplies. The quoted segment (O41) is the proof the mechanism works: one lexical form, label against a block set and key against a mapping, resolved by the container the resolver already has. Verdicts in brief: `SegLabel` **restructured** (it was never a kind, only a quoted segment against a block set); `SegHeading`/`SegBlock`/`SegMarker`/`BlockKind` **relocated to `ext/markdown`**, which is what turns [O29](#residual--genuinely-open)'s severability claim into deleting a directory; `SegComment` **restructured** into the `#` form plus a shared `ext/comment` helper, because comment addressing is capability-scoped, not universal and not single-format; `KindBlock`/`KindSection` **restructured** into extension-declared kind names; `Anchor`/`Surface` **restructured so ownership moves and the `.hewt` spelling does not**; §8.0's detection table **relocated** to the extensions and demoted to non-normative; and the binding packages **renamed `hew<format>` → `ext/<format>`** so the isolation is visible in the import path (breaking, landing with the P5 implementation; consumers pinned at `v0.1.0` are unaffected until they upgrade). The audit also found a **live defect**: `FormatID.Valid()` hardcodes the six v0 formats, so a correctly-registered seventh extension is refused by the parser before any binding is consulted — validity must be a registry lookup, which is what makes §12's documented-only families addable without a core change. Two tensions are recorded rather than forced: `Transform`'s serialized fields cannot leave the one IR without losing deterministic canonicalization and RT2, so the core retains two format-specific *key names*; and path parsing becomes format-aware, which the ruling reconciles with §9 by drawing the line at mechanics (no target, no document, no I/O) rather than at knowledge of a segment grammar. |

### P5 — the filesystem surface and TDD discipline, 2026-08-14

**Ruled by the human, 2026-08-14.** Two rulings that close the last open questions before
implementation starts: what the API's filesystem parameter is, and how the implementation is
allowed to proceed.

| # | Question | Ruling |
|---|---|---|
| **O49** | What is the filesystem type across A.0 and A.8, and may a caller hand hew a file it has already opened? | **`afero.Fs` throughout, and yes — a three-constructor family** (A.0, A.8). `io/fs.FS` is **rejected**: it is read-only, so `Doc.Write()` cannot exist on it, and an API whose write path is unreachable from its own open path is not a filesystem abstraction. `afero` is writable, is mockable in-memory (`MemMapFs`, which O50 then requires the unit suites to use), and **is already the consumer's abstraction** — ctxloom's `config-write` imports afero today, so this adds no dependency to the program most likely to adopt A.0 first. It is also the lineage this appendix already had: A.7's `NewGitResolver(fsys afero.Fs, …)` took one from the first draft. The constructors are `Open(fsys, path)` (detection from the path, §8.0), **`OpenFile(f afero.File)`** (detection from `f.Name()`, `hew.As` for a nameless or ambiguous handle), and `OpenBytes(name, data)` unchanged. `OpenFile` exists because a careful config writer has already opened, locked, and validated the file it is about to edit, and must not be made to redo any of that: **the caller owns the handle, and hew never closes a handle it did not open.** `Doc.Write()` writes back through the same `Fs`/`File`. **Bare `*os.File`-only constructors are rejected** — they make every test allocate a tmpdir, which is the tmpdir-based testing O50 forbids. The honest caveat is stated at A.0: **temp-and-rename atomicity through an arbitrary afero backend is best-effort**, because rename semantics belong to the backend; hew cannot detect a backend whose rename is copy-then-delete and does not pretend to. What holds on every backend is that a *detectable* failure writes nothing at all, because staging is entirely in memory. |
| **O50** | How does the implementation phase proceed against a corpus that is already red? | **Test-first, and the ratified-pending corpus cases ARE the acceptance tests** (§13.7). Four rules. (a) **A work package begins by deleting its skip rules** — that is the red step; a package that implements first and deletes rules afterwards wrote its acceptance test knowing the answer. (b) **Layer-level unit suites are written red/green alongside**, one each for the document API, the registry, `hewfs` and the reversal patch, and they use **`afero.MemMapFs`** — a tmpdir-based test where an in-memory filesystem would serve is a defect, because it is slower, leaks state between runs, and tests the operating system rather than the code. (c) The established **mutation gates** apply: ≥85% core, ≥75% extensions, `gremlins --timeout-coefficient 30`. (d) The **acceptance-mutation gate** (`--coverpkg --integration`) runs once at P5 completion, because §13.8 already establishes that it is the gate that measures whether the corpus itself detects a defect. This is the same discipline P0–P4 ran under; the ruling writes it down so it is reviewable rather than remembered. |

### Residual — genuinely open

Four. Each needs evidence that does not exist yet; none blocks implementation of the rest.

| # | Question | What would settle it |
|---|---|---|
| **O22** | File-level effects derived from node-level results (OP-48 `create-file-if-absent`, OP-49 `delete-file-when-empty`). | The P5 ctxloom-adoption slice. M2 genuinely removes the file when nothing user-authored remains and never creates an absent one. If hew is to replace M2, either the IR grows file-level effects or the adapter keeps owning them — and the second answer means hew does not actually replace M2. Deciding before attempting the migration would be guessing. |
| **O24** | Catalog completeness (§11.9 claims exhaustiveness over the surveyed systems). | A reviewer who knows a verb in a system absent from §11.1's table. The claim is only as good as the survey, and the survey records two of its own gaps: no TOML notation candidate existed in any surveyed tool, and no format-prevalence census was obtainable. This is a standing invitation to falsify, not a decision awaiting a decider. |
| **O25** | An HCL ordinal with no distinguishing assert available — genuinely identical sibling blocks. | Real HCL. §6.4.3 rule 3 currently lets the ordinal stand alone, which is the one construct in hew that can silently patch the wrong node; refusing it outright would leave legal HCL files unpatchable by hew at all. Nobody has measured how often truly indistinguishable sibling blocks occur in practice, and the answer decides which cost is worse. |
| **O29** | Is Markdown in the implement tier at all? | The §8.7 evaluation — a rendered side-by-side of six scenarios against plain `patch(1)`, scored. The crux is already stated: reorder-blindness is hew's largest win over `patch(1)` and it is worth approximately nothing for prose, where order *is* the content. The corpus's skip registry already encodes the deferral (`markdown/*` is skipped with a recorded reason, §13.7), and the family is severable. |

### Deliberately not specified in v0

- **The diff algorithm itself.** `hew diff` is P4. §9.4 specifies its *requirements*
  (determinism, context radius, address preference) because the IR depends on them; it does
  not specify the sequence-diff implementation beyond naming Myers.
- **Merge / conflict resolution** between two Hew patches.
- **Signing.** Hew files are content like any other; whether they ride ctxloom's signature
  envelope is a P5 question.
- **Encoding other than UTF-8**, BOM handling, and CRLF. (CRLF targets: a binding must
  preserve the target's line ending; the `.hew` file itself is LF. Not corpus-covered in v0.)
- **Performance and streaming.** Every backend is assumed to load the whole target.
- **A second human-authoring notation.** The v0 ruling is one grammar. The transform list
  (§9.6) is an accepted *input*, but it is machine-first by design and a `.hewt` file where a
  `.hew` file would do is a review-quality regression.
- **Reverting an applied patch *from a record*** (`hew revert <record.hewt>`), which needs
  inversion rules per op. Sketched only in [O14](#ruled-by-the-human-2026-08-14). Undoing an
  apply *as it happens* is specified and ratified — see
  [O40](#p5--the-api-ratification-2026-08-14)'s reversal patch, which needs no inversion rules.
