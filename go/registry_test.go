package hew

import (
	"reflect"
	"strings"
	"testing"
)

// The registry's unit suite (§13.7 rule 2, Appendix A.6). It runs entirely on
// FAKE bindings: the core cannot import ext/json without a cycle, and a suite
// that could only test the shipped six would not be able to test the one thing
// A.6 exists for — that a SEVENTH format, registered from outside this module,
// is indistinguishable from the six. The shipped defaults' own detection table
// is pinned in ext/all, the one package that can see all of them at once.

// isolate swaps in an empty registry for the duration of one test and puts the
// real one back afterwards, so a test's fake formats cannot leak into another
// test or into a package that ran init().
func isolate(t *testing.T) {
	t.Helper()
	registryMu.Lock()
	saved := registry
	registry = map[FormatID]Binding{}
	registryMu.Unlock()
	t.Cleanup(func() {
		registryMu.Lock()
		registry = saved
		registryMu.Unlock()
	})
}

// fakeBinding is a complete-looking binding whose halves are identifiable: the
// applier stamps its own format into its output, so a dispatch that reaches the
// wrong binding is visible rather than merely unequal.
func fakeBinding(id FormatID, exts, names []string) Binding {
	return Binding{
		Applier: func(target []byte, tl TransformList) ([]byte, error) {
			return append(append([]byte(nil), target...), []byte("|"+string(id))...), nil
		},
		Differ:   func(src []byte) (*DiffNode, error) { return &DiffNode{Kind: KindScalar}, nil },
		Document: func(name string, src []byte) (Document, error) { return nil, nil },
		Detect:   DetectRule{Extensions: exts, WellKnownNames: names},
	}
}

func TestRegisterThenLookup(t *testing.T) {
	isolate(t)
	Register("ini", fakeBinding("ini", []string{".ini"}, nil))

	b, ok := Lookup("ini")
	if !ok {
		t.Fatal("Lookup after Register reported not found")
	}
	if b.Applier == nil || b.Differ == nil || b.Document == nil {
		t.Fatalf("Lookup returned a hollowed binding: %+v", b)
	}
	got, err := b.Applier([]byte("x"), TransformList{})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "x|ini" {
		t.Fatalf("Lookup returned the wrong binding's applier: %q", got)
	}
}

func TestLookupUnregisteredFormat(t *testing.T) {
	isolate(t)
	Register("ini", fakeBinding("ini", []string{".ini"}, nil))

	if b, ok := Lookup("xml"); ok {
		t.Fatalf("Lookup(%q) = %+v, true; want not found", "xml", b)
	}
	if _, ok := Lookup(""); ok {
		t.Fatal(`Lookup("") reported found; the empty format is no format`)
	}
}

func TestRegisterRejectsDuplicate(t *testing.T) {
	isolate(t)
	Register("ini", fakeBinding("ini", []string{".ini"}, nil))

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("registering a second binding for one format did not panic")
		}
		if msg, _ := r.(string); !strings.Contains(msg, "ini") {
			t.Fatalf("panic message does not name the format: %v", r)
		}
	}()
	Register("ini", fakeBinding("ini", []string{".ini2"}, nil))
}

func TestRegisterRejectsEmptyFormatID(t *testing.T) {
	isolate(t)
	defer func() {
		if recover() == nil {
			t.Fatal("registering the empty format id did not panic")
		}
	}()
	Register("", fakeBinding("", []string{".x"}, nil))
}

func TestFormatsListsEveryRegisteredIDSorted(t *testing.T) {
	isolate(t)
	Register("ini", fakeBinding("ini", []string{".ini"}, nil))
	Register("xml", fakeBinding("xml", []string{".xml"}, nil))
	Register("csv", fakeBinding("csv", []string{".csv"}, nil))

	want := []FormatID{"csv", "ini", "xml"}
	if got := Formats(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Formats() = %v, want %v", got, want)
	}
}

func TestDetectFormatReadsTheName(t *testing.T) {
	isolate(t)
	Register("ini", fakeBinding("ini", []string{".ini"}, nil))
	Register("xml", fakeBinding("xml", []string{".xml", ".xsd"}, nil))

	cases := []struct {
		name string
		want FormatID
		ok   bool
	}{
		{"app.ini", "ini", true},
		{"app.xml", "xml", true},
		{"schema.xsd", "xml", true},
		{"dir/nested/app.ini", "ini", true},
		{"APP.INI", "ini", true},
		{"app.INI", "ini", true},
		{"app.toml", "", false},
		{"app", "", false},
		{"", "", false},
		{".ini", "ini", true},
	}
	for _, c := range cases {
		got, ok := DetectFormat(c.name)
		if got != c.want || ok != c.ok {
			t.Errorf("DetectFormat(%q) = %q, %v; want %q, %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestDetectFormatWellKnownNameBeatsExtension(t *testing.T) {
	isolate(t)
	Register("ini", fakeBinding("ini", []string{".conf"}, nil))
	Register("xml", fakeBinding("xml", nil, []string{"pom.conf", ".hidden.conf"}))

	for _, name := range []string{"pom.conf", "dir/pom.conf", "POM.CONF", ".hidden.conf"} {
		got, ok := DetectFormat(name)
		if !ok || got != "xml" {
			t.Errorf("DetectFormat(%q) = %q, %v; want the well-known name to win", name, got, ok)
		}
	}
	if got, ok := DetectFormat("other.conf"); !ok || got != "ini" {
		t.Errorf("DetectFormat(%q) = %q, %v; want the extension rule to still apply", "other.conf", got, ok)
	}
}

func TestDetectFormatAmbiguityIsNoDetection(t *testing.T) {
	isolate(t)
	Register("ini", fakeBinding("ini", []string{".conf"}, []string{"app.cfg"}))
	Register("xml", fakeBinding("xml", []string{".conf"}, []string{"app.cfg"}))

	// §8.0: two bindings claiming one name is HEW021 — the caller's cue to say
	// format=. Detection reports "no", it does not pick a winner.
	if got, ok := DetectFormat("thing.conf"); ok {
		t.Errorf("DetectFormat on a doubly-claimed extension = %q, true; want no detection", got)
	}
	if got, ok := DetectFormat("app.cfg"); ok {
		t.Errorf("DetectFormat on a doubly-claimed well-known name = %q, true; want no detection", got)
	}
}

func TestDetectFormatNeverReadsContent(t *testing.T) {
	isolate(t)
	Register("ini", fakeBinding("ini", []string{".ini"}, nil))

	// The whole signature is a NAME (§8.0, O48): there is nowhere for content to
	// enter. This test states the property the way a reader can check it — a file
	// whose name says .ini is ini however JSON-shaped its contents look.
	if got, ok := DetectFormat(`{"looks":"like json"}.ini`); !ok || got != "ini" {
		t.Fatalf("DetectFormat = %q, %v; want ini", got, ok)
	}
}

// --- the FormatID.Valid() defect (O48) --------------------------------------

func TestFormatIDValidIsRegistryMembership(t *testing.T) {
	isolate(t)
	Register("ini", fakeBinding("ini", []string{".ini"}, nil))

	if !FormatID("ini").Valid() {
		t.Error("a registered format is not Valid(); validity must be a registry lookup (§8.8)")
	}
	if FormatID("xml").Valid() {
		t.Error("an unregistered format is Valid()")
	}
	if FormatID("").Valid() {
		t.Error(`the empty format is Valid()`)
	}
	// The six v0 formats are not special: unregistered, they are as unknown as
	// any other name. This is what makes "linked" and "capable" the same fact.
	if FormatID("json").Valid() {
		t.Error("a v0 format is Valid() with no binding registered; Valid() still hardcodes the six")
	}
}

// TestSeventhFormatParsesAndValidates is the defect the audit found, stated end
// to end: a correctly-registered seventh extension was refused by the PARSER,
// before any binding was consulted, because Valid() hardcoded the six v0
// formats. Every seam that gates on a format id is exercised here.
func TestSeventhFormatParsesAndValidates(t *testing.T) {
	isolate(t)
	Register("ini", fakeBinding("ini", []string{".ini"}, nil))

	t.Run("target attribute", func(t *testing.T) {
		tls, err := Parse([]byte("hew: 1\n\n--- app.ini format=ini\n\n@@ /server @@\n- port: 8080\n+ port: 9090\n"))
		if err != nil {
			t.Fatalf("parsing a patch for a registered seventh format failed: %v", err)
		}
		if len(tls) != 1 || tls[0].Format != "ini" {
			t.Fatalf("got %d lists, format %q; want 1 list, ini", len(tls), tls[0].Format)
		}
		if err := tls[0].Validate(); err != nil {
			t.Fatalf("Validate refused a registered seventh format: %v", err)
		}
	})

	t.Run("preamble default", func(t *testing.T) {
		tls, err := Parse([]byte("hew: 1\nformat: ini\n\n--- app.ini\n\n@@ /server @@\n- port: 8080\n+ port: 9090\n"))
		if err != nil {
			t.Fatalf("parsing a preamble format default for a registered format failed: %v", err)
		}
		if tls[0].Format != "ini" {
			t.Fatalf("format = %q, want ini", tls[0].Format)
		}
	})

	t.Run("still refuses an unregistered format", func(t *testing.T) {
		_, err := Parse([]byte("hew: 1\n\n--- app.xml format=xml\n\n@@ /server @@\n- port: 8080\n+ port: 9090\n"))
		if err == nil {
			t.Fatal("parsing a patch for an UNregistered format succeeded; HEW021 is the point of the check")
		}
	})

	t.Run("round trips through .hewt", func(t *testing.T) {
		tls, err := Parse([]byte("hew: 1\n\n--- app.ini format=ini\n\n@@ /server @@\n- port: 8080\n+ port: 9090\n"))
		if err != nil {
			t.Fatal(err)
		}
		out, err := MarshalTransformStream(tls)
		if err != nil {
			t.Fatalf("marshalling a seventh format's transform list failed: %v", err)
		}
		back, err := UnmarshalTransforms(out)
		if err != nil {
			t.Fatalf("unmarshalling it back failed: %v", err)
		}
		if back.Format != "ini" {
			t.Fatalf("format = %q after a .hewt round trip, want ini", back.Format)
		}
	})
}

// --- extension-declared node kinds (O48) ------------------------------------

func TestNodeKindValidCoversUniversalKindsAlone(t *testing.T) {
	isolate(t)
	for _, k := range []NodeKind{KindMap, KindSeq, KindScalar} {
		if !k.Valid() {
			t.Errorf("%q is not Valid() with no extension registered; map/seq/scalar are universal (§8.8)", k)
		}
	}
	for _, k := range []NodeKind{"block", "section", "paragraph", ""} {
		if k.Valid() {
			t.Errorf("%q is Valid() with no extension declaring it", k)
		}
	}
}

func TestNodeKindValidAcceptsExtensionDeclaredKinds(t *testing.T) {
	isolate(t)
	b := fakeBinding("ini", []string{".ini"}, nil)
	b.Kinds = []NodeKind{"stanza"}
	Register("ini", b)

	if !NodeKind("stanza").Valid() {
		t.Error("an extension-declared kind is not Valid(); §8.8 moved the closed enum to the extensions")
	}
	if NodeKind("block").Valid() {
		t.Error(`"block" is Valid() with no extension declaring it`)
	}
}

func TestExtensionDeclaredKindParsesInANotationPatch(t *testing.T) {
	isolate(t)
	b := fakeBinding("ini", []string{".ini"}, nil)
	b.Kinds = []NodeKind{"stanza"}
	Register("ini", b)

	patch := "hew: 1\n\n--- app.ini format=ini\n\n@@ /server @@\n? kind /server = stanza\n  port: 8080\n"
	if _, err := Parse([]byte(patch)); err != nil {
		t.Fatalf("`? kind = stanza` was refused although ext/ini declares it: %v", err)
	}

	bad := "hew: 1\n\n--- app.ini format=ini\n\n@@ /server @@\n? kind /server = paragraph\n  port: 8080\n"
	if _, err := Parse([]byte(bad)); err == nil {
		t.Fatal("`? kind = paragraph` parsed although no extension declares it")
	}
}

// --- extension-claimed segment shapes (O48) ---------------------------------
//
// The mechanism, on a fake shape: `[name]` is an INI section header, a spelling
// the core knows nothing about and never has to. ext/markdown's suite tests the
// three shapes that actually shipped; these tests are about the seam.

// stanzaForm claims "[name]" and refuses the nameless "[]" — the malformed
// answer, which is what keeps a typo from becoming a key that resolves nowhere.
func stanzaForm() SegmentForm {
	return SegmentForm{
		Name: "stanza",
		Claim: func(raw string) (bool, error) {
			if len(raw) < 2 || raw[0] != '[' || raw[len(raw)-1] != ']' {
				return false, nil
			}
			if raw == "[]" {
				return true, segErr("segment \"[]\": stanza requires a name")
			}
			return true, nil
		},
	}
}

func registerStanza(t *testing.T) {
	t.Helper()
	isolate(t)
	b := fakeBinding("ini", []string{".ini"}, nil)
	b.Segments = []SegmentForm{stanzaForm()}
	Register("ini", b)
}

func TestClaimedSegmentParsesAndRoundTrips(t *testing.T) {
	registerStanza(t)

	p, err := ParsePath("/[server]/port")
	if err != nil {
		t.Fatalf("ParsePath: %v", err)
	}
	seg := p.Segment(0)
	if seg.Kind != SegExtension {
		t.Fatalf("kind = %s, want an extension-claimed segment", seg.Kind)
	}
	if seg.Form != "stanza" || seg.Raw != "[server]" {
		t.Fatalf("form/raw = %q/%q, want stanza/[server]", seg.Form, seg.Raw)
	}
	if got := p.String(); got != "/[server]/port" {
		t.Fatalf("String() = %q; a claimed segment must print back byte for byte", got)
	}
	if !seg.spellable() {
		t.Fatal("a claimed segment does not survive its own round trip")
	}
}

func TestClaimedSegmentCarriesTheUniversalSuffixes(t *testing.T) {
	registerStanza(t)

	p, err := ParsePath("/[server][2]?")
	if err != nil {
		t.Fatalf("ParsePath: %v", err)
	}
	seg := p.Segment(0)
	// The `?` and the `[n]` are stripped before the form is offered the token,
	// so a form describes only its own shape and cannot forget to handle them.
	if seg.Raw != "[server]" {
		t.Fatalf("raw = %q, want the token without the universal suffixes", seg.Raw)
	}
	if !seg.Optional || seg.Ordinal == nil || *seg.Ordinal != 2 {
		t.Fatalf("suffixes lost: %+v", seg)
	}
	if got := p.String(); got != "/[server][2]?" {
		t.Fatalf("String() = %q", got)
	}
}

func TestMalformedClaimIsAnError(t *testing.T) {
	registerStanza(t)

	if _, err := ParsePath("/[]"); err == nil {
		t.Fatal("a claimed-but-malformed token parsed; the form's error was dropped")
	} else if !strings.Contains(err.Error(), "stanza requires a name") {
		t.Fatalf("error %q does not carry the form's own message", err)
	}
}

func TestUnclaimedTokenFallsThroughToKey(t *testing.T) {
	registerStanza(t)

	for _, body := range []string{"[unclosed", "server]", "plain"} {
		p, err := ParsePath("/" + body)
		if err != nil {
			t.Fatalf("ParsePath(/%s): %v", body, err)
		}
		if k := p.Segment(0).Kind; k != SegKey {
			t.Errorf("/%s parsed as %s, want the key floor", body, k)
		}
	}
}

func TestCoreShapesOutrankAClaim(t *testing.T) {
	isolate(t)
	// A form that claims EVERYTHING still cannot take a shape the core owns:
	// the quoted segment, "-", and the comment address are read before any
	// extension is asked, and an index or a key-match after (§8.8).
	greedy := SegmentForm{Name: "greedy", Claim: func(string) (bool, error) { return true, nil }}
	b := fakeBinding("ini", []string{".ini"}, nil)
	b.Segments = []SegmentForm{greedy}
	Register("ini", b)

	for _, c := range []struct {
		body string
		want SegmentKind
	}{
		{`"aws"`, SegKey}, // the LITERAL form; what it means is the container's (§4.1, O41)
		{"-", SegAppend},
		{"#0", SegComment},
		{"#t", SegComment},
		// These two the extension DOES take, because §8.8 puts the claim ahead
		// of index, key-match and key — the shapes a claimed spelling has
		// always outranked (`code:0` would otherwise be a key, `@name` too).
		{"7", SegExtension},
		{"name=x", SegExtension},
	} {
		p, err := ParsePath("/" + c.body)
		if err != nil {
			t.Fatalf("ParsePath(/%s): %v", c.body, err)
		}
		if got := p.Segment(0).Kind; got != c.want {
			t.Errorf("/%s parsed as %s, want %s", c.body, got, c.want)
		}
	}
	if p, _ := ParsePath(`/"aws"`); !p.Segment(0).IsQuoted() {
		t.Error(`a greedy claim must not take the quoted form's literal spelling either`)
	}
}

func TestClaimOrderIsDeterministic(t *testing.T) {
	isolate(t)
	both := func(name string) SegmentForm {
		return SegmentForm{Name: name, Claim: func(raw string) (bool, error) {
			return strings.HasPrefix(raw, "%"), nil
		}}
	}
	// Two formats claiming one shape is an ambiguity the core cannot resolve;
	// what it CAN promise is that it resolves the same way every run, which map
	// iteration alone would not give.
	a := fakeBinding("aaa", []string{".a"}, nil)
	a.Segments = []SegmentForm{both("first")}
	Register("aaa", a)
	z := fakeBinding("zzz", []string{".z"}, nil)
	z.Segments = []SegmentForm{both("second")}
	Register("zzz", z)

	for i := 0; i < 20; i++ {
		p, err := ParsePath("/%x")
		if err != nil {
			t.Fatal(err)
		}
		if got := p.Segment(0).Form; got != "first" {
			t.Fatalf("form = %q on run %d; claims must resolve by sorted format id", got, i)
		}
	}
}

// --- extension-owned transform qualifiers (O48, tension 1) ------------------

func TestBindingDeclaresItsQualifierKeys(t *testing.T) {
	isolate(t)
	b := fakeBinding("ini", []string{".ini"}, nil)
	b.Qualifiers = []string{"stanza"}
	Register("ini", b)

	if !OwnsQualifier("ini", "stanza") {
		t.Error("the registering extension does not own the qualifier it declared")
	}
	if OwnsQualifier("ini", "anchor") {
		t.Error("an extension owns a qualifier it never declared")
	}
	if OwnsQualifier("xml", "stanza") {
		t.Error("an unregistered format owns a qualifier")
	}
}
