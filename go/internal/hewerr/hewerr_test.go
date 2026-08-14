package hewerr

import (
	"errors"
	"fmt"
	"testing"
)

// TestErrorMessageShape pins the §10.3 diagnostic rendering exactly: which
// fields appear, in what order, and what a zero field suppresses. The corpus
// asserts on message substrings, so the shape is contract, not cosmetics.
func TestErrorMessageShape(t *testing.T) {
	tests := []struct {
		name string
		err  Error
		want string
	}{
		{
			name: "all fields",
			err: Error{
				Code: CodeStaleTarget, Component: ComponentApplier,
				Target: "config.yaml", Path: "/server/timeout",
				PatchFile: "patch.hew", PatchLine: 9, TargetLine: 6,
				Want: "30", Got: "45", Detail: "target drifted since the patch was written",
			},
			want: "hew: config.yaml:/server/timeout: HEW010 stale-target\n" +
				"  patch.hew:9: expected 30\n" +
				"  config.yaml:6: found 45\n" +
				"  target drifted since the patch was written",
		},
		{
			name: "code only",
			err:  Error{Code: CodeParse},
			want: "hew:  HEW001 parse-error",
		},
		{
			name: "unknown code renders without a slug",
			err:  Error{Code: Code("HEW999")},
			want: "hew:  HEW999",
		},
		{
			name: "target without path",
			err:  Error{Code: CodeTargetPath, Target: "config.yaml"},
			want: "hew: config.yaml: HEW003 target-path-error",
		},
		{
			name: "path without target",
			err:  Error{Code: CodeNoMatch, Path: "/a/b"},
			want: "hew: /a/b: HEW013 no-match",
		},
		{
			name: "patch line without patch file falls back to 'patch'",
			err:  Error{Code: CodeAssertionFailed, PatchLine: 4},
			want: "hew:  HEW011 assertion-failed\n  patch:4:",
		},
		{
			name: "patch line with want",
			err:  Error{Code: CodeAssertionFailed, PatchFile: "p.hew", PatchLine: 4, Want: "true"},
			want: "hew:  HEW011 assertion-failed\n  p.hew:4: expected true",
		},
		{
			name: "want without patch line uses the bare expected line",
			err:  Error{Code: CodeAssertionFailed, Want: "true"},
			want: "hew:  HEW011 assertion-failed\n  expected true",
		},
		{
			name: "patch line zero and want empty emit neither line",
			err:  Error{Code: CodeConflict, Got: "x"},
			want: "hew:  HEW030 conflict\n   found x",
		},
		{
			name: "got with target but no target line",
			err:  Error{Code: CodeConflict, Target: "t.json", Got: "x"},
			want: "hew: t.json: HEW030 conflict\n  t.json: found x",
		},
		{
			name: "got with target and target line",
			err:  Error{Code: CodeConflict, Target: "t.json", TargetLine: 12, Got: "x"},
			want: "hew: t.json: HEW030 conflict\n  t.json:12: found x",
		},
		{
			name: "detail only",
			err:  Error{Code: CodeInexpressible, Detail: "no surface migration"},
			want: "hew:  HEW020 inexpressible\n  no surface migration",
		},
		{
			name: "negative patch line is not a provenance line",
			err:  Error{Code: CodeParse, PatchLine: -1},
			want: "hew:  HEW001 parse-error",
		},
		{
			name: "empty error still names itself hew",
			err:  Error{},
			want: "hew:  ",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.err.Error()
			if got != tc.want {
				t.Errorf("Error() =\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

// TestErrorPointerReceiverSatisfiesError guards the interface wiring: the
// engine passes *Error around as error everywhere.
func TestErrorPointerReceiverSatisfiesError(t *testing.T) {
	var err error = &Error{Code: CodeParse}
	if err.Error() == "" {
		t.Fatal("*Error must render a non-empty message")
	}
}

func TestCodeName(t *testing.T) {
	tests := []struct {
		code Code
		want string
	}{
		{CodeParse, "parse-error"},
		{CodeTargetParse, "target-parse-error"},
		{CodeTargetPath, "target-path-error"},
		{CodeStaleTarget, "stale-target"},
		{CodeAssertionFailed, "assertion-failed"},
		{CodeAmbiguousMatch, "ambiguous-match"},
		{CodeNoMatch, "no-match"},
		{CodeAlreadyExists, "already-exists"},
		{CodeInexpressible, "inexpressible"},
		{CodeUnsupportedFormat, "unsupported-format"},
		{CodeConflict, "conflict"},
		{CodeAnchorAmbiguity, "anchor-ambiguity"},
		{CodeSurfaceAmbiguity, "surface-ambiguity"},
		{Code("HEW999"), ""},
		{Code(""), ""},
		{Code("hew001"), ""},
	}
	for _, tc := range tests {
		t.Run(string(tc.code), func(t *testing.T) {
			if got := tc.code.Name(); got != tc.want {
				t.Errorf("Code(%q).Name() = %q, want %q", tc.code, got, tc.want)
			}
		})
	}
}

// TestCodeValues pins the numeric codes themselves — a renumbering is a
// corpus-visible break.
func TestCodeValues(t *testing.T) {
	tests := []struct {
		code Code
		want string
	}{
		{CodeParse, "HEW001"},
		{CodeTargetParse, "HEW002"},
		{CodeTargetPath, "HEW003"},
		{CodeStaleTarget, "HEW010"},
		{CodeAssertionFailed, "HEW011"},
		{CodeAmbiguousMatch, "HEW012"},
		{CodeNoMatch, "HEW013"},
		{CodeAlreadyExists, "HEW014"},
		{CodeInexpressible, "HEW020"},
		{CodeUnsupportedFormat, "HEW021"},
		{CodeConflict, "HEW030"},
		{CodeAnchorAmbiguity, "HEW040"},
		{CodeSurfaceAmbiguity, "HEW041"},
	}
	for _, tc := range tests {
		if string(tc.code) != tc.want {
			t.Errorf("code %q != %q", string(tc.code), tc.want)
		}
	}
}

func TestComponentString(t *testing.T) {
	tests := []struct {
		comp Component
		want string
	}{
		{ComponentParser, "parser"},
		{ComponentResolver, "resolver"},
		{ComponentApplier, "applier"},
		{ComponentDiffer, "differ"},
		{ComponentRenderer, "renderer"},
		{ComponentCLI, "cli"},
		{Component(6), "component(6)"},
		{Component(-1), "component(-1)"},
		{Component(99), "component(99)"},
	}
	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			if got := tc.comp.String(); got != tc.want {
				t.Errorf("Component(%d).String() = %q, want %q", int(tc.comp), got, tc.want)
			}
		})
	}
}

// TestComponentIotaOrder pins the constant order: Component is compared
// numerically against the corpus's error_seam mapping.
func TestComponentIotaOrder(t *testing.T) {
	want := []Component{
		ComponentParser, ComponentResolver, ComponentApplier,
		ComponentDiffer, ComponentRenderer, ComponentCLI,
	}
	for i, c := range want {
		if int(c) != i {
			t.Errorf("%s = %d, want %d", c, int(c), i)
		}
	}
}

func TestAs(t *testing.T) {
	base := &Error{Code: CodeNoMatch, Component: ComponentApplier, Path: "/a"}

	t.Run("direct", func(t *testing.T) {
		got, ok := As(base)
		if !ok || got != base {
			t.Fatalf("As(direct) = %v, %v; want the same pointer, true", got, ok)
		}
	})

	t.Run("wrapped once", func(t *testing.T) {
		got, ok := As(fmt.Errorf("apply: %w", base))
		if !ok || got != base {
			t.Fatalf("As(wrapped) = %v, %v; want the same pointer, true", got, ok)
		}
	})

	t.Run("wrapped twice", func(t *testing.T) {
		err := fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", base))
		got, ok := As(err)
		if !ok || got != base {
			t.Fatalf("As(double-wrapped) = %v, %v; want the same pointer, true", got, ok)
		}
	})

	t.Run("plain error", func(t *testing.T) {
		got, ok := As(errors.New("boom"))
		if ok {
			t.Fatalf("As(plain) = %v, true; want nil, false", got)
		}
		if got != nil {
			t.Fatalf("As(plain) returned %v; want nil", got)
		}
	})

	t.Run("wrapped plain error", func(t *testing.T) {
		got, ok := As(fmt.Errorf("ctx: %w", errors.New("boom")))
		if ok || got != nil {
			t.Fatalf("As(wrapped plain) = %v, %v; want nil, false", got, ok)
		}
	})

	t.Run("nil", func(t *testing.T) {
		got, ok := As(nil)
		if ok || got != nil {
			t.Fatalf("As(nil) = %v, %v; want nil, false", got, ok)
		}
	})

	t.Run("errors.Is still reaches the wrapped value", func(t *testing.T) {
		wrapped := fmt.Errorf("outer: %w", base)
		if !errors.Is(wrapped, error(base)) {
			t.Fatal("errors.Is must find the wrapped *Error")
		}
	})
}
