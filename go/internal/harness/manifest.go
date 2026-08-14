// Package harness is the conformance engine for the hew corpus. It is a
// library: nothing in it touches *testing.T, so the go-test corpus runner and
// the godog acceptance steps are two thin frontends over the same engine.
//
// The corpus (repo-level corpus/) is the standard. Where this package makes a
// judgment call about a corpus irregularity, the call is documented at the
// site and the corpus's own README rules (runner obligations 1-7) win.
package harness

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/ctxloom/hew/internal/hewerr"
)

// Seam names one independently-pinned test surface (corpus README table).
type Seam string

const (
	SeamParse   Seam = "parse"    // patch.hew                 -> transforms.hewt
	SeamApplyIR Seam = "apply-ir" // transforms.hewt + target  -> expected
	SeamE2E     Seam = "e2e"      // patch.hew + target        -> expected
	SeamRender  Seam = "render"   // transforms.hewt -> .hew -> transforms.hewt (RT2)
	SeamDiff    Seam = "diff"     // old + new                 -> expected.hew
	SeamCLI     Seam = "cli"      // argv                      -> exit code + streams
)

var validSeams = map[Seam]bool{
	SeamParse: true, SeamApplyIR: true, SeamE2E: true,
	SeamRender: true, SeamDiff: true, SeamCLI: true,
}

var validKinds = map[string]bool{"ok": true, "error": true, "cli": true}

// Manifest is a decoded case.yaml. Decoding is strict (KnownFields): an
// unrecognized manifest field is a corpus error, never a silent pass-through
// (runner obligation 5).
type Manifest struct {
	Name   string   `yaml:"name"`
	Seams  []Seam   `yaml:"seams"`
	Kind   string   `yaml:"kind"`
	Format string   `yaml:"format"`
	Ops    []string `yaml:"ops"`
	Why    string   `yaml:"why"`
	Spec   string   `yaml:"spec"`

	// error cases
	Error           string   `yaml:"error"`
	ErrorSeam       string   `yaml:"error_seam"`
	ErrorPath       string   `yaml:"error_path"`
	PatchLine       int      `yaml:"patch_line"`
	MessageContains []string `yaml:"message_contains"`

	// cli cases
	Argv            []string `yaml:"argv"`
	Exit            *int     `yaml:"exit"`
	Stdout          *string  `yaml:"stdout"` // nil = unasserted; "" = must be empty; else fixture filename
	StderrContains  []string `yaml:"stderr_contains"`
	TargetUnchanged bool     `yaml:"target_unchanged"`
	Expected        string   `yaml:"expected"` // post-run in-place comparison fixture
	Requires        string   `yaml:"requires"` // e.g. "git-fixture"
	Fixture         string   `yaml:"fixture"`  // documentation ONLY; never executed as shell

	// Multi-target cli cases (spec §10.5, ruling O12): these name their
	// targets explicitly because a two-target patch has no sole target.*
	// file to infer.
	TargetsUnchanged []string          `yaml:"targets_unchanged"`
	ExpectedTargets  map[string]string `yaml:"expected_targets"` // target file -> expected fixture
	NoFilesCreated   []string          `yaml:"no_files_created"` // globs that must match nothing after the run

	// Application-record cli cases (spec §9.7). Asserting these needs
	// digest recomputation over the fixtures, which is M10 work; until then
	// a case declaring expected_record FAILS rather than passing vacuously.
	ExpectedRecord     string   `yaml:"expected_record"`
	RecordDigestFields []string `yaml:"record_digest_fields"`
}

// ParseManifest strictly decodes a case.yaml.
func ParseManifest(src []byte) (*Manifest, error) {
	dec := yaml.NewDecoder(bytes.NewReader(src))
	dec.KnownFields(true)
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("case.yaml: %w", err)
	}
	return &m, nil
}

// Validate checks the manifest against the §13.4 schema. relName is the
// case's directory path relative to the corpus root (e.g. "json/add-key"),
// which the name field must equal.
func (m *Manifest) Validate(relName string) error {
	var probs []string
	if m.Name != relName {
		probs = append(probs, fmt.Sprintf("name %q != directory %q", m.Name, relName))
	}
	if !validKinds[m.Kind] {
		probs = append(probs, fmt.Sprintf("unknown kind %q", m.Kind))
	}
	if len(m.Seams) == 0 {
		probs = append(probs, "no seams declared")
	}
	for _, s := range m.Seams {
		if !validSeams[s] {
			probs = append(probs, fmt.Sprintf("unknown seam %q", s))
		}
	}
	switch m.Kind {
	case "error":
		if m.Error == "" || m.ErrorSeam == "" {
			probs = append(probs, "error case missing error/error_seam")
		}
		if len(m.MessageContains) == 0 {
			probs = append(probs, "error case missing message_contains")
		}
		if _, err := m.Component(); m.ErrorSeam != "" && err != nil {
			probs = append(probs, err.Error())
		}
	case "cli":
		if len(m.Argv) == 0 || m.Exit == nil {
			probs = append(probs, "cli case missing argv/exit")
		}
	}
	if m.Requires != "" {
		if _, ok := fixtureBuilders[m.Requires]; !ok {
			probs = append(probs, fmt.Sprintf("unknown requires %q", m.Requires))
		}
	}
	if len(probs) > 0 {
		return fmt.Errorf("corpus error in %s: %s", relName, strings.Join(probs, "; "))
	}
	return nil
}

// Component normalizes the corpus's error_seam vocabulary to the component
// that must be attributed with the error. The corpus is inconsistent —
// observed spellings are "apply-ir" (13 cases), "parser" (2), "applier" (1) —
// a component vocabulary leaking into a seam field. We accept both
// vocabularies; anything else is a corpus error (runner obligation 5).
func (m *Manifest) Component() (hewerr.Component, error) {
	switch m.ErrorSeam {
	case "parse", "parser":
		return hewerr.ComponentParser, nil
	case "apply-ir", "applier", "apply":
		return hewerr.ComponentApplier, nil
	case "diff", "differ":
		return hewerr.ComponentDiffer, nil
	case "render", "renderer":
		return hewerr.ComponentRenderer, nil
	case "cli":
		return hewerr.ComponentCLI, nil
	}
	return 0, fmt.Errorf("corpus error: unknown error_seam %q", m.ErrorSeam)
}

var hewCodeRE = regexp.MustCompile(`^HEW\d{3}$`)

// StderrCode extracts the HEW code embedded in stderr_contains. Quirk
// adapter: cli/empty-patch-exit-2 declares a parse seam but carries no
// error: field; its expected code is the ^HEW\d+$-shaped token in
// stderr_contains. Candidate corpus clarification.
func (m *Manifest) StderrCode() (string, bool) {
	for _, s := range m.StderrContains {
		if hewCodeRE.MatchString(s) {
			return s, true
		}
	}
	return "", false
}

// SortedSeams returns the declared seams in a stable execution order.
func (m *Manifest) SortedSeams() []Seam {
	order := map[Seam]int{SeamParse: 0, SeamApplyIR: 1, SeamE2E: 2, SeamRender: 3, SeamDiff: 4, SeamCLI: 5}
	out := append([]Seam(nil), m.Seams...)
	sort.Slice(out, func(i, j int) bool { return order[out[i]] < order[out[j]] })
	return out
}
