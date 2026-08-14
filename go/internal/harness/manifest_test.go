package harness

import (
	"reflect"
	"strings"
	"testing"

	"github.com/hew-format/hew/internal/hewerr"
)

func TestParseManifestDecodesEveryField(t *testing.T) {
	src := []byte(`
name: yaml/keyed-array-add
seams: [parse, apply-ir, e2e]
kind: ok
format: yaml
ops: [OP-16, OP-01]
why: |
  pins the headline operation
spec: "§6.2, §11 OP-16"
error: HEW010
error_seam: apply-ir
error_path: /server/timeout
patch_line: 9
message_contains: ["stale-target", "expected 30"]
argv: ["apply", "patch.hew"]
exit: 2
stdout: out.txt
stderr_contains: ["HEW001"]
target_unchanged: true
expected: expected.yaml
requires: git-fixture
fixture: |
  git init .
`)
	m, err := ParseManifest(src)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if m.Name != "yaml/keyed-array-add" {
		t.Errorf("Name = %q", m.Name)
	}
	if !reflect.DeepEqual(m.Seams, []Seam{SeamParse, SeamApplyIR, SeamE2E}) {
		t.Errorf("Seams = %v", m.Seams)
	}
	if m.Kind != "ok" || m.Format != "yaml" {
		t.Errorf("Kind/Format = %q/%q", m.Kind, m.Format)
	}
	if !reflect.DeepEqual(m.Ops, []string{"OP-16", "OP-01"}) {
		t.Errorf("Ops = %v", m.Ops)
	}
	if !strings.Contains(m.Why, "headline operation") {
		t.Errorf("Why = %q", m.Why)
	}
	if m.Spec != "§6.2, §11 OP-16" {
		t.Errorf("Spec = %q", m.Spec)
	}
	if m.Error != "HEW010" || m.ErrorSeam != "apply-ir" || m.ErrorPath != "/server/timeout" || m.PatchLine != 9 {
		t.Errorf("error fields = %q/%q/%q/%d", m.Error, m.ErrorSeam, m.ErrorPath, m.PatchLine)
	}
	if !reflect.DeepEqual(m.MessageContains, []string{"stale-target", "expected 30"}) {
		t.Errorf("MessageContains = %v", m.MessageContains)
	}
	if !reflect.DeepEqual(m.Argv, []string{"apply", "patch.hew"}) {
		t.Errorf("Argv = %v", m.Argv)
	}
	if m.Exit == nil || *m.Exit != 2 {
		t.Errorf("Exit = %v", m.Exit)
	}
	if m.Stdout == nil || *m.Stdout != "out.txt" {
		t.Errorf("Stdout = %v", m.Stdout)
	}
	if !reflect.DeepEqual(m.StderrContains, []string{"HEW001"}) {
		t.Errorf("StderrContains = %v", m.StderrContains)
	}
	if !m.TargetUnchanged {
		t.Error("TargetUnchanged must decode true")
	}
	if m.Expected != "expected.yaml" || m.Requires != "git-fixture" {
		t.Errorf("Expected/Requires = %q/%q", m.Expected, m.Requires)
	}
	if !strings.Contains(m.Fixture, "git init") {
		t.Errorf("Fixture = %q", m.Fixture)
	}
}

// TestParseManifestExitAndStdoutTristate pins the pointer fields: absent is
// distinguishable from zero, and "" (must-be-empty) from a fixture name.
func TestParseManifestExitAndStdoutTristate(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		m, err := ParseManifest([]byte("name: a/b\n"))
		if err != nil {
			t.Fatal(err)
		}
		if m.Exit != nil {
			t.Errorf("Exit = %v, want nil", *m.Exit)
		}
		if m.Stdout != nil {
			t.Errorf("Stdout = %q, want nil", *m.Stdout)
		}
	})
	t.Run("zero and empty", func(t *testing.T) {
		m, err := ParseManifest([]byte("exit: 0\nstdout: \"\"\n"))
		if err != nil {
			t.Fatal(err)
		}
		if m.Exit == nil || *m.Exit != 0 {
			t.Errorf("Exit = %v, want pointer to 0", m.Exit)
		}
		if m.Stdout == nil || *m.Stdout != "" {
			t.Errorf("Stdout = %v, want pointer to empty string", m.Stdout)
		}
	})
}

// TestParseManifestStrict is runner obligation 5's first line: an unknown
// manifest field must be a corpus error, never a silent pass-through.
func TestParseManifestStrict(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string
	}{
		{"unknown field", "name: a/b\nnosuchfield: 1\n", "nosuchfield"},
		{"near-miss field name", "name: a/b\nseam: [parse]\n", "seam"},
		{"typo in nested-looking key", "name: a/b\nmessage_contain: [x]\n", "message_contain"},
		{"wrong type", "name: [a, b]\n", "case.yaml"},
		{"malformed yaml", "name: a/b\nseams: [parse\n", "case.yaml"},
		{"empty document", "", "case.yaml"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, err := ParseManifest([]byte(tc.src))
			if err == nil {
				t.Fatalf("want error, got manifest %+v", m)
			}
			if m != nil {
				t.Errorf("manifest must be nil on error, got %+v", m)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q must mention %q", err, tc.wantErr)
			}
			if !strings.HasPrefix(err.Error(), "case.yaml: ") {
				t.Errorf("error %q must be prefixed with the file it came from", err)
			}
		})
	}
}

func okManifest() Manifest {
	return Manifest{Name: "json/add-key", Seams: []Seam{SeamParse}, Kind: "ok"}
}

func TestValidate(t *testing.T) {
	exit0 := 0
	tests := []struct {
		name     string
		mutate   func(*Manifest)
		rel      string
		wantOK   bool
		contains []string
		absent   []string
	}{
		{
			name:   "valid ok case",
			mutate: func(*Manifest) {},
			wantOK: true,
		},
		{
			name:     "name does not match directory",
			mutate:   func(m *Manifest) { m.Name = "json/other" },
			contains: []string{`name "json/other" != directory "json/add-key"`},
		},
		{
			name:     "empty name",
			mutate:   func(m *Manifest) { m.Name = "" },
			contains: []string{`name "" != directory "json/add-key"`},
		},
		{
			name:     "unknown kind",
			mutate:   func(m *Manifest) { m.Kind = "weird" },
			contains: []string{`unknown kind "weird"`},
		},
		{
			name:     "empty kind",
			mutate:   func(m *Manifest) { m.Kind = "" },
			contains: []string{`unknown kind ""`},
		},
		{
			name:     "no seams",
			mutate:   func(m *Manifest) { m.Seams = nil },
			contains: []string{"no seams declared"},
		},
		{
			name:     "unknown seam",
			mutate:   func(m *Manifest) { m.Seams = []Seam{SeamParse, "frobnicate"} },
			contains: []string{`unknown seam "frobnicate"`},
		},
		{
			name:   "every valid seam accepted",
			mutate: func(m *Manifest) { m.Seams = []Seam{SeamParse, SeamApplyIR, SeamE2E, SeamRender, SeamDiff, SeamCLI} },
			wantOK: true,
		},
		{
			name: "error kind missing error and error_seam",
			mutate: func(m *Manifest) {
				m.Kind = "error"
				m.MessageContains = []string{"boom"}
			},
			contains: []string{"error case missing error/error_seam"},
			absent:   []string{"message_contains", "unknown error_seam"},
		},
		{
			name: "error kind missing only error",
			mutate: func(m *Manifest) {
				m.Kind = "error"
				m.ErrorSeam = "parse"
				m.MessageContains = []string{"boom"}
			},
			contains: []string{"error case missing error/error_seam"},
		},
		{
			name: "error kind missing message_contains",
			mutate: func(m *Manifest) {
				m.Kind = "error"
				m.Error = "HEW001"
				m.ErrorSeam = "parse"
			},
			contains: []string{"error case missing message_contains"},
			absent:   []string{"error case missing error/error_seam"},
		},
		{
			name: "error kind with unknown error_seam",
			mutate: func(m *Manifest) {
				m.Kind = "error"
				m.Error = "HEW001"
				m.ErrorSeam = "nonsense"
				m.MessageContains = []string{"boom"}
			},
			contains: []string{`unknown error_seam "nonsense"`},
		},
		{
			name: "valid error case",
			mutate: func(m *Manifest) {
				m.Kind = "error"
				m.Error = "HEW001"
				m.ErrorSeam = "parse"
				m.MessageContains = []string{"parse-error"}
			},
			wantOK: true,
		},
		{
			name: "cli kind missing argv and exit",
			mutate: func(m *Manifest) {
				m.Kind = "cli"
				m.Seams = []Seam{SeamCLI}
			},
			contains: []string{"cli case missing argv/exit"},
		},
		{
			name: "cli kind missing exit only",
			mutate: func(m *Manifest) {
				m.Kind = "cli"
				m.Seams = []Seam{SeamCLI}
				m.Argv = []string{"apply"}
			},
			contains: []string{"cli case missing argv/exit"},
		},
		{
			name: "cli kind missing argv only",
			mutate: func(m *Manifest) {
				m.Kind = "cli"
				m.Seams = []Seam{SeamCLI}
				m.Exit = &exit0
			},
			contains: []string{"cli case missing argv/exit"},
		},
		{
			name: "valid cli case with exit 0",
			mutate: func(m *Manifest) {
				m.Kind = "cli"
				m.Seams = []Seam{SeamCLI}
				m.Argv = []string{"apply", "patch.hew"}
				m.Exit = &exit0
			},
			wantOK: true,
		},
		{
			name:     "unknown requires",
			mutate:   func(m *Manifest) { m.Requires = "docker-fixture" },
			contains: []string{`unknown requires "docker-fixture"`},
		},
		{
			name:   "known requires",
			mutate: func(m *Manifest) { m.Requires = "git-fixture" },
			wantOK: true,
		},
		{
			name: "ok kind ignores error and cli obligations",
			mutate: func(m *Manifest) {
				m.Kind = "ok"
				m.Error = ""
				m.Argv = nil
			},
			wantOK: true,
		},
		{
			name: "all problems reported together",
			mutate: func(m *Manifest) {
				m.Name = "elsewhere"
				m.Kind = "error"
				m.Seams = []Seam{"nope"}
				m.ErrorSeam = "bogus"
				m.Requires = "nothing"
			},
			contains: []string{
				`name "elsewhere" != directory "json/add-key"`,
				`unknown seam "nope"`,
				"error case missing error/error_seam",
				"error case missing message_contains",
				`unknown error_seam "bogus"`,
				`unknown requires "nothing"`,
				"; ",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := okManifest()
			tc.mutate(&m)
			rel := tc.rel
			if rel == "" {
				rel = "json/add-key"
			}
			err := m.Validate(rel)
			if tc.wantOK {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() = nil, want problems %v", tc.contains)
			}
			msg := err.Error()
			if !strings.HasPrefix(msg, "corpus error in "+rel+": ") {
				t.Errorf("message %q must be prefixed with the case name", msg)
			}
			for _, want := range tc.contains {
				if !strings.Contains(msg, want) {
					t.Errorf("message %q missing %q", msg, want)
				}
			}
			for _, bad := range tc.absent {
				if strings.Contains(msg, bad) {
					t.Errorf("message %q must not mention %q", msg, bad)
				}
			}
		})
	}
}

// TestValidateKindErrorIsNotValidKind documents that "error" is a manifest
// kind but not one of the three accepted kinds unless spelled exactly; the
// accepted set is closed.
func TestValidateAcceptedKinds(t *testing.T) {
	for _, kind := range []string{"ok", "error", "cli"} {
		m := okManifest()
		m.Kind = kind
		switch kind {
		case "error":
			m.Error, m.ErrorSeam, m.MessageContains = "HEW001", "parse", []string{"x"}
		case "cli":
			zero := 0
			m.Argv, m.Exit = []string{"apply"}, &zero
		}
		if err := m.Validate(m.Name); err != nil {
			t.Errorf("kind %q must be accepted: %v", kind, err)
		}
	}
	for _, kind := range []string{"OK", "Error", "ok ", "warn", "skip"} {
		m := okManifest()
		m.Kind = kind
		if err := m.Validate(m.Name); err == nil {
			t.Errorf("kind %q must be rejected", kind)
		}
	}
}

func TestComponent(t *testing.T) {
	tests := []struct {
		seam    string
		want    hewerr.Component
		wantErr bool
	}{
		{seam: "parse", want: hewerr.ComponentParser},
		{seam: "parser", want: hewerr.ComponentParser},
		{seam: "apply-ir", want: hewerr.ComponentApplier},
		{seam: "applier", want: hewerr.ComponentApplier},
		{seam: "apply", want: hewerr.ComponentApplier},
		{seam: "diff", want: hewerr.ComponentDiffer},
		{seam: "differ", want: hewerr.ComponentDiffer},
		{seam: "render", want: hewerr.ComponentRenderer},
		{seam: "renderer", want: hewerr.ComponentRenderer},
		{seam: "cli", want: hewerr.ComponentCLI},
		{seam: "", wantErr: true},
		{seam: "e2e", wantErr: true},
		{seam: "resolver", wantErr: true},
		{seam: "Parse", wantErr: true},
		{seam: "apply ir", wantErr: true},
		{seam: "junk", wantErr: true},
	}
	for _, tc := range tests {
		name := tc.seam
		if name == "" {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			m := Manifest{ErrorSeam: tc.seam}
			got, err := m.Component()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Component(%q) = %v, want error", tc.seam, got)
				}
				if !strings.Contains(err.Error(), "unknown error_seam") || !strings.Contains(err.Error(), tc.seam) {
					t.Errorf("error %q must name the offending seam", err)
				}
				if got != 0 {
					t.Errorf("component on error = %v, want zero", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Component(%q): %v", tc.seam, err)
			}
			if got != tc.want {
				t.Errorf("Component(%q) = %v, want %v", tc.seam, got, tc.want)
			}
		})
	}
}

func TestStderrCode(t *testing.T) {
	tests := []struct {
		name     string
		contains []string
		want     string
		wantOK   bool
	}{
		{name: "nil list", contains: nil},
		{name: "no code", contains: []string{"usage:", "no such file"}},
		{name: "bare code", contains: []string{"HEW001"}, want: "HEW001", wantOK: true},
		{name: "code among prose", contains: []string{"parse failed", "HEW021"}, want: "HEW021", wantOK: true},
		{name: "first code wins", contains: []string{"HEW013", "HEW001"}, want: "HEW013", wantOK: true},
		{name: "embedded in a sentence is not a match", contains: []string{"error HEW001 parse-error"}},
		{name: "too few digits", contains: []string{"HEW1"}},
		{name: "too many digits", contains: []string{"HEW0011"}},
		{name: "lowercase", contains: []string{"hew001"}},
		{name: "trailing space", contains: []string{"HEW001 "}},
		{name: "newline suffix rejected", contains: []string{"HEW001\n"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := Manifest{StderrContains: tc.contains}
			got, ok := m.StderrCode()
			if ok != tc.wantOK || got != tc.want {
				t.Errorf("StderrCode() = %q, %v; want %q, %v", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestSortedSeams(t *testing.T) {
	tests := []struct {
		name string
		in   []Seam
		want []Seam
	}{
		{
			name: "reversed",
			in:   []Seam{SeamCLI, SeamDiff, SeamRender, SeamE2E, SeamApplyIR, SeamParse},
			want: []Seam{SeamParse, SeamApplyIR, SeamE2E, SeamRender, SeamDiff, SeamCLI},
		},
		{
			name: "already ordered",
			in:   []Seam{SeamParse, SeamApplyIR, SeamE2E},
			want: []Seam{SeamParse, SeamApplyIR, SeamE2E},
		},
		{
			name: "sparse",
			in:   []Seam{SeamDiff, SeamParse},
			want: []Seam{SeamParse, SeamDiff},
		},
		{
			name: "render before diff before cli",
			in:   []Seam{SeamCLI, SeamRender, SeamDiff},
			want: []Seam{SeamRender, SeamDiff, SeamCLI},
		},
		{
			name: "e2e after apply-ir",
			in:   []Seam{SeamE2E, SeamApplyIR},
			want: []Seam{SeamApplyIR, SeamE2E},
		},
		{
			name: "empty",
			in:   nil,
			want: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := append([]Seam(nil), tc.in...)
			m := Manifest{Seams: in}
			got := m.SortedSeams()
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("SortedSeams() = %v, want %v", got, tc.want)
			}
			if !reflect.DeepEqual(m.Seams, tc.in) {
				t.Errorf("SortedSeams mutated the manifest: %v != %v", m.Seams, tc.in)
			}
		})
	}
}

// TestSortedSeamsDoesNotAliasManifest guards the defensive copy: mutating the
// returned slice must not reorder the manifest's own seam list.
func TestSortedSeamsDoesNotAliasManifest(t *testing.T) {
	m := Manifest{Seams: []Seam{SeamParse, SeamE2E}}
	got := m.SortedSeams()
	got[0] = "clobbered"
	if m.Seams[0] != SeamParse {
		t.Fatalf("manifest seam aliased the returned slice: %v", m.Seams)
	}
}
