package harness

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

func TestDiffBytesEqual(t *testing.T) {
	tests := []struct {
		name      string
		want, got []byte
	}{
		{"both nil", nil, nil},
		{"nil vs empty", nil, []byte{}},
		{"empty vs nil", []byte{}, nil},
		{"identical", []byte("a\nb\n"), []byte("a\nb\n")},
		{"identical with CRLF", []byte("a\r\nb\r\n"), []byte("a\r\nb\r\n")},
		{"identical binary", []byte{0x00, 0xff, 0x7f}, []byte{0x00, 0xff, 0x7f}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if d := DiffBytes(tc.want, tc.got); d != "" {
				t.Errorf("DiffBytes reported a difference between equal inputs:\n%s", d)
			}
		})
	}
}

// TestDiffBytesFullReport pins the entire report shape: the failure message is
// the only thing a corpus reader sees, so its layout is contract.
func TestDiffBytesFullReport(t *testing.T) {
	got := DiffBytes([]byte("a\nb\nc\n"), []byte("a\nB\nc\n"))
	want := "byte mismatch: want 6 bytes, got 6 bytes; first divergence at offset 2 (line 2, col 1)\n" +
		"  want: b␊\n" +
		"  got:  B␊\n" +
		"  want hex: 62 0a 63 0a\n" +
		"  got hex:  42 0a 63 0a"
	if got != want {
		t.Errorf("DiffBytes() =\n%s\n\nwant\n%s", got, want)
	}
}

var divergenceRE = regexp.MustCompile(`want (\d+) bytes, got (\d+) bytes; first divergence at offset (\d+) \(line (\d+), col (\d+)\)`)

// divergence extracts the header numbers so the position math can be asserted
// independently of the excerpt rendering.
func divergence(t *testing.T, report string) (wantLen, gotLen, off, line, col int) {
	t.Helper()
	m := divergenceRE.FindStringSubmatch(report)
	if m == nil {
		t.Fatalf("report has no divergence header:\n%s", report)
	}
	nums := make([]int, 5)
	for i := 0; i < 5; i++ {
		if _, err := fmt.Sscanf(m[i+1], "%d", &nums[i]); err != nil {
			t.Fatal(err)
		}
	}
	return nums[0], nums[1], nums[2], nums[3], nums[4]
}

func TestDiffBytesDivergencePosition(t *testing.T) {
	tests := []struct {
		name              string
		want, got         string
		offset, line, col int
		wantLen, gotLen   int
	}{
		{"first byte", "abc", "xbc", 0, 1, 1, 3, 3},
		{"mid first line", "abc", "abd", 2, 1, 3, 3, 3},
		{"start of second line", "a\nb", "a\nc", 2, 2, 1, 3, 3},
		{"mid third line", "aa\nbb\ncccc\n", "aa\nbb\nccXc\n", 8, 3, 3, 11, 11},
		{"after blank lines", "\n\n\nx", "\n\n\ny", 3, 4, 1, 4, 4},
		{"prefix equal, want shorter", "abc", "abcdef", 3, 1, 4, 3, 6},
		{"prefix equal, got shorter", "abcdef", "abc", 3, 1, 4, 6, 3},
		{"empty want", "", "x", 0, 1, 1, 0, 1},
		{"empty got", "x", "", 0, 1, 1, 1, 0},
		{"trailing newline missing", "a\n", "a", 1, 1, 2, 2, 1},
		{"newline counted before divergence", "x\ny\nz", "x\ny\nZ", 4, 3, 1, 5, 5},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report := DiffBytes([]byte(tc.want), []byte(tc.got))
			if report == "" {
				t.Fatal("expected a difference report")
			}
			wl, gl, off, line, col := divergence(t, report)
			if wl != tc.wantLen || gl != tc.gotLen {
				t.Errorf("lengths = %d/%d, want %d/%d", wl, gl, tc.wantLen, tc.gotLen)
			}
			if off != tc.offset {
				t.Errorf("offset = %d, want %d", off, tc.offset)
			}
			if line != tc.line || col != tc.col {
				t.Errorf("line/col = %d/%d, want %d/%d", line, col, tc.line, tc.col)
			}
		})
	}
}

// TestDiffBytesTrailingNewlineIsVisible is runner obligation 3's readability
// half: a missing trailing newline must be legible from the message alone.
func TestDiffBytesTrailingNewlineIsVisible(t *testing.T) {
	report := DiffBytes([]byte("key: value\n"), []byte("key: value"))
	wantLine, gotLine := reportLine(t, report, "  want: "), reportLine(t, report, "  got:  ")
	if !strings.Contains(wantLine, "␊") {
		t.Errorf("want line %q must show the trailing newline as ␊", wantLine)
	}
	if strings.Contains(gotLine, "␊") {
		t.Errorf("got line %q must not show a newline it does not have", gotLine)
	}
	if !strings.Contains(report, "want hex: 0a") {
		t.Errorf("hex window must show the 0a that differs:\n%s", report)
	}
}

func TestDiffBytesCRLFIsVisible(t *testing.T) {
	report := DiffBytes([]byte("a: 1\n"), []byte("a: 1\r\n"))
	gotLine := reportLine(t, report, "  got:  ")
	if !strings.Contains(gotLine, "␍") {
		t.Errorf("got line %q must show the CR as ␍", gotLine)
	}
	if !strings.Contains(gotLine, "␊") {
		t.Errorf("got line %q must still show the LF as ␊", gotLine)
	}
	if !strings.Contains(report, "got hex:  0d 0a") {
		t.Errorf("hex window must expose the CRLF bytes:\n%s", report)
	}
}

func TestDiffBytesTabsAndSpacesVisible(t *testing.T) {
	report := DiffBytes([]byte("a:\tb c\n"), []byte("a: b c\n"))
	if !strings.Contains(report, "␉") {
		t.Errorf("tab must render as ␉:\n%s", report)
	}
	if !strings.Contains(report, "·") {
		t.Errorf("space must render as ·:\n%s", report)
	}
}

func TestDiffBytesLengthOnlyDifference(t *testing.T) {
	report := DiffBytes([]byte("abc"), []byte("abcdef"))
	if !strings.Contains(report, "want 3 bytes, got 6 bytes") {
		t.Errorf("report must state both lengths:\n%s", report)
	}
	if !strings.Contains(report, "offset 3") {
		t.Errorf("divergence is at the end of the shorter input:\n%s", report)
	}
	if l := reportLine(t, report, "  want: "); l != "abc" {
		t.Errorf("want excerpt = %q, want %q", l, "abc")
	}
	if l := reportLine(t, report, "  got:  "); l != "abcdef" {
		t.Errorf("got excerpt = %q, want %q", l, "abcdef")
	}
}

func TestDiffBytesEmptySideShowsEndOfContent(t *testing.T) {
	report := DiffBytes(nil, []byte("x"))
	if l := reportLine(t, report, "  want: "); l != "<end of content>" {
		t.Errorf("want excerpt = %q, want <end of content>", l)
	}
	if !strings.Contains(report, "want hex: \n") {
		t.Errorf("empty want must produce an empty hex window:\n%s", report)
	}
}

// TestDiffBytesExcerptTruncation: a 120-column cap keeps a minified JSON
// document from flooding the failure message.
func TestDiffBytesExcerptTruncation(t *testing.T) {
	long := strings.Repeat("a", 500)
	report := DiffBytes([]byte("X"+long), []byte("Y"+long))
	line := reportLine(t, report, "  want: ")
	if got := len([]rune(line)); got != 120 {
		t.Errorf("excerpt is %d runes, want the 120-rune cap:\n%s", got, report)
	}
	if !strings.HasPrefix(line, "X") {
		t.Errorf("excerpt must start at the beginning of the line: %q", line)
	}
}

func TestDiffBytesHexWindowIsSixteenBytes(t *testing.T) {
	report := DiffBytes([]byte(strings.Repeat("a", 40)), []byte("b"+strings.Repeat("a", 39)))
	line := reportLine(t, report, "  want hex: ")
	if fields := strings.Fields(line); len(fields) != 16 {
		t.Errorf("hex window has %d bytes, want 16:\n%s", len(fields), report)
	}
}

func TestDiffBytesHexWindowClampsAtEOF(t *testing.T) {
	report := DiffBytes([]byte("abc"), []byte("abcdefghij"))
	// want ran out at the divergence offset: the window backs up one byte.
	if l := reportLine(t, report, "  want hex: "); l != "63" {
		t.Errorf("want hex = %q, want the final byte 63", l)
	}
	// got has 7 bytes left, fewer than the 16-byte window.
	if l := reportLine(t, report, "  got hex:  "); l != "64 65 66 67 68 69 6a" {
		t.Errorf("got hex = %q", l)
	}
}

// reportLine returns the remainder of the report line beginning with prefix.
func reportLine(t *testing.T, report, prefix string) string {
	t.Helper()
	for _, l := range strings.Split(report, "\n") {
		if strings.HasPrefix(l, prefix) {
			return strings.TrimPrefix(l, prefix)
		}
	}
	t.Fatalf("report has no %q line:\n%s", prefix, report)
	return ""
}

func TestExcerpt(t *testing.T) {
	tests := []struct {
		name string
		in   string
		off  int
		want string
	}{
		{"start of content", "abc\ndef\n", 0, "abc␊"},
		{"second line", "abc\ndef\n", 5, "def␊"},
		{"line without trailing newline", "abc\ndef", 5, "def"},
		{"offset exactly at end after newline", "abc\n", 4, "<end of content>"},
		{"offset past end is clamped", "abc", 99, "abc"},
		{"empty input", "", 0, "<end of content>"},
		{"whitespace made visible", "a\tb c\r\n", 0, "a␉b·c␍␊"},
		{"blank line", "\n\n", 1, "␊"},
		{"offset mid-line includes whole line", "hello world\n", 6, "hello·world␊"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := excerpt([]byte(tc.in), tc.off); got != tc.want {
				t.Errorf("excerpt(%q, %d) = %q, want %q", tc.in, tc.off, got, tc.want)
			}
		})
	}
}

func TestExcerptTruncatesForward(t *testing.T) {
	in := []byte(strings.Repeat("z", 400) + "\n")
	got := excerpt(in, 0)
	if n := len([]rune(got)); n != 120 {
		t.Errorf("excerpt length = %d, want 120", n)
	}
	if strings.Contains(got, "␊") {
		t.Error("a truncated excerpt must not claim to reach the newline")
	}
}

// TestExcerptTruncationIsRelativeToTheLineStart: the 120-column budget is
// measured from the start of the containing line, not from the start of the
// document — a long line deep in a file gets the same excerpt width.
func TestExcerptTruncationIsRelativeToTheLineStart(t *testing.T) {
	prefix := "alpha\nbeta\n" // 11 bytes of earlier lines
	in := []byte(prefix + strings.Repeat("z", 400) + "\n")
	for _, off := range []int{len(prefix), len(prefix) + 50} {
		got := excerpt(in, off)
		if n := len([]rune(got)); n != 120 {
			t.Errorf("excerpt at offset %d is %d runes, want 120", off, n)
		}
		if strings.HasPrefix(got, "alpha") {
			t.Errorf("excerpt at offset %d leaked an earlier line: %q", off, got[:20])
		}
	}
}

func TestDiffBytesExcerptTruncationOnALaterLine(t *testing.T) {
	long := strings.Repeat("a", 500)
	report := DiffBytes([]byte("head\n"+"X"+long), []byte("head\n"+"Y"+long))
	line := reportLine(t, report, "  want: ")
	if got := len([]rune(line)); got != 120 {
		t.Errorf("excerpt is %d runes, want the 120-rune cap:\n%s", got, report)
	}
}

func TestVisible(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want string
	}{
		{"tab", []byte("\t"), "␉"},
		{"newline", []byte("\n"), "␊"},
		{"carriage return", []byte("\r"), "␍"},
		{"space", []byte(" "), "·"},
		{"printable passthrough", []byte("aZ9{}"), "aZ9{}"},
		{"mixed", []byte("a b\tc\r\n"), "a·b␉c␍␊"},
		{"other control byte passes through raw", []byte{0x00}, "\x00"},
		{"empty", nil, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := visible(tc.in); got != tc.want {
				t.Errorf("visible(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestWindow(t *testing.T) {
	tests := []struct {
		name string
		in   string
		off  int
		size int
		want string
	}{
		{"from start", "abcdef", 0, 3, "abc"},
		{"clamped to end", "abcdef", 4, 16, "ef"},
		{"offset at length backs up one byte", "abcdef", 6, 16, "f"},
		{"offset past length backs up one byte", "abcdef", 99, 4, "f"},
		{"empty input", "", 0, 16, ""},
		{"zero size", "abcdef", 2, 0, ""},
		{"exact fit", "abcdef", 0, 6, "abcdef"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := window([]byte(tc.in), tc.off, tc.size)
			if string(got) != tc.want {
				t.Errorf("window(%q, %d, %d) = %q, want %q", tc.in, tc.off, tc.size, got, tc.want)
			}
		})
	}
	if got := window(nil, 0, 16); got != nil {
		t.Errorf("window(nil, ...) = %v, want nil", got)
	}
}
