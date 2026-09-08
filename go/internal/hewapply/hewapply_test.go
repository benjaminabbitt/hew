package hewapply

import (
	"errors"
	"strings"
	"testing"

	hew "github.com/benjaminabbitt/hew/go"
	"github.com/benjaminabbitt/hew/go/internal/hewerr"
	"github.com/benjaminabbitt/hew/go/internal/hewsplice"
)

// fakeBinding records what the loop asked of it, so these tests pin the
// SEQUENCE rather than any format's behaviour.
type fakeBinding struct {
	parses      int      // NewRun calls == reparses
	saw         []string // ops the loop evaluated, in order
	srcAtRun    []string // the bytes each run was handed
	plan        map[string][]hewsplice.Edit
	parseErr    error
	unsupported error
}

func (b *fakeBinding) FormatName() string { return "FAKE" }

func (b *fakeBinding) Unsupported(string, hew.Transform) error { return b.unsupported }

func (b *fakeBinding) NewRun(src []byte, ctx RunContext) (Run, error) {
	b.parses++
	b.srcAtRun = append(b.srcAtRun, string(src))
	if b.parseErr != nil {
		return nil, b.parseErr
	}
	return &fakeRun{b: b}, nil
}

type fakeRun struct{ b *fakeBinding }

func (r *fakeRun) EvalTest(t hew.Transform) error {
	r.b.saw = append(r.b.saw, "test:"+t.Path.String())
	return nil
}

func (r *fakeRun) PlanOne(t hew.Transform) ([]hewsplice.Edit, error) {
	r.b.saw = append(r.b.saw, "plan:"+t.Path.String())
	return r.b.plan[t.Path.String()], nil
}

func tf(op hew.OpKind, path string) hew.Transform {
	return hew.Transform{Op: op, Path: hew.MustParsePath(path)}
}

// The document is REPARSED between transforms, so each one reads the bytes its
// predecessors produced. A binding that parsed once and reused the tree would
// resolve every later path against a stale document.
func TestApply_ReparsesBetweenTransforms(t *testing.T) {
	b := &fakeBinding{plan: map[string][]hewsplice.Edit{
		"/a": {{Start: 0, End: 1, Text: "X"}},
		"/b": {{Start: 1, End: 2, Text: "Y"}},
	}}
	got, err := Apply([]byte("ab"), hew.TransformList{
		Transform: []hew.Transform{tf(hew.OpReplace, "/a"), tf(hew.OpReplace, "/b")},
	}, b)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if string(got) != "XY" {
		t.Fatalf("got %q, want %q", got, "XY")
	}
	if b.parses != 2 {
		t.Fatalf("parsed %d times, want one per transform", b.parses)
	}
	// The second run must see the first edit's output, not the original.
	if b.srcAtRun[1] != "Xb" {
		t.Fatalf("second run saw %q, want the first edit applied", b.srcAtRun[1])
	}
}

// A hint never edits and never asserts. It reaches neither arm.
func TestApply_HintNeitherAssertsNorEdits(t *testing.T) {
	b := &fakeBinding{plan: map[string][]hewsplice.Edit{
		"/h": {{Start: 0, End: 2, Text: "EDITED"}},
	}}
	got, err := Apply([]byte("ab"), hew.TransformList{
		Transform: []hew.Transform{tf(hew.OpHint, "/h")},
	}, b)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if string(got) != "ab" {
		t.Fatalf("a hint edited the document: %q", got)
	}
	if len(b.saw) != 0 {
		t.Fatalf("a hint was evaluated as %v; it must neither assert nor plan", b.saw)
	}
}

// An empty plan is a no-op, not a failure: a converged transform plans nothing.
func TestApply_EmptyPlanIsANoOp(t *testing.T) {
	b := &fakeBinding{plan: map[string][]hewsplice.Edit{}}
	got, err := Apply([]byte("ab"), hew.TransformList{
		Transform: []hew.Transform{tf(hew.OpReplace, "/a")},
	}, b)
	if err != nil {
		t.Fatalf("an empty plan must not be an error: %v", err)
	}
	if string(got) != "ab" {
		t.Fatalf("got %q, want the document unchanged", got)
	}
}

// Pass 0 runs over the whole list BEFORE any transform applies, so a list
// carrying an unimplemented qualifier never applies halfway and then refuses.
func TestApply_UnsupportedRefusesBeforeAnythingApplies(t *testing.T) {
	b := &fakeBinding{
		unsupported: errors.New("qualifier not implemented"),
		plan:        map[string][]hewsplice.Edit{"/a": {{Start: 0, End: 1, Text: "X"}}},
	}
	_, err := Apply([]byte("ab"), hew.TransformList{
		Transform: []hew.Transform{tf(hew.OpReplace, "/a")},
	}, b)
	if err == nil {
		t.Fatal("an unimplemented qualifier must refuse")
	}
	if b.parses != 0 {
		t.Fatal("pass 0 must decide before the document is read at all")
	}
}

func TestApply_ParseFailureNamesTheFormat(t *testing.T) {
	b := &fakeBinding{parseErr: errors.New("line 1: bad"), plan: map[string][]hewsplice.Edit{}}
	_, err := Apply([]byte("ab"), hew.TransformList{
		Target: "t.fake", Transform: []hew.Transform{tf(hew.OpReplace, "/a")},
	}, b)
	if err == nil {
		t.Fatal("a parse failure must be reported")
	}
	he, ok := hewerr.As(err)
	if !ok || he.Code != hewerr.CodeTargetParse {
		t.Fatalf("want a target-parse error, got %v", err)
	}
	if !strings.Contains(he.Detail, "does not parse as FAKE") {
		t.Fatalf("the diagnostic must name the format: %q", he.Detail)
	}
}

// Asserts and mutations are evaluated in LIST ORDER, interleaved as written.
func TestApply_EvaluatesInListOrder(t *testing.T) {
	b := &fakeBinding{plan: map[string][]hewsplice.Edit{}}
	_, err := Apply([]byte("ab"), hew.TransformList{Transform: []hew.Transform{
		tf(hew.OpTest, "/a"), tf(hew.OpReplace, "/b"), tf(hew.OpTest, "/c"),
	}}, b)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := []string{"test:/a", "plan:/b", "test:/c"}
	if len(b.saw) != len(want) {
		t.Fatalf("saw %v, want %v", b.saw, want)
	}
	for i := range want {
		if b.saw[i] != want[i] {
			t.Fatalf("saw %v, want %v", b.saw, want)
		}
	}
}
