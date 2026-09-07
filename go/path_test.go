package hew

import (
	"strings"
	"testing"
)

func TestParsePathEmptyIsError(t *testing.T) {
	if _, err := ParsePath(""); err == nil {
		t.Fatal("ParsePath(\"\") should fail: empty path (§4)")
	}
}

// A content-hash fragment (satisfied-recoil) is a `#<namespace>:key=value` tag
// after the `#` locator, parsed by the shared tagma grammar. It round-trips, it
// does not collide with the other `#` forms, and it decomposes into the tag's
// namespace/key/value on the segment.
func TestParsePathHashFragmentRoundTrips(t *testing.T) {
	const hexDigest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	p, err := ParsePath("/tags/#hew:sha256=" + hexDigest)
	if err != nil {
		t.Fatalf("ParsePath hash fragment: %v", err)
	}
	segs := p.Segments()
	last := segs[len(segs)-1]
	if last.Kind != SegHash {
		t.Fatalf("last segment kind = %v, want SegHash", last.Kind)
	}
	if last.Form != "hew" || last.Name != "sha256" || last.Hash != hexDigest {
		t.Fatalf("decomposed = ns:%q key:%q value:%q, want hew/sha256/<hex>", last.Form, last.Name, last.Hash)
	}
	if got := p.String(); got != "/tags/#hew:sha256="+hexDigest {
		t.Fatalf("render round-trip = %q", got)
	}
}

// The hash fragment must not swallow the native `#` forms: `#t` carries no
// namespace, and the comment digest carries one, and both stay SegComment.
func TestHashFragmentDoesNotCaptureComments(t *testing.T) {
	for _, s := range []string{"/server/" + cfrag("note"), "/server/timeout/#t"} {
		p, err := ParsePath(s)
		if err != nil {
			t.Fatalf("ParsePath(%q): %v", s, err)
		}
		segs := p.Segments()
		if k := segs[len(segs)-1].Kind; k != SegComment {
			t.Fatalf("%q last segment kind = %v, want SegComment", s, k)
		}
	}
}

// The bare `#<n>` comment ordinal is GONE, not merely deprecated. It was the
// one position-based address in a design whose §4 says it has none, and it
// mislocated silently: the ordinal came from the comment's position in the
// patch body, so a patch naming one comment resolved to another and the
// before-image assertion agreed with the wrong node. Parsing it must fail, and
// the message must name the replacement — a reader who wrote `#0` needs to be
// told what to write instead, not merely that `#0` is not a path.
func TestCommentOrdinalIsAParseError(t *testing.T) {
	for _, s := range []string{"/#0", "/server/#1", "/a/#12", "/#0/x"} {
		_, err := ParsePath(s)
		if err == nil {
			t.Fatalf("ParsePath(%q) should fail: the comment ordinal is gone (§4.5b)", s)
		}
		if !strings.Contains(err.Error(), "#hew:comment=") {
			t.Fatalf("ParsePath(%q) must name the digest form as the replacement, got: %v", s, err)
		}
	}
	// `#t` is NOT an ordinal: it names the trailing comment of a member, of
	// which there is at most one, so it is an identity and it survives.
	if _, err := ParsePath("/a/#t"); err != nil {
		t.Fatalf("#t is identity, not an ordinal, and stays legal: %v", err)
	}
	// A `#` followed by anything that is not digits or `t` is still an
	// ordinary key, which is what keeps `#foo` and `##0` addressable.
	for _, s := range []string{"/#foo", "/##0"} {
		if _, err := ParsePath(s); err != nil {
			t.Fatalf("ParsePath(%q) is a key, not a comment address: %v", s, err)
		}
	}
}

// The trailing `?` is GONE, and it is refused rather than ignored. It was
// parsed, position-checked, compared and rendered, and read by no resolver, so
// every patch that carried one got the plain no-match it was written to avoid.
// The refusal is the whole point of the removal: `?` is an ordinary character
// in a key, so a token that merely stopped being a flag would fall through to
// the key fallback and address a key literally spelled `tls?` — the failure the
// retired comment ordinal already demonstrated. The message must carry the
// quoted spelling, because a writer who typed `tls?` meant one of the two
// things it can now be, and needs to be told which spelling says which.
func TestTrailingQuestionMarkIsAParseError(t *testing.T) {
	for _, s := range []string{
		"/server/tls?",              // the retired section's first worked example
		"/mcpServers/name=ctxloom?", // and its second
		"/a?/b",                     // not merely a last-segment rule any more
		"/x?",
	} {
		_, err := ParsePath(s)
		if err == nil {
			t.Fatalf("ParsePath(%q) should fail: the optional segment is gone (§4.7)", s)
		}
		if !strings.Contains(err.Error(), `"`) {
			t.Fatalf("ParsePath(%q) must name the quoted literal as the escape hatch, got: %v", s, err)
		}
	}
	// The escape hatch itself: a key whose text really does end in `?` is
	// addressable as a literal, and that is the ONLY way to say it.
	p, err := ParsePath(`/server/"tls?"`)
	if err != nil {
		t.Fatalf(`ParsePath("/server/\"tls?\""): %v`, err)
	}
	if last := p.Segment(1); last.Kind != SegKey || last.Name != "tls?" {
		t.Fatalf("kind/name = %v/%q, want key/tls?", last.Kind, last.Name)
	}
}

func TestParsePathMustStartWithSlashOrDot(t *testing.T) {
	if _, err := ParsePath("server/timeout"); err == nil {
		t.Fatal("ParsePath without leading / or . should fail")
	}
}

func TestRelativePath(t *testing.T) {
	p, err := ParsePath("./port")
	if err != nil {
		t.Fatalf("ParsePath(./port): %v", err)
	}
	if !p.IsRelative() {
		t.Fatal("expected relative path")
	}
	if p.String() != "./port" {
		t.Fatalf("got %q", p.String())
	}
	root, err := ParsePath(".")
	if err != nil {
		t.Fatalf("ParsePath(.): %v", err)
	}
	if !root.IsRelative() || root.Len() != 0 {
		t.Fatalf("relative root: %+v", root)
	}
}

func TestPathIsZeroVsRoot(t *testing.T) {
	var zero Path
	if !zero.IsZero() {
		t.Fatal("zero Path must be zero")
	}
	root := RootPath()
	if root.IsZero() {
		t.Fatal("root path is not the zero (absent) path")
	}
	if root.String() != "/" {
		t.Fatalf("root.String() = %q, want /", root.String())
	}
	if zero.String() != "" {
		t.Fatalf("zero.String() = %q, want empty", zero.String())
	}
}

func TestEscapeRoundTrip(t *testing.T) {
	p := NewPath(Segment{Kind: SegKey, Name: "a/b~c=d"})
	s := p.String()
	const want = "/a~1b~0c~2d"
	if s != want {
		t.Fatalf("String() = %q, want %q", s, want)
	}
	p2, err := ParsePath(s)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if !p.Equal(p2) {
		t.Fatalf("round trip mismatch: %+v != %+v", p, p2)
	}
}

func TestAppendAndParent(t *testing.T) {
	p := NewPath(Segment{Kind: SegKey, Name: "server"})
	p2 := p.Append(Segment{Kind: SegKey, Name: "port"})
	if p2.String() != "/server/port" {
		t.Fatalf("Append: %q", p2.String())
	}
	parent, ok := p2.Parent()
	if !ok || !parent.Equal(p) {
		t.Fatalf("Parent() = %+v, %v; want %+v, true", parent, ok, p)
	}
	if _, ok := RootPath().Parent(); ok {
		t.Fatal("root path has no parent")
	}
}

// --- spellability -----------------------------------------------------------
//
// The predicate under test says whether the §4 grammar can spell a segment or
// a path. Since O41 that is almost always YES — the bijection is in
// bijection_test.go, and it is the property this predicate now mostly reports
// — so what survives here is the SHAPE of the predicate: it is defined by the
// round trip, it can see what one segment cannot, and it blames the right
// segment. Every case asserts TWICE: once against the expectation written down
// here, and once against an INDEPENDENT oracle that re-parses the printed text
// through the public path parser.

// reparsedSegment is the oracle: print the segment, re-read it through
// ParsePath in a position where nothing else can interfere, and report whether
// what came back is the same segment.
func reparsedSegment(s Segment) bool {
	p, err := ParsePath("/anchor/" + s.String())
	return err == nil && p.Len() == 2 && p.Segment(1).Equal(s)
}

func TestSegmentSpellability(t *testing.T) {
	tests := []struct {
		name string
		seg  Segment
		want bool
	}{
		// --- healthy keys ---------------------------------------------------
		{"plain key", Segment{Kind: SegKey, Name: "server"}, true},
		{"key needing every escape", Segment{Kind: SegKey, Name: "a/b~c=d"}, true},
		{"leading-zero digits are not an index", Segment{Kind: SegKey, Name: "08080"}, true},
		{"digits with a suffix", Segment{Kind: SegKey, Name: "8080x"}, true},
		{"hash without digits is not a comment", Segment{Kind: SegKey, Name: "#foo"}, true},
		{"double hash without a space is not a heading", Segment{Kind: SegKey, Name: "##0"}, true},
		{"block kind with a non-numeric ordinal", Segment{Kind: SegKey, Name: "para:x"}, true},
		{"unknown block kind is an ordinary key", Segment{Kind: SegKey, Name: "notablock:0"}, true},
		{"empty brackets are not an ordinal", Segment{Kind: SegKey, Name: "a[]"}, true},
		{"key ending in ?, spellable only quoted", Segment{Kind: SegKey, Name: "tls?"}, true},
		{"empty key survives in a non-leading position", Segment{Kind: SegKey, Name: ""}, true},

		// --- healthy non-key segments ---------------------------------------
		{"index zero", Segment{Kind: SegIndex, Index: 0}, true},
		{"index", Segment{Kind: SegIndex, Index: 42}, true},
		{"append", Segment{Kind: SegAppend}, true},
		{"match on a field", Segment{Kind: SegMatch, Name: "name", Value: Scalar{Kind: ScalarString, Text: "github"}}, true},
		{"empty-field match", Segment{Kind: SegMatch, Value: Scalar{Kind: ScalarString, Text: "gamma"}}, true},
		{"quoted match value", Segment{Kind: SegMatch, Name: "port", Value: Scalar{Kind: ScalarString, Text: "8080", Quoted: true}}, true},
		{"numeric match value", Segment{Kind: SegMatch, Name: "port", Value: Scalar{Kind: ScalarNumber, Text: "8080"}}, true},
		{"boolean match value", Segment{Kind: SegMatch, Name: "enabled", Value: Scalar{Kind: ScalarBool, Text: "true"}}, true},
		{"null match value", Segment{Kind: SegMatch, Name: "x", Value: Scalar{Kind: ScalarNull, Text: "null"}}, true},
		{"match field containing =", Segment{Kind: SegMatch, Name: "a=b", Value: Scalar{Kind: ScalarString, Text: "c"}}, true},
		{"comment digest", commentSegment("note"), true},
		{"trailing comment", Segment{Kind: SegComment, Trailing: true}, true},

		// --- the classes the quoted form rescued (O41) -----------------------
		// Every one of these was a REFUSAL before the literal form existed:
		// each is a key a real document holds whose bare spelling read back as
		// another segment. They are spellable now, and bijection_test.go is
		// where the whole class is exercised; keeping a few here pins that the
		// predicate FOLLOWED the grammar rather than being edited alongside it.
		{"digit-only key", Segment{Kind: SegKey, Name: "8080"}, true},
		{"zero key", Segment{Kind: SegKey, Name: "0"}, true},
		{"dash key", Segment{Kind: SegKey, Name: "-"}, true},
		{"hash-digit key", Segment{Kind: SegKey, Name: "#0"}, true},
		{"hash-t key", Segment{Kind: SegKey, Name: "#t"}, true},
		{"key ending in ?", Segment{Kind: SegKey, Name: "opt?"}, true},
		{"key ending in [n]", Segment{Kind: SegKey, Name: "a[1]"}, true},
		{"double-quoted key", Segment{Kind: SegKey, Name: `"quoted"`}, true},
		{"key opening with a quote", Segment{Kind: SegKey, Name: `"unterminated`}, true},
		{"label", Segment{Kind: SegKey, Name: "aws", Quoted: true}, true},
		{"empty label", Segment{Kind: SegKey, Name: "", Quoted: true}, true},
		{"label with a quote and a backslash", Segment{Kind: SegKey, Name: `a"b\c`, Quoted: true}, true},
		{"reserved match field, quoted", Segment{Kind: SegMatch, Name: "count>", Quoted: true, Value: Scalar{Kind: ScalarNumber, Text: "5"}}, true},

		// --- the residue: data no address can carry --------------------------
		{"negative index reads back as a key", Segment{Kind: SegIndex, Index: -1}, false},
		// A comment segment carrying neither a digest nor the `#t` flag has no
		// spelling left now that the ordinal is gone: it renders as a
		// value-less `#hew:comment=` tag, which does not read back.
		{"comment with no digest has no spelling", Segment{Kind: SegComment}, false},
		// An extension-claimed segment no linked extension claims: the token
		// re-reads as whatever the core makes of it, here an ordinary key.
		{"unclaimed extension token reads back as a key", Segment{Kind: SegExtension, Form: "heading", Raw: "# Setup"}, false},
		{"non-JSON number reads back as a string", Segment{Kind: SegMatch, Name: "mask", Value: Scalar{Kind: ScalarNumber, Text: "0x1f"}}, false},
		{"YAML-spelled boolean reads back as a string", Segment{Kind: SegMatch, Name: "on", Value: Scalar{Kind: ScalarBool, Text: "True"}}, false},
		// The line-break residue is deliberately NOT here: it is the one place
		// the predicate diverges from a literal reparse (a newline survives a
		// reparse and destroys the line it was written on), so it is pinned in
		// bijection_test.go where the divergence can be stated rather than
		// silently contradicting this table's oracle.
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.seg.spellable()
			if got != tc.want {
				t.Errorf("Segment{%s %q}.spellable() = %v, want %v (spelling %q)",
					tc.seg.Kind, tc.seg.Name, got, tc.want, tc.seg.String())
			}
			if oracle := reparsedSegment(tc.seg); got != oracle {
				t.Errorf("predicate says %v but an actual reparse of %q says %v", got, tc.seg.String(), oracle)
			}
		})
	}
}

func TestPathSpellability(t *testing.T) {
	tests := []struct {
		name string
		path Path
		want bool
	}{
		{"root", RootPath(), true},
		{"relative root", NewRelativePath(), true},
		{"ordinary path", NewPath(Segment{Kind: SegKey, Name: "server"}, Segment{Kind: SegKey, Name: "port"}), true},
		{"relative path", NewRelativePath(Segment{Kind: SegKey, Name: "port"}), true},
		{"empty key after a key", NewPath(Segment{Kind: SegKey, Name: "a"}, Segment{Kind: SegKey, Name: ""}), true},
		{"empty key before a key", NewPath(Segment{Kind: SegKey, Name: ""}, Segment{Kind: SegKey, Name: "a"}), true},
		{"relative empty key", NewRelativePath(Segment{Kind: SegKey, Name: ""}), true},
		{"key ending in ? is quoted into a spelling", NewPath(Segment{Kind: SegKey, Name: "server"}, Segment{Kind: SegKey, Name: "tls?"}), true},

		// The lone empty key used to print "/" and vanish into the document
		// root. It prints /"" now, which is RFC 6901's empty-key member and the
		// hole §4.1 says the quoted form closes.
		{"lone empty key", NewPath(Segment{Kind: SegKey, Name: ""}), true},
		{"key that used to read back as an index", NewPath(Segment{Kind: SegKey, Name: "deps"}, Segment{Kind: SegKey, Name: "8080"}, Segment{Kind: SegKey, Name: "version"}), true},

		// A path is no more spellable than its worst segment. There is no
		// longer any POSITIONAL failure to test: the optional segment's
		// "last segment only" rule was the only one, and it is retired (§4.7).
		{"an unspellable segment makes the path unspellable", NewPath(Segment{Kind: SegKey, Name: "server"}, Segment{Kind: SegIndex, Index: -1}), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.path.spellable()
			if got != tc.want {
				t.Errorf("Path(%q).spellable() = %v, want %v", tc.path.String(), got, tc.want)
			}
			q, err := ParsePath(tc.path.String())
			oracle := err == nil && tc.path.Equal(q)
			if got != oracle {
				t.Errorf("predicate says %v but an actual reparse of %q says %v", got, tc.path.String(), oracle)
			}
			if _, bad := tc.path.firstUnspellable(); bad == got {
				t.Errorf("firstUnspellable() reported bad=%v for a path whose spellable() is %v", bad, got)
			}
		})
	}
}

// TestAbsentPathIsSpellable pins the one place the predicate deliberately
// diverges from a literal reparse: the zero path has no spelling at all and
// nothing is emitted for it, so it is not a refusal.
func TestAbsentPathIsSpellable(t *testing.T) {
	var zero Path
	if !zero.spellable() {
		t.Fatal("the absent path emits nothing and must not be refused")
	}
	if _, bad := zero.firstUnspellable(); bad {
		t.Fatal("the absent path has no offending segment")
	}
	if _, err := ParsePath(zero.String()); err == nil {
		t.Fatal("guard assumption broken: the empty string should not parse")
	}
}

// TestFirstUnspellableNamesTheEarliestOffender pins ATTRIBUTION: the
// diagnostic points at the segment that broke, not at the path as a whole.
func TestFirstUnspellableNamesTheEarliestOffender(t *testing.T) {
	p := NewPath(
		Segment{Kind: SegKey, Name: "deps"},
		Segment{Kind: SegIndex, Index: -2},
		Segment{Kind: SegIndex, Index: -1},
	)
	seg, bad := p.firstUnspellable()
	if !bad {
		t.Fatal("path with two unspellable segments must be refused")
	}
	if seg.Index != -2 {
		t.Fatalf("firstUnspellable() named index %d, want the earliest offender -2", seg.Index)
	}

	// There is no positional failure left to attribute: the optional segment's
	// "last segment only" rule was the only spelling that could break a path
	// while every segment survived alone, and it is retired (§4.7). The
	// shortest-prefix fallback below firstUnspellable's loop is now a net for a
	// future form, not a path any v0 spelling can reach.

	// A key that would once have been blamed here is now spelled, not refused:
	// the guard follows the grammar rather than a remembered list.
	rescued := NewPath(Segment{Kind: SegKey, Name: ""}, Segment{Kind: SegKey, Name: "8080"})
	if seg, bad := rescued.firstUnspellable(); bad {
		t.Fatalf("%q round-trips; blaming %+v is a false refusal", rescued.String(), seg)
	}
}

// TestSpellFailureNamesTheCorruption pins the shapes of the explanation:
// "this is refused" without "and here is what it would have meant" is not
// enough for a reviewer to act on.
func TestSpellFailureNamesTheCorruption(t *testing.T) {
	tests := []struct {
		name string
		seg  Segment
		want string
	}{
		{"different kind", Segment{Kind: SegExtension, Form: "heading", Raw: "# Setup"}, "re-reads as a key segment, not a heading"},
		{"same kind, different segment", Segment{Kind: SegMatch, Name: "on", Value: Scalar{Kind: ScalarBool, Text: "True"}}, "re-reads as a different match segment"},
		{"not a legal segment", Segment{Kind: SegExtension, Form: "wildcard", Raw: "*"}, "is not a legal segment"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := spellFailure(tc.seg)
			if !strings.Contains(got, tc.want) {
				t.Errorf("spellFailure() = %q, want it to contain %q", got, tc.want)
			}
			// The spelling is shown, so the reviewer sees the text that would
			// have been written — except where the spelling is what is wrong
			// with it: a line break cannot be printed into a diagnostic that is
			// itself read line by line.
			if spelling := tc.seg.String(); !strings.ContainsAny(spelling, "\r\n") && !strings.Contains(got, spelling) {
				t.Errorf("spellFailure() = %q, want it to show the spelling %q", got, spelling)
			}
		})
	}
}
