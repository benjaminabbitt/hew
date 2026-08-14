package hew

import (
	"strings"
	"testing"
)

func ordinalPtr(n int) *int { return &n }

func TestParsePathRoundTrip(t *testing.T) {
	cases := []string{
		"/",
		"/server/timeout",
		"/tags/0",
		"/tags/-",
		"/mcpServers/name=github",
		`/tool/x/id="a b"`,
		"/servers/enabled=true",
		"/tags/=gamma",
		`/permissions/deny/="Bash(curl *)"`,
		`/provider/"aws"`,
		`/resource/"aws_instance"/"web"`,
		"/mcpServers/name=ctxloom?",
		"/server/tls?",
		"/server/#0",
		"/server/#t",
	}
	for _, s := range cases {
		p, err := ParsePath(s)
		if err != nil {
			t.Fatalf("ParsePath(%q): %v", s, err)
		}
		if got := p.String(); got != s {
			t.Errorf("ParsePath(%q).String() = %q, want %q", s, got, s)
		}
	}
}

func TestParsePathEmptyIsError(t *testing.T) {
	if _, err := ParsePath(""); err == nil {
		t.Fatal("ParsePath(\"\") should fail: empty path (§4)")
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

func TestOrdinalOnlyInIR(t *testing.T) {
	if _, err := ParsePath(`/provider/"aws"[1]`); err != nil {
		t.Fatalf("ParsePath with ordinal should succeed: %v", err)
	}
	if _, err := ParseAuthoredPath(`/provider/"aws"[1]`); err == nil {
		t.Fatal("ParseAuthoredPath must reject IR-only [n] ordinal selectors (§9.6, §11.10 reduction 4)")
	}
}

func TestTrailingOptionalOnlyOnLastSegment(t *testing.T) {
	if _, err := ParsePath("/server?/tls"); err == nil {
		t.Fatal(`trailing "?" is legal only on the last segment (§4.4)`)
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
		{"key with the optional flag", Segment{Kind: SegKey, Name: "tls", Optional: true}, true},
		{"key with an ordinal selector", Segment{Kind: SegKey, Name: "server", Ordinal: ordinalPtr(1)}, true},
		{"empty key survives in a non-leading position", Segment{Kind: SegKey, Name: ""}, true},

		// --- healthy non-key segments ---------------------------------------
		{"index zero", Segment{Kind: SegIndex, Index: 0}, true},
		{"index", Segment{Kind: SegIndex, Index: 42}, true},
		{"append", Segment{Kind: SegAppend}, true},
		{"append with an ordinal", Segment{Kind: SegAppend, Ordinal: ordinalPtr(2)}, true},
		{"match on a field", Segment{Kind: SegMatch, Name: "name", Value: Scalar{Kind: ScalarString, Text: "github"}}, true},
		{"empty-field match", Segment{Kind: SegMatch, Value: Scalar{Kind: ScalarString, Text: "gamma"}}, true},
		{"quoted match value", Segment{Kind: SegMatch, Name: "port", Value: Scalar{Kind: ScalarString, Text: "8080", Quoted: true}}, true},
		{"numeric match value", Segment{Kind: SegMatch, Name: "port", Value: Scalar{Kind: ScalarNumber, Text: "8080"}}, true},
		{"boolean match value", Segment{Kind: SegMatch, Name: "enabled", Value: Scalar{Kind: ScalarBool, Text: "true"}}, true},
		{"null match value", Segment{Kind: SegMatch, Name: "x", Value: Scalar{Kind: ScalarNull, Text: "null"}}, true},
		{"match field containing =", Segment{Kind: SegMatch, Name: "a=b", Value: Scalar{Kind: ScalarString, Text: "c"}}, true},
		{"comment ordinal", Segment{Kind: SegComment, Index: 0}, true},
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
		{"negative comment ordinal reads back as a key", Segment{Kind: SegComment, Index: -1}, false},
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
		{"optional on the last segment", NewPath(Segment{Kind: SegKey, Name: "server"}, Segment{Kind: SegKey, Name: "tls", Optional: true}), true},

		// The lone empty key used to print "/" and vanish into the document
		// root. It prints /"" now, which is RFC 6901's empty-key member and the
		// hole §4.1 says the quoted form closes.
		{"lone empty key", NewPath(Segment{Kind: SegKey, Name: ""}), true},
		{"key that used to read back as an index", NewPath(Segment{Kind: SegKey, Name: "deps"}, Segment{Kind: SegKey, Name: "8080"}, Segment{Kind: SegKey, Name: "version"}), true},

		// "?" is legal only on the last segment (§4.4) — invisible to any
		// single segment, and the last positional failure left.
		{"optional on a non-final segment", NewPath(Segment{Kind: SegKey, Name: "server", Optional: true}, Segment{Kind: SegKey, Name: "tls"}), false},
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

	// Positional failures have no locally-broken segment, so attribution falls
	// to the shortest prefix that stopped round-tripping.
	mid := NewPath(
		Segment{Kind: SegKey, Name: "server", Optional: true},
		Segment{Kind: SegKey, Name: "tls"},
	)
	seg, bad = mid.firstUnspellable()
	if !bad || seg.Name != "tls" {
		t.Fatalf(`"?" before the last segment: got (%+v, %v), want the segment that could not follow it`, seg, bad)
	}

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
		{"line break", Segment{Kind: SegKey, Name: "a\nb"}, "line break"},
		{"positional", Segment{Kind: SegKey, Name: "x", Optional: true}, "does not survive in this position"},
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
