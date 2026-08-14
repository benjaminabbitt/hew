// Command hew is the standalone CLI (Appendix B). It is a thin wrapper: all
// logic lives in internal/hewcli so the corpus harness can drive the same
// code in-process.
//
// It is also where this program's format capability is DECIDED. hewcli names
// no format package; the blank ext/all import below is what registers the six
// v0 extensions, and dropping it to a single `_ ext/json` would produce a
// working hew that links no HCL parser (O35). Nothing else has to change for
// that to hold — which is the property import-for-effect registration buys.
package main

import (
	"os"

	_ "github.com/benjaminabbitt/hew/go/ext/all"
	"github.com/benjaminabbitt/hew/go/internal/hewcli"
)

func main() {
	dir, err := os.Getwd()
	if err != nil {
		os.Exit(2)
	}
	os.Exit(hewcli.Run(os.Args[1:], dir, hewEnv(), os.Stdin, os.Stdout, os.Stderr))
}

// hewEnv is the whole of hew's environment surface: the two variables
// Appendix B.1 declares, both governing the record's applied_at (§9.7, ruling
// O37). It is built HERE, in main, rather than read inside hewcli, so that the
// set is enumerable by reading one function — and so that the corpus harness,
// which drives hewcli in-process with a pinned map, exercises the same code
// path this binary does.
func hewEnv() map[string]string {
	env := map[string]string{}
	for _, name := range []string{"HEW_APPLIED_AT", "SOURCE_DATE_EPOCH"} {
		if v, ok := os.LookupEnv(name); ok {
			env[name] = v
		}
	}
	return env
}
