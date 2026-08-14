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
