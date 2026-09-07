package all

import (
	"strings"
	"testing"

	hew "github.com/benjaminabbitt/hew/go"
)

// SLICE 1 of satisfied-recoil: the non-asserting HINT CHANNEL.
//
// Today a patch that carries context ASSERTS it. hew's differ marks every sibling
// within DiffOptions.Context of a changed slot and emits an OpTest carrying that
// sibling's FULL before-image value. Two harms follow, and these two tests pin
// them as OBSERVABLE behaviour, not as IR shape:
//
//   1. DISCLOSURE. A neighbour's value is copied into the patch verbatim. The
//      motivating incident: a ctxloom audit record captured a sibling's
//      `Authorization: Bearer ...` purely for sitting next to an edited key.
//   2. BRITTLENESS. Editing that untouched neighbour in the target makes the
//      patch REFUSE (HEW010), for a node that was never the author's business.
//
// After slice 1 a pure context sibling rides the hint channel (its KEY path only),
// which asserts nothing: the value is gone from the patch, and a later edit to the
// neighbour can no longer fail the match.

const secret = "Bearer sk-secret-DO-NOT-LEAK"

// A context neighbour's VALUE must never appear in the emitted patch. Reduced from
// the real disclosure: the secret sits next to the only key the author edited.
func TestContextNeighbourValueIsNotDisclosed(t *testing.T) {
	const name = "settings.json"
	format := hew.FormatJSON
	before := []byte(`{"enabled": false, "authorization": "` + secret + `"}`)
	after := []byte(`{"enabled": true, "authorization": "` + secret + `"}`)

	tl, err := hew.Invert(format, before, after, hew.DiffOptions{Target: name})
	if err != nil {
		t.Fatalf("Invert: %v", err)
	}

	// The IR must not carry the neighbour's value on any transform.
	for _, tr := range tl.Transform {
		if !tr.Value.IsZero() && strings.Contains(tr.Value.String(), secret) {
			t.Fatalf("a transform carries the neighbour's secret value %q: op=%s path=%s",
				secret, tr.Op, tr.Path.String())
		}
	}

	// And neither may the rendered .hew text.
	patch, err := hew.Render(tl, hew.RenderOptions{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(string(patch), secret) {
		t.Fatalf("the rendered patch discloses the untouched neighbour's value:\n%s", patch)
	}
}

// Editing the untouched neighbour in the target must NOT make the patch refuse: a
// context sibling is a hint, and a hint can never fail a match. This is the exact
// shape ctxloom worked around with Context: ContextNone.
func TestPatchAppliesWhenAContextNeighbourChanged(t *testing.T) {
	const name = "settings.json"
	format := hew.FormatJSON
	binding, ok := hew.Lookup(format)
	if !ok {
		t.Fatalf("no binding for %s", format)
	}

	before := []byte(`{"enabled": false, "authorization": "` + secret + `"}`)
	after := []byte(`{"enabled": true, "authorization": "` + secret + `"}`)

	// A reversal of the /enabled edit. Under default context it also names
	// /authorization as a neighbour of the changed slot.
	tl, err := hew.Invert(format, before, after, hew.DiffOptions{Target: name})
	if err != nil {
		t.Fatalf("Invert: %v", err)
	}
	patch, err := hew.Render(tl, hew.RenderOptions{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	// The credential is ROTATED in the live file — a disjoint change the reversal
	// never addressed.
	const rotated = "Bearer sk-ROTATED"
	drifted := []byte(`{"enabled": true, "authorization": "` + rotated + `"}`)

	replay, err := hew.ParseSingle(patch)
	if err != nil {
		t.Fatalf("ParseSingle: %v\npatch=%s", err, patch)
	}
	restored, err := binding.Applier(drifted, replay)
	if err != nil {
		t.Fatalf("applying over a rotated NEIGHBOUR must succeed; a hint cannot fail a "+
			"match: %v\npatch=%s\ndrifted=%s", err, patch, drifted)
	}

	// The reversal did its job: /enabled is back to false.
	doc, err := hew.OpenBytes(name, restored, hew.As(format))
	if err != nil {
		t.Fatalf("re-open restored: %v\n%s", err, restored)
	}
	doc.AtPath(hew.MustParsePath("/enabled")).Assert(false)
	// And it left the neighbour exactly as the live file had it — untouched, not
	// reverted to the value the patch happened to observe.
	doc.AtPath(hew.MustParsePath("/authorization")).Assert(rotated)
	if _, err := doc.Bytes(); err != nil {
		t.Fatalf("restored document is not what the hint channel promises: %v\n%s", err, restored)
	}
}

// The IR shape behind the two behavioural tests: a pure context sibling must ride
// the non-asserting hint channel (Transform.Locate.Neighbours, KEY PATHS only)
// instead of being emitted as a value-carrying OpTest. No OpTest on an untouched
// neighbour is what makes a later edit to it unable to fail the match; a []Path of
// keys is what makes the neighbour's value structurally impossible to disclose.
func TestContextNeighbourRidesTheHintChannelNotAnAssertion(t *testing.T) {
	before := []byte(`{"enabled": false, "authorization": "` + secret + `"}`)
	after := []byte(`{"enabled": true, "authorization": "` + secret + `"}`)

	tl, err := hew.Invert(hew.FormatJSON, before, after, hew.DiffOptions{Target: "settings.json"})
	if err != nil {
		t.Fatalf("Invert: %v", err)
	}

	neighbour := hew.MustParsePath("/authorization")

	// 1. The untouched neighbour must NOT be asserted: no OpTest names its path.
	for _, tr := range tl.Transform {
		if tr.Op == hew.OpTest && tr.Path.Equal(neighbour) {
			t.Fatalf("the untouched neighbour is still asserted as context: "+
				"OpTest %s carries %q", tr.Path.String(), tr.Value.String())
		}
	}

	// 2. It must instead ride the hint channel: an OpHint names the neighbour's
	// key path, and carries no value.
	var hinted bool
	for _, tr := range tl.Transform {
		if tr.Op == hew.OpHint && tr.Path.Equal(neighbour) {
			hinted = true
			if !tr.Value.IsZero() {
				t.Fatalf("an OpHint must carry no value, got %q on %s", tr.Value.String(), tr.Path.String())
			}
		}
	}
	if !hinted {
		t.Fatalf("the neighbour %s is not carried as an OpHint; context was dropped "+
			"rather than moved to the hint channel", neighbour.String())
	}
}

// The radius knob still governs the hint channel: with ContextNone, a mutation
// carries no neighbour hints at all (the ctxloom workaround stays expressible, now
// as "no hints" rather than "no asserts").
func TestContextNoneEmitsNoNeighbourHints(t *testing.T) {
	before := []byte(`{"enabled": false, "authorization": "` + secret + `"}`)
	after := []byte(`{"enabled": true, "authorization": "` + secret + `"}`)

	tl, err := hew.Invert(hew.FormatJSON, before, after,
		hew.DiffOptions{Target: "s.json", Context: hew.ContextNone})
	if err != nil {
		t.Fatalf("Invert: %v", err)
	}
	for _, tr := range tl.Transform {
		if tr.Op == hew.OpHint {
			t.Fatalf("ContextNone must emit no neighbour hints, got OpHint %s", tr.Path.String())
		}
	}
}

// SLICE 2 (satisfied-recoil): an untouched SET (by-value scalar) neighbour must
// not disclose its value either. It rides the hint channel as a content-hash
// fragment `~ #hew:sha256=<hex>` — the set analog of slice 1's `~ key`, since a
// set member has no key, only its value, and a one-way digest gives identity
// without carrying the thing. The edited member itself still shows its value on
// its own `-`/`+` line (inherent to the op); only the untouched neighbours are
// de-valued.
func TestSetContextNeighbourIsNotDisclosed(t *testing.T) {
	before := []byte(`{"tags": ["alpha", "secret-beta", "gamma"]}`)
	after := []byte(`{"tags": ["alpha", "gamma"]}`) // secret-beta removed

	tl, err := hew.Invert(hew.FormatJSON, after, before, hew.DiffOptions{Target: "t.json"})
	if err != nil {
		t.Fatalf("Invert: %v", err)
	}
	patch, err := hew.Render(tl, hew.RenderOptions{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// The untouched neighbours must not appear by value; they must ride a hash hint.
	for _, v := range []string{"alpha", "gamma"} {
		if strings.Contains(string(patch), v) {
			t.Fatalf("untouched set neighbour %q disclosed:\n%s", v, patch)
		}
	}
	if !strings.Contains(string(patch), "#hew:sha256=") {
		t.Fatalf("no content-hash hint in the patch:\n%s", patch)
	}
}

// SLICE 3 (satisfied-recoil): an untouched COMMENT neighbour must not disclose
// its text either.
//
// A comment is the last keyless member still riding the assert channel. It is
// also the one whose value is most often prose a human wrote for humans — a
// justification, a ticket reference, a credential someone pasted "temporarily"
// — so copying it verbatim into a stored patch is the same disclosure slice 1
// was filed for, in the form most likely to carry something sensitive.
//
// Nothing has to be invented to fix it: a comment is ALREADY addressed by the
// digest of its own text (`#hew:comment=<hex>`, §4.5b), in every container
// kind, because addressing a comment by ordinal was itself a defect. So the
// hint channel's requirement — an address that identifies without carrying —
// is already satisfied; the comment was simply excluded from it.
func TestCommentContextNeighbourIsNotDisclosed(t *testing.T) {
	for _, c := range []struct {
		name          string
		format        hew.FormatID
		fname         string
		before, after string
	}{
		{
			"jsonc", hew.FormatJSONC, "c.jsonc",
			"{\n  // " + secret + "\n  \"enabled\": false\n}\n",
			"{\n  // " + secret + "\n  \"enabled\": true\n}\n",
		},
		{
			"yaml", hew.FormatYAML, "c.yaml",
			"# " + secret + "\nenabled: false\n",
			"# " + secret + "\nenabled: true\n",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			tl, err := hew.Invert(c.format, []byte(c.before), []byte(c.after),
				hew.DiffOptions{Target: c.fname})
			if err != nil {
				t.Fatalf("Invert: %v", err)
			}

			// 1. No transform may carry the comment's text as a VALUE.
			for _, tr := range tl.Transform {
				if !tr.Value.IsZero() && strings.Contains(tr.Value.String(), secret) {
					t.Fatalf("a transform carries the untouched comment's text: op=%s path=%s value=%q",
						tr.Op, tr.Path.String(), tr.Value.String())
				}
			}

			// 2. Nor may the rendered patch.
			patch, err := hew.Render(tl, hew.RenderOptions{})
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if strings.Contains(string(patch), secret) {
				t.Fatalf("the rendered patch discloses the untouched comment:\n%s", patch)
			}

			// 3. It must RIDE the hint channel rather than simply being dropped:
			// a comment that vanishes costs the locator the very neighbour it
			// would place an edit against.
			var hinted bool
			for _, tr := range tl.Transform {
				if tr.Op == hew.OpHint && strings.Contains(tr.Path.String(), "hew:comment=") {
					hinted = true
					if !tr.Value.IsZero() {
						t.Fatalf("an OpHint must carry no value, got %q", tr.Value.String())
					}
				}
			}
			if !hinted {
				t.Fatalf("the comment neighbour is not carried as a `#hew:comment=` hint; "+
					"context was dropped rather than moved to the hint channel:\n%s", patch)
			}
		})
	}
}

// And the brittleness half, which is the same defect seen from the other side:
// EDITING the untouched comment in the target must not make the patch refuse.
// An asserted comment means a patch stops applying because someone reworded a
// remark next to the line it changes.
func TestPatchAppliesWhenAContextCommentChanged(t *testing.T) {
	const fname = "c.yaml"
	format := hew.FormatYAML
	binding, ok := hew.Lookup(format)
	if !ok {
		t.Fatalf("no binding for %s", format)
	}

	before := []byte("# " + secret + "\nenabled: false\n")
	after := []byte("# " + secret + "\nenabled: true\n")

	tl, err := hew.Invert(format, before, after, hew.DiffOptions{Target: fname})
	if err != nil {
		t.Fatalf("Invert: %v", err)
	}
	patch, err := hew.Render(tl, hew.RenderOptions{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	// The comment is REWORDED in the live file — a disjoint edit the reversal
	// never addressed.
	drifted := []byte("# rotated, see ticket OPS-1234\nenabled: true\n")

	replay, err := hew.ParseSingle(patch)
	if err != nil {
		t.Fatalf("ParseSingle: %v\npatch=%s", err, patch)
	}
	restored, err := binding.Applier(drifted, replay)
	if err != nil {
		t.Fatalf("applying over a REWORDED comment must succeed; a hint cannot fail a "+
			"match: %v\npatch=%s\ndrifted=%s", err, patch, drifted)
	}

	doc, err := hew.OpenBytes(fname, restored, hew.As(format))
	if err != nil {
		t.Fatalf("re-open restored: %v\n%s", err, restored)
	}
	doc.AtPath(hew.MustParsePath("/enabled")).Assert(false)
	// The reversal left the human's reworded comment alone.
	if !strings.Contains(string(restored), "OPS-1234") {
		t.Fatalf("the live comment was clobbered by the patch's stale copy:\n%s", restored)
	}
}

// A comment hint must survive a RE-PARSE of the differ's own patch. The
// rendered spelling of a comment hint is not the digest it was built from, so
// this is the place the two sides can silently disagree: if the parser reads
// the line back as an ordinary key, the patch means something different than
// the differ meant, and the disagreement is invisible in the text.
//
// This is the same class as the three pre-existing defects found by
// round-tripping the differ against its own applier; a hint that renders but
// does not re-parse is a locator that quietly evaporates.
func TestCommentHintSurvivesAReParse(t *testing.T) {
	before := []byte("# " + secret + "\nenabled: false\nname: svc\n")
	after := []byte("# " + secret + "\nenabled: true\nname: svc\n")

	tl, err := hew.Invert(hew.FormatYAML, before, after, hew.DiffOptions{Target: "c.yaml"})
	if err != nil {
		t.Fatalf("Invert: %v", err)
	}
	var want string
	for _, tr := range tl.Transform {
		if tr.Op == hew.OpHint && strings.Contains(tr.Path.String(), "hew:comment=") {
			want = tr.Path.String()
		}
	}
	if want == "" {
		t.Fatal("precondition: the differ emitted no comment hint")
	}

	patch, err := hew.Render(tl, hew.RenderOptions{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	replay, err := hew.ParseSingle(patch)
	if err != nil {
		t.Fatalf("ParseSingle: %v\n%s", err, patch)
	}

	var got []string
	for _, tr := range replay.Transform {
		if tr.Op == hew.OpHint {
			got = append(got, tr.Path.String())
		}
	}
	for _, g := range got {
		if g == want {
			return // the comment hint came back addressing the same comment
		}
	}
	t.Fatalf("the comment hint did not survive the round trip:\n  differ emitted %s\n"+
		"  re-parse produced %v\npatch:\n%s", want, got, patch)
}
