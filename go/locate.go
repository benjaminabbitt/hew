package hew

import "fmt"

// Scored location (satisfied-recoil slice 4). A content hash addresses a member
// of a collection; when two members hold the same value they hash alike, and the
// digest has then said everything it can. Locate is the single decision that
// picks between the colliding candidates, and it is one function because all six
// matching sites — resolve.go's stepHash, json's planRemove, and the four
// bindings' appliers — have to refuse and locate IDENTICALLY. Two bindings that
// disagreed about which duplicate a patch meant would corrupt a document in a way
// the corpus catches only by luck.
//
// The model is ORDINAL, not a weighted sum. Among colliding candidates the
// positions are unique — one element per index — so there is never a numeric tie
// for a weight to break, and any float would have to be invented and then tuned
// until the tests went green, which is exactly what the "a floor with a stated
// reason" property forbids. Each tier is instead a statement about the document.
//
// THE FLOOR: positional evidence must EXIST and be UNANIMOUS. The refusals below
// the floor differ in kind, and the distinction is load-bearing:
//
//   - a CONTRADICTION refuses because a tie refuses. TierConflicted (the index
//     from the front and the index from the end name different elements) and
//     TierEquidistant (two candidates sit the same distance away) are both cases
//     where hew has evidence and that evidence disagrees with itself.
//   - NO EVIDENCE refuses because nothing was recorded, or because everything
//     recorded fell outside the collection as it stands now.
//   - a FAULT refuses because the PATCH is malformed, which is not a statement
//     about the document at all.
//
// TierDisplaced sits below TierConflicted in confidence and still LOCATES. That
// is deliberate: contradiction is not weakness. A weak-but-unanimous signal is
// worth acting on; a strong pair of signals that point at different elements is
// not, however confident each one sounds alone.
type Tier int

const (
	// TierNone: no positional evidence survived to be weighed.
	TierNone Tier = iota
	// TierFaulted: the advisory contradicts itself, so the patch is malformed.
	TierFaulted
	// TierConflicted: the front and back anchors name different elements.
	TierConflicted
	// TierEquidistant: no anchor holds and the nearest candidates tie.
	TierEquidistant
	// TierDisplaced: no anchor holds, but one candidate is strictly nearest.
	TierDisplaced
	// TierOneSided: exactly one end's anchor survived, and it is unambiguous.
	TierOneSided
	// TierAnchored: the front and back anchors agree on one element.
	TierAnchored
	// TierNeighboured: the elements AROUND one candidate still hash as recorded.
	TierNeighboured
	// TierIdentity: the digest matched exactly one member.
	TierIdentity
)

// OK reports whether this tier locates. Only the tiers at or above TierDisplaced
// do; the constants ascend by confidence among those, but the refusing tiers are
// not on that scale — they say different things, not smaller amounts of one thing.
func (t Tier) OK() bool {
	return t >= TierDisplaced
}

func (t Tier) String() string {
	switch t {
	case TierIdentity:
		return "identity"
	case TierNeighboured:
		return "neighbours"
	case TierAnchored:
		return "anchored"
	case TierOneSided:
		return "one-sided"
	case TierDisplaced:
		return "displaced"
	case TierEquidistant:
		return "equidistant"
	case TierConflicted:
		return "conflicted"
	case TierFaulted:
		return "faulted"
	default:
		return "none"
	}
}

// Candidate is one element whose value hashed to the address being resolved, as
// the collection stands NOW. Before and After are the digests of the elements
// around it, NEAREST FIRST — the document's side of the neighbour hints, which
// are how an ordered collection identifies a position by content rather than by
// counting. They are empty for a binding that does not supply them.
type Candidate struct {
	Index         int
	Before, After []string
}

// Advisory is the non-asserting position advisory a transform recorded when the
// patch was written: the addressed element's zero-based index from the front (At)
// within a collection of this Length, and the digests of its neighbours at the
// time (Before/After, nearest first). None of it can fail a match — it only ever
// chooses between candidates the hash already found.
type Advisory struct {
	At, Length    *int
	Before, After []string
}

// Located is Locate's verdict. It is comparable on purpose: the same inputs must
// produce a byte-identical result at every call site.
type Located struct {
	// Index is the chosen element, meaningful only when OK.
	Index int
	OK    bool
	Tier  Tier

	// Captured at decision time so Explain can name the signals that were
	// actually consulted rather than re-deriving them from stale inputs.
	n                 int // how many candidates collided
	depth             int // neighbour digests that agreed
	dist              int // distance from the nearest derived position
	frontHit, backHit int // candidates the two anchors named, -1 for none
	at, length        int
	hasAt, hasLength  bool
}

// Locate chooses among the elements a value hash matched. cands must be in
// ascending index order and is never modified; curLen is the collection's length
// now. It consults nothing but its arguments — no map is ranged over anywhere in
// the decision — so the answer is a total function of the inputs.
func Locate(cands []Candidate, curLen int, adv Advisory) Located {
	out := Located{Index: -1, Tier: TierNone, n: len(cands), frontHit: -1, backHit: -1}

	// A malformed advisory is settled before anything else, and it outranks even
	// a unique digest. Staleness is the document moving on — an index that was
	// coherent when written and has since been overtaken — and hew locates past
	// that happily. An advisory that never described a POSSIBLE collection is a
	// different thing: quietly locating past it would hide a broken patch.
	if adv.At != nil {
		out.at, out.hasAt = *adv.At, true
		if adv.Length != nil {
			out.length, out.hasLength = *adv.Length, true
		}
		if *adv.At < 0 || (adv.Length != nil && *adv.At > *adv.Length-1) {
			out.Tier = TierFaulted
			return out
		}
	}
	if len(cands) == 0 {
		return out
	}
	// The digest identified the member. Nothing else is consulted: a position
	// that has gone stale must not talk hew out of a match it is certain of.
	if len(cands) == 1 {
		out.Tier, out.Index, out.OK = TierIdentity, cands[0].Index, true
		return out
	}

	// Neighbour digests come before the index arithmetic because they identify a
	// position by CONTENT: they still agree after an unrelated insertion anywhere
	// else in the collection, which is precisely what shifts every index.
	if idx, depth, ok := neighbourPick(cands, adv); ok {
		out.Tier, out.Index, out.OK, out.depth = TierNeighboured, idx, true, depth
		return out
	}

	// Each derived position is range-checked against the collection as it stands
	// before it is believed. A recorded index past the new end is discounted
	// rather than clamped — and note that the BACK anchor routinely survives that
	// discount, because an element near the end of a collection that shrank keeps
	// its distance from the end while its distance from the front becomes absurd.
	front, back := -1, -1
	if adv.At != nil {
		if *adv.At < curLen {
			front = *adv.At
		}
		if adv.Length != nil {
			if p := curLen - (*adv.Length - *adv.At); p >= 0 && p < curLen {
				back = p
			}
		}
	}
	for _, c := range cands {
		if c.Index == front {
			out.frontHit = c.Index
		}
		if c.Index == back {
			out.backHit = c.Index
		}
	}
	switch {
	case out.frontHit >= 0 && out.frontHit == out.backHit:
		// Both ends agree, which can only happen when the collection is still the
		// length it was: nothing on either side of the element resized.
		out.Tier, out.Index, out.OK = TierAnchored, out.frontHit, true
		return out
	case out.frontHit >= 0 && out.backHit >= 0:
		out.Tier = TierConflicted
		return out
	case out.frontHit >= 0:
		out.Tier, out.Index, out.OK = TierOneSided, out.frontHit, true
		return out
	case out.backHit >= 0:
		out.Tier, out.Index, out.OK = TierOneSided, out.backHit, true
		return out
	}

	// No anchor landed on a candidate. What is left is distance from wherever the
	// advisory still points, which locates only if one candidate is strictly
	// nearest — a tie here is the same coin flip a conflict is.
	anchors := make([]int, 0, 2)
	if front >= 0 {
		anchors = append(anchors, front)
	}
	if back >= 0 && back != front {
		anchors = append(anchors, back)
	}
	if len(anchors) == 0 {
		return out // TierNone: nothing recorded survived into this collection.
	}
	best, bestDist, ties := -1, 0, 0
	for _, c := range cands {
		d := -1
		for _, a := range anchors {
			if x := distance(c.Index, a); d < 0 || x < d {
				d = x
			}
		}
		switch {
		case best < 0 || d < bestDist:
			best, bestDist, ties = c.Index, d, 1
		case d == bestDist:
			ties++
		}
	}
	out.dist = bestDist
	if ties > 1 {
		out.Tier = TierEquidistant
		return out
	}
	out.Tier, out.Index, out.OK = TierDisplaced, best, true
	return out
}

// neighbourPick chooses the candidate whose surroundings still hash as the patch
// recorded them. Agreement is counted OUTWARD FROM THE ELEMENT and stops at the
// first disagreement on each side: neighbours further away than a change are not
// evidence about this position, so a run of three that breaks at the first is
// worth one, not three. A candidate decides only on a strict unique maximum —
// equal agreement means the neighbours did not distinguish anything, which,
// unlike a conflict, is simply uninformative, so the position signals get their
// turn.
func neighbourPick(cands []Candidate, adv Advisory) (idx, depth int, ok bool) {
	if len(adv.Before) == 0 && len(adv.After) == 0 {
		return 0, 0, false
	}
	best, bestDepth, ties := -1, 0, 0
	for _, c := range cands {
		d := agreeingRun(adv.Before, c.Before) + agreeingRun(adv.After, c.After)
		switch {
		case d > bestDepth:
			best, bestDepth, ties = c.Index, d, 1
		case d == bestDepth:
			ties++
		}
	}
	if bestDepth == 0 || ties > 1 {
		return 0, 0, false
	}
	return best, bestDepth, true
}

// distance is how far apart two indices sit in the collection.
func distance(a, b int) int {
	if a < b {
		return b - a
	}
	return a - b
}

// agreeingRun counts how many digests agree from the nearest neighbour outward.
func agreeingRun(recorded, actual []string) int {
	n := 0
	for n < len(recorded) && n < len(actual) && recorded[n] != "" && recorded[n] == actual[n] {
		n++
	}
	return n
}

// Explain says which signals were consulted and how they agreed or disagreed.
// The address it is explaining is a DIGEST, which cannot be shown to a human and
// cannot be looked for in the file, so without this the reader of a refusal has
// nothing at all to act on.
func (l Located) Explain(seg Segment) string {
	switch l.Tier {
	case TierFaulted:
		if l.hasLength {
			return fmt.Sprintf("the position advisory on %s is malformed: it names element %d of a collection it records as %d long",
				seg.String(), l.at, l.length)
		}
		return fmt.Sprintf("the position advisory on %s is malformed: it names element %d", seg.String(), l.at)
	case TierNone:
		if l.hasAt {
			return fmt.Sprintf("%d elements collide on %s and the recorded position no longer falls inside this collection",
				l.n, seg.String())
		}
		return fmt.Sprintf("%d elements collide on %s and the patch carries no recorded position to separate them",
			l.n, seg.String())
	case TierConflicted:
		return fmt.Sprintf("%d elements collide on %s and the position signals disagree: counting from the front names element %d, counting from the end names element %d",
			l.n, seg.String(), l.frontHit, l.backHit)
	case TierEquidistant:
		return fmt.Sprintf("%d elements collide on %s and two of them sit equally far (%d) from the recorded position",
			l.n, seg.String(), l.dist)
	case TierIdentity:
		return fmt.Sprintf("%s matched exactly one element", seg.String())
	case TierNeighboured:
		return fmt.Sprintf("%d elements collide on %s; element %d is the one whose %d neighbouring digests still agree",
			l.n, seg.String(), l.Index, l.depth)
	case TierAnchored:
		return fmt.Sprintf("%d elements collide on %s; element %d is at the recorded position, counting from either end",
			l.n, seg.String(), l.Index)
	case TierOneSided:
		end := "the front"
		if l.backHit >= 0 {
			end = "the end"
		}
		return fmt.Sprintf("%d elements collide on %s; the collection resized, and element %d is the only one still at the recorded distance from %s",
			l.n, seg.String(), l.Index, end)
	default:
		return fmt.Sprintf("%d elements collide on %s; no position anchor held, and element %d is the nearest (%d away) to the recorded position",
			l.n, seg.String(), l.Index, l.dist)
	}
}
