package hewcomment

import (
	"testing"

	hew "github.com/benjaminabbitt/hew/go"
	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

// seg builds the comment segment addressing text, the way a parsed patch would.
func seg(t *testing.T, text string) hew.Segment {
	t.Helper()
	return hew.NewPath(hew.Comment(text)).Segments()[0]
}

func TestPick_FindsTheCommentWhoseTextHashes(t *testing.T) {
	texts := []string{"first note", "second note", "third note"}
	i, err := Pick(seg(t, "second note"), texts, hew.Advisory{})
	if err != nil {
		t.Fatalf("Pick: %v", err)
	}
	if i != 1 {
		t.Fatalf("index = %d, want 1", i)
	}
}

func TestPick_NoMatchWhenNoCommentHashes(t *testing.T) {
	_, err := Pick(seg(t, "absent"), []string{"first note"}, hew.Advisory{})
	if err == nil {
		t.Fatal("a digest matching nothing must fail")
	}
	if err.Code != hewerr.CodeNoMatch {
		t.Fatalf("code = %v, want %v", err.Code, hewerr.CodeNoMatch)
	}
}

// Identical comments collide, and a collision the advisory cannot break
// REFUSES rather than picking the first — the same rule identical set members
// get, which is the point of addressing a comment by digest at all.
func TestPick_IdenticalCommentsRefuseWithoutAnAdvisory(t *testing.T) {
	texts := []string{"same", "same"}
	_, err := Pick(seg(t, "same"), texts, hew.Advisory{})
	if err == nil {
		t.Fatal("a collision must refuse rather than pick one")
	}
	if err.Code != hewerr.CodeAmbiguousMatch {
		t.Fatalf("code = %v, want %v", err.Code, hewerr.CodeAmbiguousMatch)
	}
	if !err.Final {
		t.Fatal("the step decided this; a transform must not reinterpret it as its own drift error")
	}
}

// A segment carrying no digest is refused rather than falling back to a
// position, which would silently address comment 0 every time.
func TestPick_ASegmentWithNoDigestIsRefused(t *testing.T) {
	_, err := Pick(hew.Segment{Kind: hew.SegComment}, []string{"a"}, hew.Advisory{})
	if err == nil {
		t.Fatal("a comment segment with no digest must be refused")
	}
	if err.Code != hewerr.CodeNoMatch {
		t.Fatalf("code = %v, want %v", err.Code, hewerr.CodeNoMatch)
	}
}
