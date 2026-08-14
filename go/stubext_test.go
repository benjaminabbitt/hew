package hew

// The core's own test binary links no extension — it cannot, since every
// extension imports this package — so nothing would be a valid format id and
// every test patch would fail at its `--- ` line. These stubs stand in for the
// six v0 extensions: identity only, no halves, no detection rules.
//
// That the core suite needs them is the point rather than an inconvenience. It
// is the same fact O35 states from the other side: a format exists for a build
// because something registered it, and a core that hardcoded the six could not
// have been made to say so.
func init() {
	for _, id := range []FormatID{
		FormatJSON, FormatJSONC, FormatYAML, FormatTOML, FormatHCL, FormatMarkdown,
	} {
		Register(id, Binding{})
	}
}
