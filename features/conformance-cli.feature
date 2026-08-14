# Acceptance criteria for ANY hew implementation, addressed at corpus/.
# An implementation binds these steps to its own runner; the corpus cases are
# the examples. These features are language-agnostic: the Go and Rust
# implementations must both satisfy them against the same corpus tree.

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
