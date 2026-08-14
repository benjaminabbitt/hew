package conformance

import (
	"path/filepath"
	"testing"

	"github.com/cucumber/godog"
)

// TestFeatures runs the repo-level Cucumber acceptance criteria
// (features/conformance.feature) under go test. Strict mode makes undefined
// or pending steps FAIL — the acceptance criteria drive development, they do
// not decorate it.
func TestFeatures(t *testing.T) {
	skipSlow(t)
	_, _ = engine(t) // fail fast on init problems before godog starts
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{filepath.Join(gRepoRoot, "features")},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("acceptance criteria not satisfied")
	}
}
