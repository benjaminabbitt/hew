// Package hewerr defines the one error type hew returns (spec Appendix A.9).
// Every field is part of the contract the conformance corpus asserts on:
// code, component (the corpus's error_seam), path, patch line, and message
// content all carry conformance weight.
package hewerr

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Code is a HEW error code (spec §10).
type Code string

const (
	// Parser layer — raised before any target is opened.
	CodeParse             Code = "HEW001" // parse-error
	CodeUnsupportedFormat Code = "HEW021" // unsupported-format

	// Resolver / IO layer.
	CodeTargetParse Code = "HEW002" // target-parse-error
	CodeTargetPath  Code = "HEW003" // target-path-error

	// Applier layer.
	CodeStaleTarget      Code = "HEW010" // stale-target
	CodeAssertionFailed  Code = "HEW011" // assertion-failed
	CodeAmbiguousMatch   Code = "HEW012" // ambiguous-match
	CodeNoMatch          Code = "HEW013" // no-match
	CodeAlreadyExists    Code = "HEW014" // already-exists
	CodeInexpressible    Code = "HEW020" // inexpressible
	CodeConflict         Code = "HEW030" // conflict
	CodeAnchorAmbiguity  Code = "HEW040" // anchor-ambiguity
	CodeSurfaceAmbiguity Code = "HEW041" // surface-ambiguity
)

// codeNames maps each code to its spec §10 slug, which is part of the
// diagnostic message contract (corpus message_contains entries name them).
var codeNames = map[Code]string{
	CodeParse:             "parse-error",
	CodeTargetParse:       "target-parse-error",
	CodeTargetPath:        "target-path-error",
	CodeStaleTarget:       "stale-target",
	CodeAssertionFailed:   "assertion-failed",
	CodeAmbiguousMatch:    "ambiguous-match",
	CodeNoMatch:           "no-match",
	CodeAlreadyExists:     "already-exists",
	CodeInexpressible:     "inexpressible",
	CodeUnsupportedFormat: "unsupported-format",
	CodeConflict:          "conflict",
	CodeAnchorAmbiguity:   "anchor-ambiguity",
	CodeSurfaceAmbiguity:  "surface-ambiguity",
}

// Name returns the spec §10 slug for a code ("stale-target"), or "" if the
// code is unknown.
func (c Code) Name() string { return codeNames[c] }

// Component is the layer that raised an error. The corpus asserts it via
// error_seam: a code surfacing from the wrong component means a layer
// deferred work it should have refused (spec A.9).
type Component int

const (
	ComponentParser Component = iota
	ComponentResolver
	ComponentApplier
	ComponentDiffer
	ComponentRenderer
	ComponentCLI
)

func (c Component) String() string {
	switch c {
	case ComponentParser:
		return "parser"
	case ComponentResolver:
		return "resolver"
	case ComponentApplier:
		return "applier"
	case ComponentDiffer:
		return "differ"
	case ComponentRenderer:
		return "renderer"
	case ComponentCLI:
		return "cli"
	}
	return fmt.Sprintf("component(%d)", int(c))
}

// Error is hew's single error type. Fields left zero are omitted from the
// rendered message.
type Error struct {
	Code       Code
	Component  Component
	Target     string // target file, as declared by the patch
	Path       string // hew path of the failing node
	PatchFile  string // the .hew (or .hewt) file, for provenance lines
	PatchLine  int
	TargetLine int
	Want, Got  string
	Detail     string
}

// Error renders the spec §10.3 human diagnostic shape:
//
//	hew: config.yaml:/server/timeout: HEW010 stale-target
//	  patch.hew:9: expected 30
//	  config.yaml:6: found 45
func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString("hew: ")
	if e.Target != "" {
		b.WriteString(e.Target)
		b.WriteString(":")
	}
	if e.Path != "" {
		b.WriteString(e.Path)
		b.WriteString(":")
	}
	b.WriteString(" ")
	b.WriteString(string(e.Code))
	if name := e.Code.Name(); name != "" {
		b.WriteString(" ")
		b.WriteString(name)
	}
	if e.PatchLine > 0 {
		b.WriteString("\n  ")
		if e.PatchFile != "" {
			b.WriteString(e.PatchFile)
		} else {
			b.WriteString("patch")
		}
		fmt.Fprintf(&b, ":%d:", e.PatchLine)
		if e.Want != "" {
			b.WriteString(" expected ")
			b.WriteString(e.Want)
		}
	} else if e.Want != "" {
		b.WriteString("\n  expected ")
		b.WriteString(e.Want)
	}
	if e.Got != "" {
		b.WriteString("\n  ")
		if e.Target != "" {
			b.WriteString(e.Target)
			if e.TargetLine > 0 {
				fmt.Fprintf(&b, ":%d", e.TargetLine)
			}
			b.WriteString(":")
		}
		b.WriteString(" found ")
		b.WriteString(e.Got)
	}
	if e.Detail != "" {
		b.WriteString("\n  ")
		b.WriteString(e.Detail)
	}
	return b.String()
}

// As unwraps err to a *Error, following error wrapping.
func As(err error) (*Error, bool) {
	var he *Error
	if errors.As(err, &he) {
		return he, true
	}
	return nil, false
}

// JSON is §10.3's machine-readable diagnostic: one object per error, carrying
// the same facts the human form prints and nothing the human form invents.
//
// It exists because the human shape is prose that a consumer would otherwise
// have to parse back out — and the fields it would want most, the codes and
// the two line numbers, are exactly the ones prose makes ambiguous. Empty
// fields are omitted so an absent fact reads as absent rather than as "".
func (e *Error) JSON() ([]byte, error) {
	type payload struct {
		Code       string `json:"code"`
		Slug       string `json:"slug,omitempty"`
		Component  string `json:"component,omitempty"`
		Target     string `json:"target,omitempty"`
		TargetLine int    `json:"targetLine,omitempty"`
		Path       string `json:"path,omitempty"`
		Patch      string `json:"patch,omitempty"`
		PatchLine  int    `json:"patchLine,omitempty"`
		Want       string `json:"want,omitempty"`
		Got        string `json:"got,omitempty"`
		Detail     string `json:"detail,omitempty"`
	}
	return json.Marshal(payload{
		Code:       string(e.Code),
		Slug:       e.Code.Name(),
		Component:  e.Component.String(),
		Target:     e.Target,
		TargetLine: e.TargetLine,
		Path:       e.Path,
		Patch:      e.PatchFile,
		PatchLine:  e.PatchLine,
		Want:       e.Want,
		Got:        e.Got,
		Detail:     e.Detail,
	})
}
