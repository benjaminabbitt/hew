// Package all links every v0 format extension, so the common case is one
// import (Appendix A.6, O35):
//
//	import _ "github.com/benjaminabbitt/hew/go/ext/all"
//
// A program that edits only one format should NOT import this: import
// ext/json and link no HCL parser. That choice is the reason registration is
// import-for-effect at all — it makes "linked" and "capable" the same fact, so
// what a build can do is visible in its import graph rather than in a switch
// somewhere it cannot see.
package all

import (
	_ "github.com/benjaminabbitt/hew/go/ext/hcl"
	_ "github.com/benjaminabbitt/hew/go/ext/json"
	_ "github.com/benjaminabbitt/hew/go/ext/jsonc"
	_ "github.com/benjaminabbitt/hew/go/ext/markdown"
	_ "github.com/benjaminabbitt/hew/go/ext/toml"
	_ "github.com/benjaminabbitt/hew/go/ext/yaml"
)
