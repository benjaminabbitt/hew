package harness

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/hew-format/hew/internal/hewerr"
)

// errorManifest is a conformant error-kind manifest; tests mutate one field at
// a time to isolate each assertion dimension.
func errorManifest() *Manifest {
	return &Manifest{
		Name: "yaml/stale", Kind: "error", Seams: []Seam{SeamApplyIR},
		Error: "HEW010", ErrorSeam: "apply-ir", ErrorPath: "/server/timeout",
		PatchLine:       9,
		MessageContains: []string{"stale-target", "expected 30"},
	}
}

// conformantError is the error that satisfies errorManifest exactly.
func conformantError() *hewerr.Error {
	return &hewerr.Error{
		Code: hewerr.CodeStaleTarget, Component: hewerr.ComponentApplier,
		Target: "config.yaml", Path: "/server/timeout",
		PatchFile: "patch.hew", PatchLine: 9, TargetLine: 6,
		Want: "30", Got: "45",
	}
}

func TestCheckErrorConformant(t *testing.T) {
	if probs := CheckError(conformantError(), errorManifest()); len(probs) > 0 {
		t.Errorf("a fully conformant error must report nothing, got %v", probs)
	}
}

func TestCheckErrorConformantThroughWrapping(t *testing.T) {
	wrapped := fmt.Errorf("applying transform 2: %w", conformantError())
	if probs := CheckError(wrapped, errorManifest()); len(probs) > 0 {
		t.Errorf("wrapping must not break the contract check, got %v", probs)
	}
}

func TestCheckErrorNil(t *testing.T) {
	probs := CheckError(nil, errorManifest())
	if len(probs) != 1 {
		t.Fatalf("probs = %v, want exactly one", probs)
	}
	if probs[0] != "expected error HEW010, got success" {
		t.Errorf("probs[0] = %q", probs[0])
	}
}

func TestCheckErrorNotAHewError(t *testing.T) {
	probs := CheckError(errors.New("some other failure"), errorManifest())
	if len(probs) != 1 {
		t.Fatalf("probs = %v, want exactly one", probs)
	}
	if !strings.Contains(probs[0], "not a *hewerr.Error") || !strings.Contains(probs[0], "some other failure") {
		t.Errorf("probs[0] = %q must name the problem and show the error", probs[0])
	}
}

func TestCheckErrorDimensions(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*hewerr.Error)
		manifest func(*Manifest)
		contains []string
		absent   []string
	}{
		{
			name:     "wrong code",
			mutate:   func(e *hewerr.Error) { e.Code = hewerr.CodeNoMatch },
			contains: []string{"code: want HEW010, got HEW013"},
		},
		{
			name:     "wrong component",
			mutate:   func(e *hewerr.Error) { e.Component = hewerr.ComponentParser },
			contains: []string{`component: want applier (error_seam "apply-ir"), got parser`},
		},
		{
			name:     "wrong path",
			mutate:   func(e *hewerr.Error) { e.Path = "/server/port" },
			contains: []string{"path: want /server/timeout, got /server/port"},
		},
		{
			name:     "empty path when one is declared",
			mutate:   func(e *hewerr.Error) { e.Path = "" },
			contains: []string{"path: want /server/timeout, got "},
		},
		{
			name:     "unasserted path is not checked",
			mutate:   func(e *hewerr.Error) { e.Path = "/anything" },
			manifest: func(m *Manifest) { m.ErrorPath = "" },
			absent:   []string{"path:"},
		},
		{
			name:     "wrong patch line",
			mutate:   func(e *hewerr.Error) { e.PatchLine = 3 },
			contains: []string{"patch_line: want 9, got 3"},
		},
		{
			name:     "missing patch line",
			mutate:   func(e *hewerr.Error) { e.PatchLine = 0 },
			contains: []string{"patch_line: want 9, got 0"},
		},
		{
			name:     "unasserted patch line is not checked",
			mutate:   func(e *hewerr.Error) { e.PatchLine = 0 },
			manifest: func(m *Manifest) { m.PatchLine = 0 },
			absent:   []string{"patch_line:"},
		},
		{
			name:     "missing message substring",
			mutate:   func(e *hewerr.Error) { e.Want = "31" },
			contains: []string{`message missing "expected 30"`},
		},
		{
			name:     "every declared substring is checked",
			mutate:   func(e *hewerr.Error) { e.Code = hewerr.CodeNoMatch; e.Want = "" },
			contains: []string{`message missing "stale-target"`, `message missing "expected 30"`},
		},
		{
			name:     "no message_contains means no message assertions",
			manifest: func(m *Manifest) { m.MessageContains = nil },
			absent:   []string{"message missing"},
		},
		{
			name:     "unknown error_seam is reported instead of a component mismatch",
			manifest: func(m *Manifest) { m.ErrorSeam = "nonsense" },
			contains: []string{`unknown error_seam "nonsense"`},
			absent:   []string{"component: want"},
		},
		{
			name:     "error_seam spelled as a component still matches",
			manifest: func(m *Manifest) { m.ErrorSeam = "applier" },
			absent:   []string{"component"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			he := conformantError()
			if tc.mutate != nil {
				tc.mutate(he)
			}
			m := errorManifest()
			if tc.manifest != nil {
				tc.manifest(m)
			}
			probs := CheckError(he, m)
			joined := strings.Join(probs, "\n")
			for _, want := range tc.contains {
				if !strings.Contains(joined, want) {
					t.Errorf("probs %v missing %q", probs, want)
				}
			}
			for _, bad := range tc.absent {
				if strings.Contains(joined, bad) {
					t.Errorf("probs %v must not mention %q", probs, bad)
				}
			}
			if len(tc.contains) == 0 && len(probs) != 0 {
				t.Errorf("probs = %v, want none", probs)
			}
		})
	}
}

// TestCheckErrorReportsEveryDimensionAtOnce is runner obligation 4's whole
// point: one failure message shows the entire distance from conformance.
func TestCheckErrorReportsEveryDimensionAtOnce(t *testing.T) {
	he := &hewerr.Error{
		Code:      hewerr.CodeNoMatch,      // want HEW010
		Component: hewerr.ComponentParser,  // want applier
		Path:      "/wrong/path",           // want /server/timeout
		PatchLine: 2,                       // want 9
		Detail:    "nothing like the text", // neither substring present
	}
	probs := CheckError(he, errorManifest())
	want := []string{
		"code: want HEW010, got HEW013",
		`component: want applier (error_seam "apply-ir"), got parser`,
		"path: want /server/timeout, got /wrong/path",
		"patch_line: want 9, got 2",
		`message missing "stale-target"`,
		`message missing "expected 30"`,
	}
	if len(probs) != len(want) {
		t.Fatalf("got %d problems %v, want %d", len(probs), probs, len(want))
	}
	for i, w := range want {
		if !strings.Contains(probs[i], w) {
			t.Errorf("probs[%d] = %q, want it to contain %q", i, probs[i], w)
		}
	}
}

// TestCheckErrorMessageCheckUsesTheOuterError: message_contains is asserted
// against the error as the user sees it, wrapping included.
func TestCheckErrorMessageCheckUsesTheOuterError(t *testing.T) {
	he := conformantError()
	he.Want = "" // "expected 30" no longer in the hewerr rendering
	wrapped := fmt.Errorf("context expected 30: %w", he)
	if probs := CheckError(wrapped, errorManifest()); len(probs) != 0 {
		t.Errorf("probs = %v; the wrapper's text counts toward message_contains", probs)
	}
}
