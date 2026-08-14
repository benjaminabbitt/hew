package hewcli

import (
	"strings"
	"testing"
)

// A record built from no patch input at all records the stdin source and no
// patch digest, rather than reaching for an input that is not there. runApply
// never gets here — it refuses an invocation with no patch — but the record
// builder must not depend on that to stay safe.
func TestBuildRecordWithoutAPatchInput(t *testing.T) {
	rec, err := buildRecord(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rec.PatchSource != "-" {
		t.Fatalf("source: %q", rec.PatchSource)
	}
	if rec.PatchDigest != "" {
		t.Fatalf("no input, no digest: %q", rec.PatchDigest)
	}
	if len(rec.Targets) != 0 {
		t.Fatalf("targets: %v", rec.Targets)
	}
	out, err := marshalRecord(rec)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "digest:") {
		t.Fatalf("an absent digest must be omitted, not empty:\n%s", out)
	}
	if !strings.Contains(string(out), "source: '-'") && !strings.Contains(string(out), `source: "-"`) {
		t.Fatalf("record should name the stdin source:\n%s", out)
	}
}

func TestDigestIsPrefixedSHA256(t *testing.T) {
	got := digest([]byte("abc"))
	const want = "sha256:ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
