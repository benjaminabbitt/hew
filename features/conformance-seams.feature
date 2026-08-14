# Acceptance criteria for ANY hew implementation, addressed at corpus/.
# An implementation binds these steps to its own runner; the corpus cases are
# the examples. These features are language-agnostic: the Go and Rust
# implementations must both satisfy them against the same corpus tree.

Feature: Corpus conformance — the four seams
  Parse, render, diff, and apply are pinned independently so implementations
  cannot compensate for a defect in one seam inside another.

  Scenario: Parse — notation compiles to the pinned transform list
    Given every corpus case that carries a transforms fixture
    When the case's patch is parsed
    Then the resulting transform list equals the fixture

  Scenario: Apply-IR — the transform list alone reproduces the output
    Given every corpus case that carries a transforms fixture
    When the fixture's transforms are applied to the input document
    Then the result is byte-identical to the case's expected output

  Scenario: Round-trip identity
    Given every corpus round-trip case
    When the old and new documents are diffed, the transforms rendered to notation, the notation re-parsed, and the result applied to the old document
    Then the final document is byte-identical to the new document
