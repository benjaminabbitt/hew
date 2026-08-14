# Acceptance criteria for ANY hew implementation, addressed at corpus/.
# An implementation binds these steps to its own runner; the corpus cases are
# the examples. These features are language-agnostic: the Go and Rust
# implementations must both satisfy them against the same corpus tree.

Feature: Corpus conformance — apply
  The corpus is the standard. Every apply-family case must produce the expected
  output byte-for-byte, and every expected-failure case must fail with the
  named error at the declared seam — an implementation that exits 0 having
  written nothing, or that misapplies silently, does not conform.

  Scenario: Every apply case in the corpus produces its expected output
    Given the conformance corpus at "corpus"
    When each apply case's patch is applied to its input document
    Then the result is byte-identical to the case's expected output
    And no case is skipped without a recorded skip reason

  Scenario: Every error case fails with its declared error
    Given the conformance corpus at "corpus"
    When each error case's patch is applied to its input document
    Then the implementation reports the case's declared error code
    And the error names the seam declared by the case
    And the target file is left byte-identical to its input

  Scenario: Tolerance — permissible target drift does not break a patch
    Given the conformance corpus at "corpus"
    When each tolerance case's patch is applied to its drifted input
    Then the result is byte-identical to the case's expected output

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
    When the old and new documents are diffed, the transforms rendered to
      notation, the notation re-parsed, and the result applied to the old
      document
    Then the final document is byte-identical to the new document

Feature: Corpus conformance — CLI contract
  The hew binary is a first-class surface with patch(1)-like semantics.

  Scenario: Exit codes follow the patch contract
    Given the corpus CLI cases
    When each case's documented invocation runs
    Then exit code 0 means the patch applied
    And exit code 1 means the patch did not apply and nothing was modified
    And exit code 2 means trouble
    And stdout and stderr match the case's declared contracts

  Scenario: Transform-list input is accepted as the escape hatch
    Given a corpus CLI case invoking apply with a transforms file
    When the invocation runs
    Then the result equals applying the equivalent notation patch
