package harness

import (
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestSkipRegistryLookupMatching(t *testing.T) {
	rules := []SkipRule{
		{Case: "markdown/*", Seam: "*", Reason: "markdown severable (§8.7)"},
		{Case: "*/*", Seam: "render", Reason: "M9 renderer not built"},
		{Case: "json/add-key", Seam: "parse", Reason: "M3 parser not built"},
	}
	tests := []struct {
		name       string
		caseName   string
		seam       Seam
		wantReason string
		wantOK     bool
	}{
		{"family glob matches", "markdown/heading-add", SeamE2E, "markdown severable (§8.7)", true},
		{"family glob does not match another family", "json/add-key", SeamE2E, "", false},
		{"seam glob within family glob", "markdown/heading-add", SeamCLI, "markdown severable (§8.7)", true},
		{"two-level glob with fixed seam", "yaml/anything", SeamRender, "M9 renderer not built", true},
		{"two-level glob rejects the wrong seam", "yaml/anything", SeamDiff, "", false},
		{"exact rule", "json/add-key", SeamParse, "M3 parser not built", true},
		{"exact rule does not cover a sibling case", "json/add-key2", SeamParse, "", false},
		{"glob does not cross the slash", "markdown", SeamE2E, "", false},
		{"no rule at all", "toml/x", SeamCLI, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := NewSkipRegistry(rules, false)
			reason, ok := r.Lookup(tc.caseName, tc.seam)
			if ok != tc.wantOK || reason != tc.wantReason {
				t.Errorf("Lookup(%q, %q) = %q, %v; want %q, %v",
					tc.caseName, tc.seam, reason, ok, tc.wantReason, tc.wantOK)
			}
		})
	}
}

// TestSkipRegistryStarDoesNotMatchNestedCase pins path.Match semantics: "*"
// alone never covers a "family/case" name, so a blanket rule must be "*/*".
func TestSkipRegistryStarDoesNotMatchNestedCase(t *testing.T) {
	r := NewSkipRegistry([]SkipRule{{Case: "*", Seam: "*", Reason: "blanket"}}, false)
	if _, ok := r.Lookup("json/add-key", SeamParse); ok {
		t.Error(`"*" must not match "json/add-key" — path.Match does not cross "/"`)
	}
	if _, ok := r.Lookup("toplevel", SeamParse); !ok {
		t.Error(`"*" must match a name with no slash`)
	}
}

func TestSkipRegistryFirstMatchWins(t *testing.T) {
	rules := []SkipRule{
		{Case: "json/*", Seam: "*", Reason: "first"},
		{Case: "json/add-key", Seam: "parse", Reason: "second"},
	}
	r := NewSkipRegistry(rules, false)
	reason, ok := r.Lookup("json/add-key", SeamParse)
	if !ok || reason != "first" {
		t.Fatalf("Lookup = %q, %v; want the first matching rule", reason, ok)
	}
	unused := r.Unused()
	if len(unused) != 1 || unused[0].Reason != "second" {
		t.Errorf("Unused = %v; the shadowed rule must be reported unused", unused)
	}
}

func TestSkipRegistryHitCountingAndUnused(t *testing.T) {
	rules := []SkipRule{
		{Case: "json/*", Seam: "parse", Reason: "used"},
		{Case: "nosuch/*", Seam: "diff", Reason: "never fires"},
	}
	r := NewSkipRegistry(rules, false)
	if u := r.Unused(); len(u) != 2 {
		t.Fatalf("before any lookup all rules are unused, got %v", u)
	}
	for i := 0; i < 3; i++ {
		if _, ok := r.Lookup("json/case", SeamParse); !ok {
			t.Fatal("rule must match")
		}
	}
	r.Lookup("json/case", SeamDiff) // matches nothing
	unused := r.Unused()
	if len(unused) != 1 {
		t.Fatalf("Unused = %v, want exactly the dead rule", unused)
	}
	if unused[0].Reason != "never fires" {
		t.Errorf("Unused = %v, want the unmatched rule", unused)
	}
}

func TestSkipRegistryUnusedEmptyWhenAllFire(t *testing.T) {
	r := NewSkipRegistry([]SkipRule{{Case: "json/*", Seam: "*", Reason: "x"}}, false)
	r.Lookup("json/c", SeamParse)
	if u := r.Unused(); u != nil {
		t.Errorf("Unused = %v, want nil once every rule has fired", u)
	}
}

func TestSkipRegistryEmptyTable(t *testing.T) {
	r := NewSkipRegistry(nil, false)
	if reason, ok := r.Lookup("json/c", SeamParse); ok || reason != "" {
		t.Errorf("Lookup on an empty table = %q, %v", reason, ok)
	}
	if u := r.Unused(); len(u) != 0 {
		t.Errorf("Unused = %v, want empty", u)
	}
}

// TestSkipRegistryBadPatternIsInert: a malformed glob must not match anything
// and must stay visible through Unused.
func TestSkipRegistryBadPattern(t *testing.T) {
	rules := []SkipRule{
		{Case: "[", Seam: "*", Reason: "bad case pattern"},
		{Case: "*/*", Seam: "[", Reason: "bad seam pattern"},
		{Case: "json/*", Seam: "parse", Reason: "good"},
	}
	r := NewSkipRegistry(rules, false)
	reason, ok := r.Lookup("json/c", SeamParse)
	if !ok || reason != "good" {
		t.Fatalf("Lookup = %q, %v; a bad pattern must not shadow a good rule", reason, ok)
	}
	unused := r.Unused()
	if len(unused) != 2 {
		t.Fatalf("Unused = %v, want both malformed rules", unused)
	}
}

func TestNewSkipRegistryStoresNoSkipsFlag(t *testing.T) {
	for _, noSkips := range []bool{true, false} {
		r := NewSkipRegistry([]SkipRule{{Case: "*/*", Seam: "*", Reason: "x"}}, noSkips)
		if r.NoSkips != noSkips {
			t.Errorf("NoSkips = %v, want %v", r.NoSkips, noSkips)
		}
		// NoSkips changes nothing about matching itself; the engine acts on it.
		if _, ok := r.Lookup("json/c", SeamParse); !ok {
			t.Errorf("NoSkips=%v must not suppress matching", noSkips)
		}
	}
}

func TestSkipRegistryNilReceiverLookup(t *testing.T) {
	var r *SkipRegistry
	reason, ok := r.Lookup("json/c", SeamParse)
	if ok || reason != "" {
		t.Errorf("nil registry Lookup = %q, %v; want \"\", false", reason, ok)
	}
}

func TestSkipRuleString(t *testing.T) {
	rule := SkipRule{Case: "markdown/*", Seam: "render", Reason: "M9 pending"}
	want := `{case "markdown/*" seam "render": M9 pending}`
	if got := rule.String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	if !strings.Contains(rule.String(), "M9 pending") {
		t.Error("the reason must survive into the printed form")
	}
}

// TestSkipRegistryConcurrentLookup exercises the mutex: the corpus frontend
// may run seams in parallel.
func TestSkipRegistryConcurrentLookup(t *testing.T) {
	r := NewSkipRegistry([]SkipRule{{Case: "json/*", Seam: "*", Reason: "x"}}, false)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Lookup("json/c", SeamParse)
		}()
	}
	wg.Wait()
	if u := r.Unused(); len(u) != 0 {
		t.Errorf("Unused = %v after 32 hits", u)
	}
}

// TestSkipRegistryRuleOrderPreserved: Unused reports rules as authored, so the
// failure message points at the right table line.
func TestSkipRegistryUnusedPreservesOrder(t *testing.T) {
	rules := []SkipRule{
		{Case: "a/*", Seam: "*", Reason: "one"},
		{Case: "b/*", Seam: "*", Reason: "two"},
		{Case: "c/*", Seam: "*", Reason: "three"},
	}
	r := NewSkipRegistry(rules, false)
	r.Lookup("b/x", SeamParse)
	got := r.Unused()
	want := []SkipRule{rules[0], rules[2]}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Unused = %v, want %v", got, want)
	}
}
