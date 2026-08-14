package harness

import (
	"fmt"
	"strings"

	"github.com/hew-format/hew/internal/hewerr"
)

// CheckError asserts an error-kind case's declared contract against an actual
// error (runner obligation 4). It reports EVERY mismatch, not just the first,
// so one failure message shows the whole distance from conformance. nil means
// conformant.
func CheckError(err error, m *Manifest) []string {
	if err == nil {
		return []string{fmt.Sprintf("expected error %s, got success", m.Error)}
	}
	he, ok := hewerr.As(err)
	if !ok {
		return []string{fmt.Sprintf("error is not a *hewerr.Error: %v", err)}
	}
	var probs []string
	if string(he.Code) != m.Error {
		probs = append(probs, fmt.Sprintf("code: want %s, got %s", m.Error, he.Code))
	}
	wantComp, cerr := m.Component()
	if cerr != nil {
		probs = append(probs, cerr.Error())
	} else if he.Component != wantComp {
		probs = append(probs, fmt.Sprintf("component: want %s (error_seam %q), got %s", wantComp, m.ErrorSeam, he.Component))
	}
	if m.ErrorPath != "" && he.Path != m.ErrorPath {
		probs = append(probs, fmt.Sprintf("path: want %s, got %s", m.ErrorPath, he.Path))
	}
	if m.PatchLine != 0 && he.PatchLine != m.PatchLine {
		probs = append(probs, fmt.Sprintf("patch_line: want %d, got %d", m.PatchLine, he.PatchLine))
	}
	msg := err.Error()
	for _, want := range m.MessageContains {
		if !strings.Contains(msg, want) {
			probs = append(probs, fmt.Sprintf("message missing %q (message: %q)", want, msg))
		}
	}
	return probs
}
