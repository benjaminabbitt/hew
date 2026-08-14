package hewjson

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/benjaminabbitt/hew"
	"github.com/benjaminabbitt/hew/internal/hewerr"
)

func corpusDir(t *testing.T, rel string) string {
	t.Helper()
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		cand := filepath.Join(root, "..", "..", "corpus", rel)
		if st, err := os.Stat(cand); err == nil && st.IsDir() {
			return cand
		}
		parent := filepath.Dir(root)
		if parent == root {
			t.Fatalf("could not find corpus/%s above %s", rel, root)
		}
		root = parent
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return b
}

func soleFile(t *testing.T, dir, prefix string) []byte {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if len(e.Name()) > len(prefix) && e.Name()[:len(prefix)] == prefix {
			return readFile(t, filepath.Join(dir, e.Name()))
		}
	}
	t.Fatalf("no %s* file in %s", prefix, dir)
	return nil
}

// applyIRCase runs the apply-ir seam directly against hewjson for a corpus
// case that carries transforms.hewt + target.json + expected.json.
func applyIRCase(t *testing.T, rel string) {
	t.Helper()
	dir := corpusDir(t, rel)
	fixture := readFile(t, filepath.Join(dir, "transforms.hewt"))
	target := soleFile(t, dir, "target.")
	want := soleFile(t, dir, "expected.")

	tl, err := hew.UnmarshalTransforms(fixture)
	if err != nil {
		t.Fatalf("UnmarshalTransforms: %v", err)
	}
	got, err := Apply(target, tl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("Apply(%s) mismatch\ngot:\n%s\nwant:\n%s", rel, got, want)
	}
}

func TestApplyIRAgainstCorpus(t *testing.T) {
	cases := []string{
		"json/add-key",
		"json/set-scalar",
		"json/keyed-array-add",
		"json/ir-copy-node",
		"json/ir-move-normalized",
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) { applyIRCase(t, c) })
	}
}

// e2eCase runs patch.hew -> parse -> apply against target/expected.
func e2eCase(t *testing.T, rel string) {
	t.Helper()
	dir := corpusDir(t, rel)
	patch := readFile(t, filepath.Join(dir, "patch.hew"))
	target := soleFile(t, dir, "target.")
	want := soleFile(t, dir, "expected.")

	tls, err := hew.Parse(patch)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(tls) != 1 {
		t.Fatalf("want 1 file section, got %d", len(tls))
	}
	got, err := Apply(target, tls[0])
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("e2e(%s) mismatch\ngot:\n%s\nwant:\n%s", rel, got, want)
	}
}

func TestE2EAgainstCorpus(t *testing.T) {
	cases := []string{
		"json/add-key",
		"json/set-scalar",
		"json/array-append",
		"json/array-remove-element",
		"json/delete-key",
		"json/keyed-array-add",
		"json/keyed-array-remove",
		"json/replace-array-wholesale",
		"json/tolerance-key-reorder",
		"json/whole-file-replace",
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) { e2eCase(t, c) })
	}
}

// errorCase runs an error-kind corpus case and checks the declared code,
// component, path, and message_contains — and that the target is untouched.
func errorCase(t *testing.T, rel string, wantCode hewerr.Code, wantComponent hewerr.Component, wantPath string, contains []string) {
	t.Helper()
	dir := corpusDir(t, rel)
	patch := readFile(t, filepath.Join(dir, "patch.hew"))
	target := soleFile(t, dir, "target.")

	tls, err := hew.Parse(patch)
	if err != nil {
		t.Fatalf("Parse should succeed (error is the applier's to raise): %v", err)
	}
	got, aerr := Apply(target, tls[0])
	if aerr == nil {
		t.Fatalf("expected error, apply succeeded")
	}
	if got != nil {
		t.Fatalf("all-or-nothing violated: Apply returned bytes alongside an error")
	}
	he, ok := hewerr.As(aerr)
	if !ok {
		t.Fatalf("error is not *hewerr.Error: %v", aerr)
	}
	if he.Code != wantCode {
		t.Errorf("code: want %s, got %s", wantCode, he.Code)
	}
	if he.Component != wantComponent {
		t.Errorf("component: want %s, got %s", wantComponent, he.Component)
	}
	if wantPath != "" && he.Path != wantPath {
		t.Errorf("path: want %s, got %s", wantPath, he.Path)
	}
	msg := aerr.Error()
	for _, c := range contains {
		if !contains1(msg, c) {
			t.Errorf("message missing %q; got %q", c, msg)
		}
	}
}

func contains1(s, sub string) bool {
	return len(s) >= len(sub) && (sub == "" || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestErrorCasesAgainstCorpus(t *testing.T) {
	errorCase(t, "json/assert-exhaustive-fail", hewerr.CodeAssertionFailed, hewerr.ComponentApplier, "/permissions",
		[]string{"assertion-failed", "exhaustive"})
	errorCase(t, "json/assert-kind-fail", hewerr.CodeAssertionFailed, hewerr.ComponentApplier, "/mcpServers",
		[]string{"assertion-failed", "kind"})
	errorCase(t, "json/comment-inexpressible", hewerr.CodeInexpressible, hewerr.ComponentApplier, "/server",
		[]string{"inexpressible", "comment"})
	errorCase(t, "json/reapply-add-exists", hewerr.CodeAlreadyExists, hewerr.ComponentApplier, "/server/tls",
		[]string{"already-exists"})
	errorCase(t, "json/stale-context", hewerr.CodeStaleTarget, hewerr.ComponentApplier, "/server/port",
		[]string{"stale-target", "8080", "9090"})
}
