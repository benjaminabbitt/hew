---
title: Writing a .hew patch
description: The preamble, the file section, the hunk header, and the six characters in column one.
---

A `.hew` file is UTF-8 text with LF line endings, and it is **line-oriented at the top
level**: the first character of every line is structural. There are three layers — a
preamble, one or more file sections, and hunks within them.

```diff
hew: 1

--- config.yaml format=yaml

@@ /service @@
  replicas: 3
- timeout: 30
+ timeout: 60
```

## The preamble

`hew: 1` declares the format version, ahead of any file section. The preamble may also
carry pragmas that set a default for every hunk below, of which the one you will meet
first is `idempotent: true` — a generating tool that expects its patches to be re-applied
says so once, here, and every reader knows before the first hunk:

```diff
# ctxloom-generated; safe to re-apply
hew: 1
idempotent: true
```

## The file section

```
--- config.yaml format=yaml
```

`--- ` names the target and, optionally, its format. Format is inferred from the
extension when it is not stated (`.json`, `.jsonc`, `.yaml`/`.yml`, `.toml`,
`.md`), so `format=` is for the cases where the extension lies — a `.conf` that is really
TOML, a file read from stdin. `hew apply -t OTHER` overrides the target from the command
line, which is how a patch generated from one copy of a file gets applied to another.

One patch may carry several file sections. They are applied **all or none**: hew stages
every section completely and commits only if every one of them staged.

## The hunk header

```
@@ /service @@
```

Between the `@@`s is a **hew path** — an address into the document's shape, not into its
text. The grammar is small:

| Segment | Means |
|---|---|
| `/service` | the `service` member of a mapping |
| `/servers/0` | element 0 of an array, by position |
| `/servers/name=github` | the element of `servers` whose `name` is `github` — by **identity** |
| `/` | the document root |

Prefer identity over position wherever the document offers one. `/servers/0` is a claim
about the order of a list, and list order is the thing that changes; `name=github` is a
claim about which server you meant, which is the thing you actually know. The default
candidate identity fields are `name`, `id` and `key`.

## The six margins

Column one of a hunk body is the margin. It is the whole vocabulary:

| Margin | Means |
|---|---|
| (space) | **context** — a node that must be present, with this value |
| `-` | remove this node; it must be present, with this value |
| `+` | add this node |
| `?` | an **assertion** about the node, with no edit (see below) |
| `!` | a **directive** to hew about the hunk |
| `#` | a comment in the patch itself, never in the target |

The thing to internalise is that a space margin is not decoration. It compiles to a
`test` transform in the IR, exactly as `-` compiles to a `test` plus a `remove`.
Staleness detection is a property of the IR, not of applier goodwill — an implementation
cannot be sloppy about it and still pass the corpus.

That has a consequence worth stating plainly: **context is a strictness dial, not a
verbosity dial**. A hunk with less context is a *weaker* patch, not a tidier one. When
`hew diff` offers `-U 0`, it is offering to make the patch assert less.

## Body lines are the target's own syntax

A body line, minus its margin and one space, is a line of the target format. A YAML
patch's bodies are YAML; a TOML patch's bodies are TOML. There is no intermediate value
language, which is why you can read a hew patch for a format you have never patched
before.

attribute-versus-block disambiguation.

## Subset matching

A context line for an object element states only the fields you want to assert:

```diff
@@ /mcpServers @@
  { "name": "filesystem" }
+ { "name": "github", "command": "npx" }
```

The context element asserts that an element with `name: filesystem` exists. It says
nothing about that element's other fields, so it neither breaks when they change nor
overwrites them. This is what keeps a keyed-array context one line long instead of eight,
and it is the largest readability win over merge-patch formats, which must restate every
element they did not intend to touch.

## Assertions and directives

A `?` line asserts without editing. Unlike a context line — which asserts a node's value
by mirroring it — a `?` line asserts something a mirror cannot express: that a node is
*absent*, or that it is of a particular kind, or that a node elsewhere in the document
holds a value. Each names its own path, so a `?` line can reach outside the hunk's
address.

```diff
@@ / @@
? expect /version = 1.2.0
? absent /env/ANTHROPIC_API_KEY
? kind /permissions = map
  permissions:
    deny:
      - Bash(rm -rf *)
```

A patch made entirely of `?` lines and context is legal and useful: it is a check, not an
edit. Run it with `--dry-run` and you have a config assertion that exits 0 or 1 and names
what it found.

A `!` line is a directive to hew about the hunk it appears in. The one you will reach for
is `! idempotent`, which opts that hunk into convergence: applying it when the
after-image already holds succeeds instead of failing. It is deliberately something you
write in the patch rather than pass on the command line — whether re-application is
expected is a property of the change, and belongs where the reviewer is looking.

## Then read one

The [examples](/hew/examples/) are the other half of this page: real patches, applied by
the real CLI, with the output they actually produced.
