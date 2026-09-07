package hew

import (
	"sort"

	"gopkg.in/yaml.v3"
)

// Advisory migration (satisfied-recoil slice 4, §4.5e).
//
// Every position advisory in a patch is written in ONE frame — the before-image
// (§4.5c) — because that is the only frame that survives reordering, a skipped
// `optional`, and reversal. But an applier RE-PARSES between transforms, so the
// nth transform meets a document that the patch's OWN earlier transforms have
// already changed. Reading a before-image coordinate against it unadjusted is
// reading it in the wrong frame.
//
// The distinction this file exists to draw:
//
//   - SELF-INFLICTED drift — the patch's own earlier edits — is EXACTLY KNOWN.
//     The transform list says what it did and in what order, so the shift is
//     arithmetic, and hew computes it rather than guessing at it.
//   - FOREIGN drift — edits made by somebody else since the patch was written —
//     is unknowable, and is what the SCORER weighs (§4.5d).
//
// Compensating the first is what stops hew refusing its own patches; scoring the
// second is what stops it guessing at other people's. Conflating them costs one
// or the other: score the self-inflicted part and a patch cannot remove three
// identical elements from one array, compensate the foreign part and hew invents
// a position it has no evidence for.

// Migrate translates each transform's position advisory from the patch's
// before-image frame into the frame that transform will actually meet, returning
// one Advisory per transform in the same order. A transform with no advisory
// gets the zero Advisory.
//
// It is a pure function of the transform list — no document is read — so every
// binding derives the same numbers, and the whole translation can be computed
// before the first edit is applied. That is what keeps appliers stateless.
func Migrate(ts []Transform) []Advisory {
	out := make([]Advisory, len(ts))
	for i := range ts {
		if ts[i].At == nil && ts[i].Length == nil {
			continue
		}
		out[i] = migrateOne(ts, i)
	}
	return out
}

// displacement is what one earlier transform did to a collection: it moved
// indices beyond `from` by `delta`, and changed the collection's length by
// `grew`. positioned is false when the transform did not record WHERE it
// acted — a partial fact, see displaces.
type displacement struct {
	scope      string
	from       int
	delta      int
	grew       int
	positioned bool
}

func migrateOne(ts []Transform, i int) Advisory {
	t := ts[i]
	adv := Advisory{At: t.At, Length: t.Length}

	idxScope, hasIdx := advisoryContainer(t)
	idxShift, idxGrew := 0, 0

	for j := 0; j < i; j++ {
		if !hasIdx || adv.At == nil {
			break
		}
		if d := displaces(ts[j]); d.scope == idxScope && (d.delta != 0 || d.grew != 0) {
			idxGrew += d.grew
			if d.positioned && d.from < *adv.At {
				idxShift += d.delta
			}
		}
	}

	if idxShift != 0 {
		adv.At = intPtr(*adv.At + idxShift)
	}
	if idxGrew != 0 && adv.Length != nil {
		adv.Length = intPtr(*adv.Length + idxGrew)
	}
	if hasIdx && adv.At != nil {
		adv.Neighbours = neighbourhood(ts, i, idxScope, *adv.At)
	}
	return adv
}

// displaces reports what transform t does to its collection.
//
// The PARTIAL-FACT rule: an edit that does not record
// WHERE it acted still had an effect, and the two halves of that effect are not
// equally certain. That a removal SHORTENED its collection is certain, so `grew`
// applies; where it removed from is unknown, so no coordinate may be shifted.
// Leaving the coordinate alone is what hands the question to the scorer, which
// reads the resulting mismatch as drift and refuses if its anchors disagree.
// Inventing a shift instead would be the guess the whole design forbids.
func displaces(t Transform) displacement {
	var d displacement
	switch t.Op {
	case OpRemove:
		d.delta, d.grew = -1, -1
	case OpAdd:
		d.delta, d.grew = 1, 1
	default:
		// A replace swaps a value in place; a test and a `~` hint do not mutate.
		// None of them moves a coordinate or changes a population.
		return displacement{}
	}
	scope, ok := advisoryContainer(t)
	if !ok {
		return displacement{}
	}
	d.scope = scope
	// An element is one index wide; only WHERE it sat can be unknown.
	if t.At != nil {
		d.from, d.positioned = *t.At, true
	}
	return d
}

// advisoryContainer names the collection an index advisory counts within. For
// anything that addresses an element, that is the element's parent. An ADD
// addresses the CONTAINER and carries its anchor's coordinates (§4.5c), so its
// collection is the anchor's parent — the same collection, reached from the
// other side.
func advisoryContainer(t Transform) (string, bool) {
	switch t.Op {
	case OpAdd:
		for _, p := range []Path{t.After, t.Before} {
			if p.IsZero() {
				continue
			}
			if parent, ok := p.Parent(); ok {
				return parent.String(), true
			}
		}
		return t.Path.String(), true
	default:
		parent, ok := t.Path.Parent()
		if !ok {
			return "", false
		}
		return parent.String(), true
	}
}

// neighbourhood reconstructs the recorded neighbourhood around transform i and
// migrates it by the patch's OWN earlier edits (§4.5e).
//
// The record needs no IR field of its own: the OpHint transforms already in the
// stream ARE the neighbourhood. Each names a neighbour by address — a key for a
// mapping, a content digest for a sequence — and carries its before-image
// position like any other body line.
//
// The migration is a WINDOW REPLAY rather than offset arithmetic, because entries
// are keyed by POSITION and not by token: removing one of three identical
// neighbours must remove exactly one, which a digest-matching approach would get
// wrong. Edits outside the window fall out for free.
//
// Positions come back out relative to the element's MIGRATED index, so the pivot
// splitNeighbourhood is handed lines up with the entries it is splitting.
func neighbourhood(ts []Transform, i int, scope string, migratedAt int) []Neighbour {
	self := ts[i]
	if self.At == nil {
		return nil
	}
	type slot struct {
		at    int
		token string
		self  bool
	}
	var win []slot
	sameScope := func(t Transform) bool {
		s, ok := advisoryContainer(t)
		return ok && s == scope
	}
	for j := 0; j < i; j++ {
		if ts[j].Op != OpHint || ts[j].At == nil || !sameScope(ts[j]) {
			continue
		}
		if tok, ok := addressToken(ts[j].Path); ok {
			win = append(win, slot{at: *ts[j].At, token: tok})
		}
	}
	if len(win) == 0 {
		return nil
	}
	win = append(win, slot{at: *self.At, self: true})
	sort.SliceStable(win, func(a, b int) bool { return win[a].at < win[b].at })

	find := func(at int) int {
		for k := range win {
			if !win[k].self && win[k].at == at {
				return k
			}
		}
		return -1
	}
	for j := 0; j < i; j++ {
		t := ts[j]
		if t.At == nil || !sameScope(t) {
			continue
		}
		switch t.Op {
		case OpRemove:
			// A neighbour the patch itself removed will not be there to agree.
			if k := find(*t.At); k >= 0 {
				win = append(win[:k], win[k+1:]...)
			}
		case OpReplace:
			// The slot stays occupied and the patch knows by what.
			if k := find(*t.At); k >= 0 {
				if tok, ok := mutationToken(t); ok {
					win[k].token = tok
				} else {
					win = append(win[:k], win[k+1:]...)
				}
			}
		case OpAdd:
			// An element inserted beside a neighbour is itself a neighbour the
			// record has never heard of. Its digest is not guessed — the patch
			// carries the value it is adding. When the value is not a scalar the
			// entry is simply omitted: less evidence, never contrary evidence.
			tok, ok := mutationToken(t)
			if !ok {
				continue
			}
			k := find(*t.At)
			if k < 0 {
				continue
			}
			if t.Before.IsZero() {
				k++ // lands after the anchor
			}
			win = append(win, slot{})
			copy(win[k+1:], win[k:])
			win[k] = slot{at: *t.At, token: tok}
		}
	}

	selfIdx := -1
	for k := range win {
		if win[k].self {
			selfIdx = k
			break
		}
	}
	if selfIdx < 0 {
		return nil
	}
	out := make([]Neighbour, 0, len(win)-1)
	for k := range win {
		if k == selfIdx {
			continue
		}
		out = append(out, Neighbour{Token: win[k].token, At: intPtr(migratedAt + (k - selfIdx))})
	}
	return out
}

// addressToken is how a neighbour is named: the digest for a hash-addressed
// member, the key for a mapping member. Anything else has no token, and a
// neighbour hew cannot name is not evidence.
func addressToken(p Path) (string, bool) {
	if p.Len() == 0 {
		return "", false
	}
	switch seg := p.Segment(p.Len() - 1); seg.Kind {
	case SegHash:
		return seg.Hash, true
	case SegKey:
		return seg.Name, true
	}
	return "", false
}

// mutationToken is the token a mutation puts INTO the neighbourhood: the digest
// of the value it writes. Only a scalar has one; for anything else the entry is
// omitted rather than guessed at.
func mutationToken(t Transform) (string, bool) {
	if t.Value.Node() == nil {
		return "", false
	}
	if t.Value.Node().Kind != yaml.ScalarNode {
		return "", false
	}
	return hashScalar(t.Value), true
}
