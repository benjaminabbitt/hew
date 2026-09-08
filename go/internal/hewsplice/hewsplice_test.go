package hewsplice

import (
	"strings"
	"testing"
)

func TestApply_SplicesEachRangeInOrder(t *testing.T) {
	got, err := Apply([]byte("abcdef"), []Edit{{4, 5, "E"}, {0, 1, "A"}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if string(got) != "AbcdEf" {
		t.Fatalf("got %q, want %q", got, "AbcdEf")
	}
}

func TestApply_InsertionIsAZeroWidthRange(t *testing.T) {
	got, err := Apply([]byte("ac"), []Edit{{1, 1, "b"}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if string(got) != "abc" {
		t.Fatalf("got %q, want %q", got, "abc")
	}
}

// Several edits can share one offset, because an insertion has no width. Their
// AUTHORED order is then the only thing that says which comes first, so the
// sort has to be stable: a separator queued at the same offset as the content
// it separates must stay in front of it.
//
// This is the property the json binding's copy had lost — it sorted with
// sort.Slice, which is free to swap equal elements — and losing it changes the
// document produced from the same transforms without failing anything.
func TestApply_SameOffsetEditsKeepTheirAuthoredOrder(t *testing.T) {
	// Enough same-offset insertions that an unstable sort is overwhelmingly
	// likely to disturb at least one pair.
	var edits []Edit
	want := ""
	for _, s := range []string{"1", "2", "3", "4", "5", "6", "7", "8"} {
		edits = append(edits, Edit{3, 3, s})
		want += s
	}
	got, err := Apply([]byte("abcdef"), edits)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if string(got) != "abc"+want+"def" {
		t.Fatalf("same-offset edits were reordered:\ngot  %q\nwant %q", got, "abc"+want+"def")
	}
}

// Overlapping ranges have no defined composition, so they are refused rather
// than silently resolved in favour of whichever sorted first.
func TestApply_OverlappingRangesAreRefused(t *testing.T) {
	_, err := Apply([]byte("abcdef"), []Edit{{0, 3, "X"}, {2, 5, "Y"}})
	if err == nil {
		t.Fatal("overlapping edits must be refused")
	}
	if !strings.Contains(err.Error(), "HEW030") {
		t.Fatalf("the refusal must name HEW030, got %v", err)
	}
}

// Abutting is not overlapping: [0,3) and [3,5) share a boundary and compose.
func TestApply_AbuttingRangesAreAccepted(t *testing.T) {
	got, err := Apply([]byte("abcdef"), []Edit{{0, 3, "X"}, {3, 5, "Y"}})
	if err != nil {
		t.Fatalf("abutting edits must apply: %v", err)
	}
	if string(got) != "XYf" {
		t.Fatalf("got %q, want %q", got, "XYf")
	}
}

func TestApply_NoEditsReturnsTheSourceUnchanged(t *testing.T) {
	got, err := Apply([]byte("abcdef"), nil)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if string(got) != "abcdef" {
		t.Fatalf("got %q, want %q", got, "abcdef")
	}
}
