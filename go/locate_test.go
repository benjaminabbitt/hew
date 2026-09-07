package hew

import (
	"strings"
	"testing"
)

// Slice 4 of satisfied-recoil: SCORED LOCATION. Once a content hash collides —
// two members of a collection hold the same value, so they hash alike — the
// digest has said everything it can, and only POSITION separates the candidates.
// Locate is that decision, and it is one function because all six matching sites
// (resolve.go's stepHash and the four bindings' appliers plus json's planRemove)
// must refuse and locate identically.
//
// The model is ORDINAL, not a weighted sum. Among colliding candidates the
// positions are unique — one element per index — so there is never a numeric tie
// for a weight to break, and any float would have to be invented and tuned. Each
// tier is instead a statement about the document, and the tier IS the explanation.
//
// The floor: positional evidence must EXIST and be UNANIMOUS. The two refusals
// differ in kind — a CONTRADICTION (the front and back anchors name different
// elements, or two candidates sit equally far from the recorded position) refuses
// because a tie refuses; NO EVIDENCE refuses because nothing was recorded. A
// weak-but-unanimous signal locates: TierDisplaced sits below TierConflicted in
// confidence and still locates, because contradiction is not weakness.

func at(n int) *int { return &n }

// The examples first: one case per tier, in descending confidence, each the
// smallest document shape that produces it.
func TestLocateTiers(t *testing.T) {
	for _, c := range []struct {
		name      string
		cands     []int // element indices whose value hashed to the address
		curLen    int   // the collection's length NOW
		at, len   *int  // the recorded advisory; nil when the patch carried none
		wantTier  Tier
		wantIndex int
		wantOK    bool
	}{
		// A digest that matches one member has identified it. No advisory is
		// needed or consulted: this is the whole point of hashing the member.
		{
			name:  "a unique digest locates on identity alone",
			cands: []int{2}, curLen: 5, at: nil, len: nil,
			wantTier: TierIdentity, wantIndex: 2, wantOK: true,
		},
		// The collection is still the length it was, and a candidate sits exactly
		// where the patch recorded it. Nothing on either side of it resized, so
		// the index from the front and the index from the end agree.
		{
			name:  "an unchanged collection anchors the recorded index from both ends",
			cands: []int{1, 3}, curLen: 5, at: at(3), len: at(5),
			wantTier: TierAnchored, wantIndex: 3, wantOK: true,
		},
		// Someone APPENDED. Every element kept its distance from the front and
		// lost its distance from the end, so only the front anchor survives —
		// and it survives uniquely. This is the drift that refuses before slice 4.
		{
			name:  "an append leaves the front anchor intact and it alone locates",
			cands: []int{2, 3}, curLen: 6, at: at(3), len: at(5),
			wantTier: TierOneSided, wantIndex: 3, wantOK: true,
		},
		// Someone PREPENDED. The mirror case: distance from the END is what
		// survived. Recorded at 1 of 4 is 2 from the end; in a 6-long collection
		// that is index 3.
		{
			name:  "a prepend leaves the back anchor intact and it alone locates",
			cands: []int{0, 3}, curLen: 6, at: at(1), len: at(4),
			wantTier: TierOneSided, wantIndex: 3, wantOK: true,
		},
		// Neither anchor holds, but one candidate is strictly nearer the recorded
		// position than the other. Weak — the element moved relative to both ends
		// — yet unanimous, so it locates. This is the maintainer's floor ruling.
		{
			name:  "with no anchor the strictly nearest candidate still locates",
			cands: []int{4, 9}, curLen: 12, at: at(3), len: at(12),
			wantTier: TierDisplaced, wantIndex: 4, wantOK: true,
		},
		// CONTRADICTION, not weakness. The front anchor names element 3 and the
		// back anchor names element 4; both are exactly as credible, and the
		// collection resized so there is nothing left to break the tie. Refuse —
		// even though a nearest-wins reading would happily answer.
		{
			name:  "front and back anchors naming different elements refuse",
			cands: []int{3, 4}, curLen: 6, at: at(3), len: at(5),
			wantTier: TierConflicted, wantOK: false,
		},
		// The other contradiction: no anchor holds and two candidates sit the
		// same distance from the recorded position. Picking either is a coin flip.
		{
			name:  "candidates equidistant from the recorded position refuse",
			cands: []int{1, 5}, curLen: 7, at: at(3), len: at(7),
			wantTier: TierEquidistant, wantOK: false,
		},
		// Below the floor: the digest collided and the patch recorded no position
		// at all, so there is no evidence to be unanimous about.
		{
			name:  "a collision with no recorded position is below the floor",
			cands: []int{0, 2}, curLen: 4, at: nil, len: nil,
			wantTier: TierNone, wantOK: false,
		},

		// Edge cases below.

		// Length alone cannot name an element; without `at` there is no position
		// to be near, so this is no evidence rather than weak evidence.
		{
			name:  "a length with no index is no positional evidence",
			cands: []int{0, 2}, curLen: 4, at: nil, len: at(4),
			wantTier: TierNone, wantOK: false,
		},
		// Without a recorded length the back anchor cannot be computed at all, so
		// a surviving front anchor is a single unanimous signal, not a both-ends
		// agreement — TierOneSided, not TierAnchored. The tier must not overstate
		// what was actually checked.
		{
			name:  "an index with no length can only ever be one-sided",
			cands: []int{1, 2}, curLen: 4, at: at(2), len: nil,
			wantTier: TierOneSided, wantIndex: 2, wantOK: true,
		},
		// Each derived position is range-checked against the collection's CURRENT
		// length before it is believed, and discounted when it falls outside. Here
		// the recorded front index is past the end of a collection that shrank, so
		// it is discounted — but the index from the END still lands inside, and it
		// is right: element 9 of 10 was the last one, and the last one is now 1.
		{
			name:  "a front index past the new end is discounted while the back anchor survives",
			cands: []int{0, 1}, curLen: 2, at: at(9), len: at(10),
			wantTier: TierOneSided, wantIndex: 1, wantOK: true,
		},
		// Both derived positions fall outside the collection, so both are
		// discounted and nothing is left to be unanimous about.
		{
			name:  "an advisory whose every derived position is out of range is no evidence",
			cands: []int{0, 1}, curLen: 2, at: at(5), len: at(10),
			wantTier: TierNone, wantOK: false,
		},
		// A single candidate is TierIdentity whatever the advisory says: the
		// digest already identified the member, and a merely STALE position — one
		// that was coherent when written and has since been overtaken — must not
		// be able to talk hew out of a match it is certain about.
		{
			name:  "a stale advisory cannot demote a unique digest",
			cands: []int{0}, curLen: 1, at: at(7), len: at(9),
			wantTier: TierIdentity, wantIndex: 0, wantOK: true,
		},
		// A SELF-CONTRADICTORY advisory is different in kind, and it outranks even
		// a unique digest: staleness is the document moving on, but a patch that
		// never described a possible collection is malformed, and hew reports that
		// rather than quietly locating past it.
		{
			name:  "a faulted advisory outranks even a unique digest",
			cands: []int{0}, curLen: 1, at: at(4), len: at(2),
			wantTier: TierFaulted, wantOK: false,
		},
		// An advisory that contradicts ITSELF is a malformed patch, not a stale
		// one: `at` is zero-based, so element 5 of a 3-long collection never
		// existed at the time the patch was written. hew faults rather than
		// computing a back index out of the contradiction.
		{
			name:  "an index beyond the recorded length is a fault, not a stale position",
			cands: []int{1, 2}, curLen: 4, at: at(5), len: at(3),
			wantTier: TierFaulted, wantOK: false,
		},
		// The same contradiction at its boundary: `at` may be at most length-1.
		{
			name:  "an index exactly at the recorded length is a fault",
			cands: []int{1, 2}, curLen: 4, at: at(3), len: at(3),
			wantTier: TierFaulted, wantOK: false,
		},
		// A non-positive length cannot hold any element, so any index into it is
		// the same contradiction; it faults rather than being silently discarded.
		{
			name:  "a non-positive recorded length faults rather than being computed on",
			cands: []int{1, 2}, curLen: 4, at: at(0), len: at(0),
			wantTier: TierFaulted, wantOK: false,
		},
		// A negative index is malformed whether or not a length was recorded.
		{
			name:  "a negative recorded index is a fault with no length to contradict",
			cands: []int{1, 2}, curLen: 4, at: at(-1), len: nil,
			wantTier: TierFaulted, wantOK: false,
		},
		// Three-way: the front anchor is unique among the candidates even though
		// the collision is wider than a pair.
		{
			name:  "an anchor picks one out of three colliding candidates",
			cands: []int{0, 4, 7}, curLen: 9, at: at(4), len: at(9),
			wantTier: TierAnchored, wantIndex: 4, wantOK: true,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			cands := make([]Candidate, len(c.cands))
			for i, idx := range c.cands {
				cands[i] = Candidate{Index: idx}
			}
			got := Locate(cands, c.curLen, Advisory{At: c.at, Length: c.len})
			if got.Tier != c.wantTier {
				t.Fatalf("tier = %v, want %v (index %d, ok %v)", got.Tier, c.wantTier, got.Index, got.OK)
			}
			if got.OK != c.wantOK {
				t.Fatalf("ok = %v, want %v", got.OK, c.wantOK)
			}
			if c.wantOK && got.Index != c.wantIndex {
				t.Fatalf("index = %d, want %d", got.Index, c.wantIndex)
			}
		})
	}
}

// An ordered collection can list the digests of the elements NEARBY, which is the
// sequence counterpart of the keys-only `~ key` neighbour hints slice 1 gave
// mappings: a value-free way to say where a member sat. It identifies a position
// by CONTENT rather than by counting, so — unlike every index-derived signal — it
// survives an unrelated insertion anywhere else in the collection, which is
// exactly the edit that shifts every index. That is why it outranks the anchors.
//
// The recorded neighbourhood is ONE list, not a before/after pair. Which
// neighbours are "before" and which are "after" is a fact about the element being
// located, so the split is derived at evaluation time from each entry's own
// position; storing it pre-split would bake an evaluation-time view into the data
// and would have no meaning at all for an unordered container.
func TestLocateByNeighbourDigests(t *testing.T) {
	// nb builds a neighbourhood from (position, token) pairs.
	nb := func(pairs ...any) []Neighbour {
		out := make([]Neighbour, 0, len(pairs)/2)
		for i := 0; i < len(pairs); i += 2 {
			out = append(out, Neighbour{At: at(pairs[i].(int)), Token: pairs[i+1].(string)})
		}
		return out
	}
	for _, c := range []struct {
		name      string
		cands     []Candidate
		curLen    int
		adv       Advisory
		wantTier  Tier
		wantIndex int
	}{
		{
			// The collection was reordered wholesale, so no index means anything;
			// the neighbours still do. Element 4 sits between "cc" and "dd".
			name: "neighbour digests locate where every index has shifted",
			cands: []Candidate{
				{Index: 1, Neighbours: nb(0, "aa", 2, "bb")},
				{Index: 4, Neighbours: nb(3, "cc", 5, "dd")},
			},
			curLen:   9,
			adv:      Advisory{At: at(7), Length: at(8), Neighbours: nb(6, "cc", 8, "dd")},
			wantTier: TierNeighboured, wantIndex: 4,
		},
		{
			// Content beats arithmetic: both anchors land on element 1, and the
			// neighbours say 4. The neighbours win, because an index agreeing
			// after an insertion is a coincidence of counting while a digest
			// agreeing is the same content.
			name: "neighbour digests outrank an index anchor that disagrees",
			cands: []Candidate{
				{Index: 1, Neighbours: nb(0, "aa", 2, "bb")},
				{Index: 4, Neighbours: nb(3, "cc", 5, "dd")},
			},
			curLen:   9,
			adv:      Advisory{At: at(1), Length: at(9), Neighbours: nb(0, "cc", 2, "dd")},
			wantTier: TierNeighboured, wantIndex: 4,
		},
		{
			// One side of the element changed, the other did not. Half the
			// neighbourhood is still evidence.
			name: "agreement on one side alone still locates",
			cands: []Candidate{
				{Index: 1, Neighbours: nb(0, "aa", 2, "zz")},
				{Index: 4, Neighbours: nb(3, "cc", 5, "zz")},
			},
			curLen:   9,
			adv:      Advisory{At: at(4), Neighbours: nb(3, "cc", 5, "dd")},
			wantTier: TierNeighboured, wantIndex: 4,
		},
		{
			// Agreement is counted OUTWARD and stops at the first disagreement.
			// Candidate 4 agrees two deep; candidate 1's nearest disagrees and its
			// coincidental match further out is not evidence about this position.
			name: "agreement stops at the first disagreement rather than totalling matches",
			cands: []Candidate{
				{Index: 1, Neighbours: nb(0, "xx", -1, "cc", -2, "ee")},
				{Index: 4, Neighbours: nb(3, "cc", 2, "ee", 1, "xx")},
			},
			curLen:   9,
			adv:      Advisory{At: at(4), Neighbours: nb(3, "cc", 2, "ee", 1, "ff")},
			wantTier: TierNeighboured, wantIndex: 4,
		},
		{
			// Equal agreement is UNINFORMATIVE, not contradictory — the
			// neighbours did not distinguish the candidates — so the position
			// signals get their turn instead of the whole thing refusing.
			name: "equal neighbour agreement defers to the position signals",
			cands: []Candidate{
				{Index: 1, Neighbours: nb(0, "cc")},
				{Index: 4, Neighbours: nb(3, "cc")},
			},
			curLen:   9,
			adv:      Advisory{At: at(4), Length: at(9), Neighbours: nb(3, "cc")},
			wantTier: TierAnchored, wantIndex: 4,
		},
		{
			// Neighbours that agree with NEITHER candidate decide nothing, and
			// with no position recorded there is nothing left.
			name: "neighbours matching nobody leave no evidence at all",
			cands: []Candidate{
				{Index: 1, Neighbours: nb(0, "aa")},
				{Index: 4, Neighbours: nb(3, "bb")},
			},
			curLen:   9,
			adv:      Advisory{Neighbours: nb(3, "zz")},
			wantTier: TierNone,
		},
		{
			// THE BOUND THAT MUST NOT BE "FIXED": migration can shrink a recorded
			// neighbourhood but never extend it, so a candidate may legitimately
			// have neighbours the patch never recorded. ABSENT IS NOT
			// DISAGREEMENT — a short list is less evidence, never contrary
			// evidence — so the candidate that agrees as far as the record goes
			// still wins.
			name: "a neighbour the patch never recorded is absent, not disagreeing",
			cands: []Candidate{
				{Index: 1, Neighbours: nb(0, "aa", 2, "bb")},
				{Index: 4, Neighbours: nb(3, "cc", 5, "dd")},
			},
			curLen:   9,
			adv:      Advisory{At: at(4), Neighbours: nb(3, "cc")},
			wantTier: TierNeighboured, wantIndex: 4,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := Locate(c.cands, c.curLen, c.adv)
			if got.Tier != c.wantTier {
				t.Fatalf("tier = %v, want %v (index %d)", got.Tier, c.wantTier, got.Index)
			}
			if c.wantTier.OK() && got.Index != c.wantIndex {
				t.Fatalf("index = %d, want %d", got.Index, c.wantIndex)
			}
		})
	}
}

// Property (a) of the scored-location ruling. Locate is called from six sites
// across five packages; if it consulted a map, two bindings could resolve the
// same patch to different elements and the corpus would only catch it by luck.
func TestLocateIsDeterministic(t *testing.T) {
	cands := []Candidate{{Index: 1}, {Index: 4}, {Index: 6}}
	first := Locate(cands, 9, Advisory{At: at(4), Length: at(9)})
	for i := 0; i < 64; i++ {
		if got := Locate(cands, 9, Advisory{At: at(4), Length: at(9)}); got != first {
			t.Fatalf("call %d returned %+v, first returned %+v", i, got, first)
		}
	}
}

// Locate must not reorder or otherwise disturb the candidate slice its caller
// built — the appliers keep their own index-parallel state beside it.
func TestLocateDoesNotMutateItsInput(t *testing.T) {
	cands := []Candidate{{Index: 5}, {Index: 2}, {Index: 8}}
	Locate(cands, 10, Advisory{At: at(2), Length: at(10)})
	for i, want := range []int{5, 2, 8} {
		if cands[i].Index != want {
			t.Fatalf("candidate %d became %d, want %d", i, cands[i].Index, want)
		}
	}
}

// Property (d): explainable — the refusal has to say which signals were
// consulted and how they disagreed, because the address itself is a digest and
// a digest cannot be shown to a human.
func TestLocateExplainsARefusal(t *testing.T) {
	seg := Segment{Kind: SegHash, Form: "hew", Name: "sha256", Hash: "abc123"}
	for _, c := range []struct {
		name   string
		cands  []Candidate
		adv    Advisory
		curLen int
		want   []string // substrings the explanation must carry
	}{
		{
			name:  "a conflict names both elements the signals chose",
			cands: []Candidate{{Index: 3}, {Index: 4}}, curLen: 6,
			adv:  Advisory{At: at(3), Length: at(5)},
			want: []string{"3", "4", "end"},
		},
		{
			name:  "no recorded position says so rather than blaming the document",
			cands: []Candidate{{Index: 0}, {Index: 2}}, curLen: 4,
			adv:  Advisory{},
			want: []string{"no recorded position"},
		},
		{
			name:  "an equidistant refusal reports the distance that tied",
			cands: []Candidate{{Index: 1}, {Index: 5}}, curLen: 7,
			adv:  Advisory{At: at(3), Length: at(7)},
			want: []string{"equally"},
		},
		// A fault blames the PATCH, not the document — the reader must not go
		// looking for a change in a file that is fine.
		{
			name:  "a fault reports the contradiction in the advisory itself",
			cands: []Candidate{{Index: 1}, {Index: 2}}, curLen: 4,
			adv:  Advisory{At: at(5), Length: at(3)},
			want: []string{"5", "3", "advisory"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := Locate(c.cands, c.curLen, c.adv)
			if got.OK {
				t.Fatalf("expected a refusal, located element %d", got.Index)
			}
			detail := got.Explain(seg)
			for _, want := range c.want {
				if !strings.Contains(detail, want) {
					t.Fatalf("explanation %q does not mention %q", detail, want)
				}
			}
		})
	}
}

// A located pick explains itself too: the tier is the reason, and a caller that
// reports a successful location (a --explain mode, a diagnostic) must be able to
// say WHY without re-deriving it.
func TestLocateExplainsASuccess(t *testing.T) {
	seg := Segment{Kind: SegHash, Form: "hew", Name: "sha256", Hash: "abc123"}
	got := Locate([]Candidate{{Index: 2}, {Index: 3}}, 6, Advisory{At: at(3), Length: at(5)})
	if !got.OK {
		t.Fatalf("expected a location, got %v", got.Tier)
	}
	if detail := got.Explain(seg); detail == "" {
		t.Fatal("a located pick must be able to explain which signal placed it")
	}
}

// A refusal's whole job is to tell a reader which elements were in play, and the
// address it can quote is a SHA-256 digest — unreadable, ungreppable, and naming
// nothing the author recognises. A source LINE is the only handle a human has on
// "which one did you mean".
//
// The line is the one in the document IN FRONT OF THEM, not the one recorded when
// the patch was written. That is why it belongs on the candidate rather than in
// the IR: the applier has already parsed the file, so it knows where the
// candidates are, and no advisory, migration or spec change is involved.
//
// This is DIAGNOSTICS ONLY. No tier consults a line, and none may: the ruling
// caps it strictly below the confidence floor, so any case where it could decide
// requires it to outrank signals it is defined to be weaker than.
func TestLocateNamesCandidateLines(t *testing.T) {
	seg := Segment{Kind: SegHash, Form: "hew", Name: "sha256", Hash: "abc123"}

	t.Run("a conflict names the line of each element the signals chose", func(t *testing.T) {
		got := Locate([]Candidate{{Index: 3, Line: 12}, {Index: 4, Line: 17}}, 6,
			Advisory{At: at(3), Length: at(5)})
		if got.OK {
			t.Fatalf("expected a refusal, located %d", got.Index)
		}
		detail := got.Explain(seg)
		for _, want := range []string{"line 12", "line 17"} {
			if !strings.Contains(detail, want) {
				t.Fatalf("explanation does not name the candidate lines: %s", detail)
			}
		}
	})

	t.Run("an equidistant refusal names the tied candidates' lines", func(t *testing.T) {
		got := Locate([]Candidate{{Index: 1, Line: 8}, {Index: 5, Line: 20}}, 7,
			Advisory{At: at(3), Length: at(7)})
		if got.OK {
			t.Fatalf("expected a refusal, located %d", got.Index)
		}
		if detail := got.Explain(seg); !strings.Contains(detail, "8") || !strings.Contains(detail, "20") {
			t.Fatalf("explanation does not name the tied lines: %s", detail)
		}
	})

	t.Run("a binding that supplies no line still explains itself", func(t *testing.T) {
		// Not every binding can place every node, and a missing line must cost
		// the reader the line, not the whole diagnostic.
		got := Locate([]Candidate{{Index: 3}, {Index: 4}}, 6, Advisory{At: at(3), Length: at(5)})
		detail := got.Explain(seg)
		if detail == "" || strings.Contains(detail, "line 0") {
			t.Fatalf("a lineless candidate produced a broken explanation: %s", detail)
		}
	})
}
