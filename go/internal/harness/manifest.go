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
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/benjaminabbitt/hew/go/internal/hewerr"
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

// envAllowed is the closed set a case's `env:` block may name: the two
// variables the spec itself declares environment-readable (§9.7's applied_at
// pinning, Appendix B.1, ruling O37). Everything else about hew's behaviour is
// reachable only through argv and the files, and the corpus must not be able
// to pin more than the spec promises.
var envAllowed = map[string]bool{"HEW_APPLIED_AT": true, "SOURCE_DATE_EPOCH": true}

// PinnedAppliedAt returns the instant this case pins applied_at to, or "".
// HEW_APPLIED_AT wins over SOURCE_DATE_EPOCH, which is §9.7's own precedence.
func (m *Manifest) PinnedAppliedAt() string {
	if v := m.Env["HEW_APPLIED_AT"]; v != "" {
		return v
	}
	if v := m.Env["SOURCE_DATE_EPOCH"]; v != "" {
		return epochToRFC3339(v)
	}
	return ""
}

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

	// Env pins the run's environment (spec §13.4, ruling O37). Deliberately
	// restricted to envAllowed: a corpus that could reach any environment
	// variable could pin behaviour the spec never promised.
	Env map[string]string `yaml:"env"`

	// Multi-target cli cases (spec §10.5, ruling O12): these name their
	// targets explicitly because a two-target patch has no sole target.*
	// file to infer.
	TargetsUnchanged []string          `yaml:"targets_unchanged"`
	ExpectedTargets  map[string]string `yaml:"expected_targets"` // target file -> expected fixture
	NoFilesCreated   []string          `yaml:"no_files_created"` // globs that must match nothing after the run

	// Application-record cli cases (spec §9.7). The fields named by
	// record_digest_fields are not pinned by the fixture: the runner
	// RECOMPUTES each digest from the case's own fixture bytes and asserts
	// the record got it right (CheckRecord), so excluding them from the
	// structural comparison costs no assertion.
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
	for _, k := range sortedEnvKeys(m.Env) {
		switch {
		case !envAllowed[k]:
			probs = append(probs, fmt.Sprintf("env: %q is not one of the variables the spec declares environment-readable (§13.4)", k))
		case m.Kind != "cli":
			probs = append(probs, "env: is meaningful only on a cli case")
		case k == "SOURCE_DATE_EPOCH" && epochToRFC3339(m.Env[k]) == "":
			probs = append(probs, fmt.Sprintf("env: SOURCE_DATE_EPOCH %q is not an integer number of seconds", m.Env[k]))
		case k == "HEW_APPLIED_AT" && !validRFC3339UTC(m.Env[k]):
			probs = append(probs, fmt.Sprintf("env: HEW_APPLIED_AT %q is not an RFC 3339 UTC timestamp (§9.7)", m.Env[k]))
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

// epochToRFC3339 renders a SOURCE_DATE_EPOCH value as RFC 3339 UTC, or ""
// if it is not an integer number of seconds.
func epochToRFC3339(v string) string {
	secs, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil {
		return ""
	}
	return time.Unix(secs, 0).UTC().Format(time.RFC3339)
}

// validRFC3339UTC reports whether v is an RFC 3339 timestamp at zero offset.
// §9.7 requires UTC, so a corpus fixture that pins a non-UTC instant is
// pinning something the spec does not allow an implementation to emit.
func validRFC3339UTC(v string) bool {
	ts, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return false
	}
	_, offset := ts.Zone()
	return offset == 0
}

func sortedEnvKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// SortedSeams returns the declared seams in a stable execution order.
func (m *Manifest) SortedSeams() []Seam {
	order := map[Seam]int{SeamParse: 0, SeamApplyIR: 1, SeamE2E: 2, SeamRender: 3, SeamDiff: 4, SeamCLI: 5}
	out := append([]Seam(nil), m.Seams...)
	sort.Slice(out, func(i, j int) bool { return order[out[i]] < order[out[j]] })
	return out
}
