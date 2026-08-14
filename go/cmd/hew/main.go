// Command hew is the standalone CLI (Appendix B). It is a thin wrapper: all
// logic lives in internal/hewcli so the corpus harness can drive the same
// code in-process.
package main

import (
	"os"

	"github.com/benjaminabbitt/hew/internal/hewcli"
)

func main() {
	dir, err := os.Getwd()
	if err != nil {
		os.Exit(2)
	}
	os.Exit(hewcli.Run(os.Args[1:], dir, os.Stdin, os.Stdout, os.Stderr))
}
