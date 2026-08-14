# examples — executable scenarios

Each directory here is one scenario: a `scenario.yaml` manifest plus the fixture files it
starts from. `just examples` copies each into a scratch directory, runs the real `hew`
binary there, and renders what happened into `website/src/content/docs/examples/`.

**The transcripts are not committed.** A transcript on disk is a claim about how the CLI
behaves, and a committed one can be stale; generating them at build time means the site
cannot publish a transcript the current binary does not produce. The generator lives in
[`go/cmd/hew-examples`](../go/cmd/hew-examples).

This is documentation, not conformance. [`corpus/`](../corpus) is the standard; these
pages exist to be read.

## Writing a scenario

```yaml
title: Your first patch          # page title (required)
description: One sentence.       # Starlight frontmatter + index entry (required)
order: 1                         # sidebar position
show: [main.tf, region.hew]      # fixtures displayed before the first step
intro: |                         # framing prose, markdown
  ...
steps:
  - title: Apply it in place
    note: |                      # prose before the command
      ...
    write:                       # pre-run file writes — the edits a human would make
      - path: settings.yaml
        label: "**`settings.yaml`** — as it stands now"
        content: |
          ...
    git:                         # optional scaffolding, run before `run`; must exit 0
      - [init, -q, -b, main, .]
    run: [apply, -i, region.hew] # argv for hew, without argv0
    expect_exit: 0               # asserted; the generator fails on disagreement
    watch: [main.tf]             # show the resulting file + a unified diff of before→after
    show: [applied.hewt]         # show a file in full, no diff (artifacts created by the step)
    unchanged: [config.yaml]     # assert byte-identical across the step, and show it
    identical: ["a.json == b.json"]  # assert two files have identical bytes
    caption: |                   # prose after the command's output
      ...
outro: |                         # closing prose
  ...
```

Determinism is a hard requirement — CI regenerates and compares. Commands run with the
scratch directory as their cwd and take relative paths only, directory reads are sorted,
git runs with a pinned identity and clock, and the record's `applied_at` stamp is
replaced. Do not add a step whose output embeds a path, a clock, or a hash of either.
