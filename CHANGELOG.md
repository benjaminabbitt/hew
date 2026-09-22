# Changelog

## v0.2.1

A patch hew rendered for a sequence element with no identity field is one hew
can now read back.

### Fixed

- **A sequence element with no usable identity field round-trips.** The differ
  addresses such an element positionally, but nothing wrote that position into
  the text, and the parser is purely textual — §9.1 lowering never opens a
  target, so a position it was not given is one it cannot recover. Rendering a
  patch and re-parsing it failed with `HEW001` against §6.4.2, which made the
  render-then-reparse self-check any consumer performs on a computed reversal
  fail outright.

  The position advisory now carries that index: `~hew:at=` is written on an
  element whose container has no usable identity field, and the parser resolves
  it to the positional segment §6.4.3 already requires. A hand-written patch
  that names no position is still refused — nothing can invent one.

  The shape that exercises it is an element whose sole key holds an array, so
  no scalar sits anywhere on it.

### Clarified

- §4.5c and the `At`/`Length` doc comment described the position advisory as
  only ever disambiguating a value-hash collision. That is one of its two
  roles; the other is being the whole address when no other one exists.
  §6.4.2 and §6.4.3 are unchanged — they already required this outcome.

## v0.2.0

The release that makes a patch locate by **identity** rather than by position,
and stop disclosing what it did not change.

### Breaking

- **A trailing `?` is an ordinary character.** The optional segment it once
  spelled is gone — no resolver ever read one, so every patch carrying one got
  exactly the `HEW013 no-match` it was written to avoid. `! default` (OP-04) is
  the spelling for "create if absent". `/server/tls?` now addresses a key
  literally named `tls?`, and needs no quoting to do it.

  A stale patch carrying the old spelling gets `HEW013 no-match: no key "tls?"`
  rather than a message naming the replacement. Still loud — hew does not
  misapply it — but the reader has to work out why.

- **The bare `#<n>` comment ordinal is gone.** It resolved against the patch
  body rather than the target, so a patch naming one comment deleted a
  different one, silently, exit 0. A comment is a keyless member and is now
  addressed the way every other keyless member is: by a digest of its own text,
  `#hew:comment=<hex>` (§4.5b).

### Locating by identity

- **Content-hash addressing** for set and sequence members:
  `/tags/#hew:sha256=<hex>`, a digest of the canonical member rather than its
  value. A collision refuses rather than guessing.
- **Position advisories** (`~hew:at=N ~hew:length=M`) disambiguate duplicates,
  and **migrate** with the patch's own earlier edits, so a patch can remove
  three identical elements from one array without its second transform
  refusing.
- **Neighbourhood location** resolves a candidate when position has run out —
  the case where a document changed on *both* sides of the element, which no
  position anchor can survive.
- **Scored location** with a stated floor: a contradiction refuses because a
  tie refuses, and no evidence refuses because nothing survived. It buys
  robustness against irrelevant edits, never permission to guess.

### The hint channel

A patch that carries context used to **assert** it, which cost twice: editing
an untouched neighbour refused the whole patch, and the neighbour's value was
copied verbatim into the patch — how a sibling's `Authorization: Bearer …`
reached a stored reversal.

- Untouched neighbours now ride a **non-asserting `~` hint**: a mapping member
  by its key, a set member by its content digest, and a **comment** by the
  digest of its text. A hint can never fail a match and never carries a value.
- **`--hint-context N`** sets the hint radius, default **3** — deliberately
  wider than `--context`. The two dials move in opposite directions: widening
  asserting context makes a patch more brittle, widening hint context makes it
  easier to place. `--context=0` and `--context=all` remain body-wide.

### Fixes

- The differ's rendered hint for a comment re-parsed as an ordinary key, so the
  patch meant something other than what the differ built.
- `json` spliced its edits with an unstable sort, where the other three
  bindings used a stable one. Insertions share offsets, so their authored order
  is all that orders them — an unstable sort could reorder a separator and the
  content it separates.
- The differ collapsed duplicate removes, producing a patch that under-removed
  and then failed its own round trip.
- Addressing flipped between the differ and a re-parse of its own patch when
  one side of a sequence was empty.

### Internal

The four format bindings now share what was never format-specific: the byte
splice, the §6.1 value matcher, the resolve-failure type, comment resolution,
and the apply loop itself. The loop's rules are the ones that fail silently
when they drift — that the document is reparsed between transforms, that a hint
neither asserts nor edits — so they now have one home and tests of their own.

## v0.1.0

Initial release.
