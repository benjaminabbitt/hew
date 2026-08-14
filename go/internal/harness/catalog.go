package harness

import (
	"fmt"
	"os"
	"regexp"
	"sort"
)

// CatalogOp is one §11 operations-catalog entry.
type CatalogOp struct {
	ID   string // "OP-01"
	Tier string // "v0", "deferred", "rejected"
}

var (
	opBlockRE  = regexp.MustCompile(`(?m)^#### `)
	opIDRE     = regexp.MustCompile(`OP-\d+`)
	statusRE   = regexp.MustCompile(`\*\*Status\*\*\s+(\w+)`)
	opTableRE  = regexp.MustCompile(`(?m)^\|\s*(OP-\d+)\s*\|[^|\n]*\|[^|\n]*\|\s*(\w+)\s*\|`)
)

// LoadCatalog extracts the §11 catalog from the spec. Entries appear in two
// shapes: `#### OP-nn ...` header blocks (sometimes several ops combined on
// one header line, sharing one **Status**) and `| OP-nn | name | sources |
// tier |` table rows (the §11.9 tail entries such as OP-52).
func LoadCatalog(specPath string) ([]CatalogOp, error) {
	src, err := os.ReadFile(specPath)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var ops []CatalogOp
	add := func(id, tier string) {
		if !seen[id] {
			seen[id] = true
			ops = append(ops, CatalogOp{ID: id, Tier: tier})
		}
	}
	locs := opBlockRE.FindAllIndex(src, -1)
	for i, loc := range locs {
		end := len(src)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := src[loc[0]:end]
		headerEnd := len(block)
		if nl := indexByte(block, '\n'); nl >= 0 {
			headerEnd = nl
		}
		ids := opIDRE.FindAllString(string(block[:headerEnd]), -1)
		if len(ids) == 0 {
			continue
		}
		tier := ""
		if m := statusRE.FindSubmatch(block); m != nil {
			tier = string(m[1])
		}
		for _, id := range ids {
			add(id, tier)
		}
	}
	for _, m := range opTableRE.FindAllSubmatch(src, -1) {
		add(string(m[1]), string(m[2]))
	}
	if len(ops) == 0 {
		return nil, fmt.Errorf("no OP-nn catalog entries found in %s", specPath)
	}
	return ops, nil
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

// Coverage reports the §13.6 obligation-7 view: how many cases exercise each
// catalog op, which v0 ops have no case, and which case ops: references name
// no catalog entry (typo guard).
type Coverage struct {
	CasesPerOp   map[string]int
	UncoveredV0  []string // v0-tier ops with zero cases
	UnknownRefs  []string // "case: OP-xx" references not in the catalog
}

func ComputeCoverage(catalog []CatalogOp, cases []*Case) Coverage {
	known := make(map[string]CatalogOp, len(catalog))
	for _, op := range catalog {
		known[op.ID] = op
	}
	cov := Coverage{CasesPerOp: map[string]int{}}
	for _, c := range cases {
		for _, ref := range c.Ops {
			if _, ok := known[ref]; !ok {
				cov.UnknownRefs = append(cov.UnknownRefs, fmt.Sprintf("%s: %s", c.Rel, ref))
				continue
			}
			cov.CasesPerOp[ref]++
		}
	}
	for _, op := range catalog {
		if op.Tier == "v0" && cov.CasesPerOp[op.ID] == 0 {
			cov.UncoveredV0 = append(cov.UncoveredV0, op.ID)
		}
	}
	sort.Strings(cov.UncoveredV0)
	sort.Strings(cov.UnknownRefs)
	return cov
}
