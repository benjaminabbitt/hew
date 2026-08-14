---
title: The exit-code contract
description: Three states, and what each one promises about the files on disk.
---

hew has three exit codes, and the reason to read this page is that one of them does not
mean what the same number means in `patch(1)`.

| Code | Meaning | State of your files |
|---|---|---|
| `0` | Every hunk applied. | Written — or, under `--dry-run`, *would have been*. |
| `1` | The patch did not apply. | **Unchanged. Every byte.** |
| `2` | Trouble: usage error, unreadable patch, unparseable target, unsupported format, missing `git`, I/O failure. | Unchanged. |

## Exit 1 is a promise, not a warning

In `patch(1)`, exit 1 means "some hunks failed, some may have succeeded, and there are
`.rej` files somewhere". You are left holding a file in an unknown state and a cleanup
job.

In hew, exit 1 means **nothing happened**. That is enforceable rather than aspirational,
because applying is two phases: hew *stages* every file section — reads the target,
resolves the addresses, evaluates every assertion, computes the resulting bytes — and only
then *commits* by writing. A failure at the staging boundary means no file was opened for
writing at all. There is no partial application to detect and no reject file to find,
which is why there is also no `--reject-file` flag.

So a caller may treat exit 1 as "unchanged" and be right. Being defensive about it —
re-reading the file, diffing it, restoring a backup — is work hew has already done.

## Which errors are which

`HEW010`–`HEW041` are "the patch did not apply", and they exit 1:

| Code | Name | Raised when |
|---|---|---|
| `HEW010` | stale-target | A context line asserted a value the document does not hold. |
| `HEW011` | assertion-failed | A `?` assertion failed, or a strict hunk found its after-image already in place. |
| `HEW012` | ambiguous-match | An address matched more than one node. |
| `HEW013` | no-match | An address matched nothing. |
| `HEW014` | already-exists | An `+` would create a node that is already there. |
| `HEW020` | inexpressible | The edit cannot be expressed in the target format's surface. |
| `HEW030` | conflict | Two hunks in one patch disagree about the same node. |
| `HEW040`/`HEW041` | anchor / surface ambiguity | The document offers no unambiguous place to make the edit. |

Everything else is trouble, and exits 2: `HEW001` (the patch will not parse), `HEW002`
(the target will not parse), `HEW003` (the target cannot be read), `HEW021` (no binding
for that format), plus usage errors and I/O failures.

The distinction is worth internalising because it is the one a script cares about. Exit 1
is a *fact about the document* — the patch and the file disagree, and a human should
look. Exit 2 is a fact about the invocation — something is wrong with how hew was called
or with the environment it was called in, and the same patch might apply perfectly once
that is fixed.

## Finding out before you commit to it

```console
$ hew apply --dry-run change.hew
```

`--dry-run` performs the entire apply — read, address, match, assert, compute — and then
writes nothing, exiting as though it had. It is the way to ask "does this patch still
fit?" without a backup file and without a `git stash`. See
[when the target has drifted](/hew/examples/stale-refusal/) for it refusing in practice.

## Streams

Stdout carries **results**: `--ops` output, `--transforms-out`, `-o -`, and everything
`hew diff` produces. Stderr carries **human diagnostics**. Never both on stdout — piping
`hew diff` into a file is always safe, and a diagnostic can never end up inside a
generated patch.

A diagnostic names the code, the target and node it is about, the patch line that made
the claim, and both values:

```console
hew: config.yaml:/service/replicas: HEW010 stale-target
  bump-timeout.hew:6: expected 3
  config.yaml: found 5
```
