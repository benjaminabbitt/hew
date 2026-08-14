package hewyaml

import (
	"strings"
	"testing"
)

// These are white-box tests over the span tree: every edit this binding makes
// is a splice against a node's source span, so a span that is one byte off is
// a corrupted file. Asserting the spans directly is how that stays pinned for
// shapes the corpus does not happen to contain.

func mustDoc(t *testing.T, src string) *doc {
	t.Helper()
	d, err := parseDoc([]byte(src))
	if err != nil {
		t.Fatalf("parseDoc: %v", err)
	}
	return d
}

// text is the exact source of a node's span.
func (d *doc) text(n *ynode) string { return string(d.src[n.start:n.end]) }

func valueOf(t *testing.T, d *doc, key string) *ynode {
	t.Helper()
	e := d.root.lookup(key)
	if e == nil {
		t.Fatalf("no key %q", key)
	}
	return e.val
}

const spanDoc = "" +
	"plain: hello world\n" +
	"trailing: spaced   \n" +
	"comment: value # note\n" +
	"tabbed: value\t# note\n" +
	"spacehash: a #b\n" +
	"hash: a#b\n" +
	"colon: http://example.com\n" +
	"sq: 'it''s here'\n" +
	"dq: \"a \\\" b # c\"\n" +
	"lit: |\n  one\n\n  two\n" +
	"fold: >-\n  a\n  b\n" +
	"cont: first\n  second\n" +
	"gapped: a\n  b\n\n  c\n" +
	"commented: a\n  b\n  # not content\n" +
	"flowmap: {a: 1, b: [2, 3], c: \"}\"}\n" +
	"flowseq: [1, 'x, y', {k: v}]\n" +
	"empty:\n" +
	"anchored: &anc 42\n" +
	"alias: *anc\n"

func TestScalarSpans(t *testing.T) {
	d := mustDoc(t, spanDoc)
	for key, want := range map[string]string{
		"plain":     "hello world",
		"trailing":  "spaced",
		"comment":   "value",
		"tabbed":    "value",
		"spacehash": "a",
		"hash":      "a#b",
		"gapped":    "a\n  b\n\n  c",
		"commented": "a\n  b",
		"colon":     "http://example.com",
		"sq":        "'it''s here'",
		"dq":        `"a \" b # c"`,
		"lit":       "|\n  one\n\n  two",
		"fold":      ">-\n  a\n  b",
		"cont":      "first\n  second",
		"flowmap":   `{a: 1, b: [2, 3], c: "}"}`,
		"flowseq":   "[1, 'x, y', {k: v}]",
		"empty":     "",
		"anchored":  "42",
		"alias":     "*anc",
	} {
		if got := d.text(valueOf(t, d, key)); got != want {
			t.Errorf("%s: span is %q, want %q", key, got, want)
		}
	}
}

func TestFlowChildSpans(t *testing.T) {
	d := mustDoc(t, spanDoc)
	fm := valueOf(t, d, "flowmap")
	if got := d.text(fm.lookup("a").val); got != "1" {
		t.Errorf("flowmap.a: %q", got)
	}
	if got := d.text(fm.lookup("b").val); got != "[2, 3]" {
		t.Errorf("flowmap.b: %q", got)
	}
	if got := d.text(fm.lookup("c").val); got != `"}"` {
		t.Errorf("flowmap.c: %q", got)
	}
	fs := valueOf(t, d, "flowseq")
	if got := d.text(fs.elems[1].val); got != "'x, y'" {
		t.Errorf("flowseq[1]: %q", got)
	}
	if got := d.text(fs.elems[2].val); got != "{k: v}" {
		t.Errorf("flowseq[2]: %q", got)
	}
	// A flow element has no "-" marker, so its element start is the value.
	for i, el := range fs.elems {
		if el.dash != el.val.start {
			t.Errorf("flowseq[%d]: dash %d is not the value start %d", i, el.dash, el.val.start)
		}
	}
}

func TestKeySpans(t *testing.T) {
	d := mustDoc(t, "\"quoted: key\": 1\nplain: 2\n'sq key': 3\nempty:\n")
	for i, want := range []string{`"quoted: key"`, "plain", "'sq key'", "empty"} {
		e := d.root.entries[i]
		if got := string(d.src[e.keyStart:e.keyEnd]); got != want {
			t.Errorf("key %d: %q, want %q", i, got, want)
		}
		if d.src[e.colon] != ':' {
			t.Errorf("key %d: colon offset points at %q", i, d.src[e.colon])
		}
	}
}

func TestSequenceElementSpans(t *testing.T) {
	d := mustDoc(t, "s:\n  - one\n  -   two\n  - name: a\n    cmd: b\n")
	s := valueOf(t, d, "s")
	if len(s.elems) != 3 {
		t.Fatalf("want 3 elements, got %d", len(s.elems))
	}
	for i, want := range []string{"one", "two", "name: a\n    cmd: b"} {
		if got := d.text(s.elems[i].val); got != want {
			t.Errorf("element %d: %q, want %q", i, got, want)
		}
		if d.src[s.elems[i].dash] != '-' {
			t.Errorf("element %d: dash offset points at %q", i, d.src[s.elems[i].dash])
		}
		if s.elems[i].indent != 2 {
			t.Errorf("element %d: indent %d, want 2", i, s.elems[i].indent)
		}
	}
	// The sequence's own span runs from the first dash to the last value.
	if got := d.text(s); !strings.HasPrefix(got, "- one") || !strings.HasSuffix(got, "cmd: b") {
		t.Errorf("sequence span: %q", got)
	}
}

func TestAliasSpans(t *testing.T) {
	d := mustDoc(t, "a: &x 1\nb: &y {p: 1}\nflow: [*x, *y]\nm:\n  <<: *y # note\n  q: 2\n")
	if got := d.text(valueOf(t, d, "b")); got != "{p: 1}" {
		t.Errorf("anchored flow map: %q", got)
	}
	fl := valueOf(t, d, "flow")
	for i, want := range []string{"*x", "*y"} {
		if got := d.text(fl.elems[i].val); got != want {
			t.Errorf("flow alias %d: %q, want %q", i, got, want)
		}
	}
	m := valueOf(t, d, "m")
	if !m.entries[0].merge {
		t.Fatal("<< is a merge key")
	}
	if got := d.text(m.entries[0].val); got != "*y" {
		t.Errorf("merge alias: %q (a trailing comment is not part of it)", got)
	}
	if d.aliases["y"] != 2 {
		t.Errorf("alias count for &y: %d, want 2", d.aliases["y"])
	}
	if d.aliases["x"] != 1 {
		t.Errorf("alias count for &x: %d, want 1", d.aliases["x"])
	}

	// An alias ends at a flow terminator or at end of file, not only at a
	// newline.
	d = mustDoc(t, "a: &x 1\nm: {k: *x}\nlast: *x")
	if got := d.text(valueOf(t, d, "m").lookup("k").val); got != "*x" {
		t.Errorf("alias in a flow map: %q", got)
	}
	if got := d.text(valueOf(t, d, "last")); got != "*x" {
		t.Errorf("alias at end of file: %q", got)
	}
}

func TestMergeSourcesSkipNonMappings(t *testing.T) {
	// A merge list whose first entry does not name a mapping is skipped, not
	// treated as the end of the search.
	src := "s: &s 1\nbase: &base\n  a: 1\nm:\n  <<: [*s, *base]\n  b: 2\n"
	d := mustDoc(t, src)
	e, _, anchor := d.mergedLookup(valueOf(t, d, "m"), "a")
	if e == nil || anchor != "base" {
		t.Fatalf("merge list lookup: entry=%v anchor=%q", e, anchor)
	}
}

func TestCommentChildren(t *testing.T) {
	src := "" +
		"m:\n" +
		"  # free one\n" +
		"\n" +
		"  # leading for a\n" +
		"  a: 1\n" +
		"  b: 2 # trailing\n" +
		"  # before c\n" +
		"  c: 3\n"
	d := mustDoc(t, src)
	m := valueOf(t, d, "m")
	var got []string
	for _, c := range d.commentChildren(m) {
		got = append(got, c.text)
	}
	want := []string{"free one", "leading for a", "before c"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("comments: %v, want %v", got, want)
	}
	// A comment separated from its member by a blank line is not part of that
	// member's block; the adjacent one is.
	a := m.lookup("a")
	if block := string(d.src[a.blockStart:a.blockEnd]); block != "  # leading for a\n  a: 1\n" {
		t.Errorf("a's block: %q", block)
	}
	if tc := d.trailingComment(m.lookup("b").val); tc == nil || tc.text != "trailing" {
		t.Errorf("trailing comment: %v", tc)
	}
	if tc := d.trailingComment(m.lookup("c").val); tc != nil {
		t.Errorf("c has no trailing comment, got %q", tc.text)
	}
	// Comments inside a nested container belong to it, not to its parent.
	d = mustDoc(t, "outer:\n  # outer-1\n  inner:\n    # inner-1\n    x: 1\n")
	outer := valueOf(t, d, "outer")
	if cs := d.commentChildren(outer); len(cs) != 1 || cs[0].text != "outer-1" {
		t.Errorf("outer's comments: %v", cs)
	}
	if cs := d.commentChildren(outer.lookup("inner").val); len(cs) != 1 || cs[0].text != "inner-1" {
		t.Errorf("inner's comments: %v", cs)
	}
}

func TestCommentChildrenOfNonContainers(t *testing.T) {
	d := mustDoc(t, "a: 1\n")
	if cs := d.commentChildren(valueOf(t, d, "a")); cs != nil {
		t.Errorf("a scalar has no comment children: %v", cs)
	}
}

func TestCommentTextStripsTheMarkerAndOneSpace(t *testing.T) {
	// §6.1: comment equality is the text after the marker and ONE leading
	// space. A marker with no space, and a marker with nothing after it, are
	// both legal.
	d := mustDoc(t, "m:\n  #tight\n  #  two spaces\n  #\n  # \n  a: 1\n")
	var got []string
	for _, c := range d.commentChildren(valueOf(t, d, "m")) {
		got = append(got, c.text)
	}
	want := []string{"tight", " two spaces", "", ""}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("comment texts: %q, want %q", got, want)
	}
}

func TestScalarDocumentContinues(t *testing.T) {
	// The root node is owned by no key, so a plain scalar document continues
	// over any indented line at all.
	d := mustDoc(t, "hello\n world\n")
	if got := d.text(d.root); got != "hello\n world" {
		t.Errorf("scalar document span: %q", got)
	}
}

func TestInferIndent(t *testing.T) {
	for name, tc := range map[string]struct {
		src  string
		want int
	}{
		"two":                  {"a:\n  b: 1\n", 2},
		"four":                 {"a:\n    b: 1\n", 4},
		"under a sequence":     {"s:\n  - m:\n        k: 1\n", 4},
		"no nested block map":  {"a: 1\nb: [1, 2]\nc: {x: 1}\n", 2},
		"deeply nested wins":   {"a:\n   b:\n      c: 1\n", 3},
		"empty flow is no map": {"a: {}\nb:\n  c: 1\n", 2},
	} {
		t.Run(name, func(t *testing.T) {
			if got := mustDoc(t, tc.src).indent; got != tc.want {
				t.Errorf("indent step: got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestMergeLookupChain(t *testing.T) {
	d := mustDoc(t, "base: &base\n  a: 1\nmid: &mid\n  <<: *base\n  b: 2\nleaf:\n  <<: *mid\n  c: 3\n")
	leaf := valueOf(t, d, "leaf")
	e, holder, anchor := d.mergedLookup(leaf, "a")
	if e == nil || anchor != "base" || holder != valueOf(t, d, "base") {
		t.Fatalf("merge chain: entry=%v anchor=%q", e, anchor)
	}
	if e, _, _ := d.mergedLookup(leaf, "zz"); e != nil {
		t.Error("an absent key is not inherited")
	}
	if got := d.mergeAnchors(leaf); len(got) != 1 || got[0] != "mid" {
		t.Errorf("merge anchors: %v", got)
	}
	if got := d.mergeSources(valueOf(t, d, "base")); got != nil {
		t.Errorf("a mapping is not a merge source: %v", got)
	}
}

func TestCRLFDocument(t *testing.T) {
	target := "server:\r\n  host: localhost\r\n  timeout: 30\r\n"
	got, err := applyIR(t, target, "  - op: replace\n    path: /server/timeout\n    value: 60\n")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if string(got) != "server:\r\n  host: localhost\r\n  timeout: 60\r\n" {
		t.Errorf("CRLF not preserved: %q", got)
	}
}

func TestMultibyteColumns(t *testing.T) {
	// yaml.v3 reports columns in characters, so a line with multi-byte runes
	// before the edited node must be walked rune by rune.
	target := "café: ☕\nnaïve: 1\n"
	got, err := applyIR(t, target, "  - op: replace\n    path: /caf\u00e9\n    value: tea\n")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if string(got) != "café: tea\nnaïve: 1\n" {
		t.Errorf("multibyte span: %q", got)
	}
}

func TestAnchoredValuesKeepTheirAnchor(t *testing.T) {
	// The "&name" token belongs to the declaration, not the value, so an edit
	// to the value leaves it in place.
	target := "a: &keep 30\nb: *keep\n"
	got, err := applyIR(t, target, "  - op: replace\n    path: /a\n    value: 60\n")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if string(got) != "a: &keep 60\nb: *keep\n" {
		t.Errorf("anchor token not preserved: %q", got)
	}
}

func TestTrailingCommentIsNotPartOfTheValue(t *testing.T) {
	target := "a: 30 # keep me\nb: 1\n"
	got, err := applyIR(t, target, "  - op: replace\n    path: /a\n    value: 60\n")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if string(got) != "a: 60 # keep me\nb: 1\n" {
		t.Errorf("trailing comment clobbered: %q", got)
	}
}
