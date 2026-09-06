package hew

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
