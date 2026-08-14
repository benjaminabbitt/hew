package hew

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/hew-format/hew/internal/hewerr"
)

// --- tree builders ----------------------------------------------------------

func dscalar(tag, text string) *DiffNode {
	return &DiffNode{Kind: KindScalar, Value: NodeValue(&yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: text})}
}

func dstr(s string) *DiffNode { return dscalar("!!str", s) }
func dnum(s string) *DiffNode { return dscalar("!!int", s) }

func dmap(kv ...any) *DiffNode {
	n := &DiffNode{Kind: KindMap}
	y := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for i := 0; i+1 < len(kv); i += 2 {
		key := kv[i].(string)
		val := kv[i+1].(*DiffNode)
		n.Children = append(n.Children, DiffChild{Key: key, Node: val})
		y.Content = append(y.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, val.Value.Node())
	}
	n.Value = NodeValue(y)
	return n
}

func dseq(elems ...*DiffNode) *DiffNode {
	n := &DiffNode{Kind: KindSeq}
	y := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, e := range elems {
		n.Children = append(n.Children, DiffChild{Node: e})
		y.Content = append(y.Content, e.Value.Node())
	}
	n.Value = NodeValue(y)
	return n
}

// withComment inserts a standalone comment child before index i. The container's
// Value is unaffected: a comment is a positional child, not part of the value.
func dcomment(n *DiffNode, i int, text string) *DiffNode {
	kids := append([]DiffChild(nil), n.Children[:i]...)
	kids = append(kids, DiffChild{Comment: true, Text: text})
	kids = append(kids, n.Children[i:]...)
	n.Children = kids
	return n
}

// summarize renders a transform list as one compact line per record, so a test
// pins the whole list rather than poking at fields.
func summarize(tl TransformList) string {
	var b strings.Builder
	for _, t := range tl.Transform {
		b.WriteString(string(t.Op))
		b.WriteString(" ")
		b.WriteString(t.Path.String())
		if !t.Value.IsZero() {
			b.WriteString(" =" + t.Value.String())
		}
		if !t.After.IsZero() {
			b.WriteString(" after:" + t.After.String())
		}
		if !t.Before.IsZero() {
			b.WriteString(" before:" + t.Before.String())
		}
		b.WriteString("\n")
	}
	return b.String()
}

func diffOK(t *testing.T, old, new *DiffNode, opt DiffOptions) TransformList {
	t.Helper()
	if opt.Target == "" {
		opt.Target = "target.yaml"
	}
	tl, err := DiffTrees(old, new, FormatYAML, opt)
	if err != nil {
		t.Fatalf("DiffTrees: %v", err)
	}
	return tl
}

// --- the walk ---------------------------------------------------------------

func TestDiffEqualDocumentsProduceNoTransforms(t *testing.T) {
	doc := dmap("a", dnum("1"), "b", dstr("x"))
	tl := diffOK(t, doc, dmap("a", dnum("1"), "b", dstr("x")), DiffOptions{})
	if len(tl.Transform) != 0 {
		t.Fatalf("want an empty list, got:\n%s", summarize(tl))
	}
	if tl.Target != "target.yaml" || tl.Format != FormatYAML {
		t.Fatalf("target/format not stamped: %+v", tl)
	}
}

// §9.4-R3: the anchor is the DEEPEST container holding every changed node, so
// a nested edit must not drag its ancestors into a hunk of their own.
func TestDiffAnchorsAtTheDeepestContainer(t *testing.T) {
	old := dmap("root", dmap("inner", dmap("k", dnum("1"))))
	new := dmap("root", dmap("inner", dmap("k", dnum("2"))))
	got := summarize(diffOK(t, old, new, DiffOptions{}))
	want := "test /root/inner/k =1\nreplace /root/inner/k =2\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestDiffMapRemoveReplaceAdd(t *testing.T) {
	old := dmap("host", dstr("localhost"), "port", dnum("8080"), "timeout", dnum("30"))
	new := dmap("port", dnum("8080"), "timeout", dnum("60"), "tls", dstr("on"))
	got := summarize(diffOK(t, old, new, DiffOptions{}))
	want := strings.Join([]string{
		"test /host =localhost",
		"test /port =8080",
		"test /timeout =30",
		"remove /host",
		"replace /timeout =60",
		"add /tls =on after:/timeout",
		"",
	}, "\n")
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// A member whose value changed KIND cannot be descended into; it is one
// wholesale replace, not a container diff.
func TestDiffKindChangeIsAReplace(t *testing.T) {
	old := dmap("a", dmap("k", dnum("1")))
	new := dmap("a", dstr("scalar now"))
	got := summarize(diffOK(t, old, new, DiffOptions{}))
	if !strings.Contains(got, "replace /a =scalar now") {
		t.Fatalf("got:\n%s", got)
	}
	if strings.Contains(got, "/a/k") {
		t.Fatalf("must not descend into a replaced node:\n%s", got)
	}
}

// --- §9.4-R2, the context radius --------------------------------------------

func contextBody(t *testing.T, ctx int) string {
	t.Helper()
	old := dseq(dstr("a"), dstr("b"), dstr("c"), dstr("d"), dstr("e"))
	new := dseq(dstr("a"), dstr("b"), dstr("c"), dstr("d"), dstr("e"), dstr("f"))
	return summarize(diffOK(t, old, new, DiffOptions{Context: ctx}))
}

func TestDiffContextRadius(t *testing.T) {
	cases := []struct {
		name string
		ctx  int
		want string
	}{
		{"zero value is the default of one", 0, "test /=e =e\nadd / =f after:/=e\n"},
		{"explicit one", 1, "test /=e =e\nadd / =f after:/=e\n"},
		{"two", 2, "test /=d =d\ntest /=e =e\nadd / =f after:/=e\n"},
		{"none", ContextNone, "add / =f after:/=e\n"},
		{"all", ContextAll, "test /=a =a\ntest /=b =b\ntest /=c =c\ntest /=d =d\ntest /=e =e\nadd / =f after:/=e\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := contextBody(t, c.ctx); got != c.want {
				t.Fatalf("got:\n%s\nwant:\n%s", got, c.want)
			}
		})
	}
}

// Two changed runs whose windows overlap coalesce into one hunk, exactly as
// unified diff coalesces — the shared unchanged sibling is emitted once.
func TestDiffCoalescesOverlappingWindows(t *testing.T) {
	old := dmap("a", dnum("1"), "b", dnum("2"), "c", dnum("3"))
	new := dmap("a", dnum("9"), "b", dnum("2"), "c", dnum("8"))
	got := summarize(diffOK(t, old, new, DiffOptions{}))
	if strings.Count(got, "test /b =2") != 1 {
		t.Fatalf("the shared context sibling must appear exactly once:\n%s", got)
	}
}

func TestDiffContextDoesNotSpanUnrelatedRuns(t *testing.T) {
	old := dmap("a", dnum("1"), "b", dnum("2"), "c", dnum("3"), "d", dnum("4"), "e", dnum("5"))
	new := dmap("a", dnum("9"), "b", dnum("2"), "c", dnum("3"), "d", dnum("4"), "e", dnum("5"))
	got := summarize(diffOK(t, old, new, DiffOptions{}))
	if strings.Contains(got, "test /c") || strings.Contains(got, "test /d") {
		t.Fatalf("radius 1 must not reach the third sibling:\n%s", got)
	}
}

// A container with no changed child of its own emits nothing at all — that is
// how R3's "deepest container" falls out structurally, and it is why the
// corpus round-trips show no root hunk.
func TestDiffUnchangedContainerEmitsNoContext(t *testing.T) {
	old := dmap("keep", dstr("x"), "sub", dmap("k", dnum("1")))
	new := dmap("keep", dstr("x"), "sub", dmap("k", dnum("2")))
	got := summarize(diffOK(t, old, new, DiffOptions{}))
	if strings.Contains(got, "/keep") {
		t.Fatalf("an untouched container must emit no context:\n%s", got)
	}
}

// --- §9.4-R4, address preference --------------------------------------------

func delem(name, cmd string) *DiffNode { return dmap("name", dstr(name), "command", dstr(cmd)) }

func TestDiffPrefersKeyMatchOverIndex(t *testing.T) {
	old := dseq(delem("filesystem", "npx"), delem("github", "npx"))
	new := dseq(delem("filesystem", "npx"), delem("github", "npx"), delem("ctxloom", "ctxloom"))
	got := summarize(diffOK(t, old, new, DiffOptions{}))
	if !strings.Contains(got, "test /name=github/name =github") {
		t.Fatalf("a keyed context line must address by key-match:\n%s", got)
	}
	if !strings.Contains(got, "after:/name=github") {
		t.Fatalf("placement must name the key-matched sibling:\n%s", got)
	}
	if strings.Contains(got, "/1") || strings.Contains(got, "/0") {
		t.Fatalf("no positional address may survive:\n%s", got)
	}
}

func TestDiffCandidateOrderIsNameThenIdThenKey(t *testing.T) {
	mk := func(id, key string) *DiffNode { return dmap("id", dstr(id), "key", dstr(key)) }
	old := dseq(mk("i1", "k1"), mk("i2", "k2"))
	new := dseq(mk("i1", "k1"), mk("i2", "k2"), mk("i3", "k3"))
	got := summarize(diffOK(t, old, new, DiffOptions{}))
	if !strings.Contains(got, "test /id=i2/id =i2") {
		t.Fatalf("id must win over key, being earlier in the candidate list:\n%s", got)
	}
}

func TestDiffKeyFieldsOverride(t *testing.T) {
	mk := func(name, slug string) *DiffNode { return dmap("name", dstr(name), "slug", dstr(slug)) }
	old := dseq(mk("a", "s1"), mk("b", "s2"))
	new := dseq(mk("a", "s1"), mk("b", "s2"), mk("c", "s3"))
	got := summarize(diffOK(t, old, new, DiffOptions{KeyFields: []string{"slug"}}))
	if !strings.Contains(got, "test /slug=s2/slug =s2") {
		t.Fatalf("--key-fields must displace the default candidates:\n%s", got)
	}
}

// A field that is not a candidate but is the ONLY one satisfying the condition
// is used; R4's list is a preference, not a whitelist.
func TestDiffSingleQualifyingNonCandidateFieldIsUsed(t *testing.T) {
	mk := func(slug, shared string) *DiffNode { return dmap("slug", dstr(slug), "shared", dstr(shared)) }
	old := dseq(mk("a", "same"), mk("b", "same"))
	new := dseq(mk("a", "same"), mk("b", "same"), mk("c", "same"))
	got := summarize(diffOK(t, old, new, DiffOptions{}))
	if !strings.Contains(got, "test /slug=b/slug =b") {
		t.Fatalf("the single qualifying field must be used:\n%s", got)
	}
}

// More than one field qualifies and none is a candidate: indices, and a note
// saying so. Silence here would hide a fragile address behind a plausible one.
func TestDiffAmbiguousIdentityFallsBackToIndexWithANote(t *testing.T) {
	mk := func(a, b string) *DiffNode { return dmap("alpha", dstr(a), "beta", dstr(b)) }
	old := dseq(mk("a1", "b1"), mk("a2", "b2"))
	new := dseq(mk("a1", "b1"), mk("a2", "b2"), mk("a3", "b3"))
	var notes []string
	got := summarize(diffOK(t, old, new, DiffOptions{Note: func(s string) { notes = append(notes, s) }}))
	if !strings.Contains(got, "test /1 =") {
		t.Fatalf("want an index address:\n%s", got)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "alpha, beta") || !strings.Contains(notes[0], "index") {
		t.Fatalf("want one note naming both fields, got %q", notes)
	}
}

// A nil Note must not be a crash, and must not change the addressing decision.
func TestDiffNoteIsOptional(t *testing.T) {
	mk := func(a, b string) *DiffNode { return dmap("alpha", dstr(a), "beta", dstr(b)) }
	old := dseq(mk("a1", "b1"))
	new := dseq(mk("a1", "b1"), mk("a2", "b2"))
	if got := summarize(diffOK(t, old, new, DiffOptions{})); !strings.Contains(got, "test /0 =") {
		t.Fatalf("got:\n%s", got)
	}
}

// A field missing from ONE element disqualifies it, even if it is unique on the
// rest — R4's condition is "present on every element".
func TestDiffIdentityFieldMustBePresentOnEveryElement(t *testing.T) {
	old := dseq(dmap("name", dstr("a")), dmap("other", dstr("b")))
	new := dseq(dmap("name", dstr("a")), dmap("other", dstr("b")), dmap("name", dstr("c")))
	got := summarize(diffOK(t, old, new, DiffOptions{}))
	if strings.Contains(got, "name=") {
		t.Fatalf("name is absent from one element and must not be used:\n%s", got)
	}
}

func TestDiffIdentityFieldMustBeUnique(t *testing.T) {
	old := dseq(dmap("name", dstr("dup")), dmap("name", dstr("dup")))
	new := dseq(dmap("name", dstr("dup")), dmap("name", dstr("dup")), dmap("name", dstr("x")))
	got := summarize(diffOK(t, old, new, DiffOptions{}))
	if strings.Contains(got, "name=dup") {
		t.Fatalf("a duplicated identity must not be used as an address:\n%s", got)
	}
}

func TestDiffIdentityFieldMustBeScalar(t *testing.T) {
	old := dseq(dmap("name", dmap("nested", dstr("a"))), dmap("name", dmap("nested", dstr("b"))))
	new := dseq(dmap("name", dmap("nested", dstr("a"))))
	got := summarize(diffOK(t, old, new, DiffOptions{}))
	if strings.Contains(got, "name=") {
		t.Fatalf("a container-valued field is not an identity:\n%s", got)
	}
}

func TestDiffScalarSequenceAddressesByValue(t *testing.T) {
	old := dseq(dstr("alpha"), dstr("beta"))
	new := dseq(dstr("alpha"), dstr("beta"), dstr("gamma"))
	got := summarize(diffOK(t, old, new, DiffOptions{}))
	want := "test /=beta =beta\nadd / =gamma after:/=beta\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestDiffDuplicateScalarsFallBackToIndex(t *testing.T) {
	old := dseq(dstr("a"), dstr("a"))
	new := dseq(dstr("a"), dstr("a"), dstr("b"))
	got := summarize(diffOK(t, old, new, DiffOptions{}))
	if strings.Contains(got, "/=a") {
		t.Fatalf("a duplicated value is not an address:\n%s", got)
	}
}

// A prepend has no preceding sibling to hang off, so the placement is
// `before:` — never an index, which would not survive a reorder.
func TestDiffPrependUsesBefore(t *testing.T) {
	old := dseq(dstr("alpha"))
	new := dseq(dstr("aardvark"), dstr("alpha"))
	got := summarize(diffOK(t, old, new, DiffOptions{}))
	want := "test /=alpha =alpha\nadd / =aardvark before:/=alpha\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// A forward-looking placement must not name a sibling that is itself being
// added: mutations execute in order, so it is not in the document yet and the
// applier would have nothing to resolve.
func TestDiffPlacementNeverNamesALaterAdd(t *testing.T) {
	old := dseq(dstr("alpha"))
	new := dseq(dstr("a1"), dstr("a2"), dstr("alpha"))
	got := summarize(diffOK(t, old, new, DiffOptions{}))
	want := strings.Join([]string{
		"test /=alpha =alpha",
		"add / =a1 before:/=alpha",
		"add / =a2 after:/=a1",
		"",
	}, "\n")
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// Placement must skip a REMOVED sibling: it will not be there to insert after.
func TestDiffPlacementSkipsRemovedSiblings(t *testing.T) {
	old := dmap("a", dnum("1"), "gone", dnum("2"))
	new := dmap("a", dnum("1"), "fresh", dnum("3"))
	got := summarize(diffOK(t, old, new, DiffOptions{}))
	if !strings.Contains(got, "add /fresh =3 after:/a") {
		t.Fatalf("placement must name a surviving sibling:\n%s", got)
	}
}

func TestDiffAddIntoEmptyContainerHasNoPlacement(t *testing.T) {
	old := dmap()
	new := dmap("only", dnum("1"))
	got := summarize(diffOK(t, old, new, DiffOptions{}))
	if got != "add /only =1\n" {
		t.Fatalf("got:\n%s", got)
	}
}

// A removed keyed element's "-" line has to say what is going away, so every
// field is asserted; a context line only needs the address.
func TestDiffRemovedKeyedElementAssertsEveryField(t *testing.T) {
	old := dseq(delem("legacy", "old"), delem("github", "npx"))
	new := dseq(delem("github", "npx"))
	got := summarize(diffOK(t, old, new, DiffOptions{}))
	for _, want := range []string{
		"test /name=legacy/name =legacy",
		"test /name=legacy/command =old",
		"test /name=github/name =github",
		"remove /name=legacy",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "test /name=github/command") {
		t.Fatalf("a context line must show the identity only:\n%s", got)
	}
}

// §9.4-R2's identity-line exemption, checked as the property it protects:
// every hunk stays addressable at radius 0, because an identity is never a
// suppressible context line — a changed element carries it on its own line,
// and an inner change anchors at the element itself.
func TestDiffHunksStayAddressableAtRadiusZero(t *testing.T) {
	old := dseq(delem("a", "1"), delem("b", "1"), delem("legacy", "old"))
	new := dseq(delem("a", "1"), delem("b", "2"), delem("fresh", "new"))
	got := summarize(diffOK(t, old, new, DiffOptions{Context: ContextNone}))
	for _, want := range []string{
		"test /name=legacy/name =legacy", // the removal's own line carries the identity
		"remove /name=legacy",
		"add / ={name: fresh, command: new}",
		"test /name=b/command =\"1\"", // the inner change anchors at /name=b
		"replace /name=b/command =\"2\"",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q at radius 0:\n%s", want, got)
		}
	}
	if strings.Contains(got, "test /name=a") {
		t.Fatalf("radius 0 must emit no context at all:\n%s", got)
	}
}

// --- comments ---------------------------------------------------------------

func TestDiffComments(t *testing.T) {
	old := dmap("timeout", dnum("30"))
	new := dcomment(dmap("timeout", dnum("60"), "retries", dnum("5")), 1, "slow upstream")
	got := summarize(diffOK(t, old, new, DiffOptions{}))
	want := strings.Join([]string{
		"test /timeout =30",
		"replace /timeout =60",
		"add /#0 ={comment: slow upstream} after:/timeout",
		"add /retries =5 after:/#0",
		"",
	}, "\n")
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestDiffCommentRemoval(t *testing.T) {
	old := dcomment(dmap("a", dnum("1")), 0, "doomed")
	new := dmap("a", dnum("1"))
	got := summarize(diffOK(t, old, new, DiffOptions{}))
	if !strings.Contains(got, "remove /#0") || !strings.Contains(got, "test /#0 ={comment: doomed}") {
		t.Fatalf("got:\n%s", got)
	}
}

// A container that differs ONLY in a comment is still a change: node equality
// is comment-aware, or a JSONC comment edit would vanish.
func TestDiffNoticesACommentOnlyChange(t *testing.T) {
	old := dmap("outer", dcomment(dmap("a", dnum("1")), 0, "before"))
	new := dmap("outer", dcomment(dmap("a", dnum("1")), 0, "after"))
	got := summarize(diffOK(t, old, new, DiffOptions{}))
	if !strings.Contains(got, "/outer/#0") {
		t.Fatalf("a comment-only change must be reported:\n%s", got)
	}
}

// --- §9.4-R6, inexpressible changes -----------------------------------------

func TestDiffRootKindChangeIsHEW020(t *testing.T) {
	_, err := DiffTrees(dmap("a", dnum("1")), dseq(dnum("1")), FormatYAML, DiffOptions{Target: "t.yaml"})
	he, ok := hewerr.As(err)
	if !ok {
		t.Fatalf("want a hew error, got %v", err)
	}
	if he.Code != hewerr.CodeInexpressible {
		t.Fatalf("want HEW020, got %s", he.Code)
	}
	if he.Component != hewerr.ComponentDiffer {
		t.Fatalf("want the differ component, got %s", he.Component)
	}
}

func TestDiffScalarRootChangeIsHEW020(t *testing.T) {
	_, err := DiffTrees(dstr("a"), dstr("b"), FormatYAML, DiffOptions{Target: "t.yaml"})
	if he, ok := hewerr.As(err); !ok || he.Code != hewerr.CodeInexpressible {
		t.Fatalf("want HEW020, got %v", err)
	}
}

func TestDiffEqualScalarRootsAreNotAnError(t *testing.T) {
	tl, err := DiffTrees(dstr("a"), dstr("a"), FormatYAML, DiffOptions{Target: "t.yaml"})
	if err != nil {
		t.Fatalf("identical scalar roots are no change at all: %v", err)
	}
	if len(tl.Transform) != 0 {
		t.Fatalf("want an empty list, got %d", len(tl.Transform))
	}
}

func TestDiffNilTreeIsAnError(t *testing.T) {
	if _, err := DiffTrees(nil, dmap(), FormatYAML, DiffOptions{}); err == nil {
		t.Fatal("want an error for a nil old tree")
	}
	if _, err := DiffTrees(dmap(), nil, FormatYAML, DiffOptions{}); err == nil {
		t.Fatal("want an error for a nil new tree")
	}
}

// --- determinism (§9.4-R1) ---------------------------------------------------

func TestDiffIsDeterministic(t *testing.T) {
	old := dmap("m", dseq(delem("a", "1"), delem("b", "2")), "s", dnum("1"))
	new := dmap("m", dseq(delem("a", "1"), delem("b", "9"), delem("c", "3")), "s", dnum("2"))
	first := summarize(diffOK(t, old, new, DiffOptions{}))
	for i := 0; i < 25; i++ {
		if again := summarize(diffOK(t, old, new, DiffOptions{})); again != first {
			t.Fatalf("run %d differs:\n%s\nvs\n%s", i, again, first)
		}
	}
}

// --- identity scalars --------------------------------------------------------

func TestValueScalarQuotesAmbiguousStrings(t *testing.T) {
	cases := []struct {
		tag, text string
		kind      ScalarKind
		quoted    bool
	}{
		{"!!str", "plain", ScalarString, false},
		{"!!str", "8080", ScalarString, true},
		{"!!str", "true", ScalarString, true},
		{"!!str", "null", ScalarString, true},
		{"!!str", "", ScalarString, true},
		{"!!int", "8080", ScalarNumber, false},
		{"!!float", "1.5", ScalarNumber, false},
		{"!!bool", "true", ScalarBool, false},
		{"!!null", "null", ScalarNull, false},
	}
	for _, c := range cases {
		got := valueScalar(NodeValue(&yaml.Node{Kind: yaml.ScalarNode, Tag: c.tag, Value: c.text}))
		if got.Kind != c.kind || got.Quoted != c.quoted {
			t.Fatalf("%s %q -> %+v, want kind %v quoted %v", c.tag, c.text, got, c.kind, c.quoted)
		}
	}
}

func TestValueScalarOfANonScalarIsQuotedText(t *testing.T) {
	got := valueScalar(dmap("a", dnum("1")).Value)
	if got.Kind != ScalarString || !got.Quoted {
		t.Fatalf("got %+v", got)
	}
}

// A string that would read back as a number keeps its quotes all the way into
// the rendered address, or `port="8080"` would come back as the number.
func TestNumericLookingStringRoundTripsAsAnAddress(t *testing.T) {
	old := dseq(dstr("8080"), dstr("x"))
	new := dseq(dstr("8080"))
	got := summarize(diffOK(t, old, new, DiffOptions{}))
	if !strings.Contains(got, `/="8080"`) {
		t.Fatalf("want a quoted identity, got:\n%s", got)
	}
}

func TestDiffOptionsDefaults(t *testing.T) {
	var o DiffOptions
	if got := o.keyFields(); len(got) != 3 || got[0] != "name" || got[1] != "id" || got[2] != "key" {
		t.Fatalf("default key fields = %v", got)
	}
	if fields := (DiffOptions{KeyFields: []string{"x"}}).keyFields(); len(fields) != 1 || fields[0] != "x" {
		t.Fatalf("override ignored: %v", fields)
	}
	for _, c := range []struct {
		in   int
		n    int
		all  bool
		name string
	}{
		{0, 1, false, "zero is the default"},
		{1, 1, false, "one"},
		{3, 3, false, "three"},
		{ContextNone, 0, false, "none"},
		{ContextAll, 0, true, "all"},
	} {
		n, all := DiffOptions{Context: c.in}.radius()
		if n != c.n || all != c.all {
			t.Fatalf("%s: radius(%d) = (%d,%v), want (%d,%v)", c.name, c.in, n, all, c.n, c.all)
		}
	}
}

func TestCanonicalDistinguishesShapes(t *testing.T) {
	cases := [][2]*DiffNode{
		{dstr("1"), dnum("1")},                                         // "1" is not 1
		{dmap("a", dnum("1")), dmap("b", dnum("1"))},                   // key names count
		{dmap("a", dnum("1")), dseq(dnum("1"))},                        // kind counts
		{dseq(dstr("ab")), dseq(dstr("a"), dstr("b"))},                 // no token splicing
		{dmap("a", dnum("1")), dcomment(dmap("a", dnum("1")), 0, "c")}, // comments count
	}
	for i, c := range cases {
		if sameNode(c[0], c[1]) {
			t.Fatalf("case %d: %q must not equal %q", i, c[0].canonical(), c[1].canonical())
		}
	}
	if !sameNode(dmap("a", dnum("1")), dmap("a", dnum("1"))) {
		t.Fatal("equal trees must compare equal")
	}
	if !sameNode(nil, nil) {
		t.Fatal("two absent nodes are the same node")
	}
	if sameNode(nil, dstr("x")) {
		t.Fatal("absent is not a scalar")
	}
}

func TestMemberLookup(t *testing.T) {
	n := dmap("a", dnum("1"))
	if v, ok := n.member("a"); !ok || v.Value.String() != "1" {
		t.Fatalf("member(a) = %v %v", v, ok)
	}
	if _, ok := n.member("missing"); ok {
		t.Fatal("member of an absent key must report false")
	}
	if _, ok := dstr("x").member("a"); ok {
		t.Fatal("a scalar has no members")
	}
	if _, ok := (*DiffNode)(nil).member("a"); ok {
		t.Fatal("a nil node has no members")
	}
}
