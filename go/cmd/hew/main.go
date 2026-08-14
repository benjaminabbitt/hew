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
	os.Exit(hewcli.Run(os.Args[1:], dir, os.Stdin, os.Stdout, os.Stderr))
}
