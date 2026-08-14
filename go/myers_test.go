package hew

import (
	"strings"
	"testing"
)

// script renders an edit script compactly: "=a" kept, "-a" deleted, "+a"
// inserted, in merged order.
func script(a, b []string) string {
	var sb strings.Builder
	for _, s := range myers(a, b) {
		switch s.Kind {
		case editEqual:
			sb.WriteString("=" + a[s.A])
		case editDelete:
			sb.WriteString("-" + a[s.A])
		case editInsert:
			sb.WriteString("+" + b[s.B])
		}
	}
	return sb.String()
}

func TestMyersScripts(t *testing.T) {
	cases := []struct {
		name string
		a, b []string
		want string
	}{
		{"identical", []string{"a", "b"}, []string{"a", "b"}, "=a=b"},
		{"append", []string{"a", "b"}, []string{"a", "b", "c"}, "=a=b+c"},
		{"prepend", []string{"a"}, []string{"z", "a"}, "+z=a"},
		{"delete middle", []string{"a", "b", "c"}, []string{"a", "c"}, "=a-b=c"},
		{"insert middle", []string{"a", "c"}, []string{"a", "b", "c"}, "=a+b=c"},
		{"all new", []string{"a"}, []string{"b"}, "-a+b"},
		{"empty old", nil, []string{"a", "b"}, "+a+b"},
		{"empty new", []string{"a", "b"}, nil, "-a-b"},
		{"both empty", nil, nil, ""},
		{"reorder", []string{"a", "b"}, []string{"b", "a"}, "-a=b+a"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := script(c.a, c.b); got != c.want {
				t.Fatalf("script(%v,%v) = %q, want %q", c.a, c.b, got, c.want)
			}
		})
	}
}

// A minimal script is not unique; §9.4-R1 pins WHICH one by breaking ties
// toward the earlier deletion, and a corpus diff case is only reproducible
// because of it. Swapping a single element is the smallest input where two
// minimal scripts exist.
func TestMyersBreaksTiesTowardEarlierDeletion(t *testing.T) {
	got := script([]string{"a", "b"}, []string{"b", "a"})
	if got != "-a=b+a" {
		t.Fatalf("tie must delete first: got %q, want %q", got, "-a=b+a")
	}
	// Determinism: same input, same script, every run.
	for i := 0; i < 20; i++ {
		if again := script([]string{"a", "b"}, []string{"b", "a"}); again != got {
			t.Fatalf("run %d produced %q, first run produced %q", i, again, got)
		}
	}
}

func TestMyersIsMinimal(t *testing.T) {
	a := []string{"1", "2", "3", "4", "5", "6"}
	b := []string{"1", "3", "4", "9", "5", "6"}
	steps := myers(a, b)
	edits := 0
	for _, s := range steps {
		if s.Kind != editEqual {
			edits++
		}
	}
	if edits != 2 {
		t.Fatalf("want a 2-edit script, got %d: %q", edits, script(a, b))
	}
	if got := script(a, b); got != "=1-2=3=4+9=5=6" {
		t.Fatalf("script = %q", got)
	}
}

// The merged order every step is emitted in must cover both inputs exactly
// once, or the slot list the differ builds from it would drop a child.
func TestMyersCoversBothSides(t *testing.T) {
	a := []string{"a", "b", "c", "d"}
	b := []string{"b", "x", "c", "d", "e"}
	seenA := make([]bool, len(a))
	seenB := make([]bool, len(b))
	for _, s := range myers(a, b) {
		if s.A >= 0 {
			if seenA[s.A] {
				t.Fatalf("a[%d] visited twice", s.A)
			}
			seenA[s.A] = true
		}
		if s.B >= 0 {
			if seenB[s.B] {
				t.Fatalf("b[%d] visited twice", s.B)
			}
			seenB[s.B] = true
		}
	}
	for i, ok := range seenA {
		if !ok {
			t.Fatalf("a[%d] never visited", i)
		}
	}
	for i, ok := range seenB {
		if !ok {
			t.Fatalf("b[%d] never visited", i)
		}
	}
}
