package hew

import (
	"strings"
	"testing"

	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

// O46: a HEW013 from a key-match that hit nothing MUST name its nearest miss,
// with the miss's TYPE (§10.3).
//
// §4.2 compares after decoding, so the spelling of a value carries its type and
// a match can fail for a reason that is invisible in the address: the element is
// there, its field reads the same, and only the scalar KIND differs. "no element
// matched" then sends the author looking for an element that is in front of
// them, and the remedy — quote the value, or unquote it — is not guessable from
// those words.
//
// The wording lives in the core (NoMatchDetail) rather than in each binding, so
// that the diagnostic is one sentence in every format rather than five.

const deps = `
deps:
  - name: left-pad
    version: "1.0"
  - name: right-pad
    version: 2
flags:
  - enabled: "true"
tags: ["8080", other]
`

func TestNoMatchNamesTheNearestMiss(t *testing.T) {
	doc := mustDoc(t, deps)
	tests := []struct {
		name  string
		path  string
		wants []string
	}{
		{
			// The spec's own example: the address says the number, the
			// document holds the string.
			name: "number address, string in the document",
			path: "/deps/version=1.0",
			wants: []string{"no element matches version=1.0 (number)",
				`1 element has version="1.0" (string)`, "quote the value to match a string"},
		},
		{
			name: "string address, number in the document",
			path: `/deps/version="2"`,
			wants: []string{`no element matches version="2" (string)`,
				"1 element has version=2 (number)", "remove the quotes to match a number"},
		},
		{
			name:  "string address, boolean in the document",
			path:  "/flags/enabled=true",
			wants: []string{`1 element has enabled="true" (string)`, "quote the value to match a string"},
		},
		{
			// The empty-field form has no field to name, so the clause names
			// the value alone.
			name:  "the =value form",
			path:  "/tags/=8080",
			wants: []string{`1 element has "8080" (string)`, "quote the value to match a string"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := resolveErrOf(t, tlOf(Transform{Op: OpRemove, Path: MustParsePath(tc.path)}), doc)
			mustCode(t, err, hewerr.CodeNoMatch)
			for _, want := range tc.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("message %q does not contain %q", err.Error(), want)
				}
			}
		})
	}
}

// TestNoMatchWithoutANearMissStaysShort: the clause is a diagnosis, not
// decoration. Where nothing in the container has the value at all, there is
// nothing to point at and the message says only what it knows.
func TestNoMatchWithoutANearMissStaysShort(t *testing.T) {
	doc := mustDoc(t, deps)
	err := resolveErrOf(t, tlOf(Transform{Op: OpRemove, Path: MustParsePath("/deps/name=nope")}), doc)
	mustCode(t, err, hewerr.CodeNoMatch)
	if got := err.Error(); !strings.Contains(got, "no element matches name=nope") {
		t.Fatalf("message: %v", got)
	}
	if strings.Contains(err.Error(), "element has") {
		t.Errorf("there is no near miss here; the message invented one: %v", err)
	}
}

// TestNoMatchDetailCountsTheElementsThatMiss pins the plural, and pins that an
// element with no such field at all is NOT a near miss — it is a different
// shape, and reporting it would send the reader after the wrong thing.
func TestNoMatchDetailCountsTheElementsThatMiss(t *testing.T) {
	seg := MustParsePath("/x/version=1.0").Segment(1)
	str := func(s string) Value { v, _ := ValueOf(s); return v }
	num := func(n float64) Value { v, _ := ValueOf(n); return v }

	got := NoMatchDetail(seg, []Value{str("1.0"), str("1.0"), num(2)})
	if !strings.Contains(got, "2 elements have") {
		t.Errorf("two misses must be counted: %q", got)
	}
	if got := NoMatchDetail(seg, nil); strings.Contains(got, "element has") {
		t.Errorf("no candidates, no near miss: %q", got)
	}
	// A candidate of the same kind AND text would have matched, so it can
	// never be a near miss; one with a different text is simply another
	// element.
	if got := NoMatchDetail(seg, []Value{num(1.0), str("9.9")}); strings.Contains(got, "element has") {
		t.Errorf("only a TYPE difference is a near miss: %q", got)
	}
}

// TestScalarOfProjectsDocumentValues pins the projection the diagnostic spells
// its miss with: a decoded document value has to come back as the scalar an
// ADDRESS would use for it, or the suggested spelling would be wrong.
func TestScalarOfProjectsDocumentValues(t *testing.T) {
	tests := []struct {
		yaml string
		kind ScalarKind
		text string
	}{
		{`"1.0"`, ScalarString, "1.0"},
		{`1.0`, ScalarNumber, "1.0"},
		{`8080`, ScalarNumber, "8080"},
		{`true`, ScalarBool, "true"},
		{`null`, ScalarNull, "null"},
		{`plain`, ScalarString, "plain"},
	}
	for _, tc := range tests {
		t.Run(tc.yaml, func(t *testing.T) {
			doc := mustDoc(t, "v: "+tc.yaml+"\n")
			node, _ := doc.Root().Member("v")
			got, ok := scalarOf(node.Value())
			if !ok || got.Kind != tc.kind || got.Text != tc.text {
				t.Fatalf("scalarOf(%s) = %v %q (%v), want %v %q", tc.yaml, got.Kind, got.Text, ok, tc.kind, tc.text)
			}
		})
	}
	// A container has no scalar spelling at all.
	doc := mustDoc(t, "v: {a: 1}\n")
	node, _ := doc.Root().Member("v")
	if _, ok := scalarOf(node.Value()); ok {
		t.Error("a mapping is not a scalar and has no address spelling")
	}
	if _, ok := scalarOf(Value{}); ok {
		t.Error("the absent value is not a scalar")
	}
}
