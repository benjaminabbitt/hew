---
title: The standard
description: Where the specification lives, what the conformance corpus is, and how an implementation proves it is one.
---

hew is spec-first, and the spec is not the last word. **Where the prose and the corpus
disagree, the corpus wins and the prose has a bug.**

## The two documents

- **[`docs/hew-spec.md`](https://github.com/benjaminabbitt/hew/blob/main/docs/hew-spec.md)** —
  the normative definition. File format, margins, the path grammar, hunk semantics, the
  tolerance model, annotations, per-format bindings, the IR, the error taxonomy, the
  operations catalog, and the CLI surface.
- **[`corpus/`](https://github.com/benjaminabbitt/hew/tree/main/corpus)** — the
  *executable* definition. One directory per case, each with a manifest and its
  fixtures. This is what an implementation is measured against.

The corpus lives at the repository root rather than under a `tests/` directory,
deliberately: it is consumed by every implementation as a peer, and `tests/` would imply
Go-test-only ownership.

## What a case pins

A case declares which **seams** it exercises, and a runner must run each declared seam
separately and assert only that seam's output. The five seams follow
[the architecture](/hew/architecture/) exactly:

| Seam | Input → output | Isolates |
|---|---|---|
| `parse` | `patch.hew` → `transforms.hewt` | The notation. No target file, no backend. |
| `apply-ir` | `transforms.hewt` + target → expected | The applier: format mechanics and byte preservation. |
| `e2e` | `patch.hew` + target → expected | The composition — what a user experiences. |
| `render` | `transforms.hewt` → `.hew` → `transforms.hewt` | The renderer, via IR round-trip identity. |
| `diff` | old + new → `expected.hew` | The differ's determinism and context radius. |
| `cli` | argv → exit code + streams | The CLI contract. |

Expected outputs are compared **byte for byte**. A case that produces the right tree with
different bytes has failed: preserving the parts of a file nobody asked to change is not
a nicety here, it is the feature.

Error cases are first-class. A case may declare that it must fail, with which code, at
which seam, naming which path — so "refuses correctly" is as testable as "applies
correctly", which for this format is the more important half.

## The acceptance criteria

[`features/`](https://github.com/benjaminabbitt/hew/tree/main/features) expresses the
corpus's obligations as Cucumber scenarios. An implementation is conformant when those
features pass against it. For the Go implementation they run under `go test` alongside
everything else.

## Claiming conformance

There is one bar and it is mechanical: **pass the corpus, with the skip registry
disallowed.** No self-assessment, no partial-credit matrix, no "conformant except for".
The Go library and CLI are the first implementation; the planned Rust port runs the same
directory, which is what makes it a port rather than a rewrite that shares a name.

## Reading the spec

It is long, and the parts that repay reading first are §1 (why the format exists, and the
single property everything is derived from), §6.4 (the normative tolerance table — what
is invisible to a patch and what fails loudly), and Appendix C (the honest inventory of
what the mirror grammar cannot express, and what to write instead).
