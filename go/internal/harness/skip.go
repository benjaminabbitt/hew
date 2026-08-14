package harness

import (
	"fmt"
	"path"
	"sync"
)

// SkipRule marks a (case, seam) family as not-yet-implemented, with a
// recorded reason — the mechanism that satisfies "no case is skipped without
// a recorded skip reason" while milestones are in flight. Case and Seam are
// path.Match globs ("toml/*", "*").
type SkipRule struct {
	Case, Seam, Reason string
}

// SkipRegistry matches skip rules and tracks which ones fired. Two ratchets
// drive the table to zero: Unused() lets a test fail on rules that matched
// nothing (the table can only shrink truthfully as milestones land), and
// NoSkips mode turns every match into a failure — the end-state gate.
type SkipRegistry struct {
	NoSkips bool // HEW_CORPUS_NO_SKIPS=1: matched rules FAIL instead of skipping

	mu    sync.Mutex
	rules []SkipRule
	hits  []int
}

func NewSkipRegistry(rules []SkipRule, noSkips bool) *SkipRegistry {
	return &SkipRegistry{NoSkips: noSkips, rules: rules, hits: make([]int, len(rules))}
}

// Lookup returns the recorded reason if a rule covers (caseName, seam).
func (r *SkipRegistry) Lookup(caseName string, seam Seam) (string, bool) {
	if r == nil {
		return "", false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, rule := range r.rules {
		cm, err1 := path.Match(rule.Case, caseName)
		sm, err2 := path.Match(rule.Seam, string(seam))
		if err1 != nil || err2 != nil {
			continue // bad pattern; surfaces via Unused
		}
		if cm && sm {
			r.hits[i]++
			return rule.Reason, true
		}
	}
	return "", false
}

// Unused returns every rule that never matched — dead rules must be deleted,
// so the skip table stays an honest record of what is actually unbuilt.
func (r *SkipRegistry) Unused() []SkipRule {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []SkipRule
	for i, rule := range r.rules {
		if r.hits[i] == 0 {
			out = append(out, rule)
		}
	}
	return out
}

func (r *SkipRule) String() string {
	return fmt.Sprintf("{case %q seam %q: %s}", r.Case, r.Seam, r.Reason)
}
