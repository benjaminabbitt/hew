package hew

import "testing"

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
		"/@ctxloom:context",
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
