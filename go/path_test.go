package hew

import (
	"strings"
	"testing"

	"github.com/ctxloom/hew/internal/hewerr"
)

func ptr[T any](v T) *T { return &v }

func key(s string) Segment    { return Segment{Kind: SegKey, Name: s} }
func idx(i int) Segment       { return Segment{Kind: SegIndex, Index: i} }
func label(s string) Segment  { return Segment{Kind: SegLabel, Name: s} }
func marker(s string) Segment { return Segment{Kind: SegMarker, Name: s} }

func head(level int, text string) Segment {
	return Segment{Kind: SegHeading, Level: level, Name: text}
}

func block(k BlockKind, n int) Segment {
	return Segment{Kind: SegBlock, Block: k, Index: n}
}

func match(field string, v Scalar) Segment {
	return Segment{Kind: SegMatch, Name: field, Value: v}
}

func str(s string) Scalar     { return Scalar{Kind: ScalarString, Text: s} }
func qstr(s string) Scalar    { return Scalar{Kind: ScalarString, Text: s, Quoted: true} }
func num(s string) Scalar     { return Scalar{Kind: ScalarNumber, Text: s} }
func boolean(s string) Scalar { return Scalar{Kind: ScalarBool, Text: s} }

var nullScalar = Scalar{Kind: ScalarNull, Text: "null"}

// pathCases pins the whole §4 grammar. Every entry asserts the decoded
// structure AND that printing reproduces the input verbatim, so the two
// directions cannot drift apart silently.
var pathCases = []struct {
	in   string
	rel  bool
	segs []Segment
}{
	// §4.1 — RFC 6901 keys, indices, the append token, escapes.
	{in: "/", segs: nil},
	{in: "/server", segs: []Segment{key("server")}},
	{in: "/server/timeout", segs: []Segment{key("server"), key("timeout")}},
	{in: "/tags/0", segs: []Segment{key("tags"), idx(0)}},
	{in: "/tags/12", segs: []Segment{key("tags"), idx(12)}},
	{in: "/tags/-", segs: []Segment{key("tags"), {Kind: SegAppend}}},
	{in: "//", segs: []Segment{key(""), key("")}},
	{in: "/a~0b", segs: []Segment{key("a~b")}},
	{in: "/a~1b", segs: []Segment{key("a/b")}},
	{in: "/a~2b", segs: []Segment{key("a=b")}},
	{in: "/~0~1~2", segs: []Segment{key("~/=")}},
	{in: "/007", segs: []Segment{key("007")}},   // leading zero is not an index
	{in: "/-1", segs: []Segment{key("-1")}},     // only a lone "-" is the append token
	{in: "/--", segs: []Segment{key("--")}},     //
	{in: "/#", segs: []Segment{key("#")}},       // bare "#" is a key
	{in: "/#foo", segs: []Segment{key("#foo")}}, // "#" + non-digits is a key
	{in: "/##0", segs: []Segment{key("##0")}},   // only a single "#" makes a comment
	{in: "/code:x", segs: []Segment{key("code:x")}},
	{in: "/code:", segs: []Segment{key("code:")}},
	{in: "/foo:0", segs: []Segment{key("foo:0")}}, // "foo" is not a block kind

	// §4.2 — key-match, with format-native value decoding.
	{in: "/mcpServers/name=github", segs: []Segment{key("mcpServers"), match("name", str("github"))}},
	{in: "/servers/port=8080", segs: []Segment{key("servers"), match("port", num("8080"))}},
	{in: `/servers/port="8080"`, segs: []Segment{key("servers"), match("port", qstr("8080"))}},
	{in: "/servers/enabled=true", segs: []Segment{key("servers"), match("enabled", boolean("true"))}},
	{in: "/servers/enabled=false", segs: []Segment{key("servers"), match("enabled", boolean("false"))}},
	{in: "/servers/x=null", segs: []Segment{key("servers"), match("x", nullScalar)}},
	{in: "/x/k=-3.5e10", segs: []Segment{key("x"), match("k", num("-3.5e10"))}},
	{in: "/x/k=0", segs: []Segment{key("x"), match("k", num("0"))}},
	{in: "/x/k=08080", segs: []Segment{key("x"), match("k", str("08080"))}}, // leading zero => string
	{in: "/x/k=1.", segs: []Segment{key("x"), match("k", str("1."))}},       // not a JSON number
	{in: "/x/k=", segs: []Segment{key("x"), match("k", str(""))}},
	{in: `/tool/x/id="a b"`, segs: []Segment{key("tool"), key("x"), match("id", qstr("a b"))}},
	{in: "/x/a~2b=c", segs: []Segment{key("x"), match("a=b", str("c"))}},
	{in: "/tags/=gamma", segs: []Segment{key("tags"), match("", str("gamma"))}},
	{in: `/permissions/deny/="Bash(curl *)"`, segs: []Segment{key("permissions"), key("deny"), match("", qstr("Bash(curl *)"))}},
	{in: "/x/=", segs: []Segment{key("x"), match("", str(""))}},
	{in: `/x/k="a~1b"`, segs: []Segment{key("x"), match("k", qstr("a/b"))}},

	// §4.3 — HCL label segments.
	{in: `/provider/"aws"`, segs: []Segment{key("provider"), label("aws")}},
	{in: `/provider/"aws"/region`, segs: []Segment{key("provider"), label("aws"), key("region")}},
	{in: `/resource/"aws_instance"/"web"`, segs: []Segment{key("resource"), label("aws_instance"), label("web")}},
	{in: "/terraform", segs: []Segment{key("terraform")}},
	{in: `/x/""`, segs: []Segment{key("x"), label("")}},
	{in: `/x/"a\"b"`, segs: []Segment{key("x"), label(`a"b`)}},
	{in: `/x/"a\\b"`, segs: []Segment{key("x"), label(`a\b`)}},
	{in: `/x/"a~1b"`, segs: []Segment{key("x"), label("a/b")}},
	{in: `/x/"a~0b"`, segs: []Segment{key("x"), label("a~b")}},
	{in: `/x/"a=b"`, segs: []Segment{key("x"), label("a=b")}}, // labels never key-match

	// §4.5 — Markdown headings, blocks, markers (model only, no backend).
	{in: "/# Getting started", segs: []Segment{head(1, "Getting started")}},
	{in: "/# Getting started/## Install", segs: []Segment{head(1, "Getting started"), head(2, "Install")}},
	{in: "/### Deep", segs: []Segment{head(3, "Deep")}},
	{in: "/# a~1b", segs: []Segment{head(1, "a/b")}},
	{in: "/# a~0b", segs: []Segment{head(1, "a~b")}},
	{in: "/# a=b", segs: []Segment{head(1, "a=b")}}, // heading wins over key-match
	{in: "/# ", segs: []Segment{head(1, "")}},
	{in: "/# Install/code:0", segs: []Segment{head(1, "Install"), block(BlockCode, 0)}},
	{in: "/# Install/para:1", segs: []Segment{head(1, "Install"), block(BlockPara, 1)}},
	{in: "/# Install/list:0", segs: []Segment{head(1, "Install"), block(BlockList, 0)}},
	{in: "/table:2", segs: []Segment{block(BlockTable, 2)}},
	{in: "/quote:0", segs: []Segment{block(BlockQuote, 0)}},
	{in: "/html:11", segs: []Segment{block(BlockHTML, 11)}},
	{in: "/@ctxloom:context", segs: []Segment{marker("ctxloom:context")}},
	{in: "/@a~1b", segs: []Segment{marker("a/b")}},

	// §4.5b — comment addresses.
	{in: "/server/#0", segs: []Segment{key("server"), {Kind: SegComment, Index: 0}}},
	{in: "/server/#2", segs: []Segment{key("server"), {Kind: SegComment, Index: 2}}},
	{in: "/#0", segs: []Segment{{Kind: SegComment, Index: 0}}},
	{in: "/server/timeout/#t", segs: []Segment{key("server"), key("timeout"), {Kind: SegComment, Trailing: true}}},

	// §4.4 — the trailing "?".
	{in: "/server/tls?", segs: []Segment{key("server"), {Kind: SegKey, Name: "tls", Optional: true}}},
	{in: "/mcpServers/name=ctxloom?", segs: []Segment{
		key("mcpServers"),
		{Kind: SegMatch, Name: "name", Value: str("ctxloom"), Optional: true},
	}},
	{in: `/provider/"aws"?`, segs: []Segment{key("provider"), {Kind: SegLabel, Name: "aws", Optional: true}}},

	// §9.6 — the IR-only "[n]" ordinal selector.
	{in: `/provider/"aws"[0]/region`, segs: []Segment{
		key("provider"),
		{Kind: SegLabel, Name: "aws", Ordinal: ptr(0)},
		key("region"),
	}},
	{in: `/provider/"aws"[1]/profile`, segs: []Segment{
		key("provider"),
		{Kind: SegLabel, Name: "aws", Ordinal: ptr(1)},
		key("profile"),
	}},
	{in: "/x[12]", segs: []Segment{{Kind: SegKey, Name: "x", Ordinal: ptr(12)}}},
	{in: "/x[2]?", segs: []Segment{{Kind: SegKey, Name: "x", Ordinal: ptr(2), Optional: true}}},
	{in: "/x[]", segs: []Segment{key("x[]")}},   // empty brackets are part of the key
	{in: "/x[a]", segs: []Segment{key("x[a]")}}, // non-digits are part of the key
	{in: "/[0]", segs: []Segment{key("[0]")}},   // a selector needs a segment to select from

	// §4.6 — relative paths.
	{in: ".", rel: true, segs: nil},
	{in: "./port", rel: true, segs: []Segment{key("port")}},
	{in: "./server/port", rel: true, segs: []Segment{key("server"), key("port")}},
	{in: "./", rel: true, segs: []Segment{key("")}},
	{in: "./#t", rel: true, segs: []Segment{{Kind: SegComment, Trailing: true}}},
}

func TestParsePath(t *testing.T) {
	for _, tc := range pathCases {
		t.Run(tc.in, func(t *testing.T) {
			p, err := ParsePath(tc.in)
			if err != nil {
				t.Fatalf("ParsePath(%q) = error %v", tc.in, err)
			}
			if p.IsZero() {
				t.Fatalf("ParsePath(%q) produced the zero (absent) path", tc.in)
			}
			if p.IsRelative() != tc.rel {
				t.Errorf("IsRelative() = %v, want %v", p.IsRelative(), tc.rel)
			}
			if p.Len() != len(tc.segs) {
				t.Fatalf("Len() = %d, want %d (segments %+v)", p.Len(), len(tc.segs), p.Segments())
			}
			for i, want := range tc.segs {
				if got := p.Segment(i); !got.Equal(want) {
					t.Errorf("segment %d = %+v, want %+v", i, got, want)
				}
			}
			if got := p.String(); got != tc.in {
				t.Errorf("String() = %q, want %q", got, tc.in)
			}
			if !p.Equal(NewPathOf(tc.rel, tc.segs)) {
				t.Errorf("Equal against the expected structure failed")
			}
		})
	}
}

// NewPathOf builds the expected path for a table row.
func NewPathOf(rel bool, segs []Segment) Path {
	if rel {
		return NewRelativePath(segs...)
	}
	return NewPath(segs...)
}

func TestParsePathErrors(t *testing.T) {
	cases := []struct {
		in     string
		detail string
	}{
		{"", "empty path"},
		{"port", `must begin with "/" or "."`},
		{"server/port", `must begin with "/" or "."`},
		{".port", `must continue with "/" after "."`},
		{"/a~", `dangling "~" escape`},
		{"/a~3b", `invalid escape "~3"`},
		{"/a~2b~9", `invalid escape "~9"`},
		{"/# a~2b", `invalid escape "~2" here`},
		{"/@a~2b", `invalid escape "~2" here`},
		{`/"unterminated`, "unterminated quoted segment"},
		{`/"a"b"`, "unescaped quote inside quoted segment"},
		{`/"a\qb"`, `invalid escape "\q" in quoted segment`},
		{`/"a~9b"`, `invalid escape "~9" in quoted segment`},
		{`/"a\`, `dangling "\" escape in quoted segment`},
		{`/"a~`, `dangling "~" escape in quoted segment`},
		{"/@", "marker segment requires a name"},
		{`/x/k="unterminated`, "unterminated quoted segment"},
		{"/x/k=a~5b", `invalid escape "~5"`},
		{"/a?/b", `legal only on the last segment`},
		{"/a?/b?", `legal only on the last segment`},
		{"/99999999999999999999999", "index out of range"},
		{"/#99999999999999999999999", "comment ordinal out of range"},
		{"/code:99999999999999999999999", "block ordinal out of range"},
		{"/x[99999999999999999999999]", "ordinal selector out of range"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			p, err := ParsePath(tc.in)
			if err == nil {
				t.Fatalf("ParsePath(%q) = %v, want an error", tc.in, p)
			}
			if !p.IsZero() {
				t.Errorf("ParsePath returned %v alongside an error", p)
			}
			he, ok := hewerr.As(err)
			if !ok {
				t.Fatalf("error is not a *hewerr.Error: %T", err)
			}
			if he.Code != hewerr.CodeParse {
				t.Errorf("code = %s, want %s", he.Code, hewerr.CodeParse)
			}
			if he.Component != hewerr.ComponentParser {
				t.Errorf("component = %s, want parser", he.Component)
			}
			if he.Path != tc.in {
				t.Errorf("Path = %q, want the offending input %q", he.Path, tc.in)
			}
			if !strings.Contains(he.Detail, tc.detail) {
				t.Errorf("detail %q does not contain %q", he.Detail, tc.detail)
			}
		})
	}
}

func TestParseAuthoredPathRejectsOrdinals(t *testing.T) {
	// §7.2/§11.10-4: the notation spells ordinal selection as a visible
	// annotation, so the selector is IR-only.
	for _, in := range []string{`/provider/"aws"[0]`, `/provider/"aws"[0]/region`, "/x[2]?"} {
		if _, err := ParseAuthoredPath(in); err == nil {
			t.Errorf("ParseAuthoredPath(%q) succeeded, want HEW001", in)
		} else if he, ok := hewerr.As(err); !ok || he.Code != hewerr.CodeParse {
			t.Errorf("ParseAuthoredPath(%q) = %v, want HEW001", in, err)
		} else if !strings.Contains(he.Detail, "! match ord=") {
			t.Errorf("detail %q should point at the notation spelling", he.Detail)
		}
	}
	// Everything else parses identically under both entry points.
	for _, tc := range pathCases {
		p, err := ParsePath(tc.in)
		if err != nil {
			t.Fatal(err)
		}
		a, aerr := ParseAuthoredPath(tc.in)
		if p.HasOrdinal() {
			if aerr == nil {
				t.Errorf("ParseAuthoredPath(%q) accepted an ordinal", tc.in)
			}
			continue
		}
		if aerr != nil {
			t.Errorf("ParseAuthoredPath(%q) = %v", tc.in, aerr)
		} else if !a.Equal(p) {
			t.Errorf("ParseAuthoredPath(%q) != ParsePath", tc.in)
		}
	}
}

func TestPathHasOrdinal(t *testing.T) {
	if MustParsePath("/a/b").HasOrdinal() {
		t.Error("/a/b reports an ordinal")
	}
	if !MustParsePath("/a[0]/b").HasOrdinal() {
		t.Error("/a[0]/b reports no ordinal")
	}
	if !MustParsePath("/a/b[3]").HasOrdinal() {
		t.Error("/a/b[3] reports no ordinal")
	}
}

func TestZeroPath(t *testing.T) {
	var p Path
	if !p.IsZero() {
		t.Error("the zero Path is not IsZero")
	}
	if p.String() != "" {
		t.Errorf("zero path prints %q, want the empty string", p.String())
	}
	if p.Len() != 0 || p.Segments() != nil {
		t.Error("the zero path has segments")
	}
	if p.IsRelative() {
		t.Error("the zero path is relative")
	}
	if _, ok := p.Parent(); ok {
		t.Error("the zero path has a parent")
	}
	if !p.Append(key("x")).IsZero() {
		t.Error("appending to the zero path produced a path")
	}
	// The root path is emphatically NOT the zero path: an absent From is not
	// a From of "/".
	if RootPath().IsZero() {
		t.Error("RootPath is IsZero")
	}
	if p.Equal(RootPath()) {
		t.Error("the zero path equals the root path")
	}
}

func TestPathAppendAndParent(t *testing.T) {
	p := MustParsePath("/server")
	child := p.Append(key("port"))
	if got := child.String(); got != "/server/port" {
		t.Errorf("Append = %q", got)
	}
	if got := p.String(); got != "/server" {
		t.Errorf("Append mutated the receiver: %q", got)
	}
	parent, ok := child.Parent()
	if !ok || !parent.Equal(p) {
		t.Errorf("Parent = %v, %v; want /server", parent, ok)
	}
	root, ok := parent.Parent()
	if !ok || !root.Equal(RootPath()) {
		t.Errorf("Parent of /server = %v, %v; want the root path", root, ok)
	}
	if _, ok := root.Parent(); ok {
		t.Error("the root path has a parent")
	}
	rel := MustParsePath("./a").Append(key("b"))
	if !rel.IsRelative() || rel.String() != "./a/b" {
		t.Errorf("relative Append = %q (relative=%v)", rel.String(), rel.IsRelative())
	}
	relParent, _ := rel.Parent()
	if !relParent.IsRelative() {
		t.Error("Parent dropped the relative origin")
	}
}

func TestPathSegmentsIsACopy(t *testing.T) {
	p := MustParsePath("/a/b")
	segs := p.Segments()
	segs[0] = key("mutated")
	if p.Segment(0).Name != "a" {
		t.Error("Segments() aliased the path's storage")
	}
	built := NewPath(segs...)
	segs[1] = key("mutated")
	if built.Segment(1).Name != "b" {
		t.Error("NewPath aliased its argument")
	}
}

func TestPathEqual(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"/a/b", "/a/b", true},
		{"/a/b", "/a/c", false},
		{"/a/b", "/a", false},
		{"/a/b", "./a/b", false},
		{"/tags/0", "/tags/=0", false},
		{"/x/k=8080", `/x/k="8080"`, false}, // number vs string is a real difference
		{"/x[0]", "/x[1]", false},
		{"/x[0]", "/x", false},
		{"/x", "/x[0]", false},
		{"/x[0]", "/x[0]", true},
		{"/x?", "/x", false},
		{"/# a", "/# a", true},
		{"/# a", "/## a", false},
		{"/code:0", "/para:0", false},
		{"/#t", "/#0", false},
	}
	for _, tc := range cases {
		a, b := MustParsePath(tc.a), MustParsePath(tc.b)
		if got := a.Equal(b); got != tc.want {
			t.Errorf("%s.Equal(%s) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
		if got := b.Equal(a); got != tc.want {
			t.Errorf("%s.Equal(%s) = %v, want %v (asymmetric)", tc.b, tc.a, got, tc.want)
		}
	}
}

func TestMustParsePathPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("MustParsePath did not panic on a malformed path")
		}
	}()
	MustParsePath("nope")
}

func TestPrintEscapesConstructedSegments(t *testing.T) {
	// Printing must escape whatever would otherwise change a segment's form.
	cases := []struct {
		seg  Segment
		want string
	}{
		{key("a/b"), "a~1b"},
		{key("a~b"), "a~0b"},
		{key("a=b"), "a~2b"},
		{match("a=b", str("c=d")), "a~2b=c~2d"},
		{match("a/b", str("c/d")), "a~1b=c~1d"},
		{label(`a"b`), `"a\"b"`},
		{label(`a\b`), `"a\\b"`},
		{label("a/b"), `"a~1b"`},
		{label("a~b"), `"a~0b"`},
		{head(2, "a/b"), "## a~1b"},
		{head(2, "a=b"), "## a=b"}, // "=" needs no escape in heading text
		{marker("a/b"), "@a~1b"},
		{match("", qstr("a b")), `="a b"`},
		{match("k", nullScalar), "k=null"},
		{Segment{Kind: SegAppend}, "-"},
		{Segment{Kind: SegComment, Trailing: true}, "#t"},
		{Segment{Kind: SegComment, Index: 7}, "#7"},
		{Segment{Kind: SegKey, Name: "x", Ordinal: ptr(3), Optional: true}, "x[3]?"},
	}
	for _, tc := range cases {
		if got := tc.seg.String(); got != tc.want {
			t.Errorf("%+v printed %q, want %q", tc.seg, got, tc.want)
		}
		// And what it prints must read back as the same segment.
		p, err := ParsePath("/" + tc.want)
		if err != nil {
			t.Fatalf("reparsing %q: %v", tc.want, err)
		}
		if !p.Segment(0).Equal(tc.seg) {
			t.Errorf("reparse of %q = %+v, want %+v", tc.want, p.Segment(0), tc.seg)
		}
	}
}

func TestSegmentKindString(t *testing.T) {
	want := map[SegmentKind]string{
		SegKey: "key", SegIndex: "index", SegAppend: "append", SegMatch: "match",
		SegLabel: "label", SegHeading: "heading", SegBlock: "block",
		SegMarker: "marker", SegComment: "comment",
	}
	for k, s := range want {
		if k.String() != s {
			t.Errorf("SegmentKind(%d).String() = %q, want %q", k, k.String(), s)
		}
	}
	if got := SegmentKind(99).String(); got != "segment(99)" {
		t.Errorf("unknown kind prints %q", got)
	}
	if got := ScalarKind(99).String(); got != "scalar(99)" {
		t.Errorf("unknown scalar kind prints %q", got)
	}
	for k, s := range map[ScalarKind]string{
		ScalarString: "string", ScalarNumber: "number", ScalarBool: "bool", ScalarNull: "null",
	} {
		if k.String() != s {
			t.Errorf("ScalarKind(%d).String() = %q, want %q", k, k.String(), s)
		}
	}
}

func TestSegmentEqualOrdinalPointers(t *testing.T) {
	a := Segment{Kind: SegKey, Name: "x", Ordinal: ptr(1)}
	b := Segment{Kind: SegKey, Name: "x", Ordinal: ptr(1)}
	if !a.Equal(b) {
		t.Error("equal ordinals compared unequal (pointer identity leaked)")
	}
	if a.Equal(Segment{Kind: SegKey, Name: "x", Ordinal: ptr(2)}) {
		t.Error("different ordinals compared equal")
	}
	if a.Equal(key("x")) || key("x").Equal(a) {
		t.Error("an ordinal compared equal to no ordinal")
	}
	if !key("x").Equal(key("x")) {
		t.Error("two nil ordinals compared unequal")
	}
}

func TestIsNumberBoundaries(t *testing.T) {
	yes := []string{"0", "-0", "1", "8080", "-3", "1.5", "-1.5", "1e10", "1E10", "1e+10", "1e-10", "1.5e3", "0.5"}
	no := []string{"", "-", ".", "01", "-01", "1.", ".5", "1e", "1e+", "+1", "1.2.3", "0x10", "8080 ", "nan", "1_000"}
	for _, s := range yes {
		if !isNumber(s) {
			t.Errorf("isNumber(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if isNumber(s) {
			t.Errorf("isNumber(%q) = true, want false", s)
		}
	}
}

// FuzzPathRoundTrip is the parse∘print identity guard: whatever parses must
// print to something that parses to the same path.
func FuzzPathRoundTrip(f *testing.F) {
	for _, tc := range pathCases {
		f.Add(tc.in)
	}
	for _, s := range []string{"/", ".", "//", "/~0", `/"a"`, "/# h", "/@m", "/#t", "/x[0]", "/a?"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		p, err := ParsePath(in)
		if err != nil {
			return
		}
		printed := p.String()
		q, err := ParsePath(printed)
		if err != nil {
			t.Fatalf("parse(%q) printed %q, which does not parse: %v", in, printed, err)
		}
		if !p.Equal(q) {
			t.Fatalf("parse∘print is not the identity: %q -> %q -> %q", in, printed, q.String())
		}
		if again := q.String(); again != printed {
			t.Fatalf("print is not stable: %q -> %q -> %q", in, printed, again)
		}
	})
}
