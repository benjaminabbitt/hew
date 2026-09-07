package hew

import (
	"strconv"
	"testing"
)

// A patch's advisories are all written in ONE frame — the before-image (§4.5c) —
// but an applier RE-PARSES between transforms, so the nth transform meets a
// collection that the patch's own earlier transforms have already changed. That
// drift is not foreign and must not be guessed at: the transform list says
// exactly what it did, so the shift is arithmetic.
//
// Migrate does that arithmetic, translating each advisory out of the before-image
// frame and into the one its transform will actually meet. What is left over for
// the scorer is only FOREIGN drift — edits made by someone else since the patch
// was written — which is what scoring is for.
func TestMigrateCompensatesThePatchsOwnEdits(t *testing.T) {
	seq := func(hex string) Path {
		return MustParsePath("/tags").Append(Segment{Kind: SegHash, Form: "hew", Name: "sha256", Hash: hex})
	}
	const dup = "aaaa"

	for _, c := range []struct {
		name string
		ts   []Transform
		want []Advisory // one per transform; the zero value means "no advisory"
	}{
		{
			// Three identical elements, all removed. Each advisory says where the
			// element was in the pristine document; by the time the second runs,
			// the first removal has already shifted everything down one.
			name: "successive removes from one collection each shift the next",
			ts: []Transform{
				{Op: OpRemove, Path: seq(dup), At: at(0), Length: at(3)},
				{Op: OpRemove, Path: seq(dup), At: at(1), Length: at(3)},
				{Op: OpRemove, Path: seq(dup), At: at(2), Length: at(3)},
			},
			want: []Advisory{
				{At: at(0), Length: at(3)}, // nothing has happened yet
				{At: at(0), Length: at(2)}, // one earlier removal, below it
				{At: at(0), Length: at(1)}, // two earlier removals, both below it
			},
		},
		{
			// A removal ABOVE an element does not move it; only the length moves.
			name: "a removal above an element shifts only the length",
			ts: []Transform{
				{Op: OpRemove, Path: seq(dup), At: at(4), Length: at(5)},
				{Op: OpRemove, Path: seq(dup), At: at(1), Length: at(5)},
			},
			want: []Advisory{
				{At: at(4), Length: at(5)},
				{At: at(1), Length: at(4)},
			},
		},
		{
			// An add lands after its anchor, so an element below the anchor is
			// untouched and one above it moves up by one.
			name: "an insertion shifts what follows it",
			ts: []Transform{
				{Op: OpAdd, Path: MustParsePath("/tags"), After: seq(dup), At: at(1), Length: at(4)},
				{Op: OpRemove, Path: seq(dup), At: at(3), Length: at(4)},
			},
			want: []Advisory{
				{At: at(1), Length: at(4)},
				{At: at(4), Length: at(5)},
			},
		},
		{
			// Edits to a DIFFERENT collection must not be counted. This is the
			// failure that would corrupt a document rather than refuse: a shift
			// borrowed from an unrelated array lands the write on the wrong element.
			name: "another collection's edits do not shift this one",
			ts: []Transform{
				{Op: OpRemove, Path: MustParsePath("/other").Append(Segment{Kind: SegHash, Form: "hew", Name: "sha256", Hash: dup}),
					At: at(0), Length: at(3)},
				{Op: OpRemove, Path: seq(dup), At: at(2), Length: at(3)},
			},
			want: []Advisory{
				{At: at(0), Length: at(3)},
				{At: at(2), Length: at(3)},
			},
		},
		{
			// A replace swaps a value in place: same element count, same indices.
			name: "a replace shifts nothing",
			ts: []Transform{
				{Op: OpReplace, Path: seq(dup), At: at(0), Length: at(3)},
				{Op: OpRemove, Path: seq(dup), At: at(2), Length: at(3)},
			},
			want: []Advisory{
				{At: at(0), Length: at(3)},
				{At: at(2), Length: at(3)},
			},
		},
		{
			// A remove with no advisory is a partial fact, and the two halves are
			// treated differently because only one of them is in doubt. That it
			// SHORTENED the collection is certain, so the length moves. WHERE it
			// removed from is unknown, so the index cannot move — and leaving it
			// alone is what hands the question to the scorer, which will read the
			// mismatch between the recorded index and the shortened length as
			// drift and try both anchors, refusing if they disagree. Inventing a
			// shift here instead would be the guess the whole design forbids.
			name: "an unpositioned remove shortens the collection but shifts no index",
			ts: []Transform{
				{Op: OpRemove, Path: seq(dup)},
				{Op: OpRemove, Path: seq(dup), At: at(2), Length: at(3)},
			},
			want: []Advisory{
				{},
				{At: at(2), Length: at(2)},
			},
		},
		{
			// A before-image test resolves the same address its mutation does, so
			// it needs the same translation — but asserting changes nothing, so it
			// causes no shift for what follows.
			name: "a test is translated but shifts nothing",
			ts: []Transform{
				{Op: OpRemove, Path: seq(dup), At: at(0), Length: at(3)},
				{Op: OpTest, Path: seq(dup), At: at(2), Length: at(3)},
			},
			want: []Advisory{
				{At: at(0), Length: at(3)},
				{At: at(1), Length: at(2)},
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := Migrate(c.ts)
			if len(got) != len(c.want) {
				t.Fatalf("got %d advisories, want %d", len(got), len(c.want))
			}
			for i := range c.want {
				if !sameAdvisory(got[i], c.want[i]) {
					t.Fatalf("transform %d: got %s, want %s", i, showAdv(got[i]), showAdv(c.want[i]))
				}
			}
		})
	}
}

// Migrate must never hand the scorer a self-contradictory advisory: `at` outside
// 0…length−1 is TierFaulted, which reports a MALFORMED PATCH. A patch that was
// well-formed when written must not be turned into a faulted one by arithmetic
// hew did to it itself.
func TestMigrateNeverManufacturesAFault(t *testing.T) {
	seq := MustParsePath("/tags").Append(Segment{Kind: SegHash, Form: "hew", Name: "sha256", Hash: "aaaa"})
	// Six removals of a 6-long collection, in every order the differ might emit.
	for _, order := range [][]int{{0, 1, 2, 3, 4, 5}, {5, 4, 3, 2, 1, 0}, {3, 0, 5, 1, 4, 2}} {
		ts := make([]Transform, 0, len(order))
		for _, a := range order {
			ts = append(ts, Transform{Op: OpRemove, Path: seq, At: at(a), Length: at(6)})
		}
		for i, adv := range Migrate(ts) {
			if adv.At == nil || adv.Length == nil {
				t.Fatalf("order %v, transform %d: advisory went missing", order, i)
			}
			if *adv.At < 0 || *adv.At > *adv.Length-1 {
				t.Fatalf("order %v, transform %d: migrated to a faulted advisory %s", order, i, showAdv(adv))
			}
		}
	}
}

func sameAdvisory(a, b Advisory) bool {
	return eqIntPtr(a.At, b.At) && eqIntPtr(a.Length, b.Length)
}

func showAdv(a Advisory) string {
	s := "at="
	if a.At == nil {
		s += "-"
	} else {
		s += strconv.Itoa(*a.At)
	}
	s += " length="
	if a.Length == nil {
		return s + "-"
	}
	return s + strconv.Itoa(*a.Length)
}

// The neighbourhood is a coordinate too, and the patch's own edits move it. A
// recorded neighbour the patch itself REMOVES will not be there to agree, and an
// element it INSERTS will be there without having been recorded — both read as
// disagreement, so a patch would score its own later transforms worse the more
// work it did.
//
// The recorded neighbourhood needs no new IR field: it is read from the OpHint
// records already in Migrate's stream, which is what those records are. A hint
// names a neighbour by address — a key for a mapping, a digest for a sequence —
// and carries its position like any other body line.
func TestMigrateNeighbourhood(t *testing.T) {
	el := func(h string) Path {
		return MustParsePath("/tags").Append(Segment{Kind: SegHash, Form: "hew", Name: "sha256", Hash: h})
	}
	hint := func(h string, a int) Transform {
		return Transform{Op: OpHint, Path: el(h), At: at(a), Length: at(9)}
	}
	tokens := func(ts []Transform) (before, after []string) {
		adv := Migrate(ts)[len(ts)-1]
		if adv.At == nil {
			t.Fatalf("no position on the addressed transform")
		}
		return splitNeighbourhood(adv.Neighbours, *adv.At)
	}

	t.Run("hints in the same container become the neighbourhood", func(t *testing.T) {
		b, a := tokens([]Transform{
			hint("n3", 3), hint("n6", 6),
			{Op: OpRemove, Path: el("me"), At: at(5), Length: at(9)},
		})
		if !sameStrings(b, []string{"n3"}) || !sameStrings(a, []string{"n6"}) {
			t.Fatalf("before=%v after=%v", b, a)
		}
	})

	t.Run("a neighbour the patch itself removed drops out", func(t *testing.T) {
		b, _ := tokens([]Transform{
			hint("n3", 3), hint("n4", 4),
			{Op: OpRemove, Path: el("n4"), At: at(4), Length: at(9)},
			{Op: OpRemove, Path: el("me"), At: at(5), Length: at(9)},
		})
		if !sameStrings(b, []string{"n3"}) {
			t.Fatalf("removed neighbour still recorded: before=%v", b)
		}
	})

	t.Run("an element the patch inserted joins the neighbourhood", func(t *testing.T) {
		b, _ := tokens([]Transform{
			hint("n3", 3),
			{Op: OpAdd, Path: MustParsePath("/tags"), After: el("n3"), At: at(3), Length: at(9),
				Value: mustValNoT("fresh")},
			{Op: OpRemove, Path: el("me"), At: at(5), Length: at(9)},
		})
		if !sameStrings(b, []string{hashScalar(mustValNoT("fresh")), "n3"}) {
			t.Fatalf("inserted element missing or misplaced: before=%v", b)
		}
	})

	t.Run("a replaced neighbour keeps its slot with the new digest", func(t *testing.T) {
		b, _ := tokens([]Transform{
			hint("n3", 3), hint("n4", 4),
			{Op: OpReplace, Path: el("n4"), At: at(4), Length: at(9), Value: mustValNoT("swapped")},
			{Op: OpRemove, Path: el("me"), At: at(5), Length: at(9)},
		})
		if !sameStrings(b, []string{hashScalar(mustValNoT("swapped")), "n3"}) {
			t.Fatalf("replace did not keep the slot: before=%v", b)
		}
	})

	t.Run("another container's hints are not this one's neighbours", func(t *testing.T) {
		other := MustParsePath("/other").Append(Segment{Kind: SegHash, Form: "hew", Name: "sha256", Hash: "x9"})
		b, a := tokens([]Transform{
			{Op: OpHint, Path: other, At: at(4), Length: at(9)},
			hint("n6", 6),
			{Op: OpRemove, Path: el("me"), At: at(5), Length: at(9)},
		})
		if len(b) != 0 || !sameStrings(a, []string{"n6"}) {
			t.Fatalf("borrowed a neighbour from /other: before=%v after=%v", b, a)
		}
	})

	t.Run("a mapping hint names its key", func(t *testing.T) {
		key := func(k string, a int) Transform {
			return Transform{Op: OpHint, Path: MustParsePath("/server").Append(Segment{Kind: SegKey, Name: k}), At: at(a)}
		}
		adv := Migrate([]Transform{
			key("host", 0), key("port", 2),
			{Op: OpRemove, Path: MustParsePath("/server").Append(Segment{Kind: SegKey, Name: "tls"}), At: at(1)},
		})[2]
		b, a := splitNeighbourhood(adv.Neighbours, 1)
		if !sameStrings(b, []string{"host"}) || !sameStrings(a, []string{"port"}) {
			t.Fatalf("mapping neighbourhood wrong: before=%v after=%v", b, a)
		}
	})
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
