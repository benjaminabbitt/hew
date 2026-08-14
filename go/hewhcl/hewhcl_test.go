package hewhcl

import (
	"strings"
	"testing"

	"github.com/benjaminabbitt/hew/go"
	"github.com/benjaminabbitt/hew/go/internal/hewerr"
	"gopkg.in/yaml.v3"
)

// --- helpers ----------------------------------------------------------------

func val(t testing.TB, src string) hew.Value {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(src), &n); err != nil {
		t.Fatalf("test value %q: %v", src, err)
	}
	if len(n.Content) == 0 {
		t.Fatalf("test value %q decoded to nothing", src)
	}
	return hew.NodeValue(n.Content[0])
}

func p(t testing.TB, s string) hew.Path {
	t.Helper()
	path, err := hew.ParsePath(s)
	if err != nil {
		t.Fatalf("path %q: %v", s, err)
	}
	return path
}

func list(ts ...hew.Transform) hew.TransformList {
	return hew.TransformList{Target: "target.tf", Format: hew.FormatHCL, Transform: ts}
}

func apply(t testing.TB, src string, tl hew.TransformList) string {
	t.Helper()
	out, err := Apply([]byte(src), tl)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	return string(out)
}

// failWith asserts the whole error contract the corpus asserts on: code, path,
// patch line, and message content.
func failWith(t testing.TB, src string, tl hew.TransformList, code hewerr.Code, path string, line int, contains ...string) *hewerr.Error {
	t.Helper()
	out, err := Apply([]byte(src), tl)
	if err == nil {
		t.Fatalf("want %s, got success:\n%s", code, out)
	}
	if out != nil {
		t.Errorf("all-or-nothing violated: non-nil bytes alongside an error")
	}
	he, ok := hewerr.As(err)
	if !ok {
		t.Fatalf("not a *hewerr.Error: %v", err)
	}
	if he.Code != code {
		t.Errorf("code: want %s, got %s (%v)", code, he.Code, err)
	}
	if he.Component != hewerr.ComponentApplier {
		t.Errorf("component: want applier, got %s", he.Component)
	}
	if path != "" && he.Path != path {
		t.Errorf("path: want %s, got %s", path, he.Path)
	}
	if line != 0 && he.PatchLine != line {
		t.Errorf("patch line: want %d, got %d", line, he.PatchLine)
	}
	for _, want := range contains {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message missing %q: %v", want, err)
		}
	}
	return he
}

func eq(t testing.TB, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// --- the corpus shapes, as unit tests ---------------------------------------

const providers = `provider "aws" {
  region  = "us-west-1"
  profile = "default"
}

provider "aws" {
  alias  = "east"
  region = "us-east-1"
}
`

func TestSetAttributeSplicesTheExpressionOnly(t *testing.T) {
	src := `terraform {
  required_version = ">= 1.6"
}

provider "google" {
  project = "old-project"
  region  = "us-central1"
}
`
	got := apply(t, src, list(
		hew.Transform{Op: hew.OpTest, Path: p(t, `/provider/"google"/project`), Value: val(t, "old-project")},
		hew.Transform{Op: hew.OpReplace, Path: p(t, `/provider/"google"/project`), Value: val(t, "new-project")},
	))
	eq(t, got, `terraform {
  required_version = ">= 1.6"
}

provider "google" {
  project = "new-project"
  region  = "us-central1"
}
`)
}

func TestAddBlockWritesBlockOuterAndObjectInner(t *testing.T) {
	src := `terraform {
  required_version = ">= 1.6"
}
`
	got := apply(t, src, list(hew.Transform{
		Op:    hew.OpAdd,
		Path:  p(t, "/terraform/required_providers"),
		After: p(t, "/terraform/required_version"),
		Value: val(t, "aws:\n  source: hashicorp/aws\n  version: \"~> 5.0\"\n"),
	}))
	eq(t, got, `terraform {
  required_version = ">= 1.6"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}
`)
}

func TestRemoveBlockTakesTheBlankLineBefore(t *testing.T) {
	src := `provider "google" {
  project = "p"
}

provider "azurerm" {
  features {}
}
`
	got := apply(t, src, list(
		hew.Transform{Op: hew.OpTest, Path: p(t, `/provider/"azurerm"/features`), Value: val(t, "{}")},
		hew.Transform{Op: hew.OpRemove, Path: p(t, `/provider/"azurerm"`)},
	))
	eq(t, got, `provider "google" {
  project = "p"
}
`)
}

func TestRemoveFirstBlockTakesTheBlankLineAfter(t *testing.T) {
	src := `provider "google" {
  project = "p"
}

provider "azurerm" {
  features {}
}
`
	got := apply(t, src, list(hew.Transform{Op: hew.OpRemove, Path: p(t, `/provider/"google"`)}))
	eq(t, got, `provider "azurerm" {
  features {}
}
`)
}

func TestRelabelIsCopyPlusRemove(t *testing.T) {
	src := `provider "aws" {
  region = "us-west-1"
}
`
	got := apply(t, src, list(
		hew.Transform{Op: hew.OpTest, Path: p(t, `/provider/"aws"/region`), Value: val(t, "us-west-1")},
		hew.Transform{Op: hew.OpCopy, From: p(t, `/provider/"aws"`), Path: p(t, `/provider/"aws-legacy"`)},
		hew.Transform{Op: hew.OpRemove, Path: p(t, `/provider/"aws"`)},
	))
	eq(t, got, `provider "aws-legacy" {
  region = "us-west-1"
}
`)
}

func TestCopyKeepsTheOriginalAndSeparatesWithABlankLine(t *testing.T) {
	src := `provider "aws" {
  region = "us-west-1"
}
`
	got := apply(t, src, list(
		hew.Transform{Op: hew.OpCopy, From: p(t, `/provider/"aws"`), Path: p(t, `/provider/"aws-legacy"`)},
	))
	eq(t, got, `provider "aws" {
  region = "us-west-1"
}

provider "aws-legacy" {
  region = "us-west-1"
}
`)
}

func TestOrdinalSelectsAmongRepeatedTuplesAndRealignsTheTouchedBody(t *testing.T) {
	got := apply(t, providers, list(
		hew.Transform{Op: hew.OpTest, Path: p(t, `/provider/"aws"[0]/region`), Value: val(t, "us-west-1")},
		hew.Transform{Op: hew.OpReplace, Path: p(t, `/provider/"aws"[0]/region`), Value: val(t, "us-west-2")},
		hew.Transform{Op: hew.OpTest, Path: p(t, `/provider/"aws"[1]/alias`), Value: val(t, "east")},
		hew.Transform{Op: hew.OpAdd, Path: p(t, `/provider/"aws"[1]/profile`),
			After: p(t, `/provider/"aws"[1]/region`), Value: val(t, "ctxloom")},
	))
	eq(t, got, `provider "aws" {
  region  = "us-west-2"
  profile = "default"
}

provider "aws" {
  alias   = "east"
  region  = "us-east-1"
  profile = "ctxloom"
}
`)
}

// --- addressing errors ------------------------------------------------------

func TestRepeatedTupleWithNoOrdinalIsAmbiguous(t *testing.T) {
	tl := list(hew.Transform{
		Op:         hew.OpTest,
		Path:       p(t, `/provider/"aws"/region`),
		Value:      val(t, "us-east-1"),
		PatchLine:  6,
		AnchorPath: p(t, `/provider/"aws"`),
		AnchorLine: 5,
	})
	// The failure is INSIDE the anchor, so it is reported at the `@@` line.
	failWith(t, providers, tl, hewerr.CodeAmbiguousMatch, `/provider/"aws"`, 5,
		"ambiguous-match", "provider", "aws")
}

func TestAmbiguityBelowTheAnchorKeepsTheBodyLine(t *testing.T) {
	src := `provider "aws" {
  network {}
  network {}
}
`
	tl := list(hew.Transform{
		Op:         hew.OpTest,
		Path:       p(t, `/provider/"aws"/network/name`),
		Value:      val(t, "n"),
		PatchLine:  6,
		AnchorPath: p(t, `/provider/"aws"`),
		AnchorLine: 5,
	})
	failWith(t, src, tl, hewerr.CodeAmbiguousMatch, `/provider/"aws"/network`, 6, "ambiguous-match")
}

func TestAnchorLineIsIgnoredWhenTheParserDidNotRecordOne(t *testing.T) {
	tl := list(hew.Transform{
		Op:        hew.OpTest,
		Path:      p(t, `/provider/"aws"/region`),
		Value:     val(t, "us-east-1"),
		PatchLine: 6,
	})
	failWith(t, providers, tl, hewerr.CodeAmbiguousMatch, `/provider/"aws"`, 6, "ambiguous-match")
}

// TestShiftedOrdinalFailsByName is hcl/ordinal-shifted-target: a third block
// was inserted first, so ord=1 now selects the wrong one, and the required
// distinguishing assert catches it.
func TestShiftedOrdinalFailsByName(t *testing.T) {
	src := `provider "aws" {
  alias  = "brand-new"
  region = "eu-west-1"
}

provider "aws" {
  region  = "us-west-1"
  profile = "default"
}

provider "aws" {
  alias  = "east"
  region = "us-east-1"
}
`
	tl := list(
		hew.Transform{Op: hew.OpTest, Path: p(t, `/provider/"aws"[1]/alias`), Value: val(t, "east"),
			PatchLine: 7, AnchorPath: p(t, `/provider/"aws"[1]`), AnchorLine: 5},
		hew.Transform{Op: hew.OpTest, Path: p(t, `/provider/"aws"[1]/region`), Value: val(t, "us-east-1"),
			PatchLine: 8, AnchorPath: p(t, `/provider/"aws"[1]`), AnchorLine: 5},
		hew.Transform{Op: hew.OpAdd, Path: p(t, `/provider/"aws"[1]/profile`),
			After: p(t, `/provider/"aws"[1]/region`), Value: val(t, "ctxloom"),
			PatchLine: 9, AnchorPath: p(t, `/provider/"aws"[1]`), AnchorLine: 5},
	)
	// The path names the first assert that caught the shift; the line brackets
	// the stale before-image at its last failing line.
	failWith(t, src, tl, hewerr.CodeStaleTarget, `/provider/"aws"[1]/alias`, 8, "stale-target", "east")
}

func TestOrdinalDriftStopsAtTheFirstAssertWhenTheRestHold(t *testing.T) {
	src := `provider "aws" {
  region = "us-west-1"
}

provider "aws" {
  region = "us-east-1"
}
`
	tl := list(
		hew.Transform{Op: hew.OpTest, Path: p(t, `/provider/"aws"[1]/alias`), Value: val(t, "east"), PatchLine: 7},
		hew.Transform{Op: hew.OpTest, Path: p(t, `/provider/"aws"[1]/region`), Value: val(t, "us-east-1"), PatchLine: 8},
	)
	failWith(t, src, tl, hewerr.CodeStaleTarget, `/provider/"aws"[1]/alias`, 7, "stale-target")
}

func TestDriftWithoutAnOrdinalKeepsItsOwnLine(t *testing.T) {
	src := `provider "aws" {
  region = "us-west-1"
}
`
	tl := list(hew.Transform{Op: hew.OpTest, Path: p(t, `/provider/"aws"/region`),
		Value: val(t, "us-east-1"), PatchLine: 6})
	he := failWith(t, src, tl, hewerr.CodeStaleTarget, `/provider/"aws"/region`, 6, "stale-target")
	if he.Want != "us-east-1" || he.Got != "us-west-1" {
		t.Errorf("want/got: %q / %q", he.Want, he.Got)
	}
}

func TestOrdinalOutOfRange(t *testing.T) {
	tl := list(hew.Transform{Op: hew.OpTest, Path: p(t, `/provider/"aws"[4]/region`), Value: val(t, "x"), PatchLine: 3})
	failWith(t, providers, tl, hewerr.CodeStaleTarget, `/provider/"aws"[4]`, 3, "5th", "only 2", "same-label sibling")
}

func TestOrdinalOnBothNameAndLabelIsRefused(t *testing.T) {
	tl := list(hew.Transform{Op: hew.OpTest, Path: p(t, `/provider[0]/"aws"[1]/region`), Value: val(t, "x")})
	failWith(t, providers, tl, hewerr.CodeInexpressible, `/provider[0]/"aws"[1]`, 0, "more than one ordinal")
}

func TestOrdinalOnANameOnlyBlock(t *testing.T) {
	src := `network {}
network {
  id = "b"
}
`
	tl := list(hew.Transform{Op: hew.OpTest, Path: p(t, "/network[1]/id"), Value: val(t, "b")})
	if _, err := Apply([]byte(src), tl); err != nil {
		t.Fatalf("ordinal on a label-less block: %v", err)
	}
}

func TestUnknownNameIsNoMatch(t *testing.T) {
	tl := list(hew.Transform{Op: hew.OpRemove, Path: p(t, `/provider/"gcp"`), PatchLine: 4})
	failWith(t, providers, tl, hewerr.CodeNoMatch, `/provider/"gcp"`, 4, "no-match", "does not exist")
}

func TestDescendingIntoAnAttributeIsNoMatch(t *testing.T) {
	src := "region = \"us-west-1\"\n"
	tl := list(hew.Transform{Op: hew.OpRemove, Path: p(t, "/region/deeper")})
	failWith(t, src, tl, hewerr.CodeNoMatch, "/region/deeper", 0, "does not exist")

	test := list(hew.Transform{Op: hew.OpTest, Path: p(t, "/region/deeper"), Value: val(t, "x")})
	failWith(t, src, test, hewerr.CodeStaleTarget, "/region/deeper", 0, "no body to descend into")
}

func TestNonHCLSegmentIsInexpressible(t *testing.T) {
	tl := list(hew.Transform{Op: hew.OpTest, Path: p(t, "/tags/0"), Value: val(t, "x")})
	failWith(t, "tags = [\"a\"]\n", tl, hewerr.CodeInexpressible, "/tags/0", 0, "no HCL representation")
}

func TestTargetThatDoesNotParse(t *testing.T) {
	tl := list(hew.Transform{Op: hew.OpTest, Path: p(t, "/a"), Value: val(t, "1")})
	failWith(t, "provider \"aws\" {\n", tl, hewerr.CodeTargetParse, "", 0, "does not parse as HCL")
}

func TestUnsupportedQualifiers(t *testing.T) {
	surface := list(hew.Transform{Op: hew.OpAdd, Path: p(t, "/a"), Value: val(t, "1"), Surface: hew.SurfaceTable})
	failWith(t, "b = 1\n", surface, hewerr.CodeInexpressible, "/a", 0, "TOML placement directive")

	anchor := list(hew.Transform{Op: hew.OpTest, Path: p(t, "/a"), Value: val(t, "1"), Anchor: hew.AnchorFork})
	failWith(t, "b = 1\n", anchor, hewerr.CodeInexpressible, "/a", 0, "YAML alias policy")
}

func TestUnsupportedOp(t *testing.T) {
	tl := list(hew.Transform{Op: hew.OpKind("frobnicate"), Path: p(t, "/a")})
	failWith(t, "a = 1\n", tl, hewerr.CodeInexpressible, "/a", 0, "unsupported op")
}

func TestOverlappingEditsConflict(t *testing.T) {
	src := `provider "aws" {
  region = "us-west-1"
}
`
	tl := list(
		hew.Transform{Op: hew.OpRemove, Path: p(t, `/provider/"aws"`)},
		hew.Transform{Op: hew.OpReplace, Path: p(t, `/provider/"aws"/region`), Value: val(t, "us-east-1")},
	)
	failWith(t, src, tl, hewerr.CodeConflict, "", 0, "overlapping regions")
}

// --- add semantics ----------------------------------------------------------

func TestAddOverAnExistingNodeIsAlreadyExists(t *testing.T) {
	tl := list(hew.Transform{Op: hew.OpAdd, Path: p(t, `/provider/"aws"[0]/region`), Value: val(t, "x"), PatchLine: 7})
	failWith(t, providers, tl, hewerr.CodeAlreadyExists, `/provider/"aws"[0]/region`, 7, "already-exists")
}

func TestAddOnConflictKeepIsZeroOps(t *testing.T) {
	got := apply(t, providers, list(hew.Transform{Op: hew.OpAdd, Path: p(t, `/provider/"aws"[0]/region`),
		Value: val(t, "x"), OnConflict: hew.ConflictKeep}))
	eq(t, got, providers)
}

func TestAddOnConflictReplaceOverwrites(t *testing.T) {
	got := apply(t, providers, list(hew.Transform{Op: hew.OpAdd, Path: p(t, `/provider/"aws"[0]/region`),
		Value: val(t, "us-west-9"), OnConflict: hew.ConflictReplace}))
	if !strings.Contains(got, `region  = "us-west-9"`) {
		t.Errorf("upsert did not write:\n%s", got)
	}
}

func TestAddIdempotentOverAnEqualNodeIsZeroOps(t *testing.T) {
	got := apply(t, providers, list(hew.Transform{Op: hew.OpAdd, Path: p(t, `/provider/"aws"[0]/region`),
		Value: val(t, "us-west-1"), Idempotent: true}))
	eq(t, got, providers)
}

func TestAddIdempotentOverADifferentValueStillFails(t *testing.T) {
	tl := list(hew.Transform{Op: hew.OpAdd, Path: p(t, `/provider/"aws"[0]/region`),
		Value: val(t, "elsewhere"), Idempotent: true})
	failWith(t, providers, tl, hewerr.CodeAlreadyExists, "", 0, "already-exists")
}

func TestAddBeforeAPlacementSibling(t *testing.T) {
	src := `provider "aws" {
  region = "us-west-1"
}
`
	got := apply(t, src, list(hew.Transform{Op: hew.OpAdd, Path: p(t, `/provider/"aws"/alias`),
		Before: p(t, `/provider/"aws"/region`), Value: val(t, "east")}))
	eq(t, got, `provider "aws" {
  alias  = "east"
  region = "us-west-1"
}
`)
}

func TestAddWithAPlacementSiblingInAnotherBody(t *testing.T) {
	src := `provider "aws" {
  region = "us-west-1"
}

provider "google" {
  project = "p"
}
`
	tl := list(hew.Transform{Op: hew.OpAdd, Path: p(t, `/provider/"aws"/alias`),
		After: p(t, `/provider/"google"/project`), Value: val(t, "east"), PatchLine: 3})
	failWith(t, src, tl, hewerr.CodeNoMatch, `/provider/"google"/project`, 3, "not a child")
}

func TestAddIntoAnEmptySingleLineBody(t *testing.T) {
	src := `provider "azurerm" {
  features {}
}
`
	got := apply(t, src, list(hew.Transform{Op: hew.OpAdd, Path: p(t, `/provider/"azurerm"/features/enabled`),
		Value: val(t, "true")}))
	eq(t, got, `provider "azurerm" {
  features {
    enabled = true
  }
}
`)
}

func TestAddIntoAnEmptyMultiLineBody(t *testing.T) {
	src := `provider "azurerm" {
  features {
  }
}
`
	got := apply(t, src, list(hew.Transform{Op: hew.OpAdd, Path: p(t, `/provider/"azurerm"/features/enabled`),
		Value: val(t, "true")}))
	eq(t, got, `provider "azurerm" {
  features {
    enabled = true
  }
}
`)
}

func TestAddAtTheRootAppendsABlock(t *testing.T) {
	src := `provider "google" {
  project = "p"
}
`
	got := apply(t, src, list(hew.Transform{Op: hew.OpAdd, Path: p(t, `/provider/"aws"`),
		Value: val(t, "region: us-west-1")}))
	eq(t, got, `provider "google" {
  project = "p"
}

provider "aws" {
  region = "us-west-1"
}
`)
}

func TestAddAnEmptyBlock(t *testing.T) {
	got := apply(t, "a = 1\n", list(hew.Transform{Op: hew.OpAdd, Path: p(t, "/features"), Value: val(t, "{}")}))
	eq(t, got, "a = 1\n\nfeatures {}\n")
}

// --- remove / replace semantics ---------------------------------------------

func TestRemoveOptionalOnAMissingNodeIsSuccess(t *testing.T) {
	got := apply(t, providers, list(hew.Transform{Op: hew.OpRemove, Path: p(t, `/provider/"gcp"`), Optional: true}))
	eq(t, got, providers)
}

func TestRemoveIdempotentOnAMissingNodeIsSuccess(t *testing.T) {
	got := apply(t, providers, list(hew.Transform{Op: hew.OpRemove, Path: p(t, `/provider/"gcp"`), Idempotent: true}))
	eq(t, got, providers)
}

func TestRemoveTheRootIsRefused(t *testing.T) {
	tl := list(hew.Transform{Op: hew.OpRemove, Path: p(t, "/")})
	failWith(t, "a = 1\n", tl, hewerr.CodeInexpressible, "/", 0, "document root")
}

func TestReplaceOnAMissingNodeSaysUseAdd(t *testing.T) {
	tl := list(hew.Transform{Op: hew.OpReplace, Path: p(t, `/provider/"aws"[0]/alias`), Value: val(t, "east")})
	failWith(t, providers, tl, hewerr.CodeNoMatch, `/provider/"aws"[0]/alias`, 0, "use add")
}

func TestReplaceWithTheValueAlreadyThereIsZeroOps(t *testing.T) {
	got := apply(t, providers, list(hew.Transform{Op: hew.OpReplace,
		Path: p(t, `/provider/"aws"[0]/region`), Value: val(t, "us-west-1")}))
	eq(t, got, providers)
}

func TestReplaceABlockRewritesItsBody(t *testing.T) {
	src := `provider "aws" {
  region = "us-west-1"
}
`
	got := apply(t, src, list(hew.Transform{Op: hew.OpReplace, Path: p(t, `/provider/"aws"`),
		Value: val(t, "alias: east\nregion: us-east-1")}))
	eq(t, got, `provider "aws" {
  alias  = "east"
  region = "us-east-1"
}
`)
}

func TestReplaceABlockWithAnEmptyBody(t *testing.T) {
	src := `provider "aws" {
  region = "us-west-1"
}
`
	got := apply(t, src, list(hew.Transform{Op: hew.OpReplace, Path: p(t, `/provider/"aws"`), Value: val(t, "{}")}))
	eq(t, got, "provider \"aws\" {}\n")
}

func TestReplaceABlockWithAScalarIsRefused(t *testing.T) {
	src := `provider "aws" {
  region = "us-west-1"
}
`
	tl := list(hew.Transform{Op: hew.OpReplace, Path: p(t, `/provider/"aws"`), Value: val(t, "nope")})
	failWith(t, src, tl, hewerr.CodeInexpressible, `/provider/"aws"`, 0, "must be a mapping")
}

// --- convergence (§7.5, §10.6) ----------------------------------------------

func TestReapplyIsRefusedWhenStrict(t *testing.T) {
	src := `provider "aws" {
  region = "us-east-1"
}
`
	tl := list(
		hew.Transform{Op: hew.OpTest, Path: p(t, `/provider/"aws"/region`), Value: val(t, "us-west-1"), PatchLine: 6},
		hew.Transform{Op: hew.OpReplace, Path: p(t, `/provider/"aws"/region`), Value: val(t, "us-east-1"), PatchLine: 7},
	)
	failWith(t, src, tl, hewerr.CodeAssertionFailed, `/provider/"aws"/region`, 6, "already applied", "idempotent")
}

func TestReapplyIsToleratedWhenIdempotent(t *testing.T) {
	src := `provider "aws" {
  region = "us-east-1"
}
`
	got := apply(t, src, list(
		hew.Transform{Op: hew.OpTest, Path: p(t, `/provider/"aws"/region`), Value: val(t, "us-west-1"), Idempotent: true},
		hew.Transform{Op: hew.OpReplace, Path: p(t, `/provider/"aws"/region`), Value: val(t, "us-east-1"), Idempotent: true},
	))
	eq(t, got, src)
}

func TestReapplyOfARemoveConverges(t *testing.T) {
	src := `provider "google" {
  project = "p"
}
`
	got := apply(t, src, list(
		hew.Transform{Op: hew.OpTest, Path: p(t, `/provider/"aws"`), Value: val(t, "region: x"), Idempotent: true},
		hew.Transform{Op: hew.OpRemove, Path: p(t, `/provider/"aws"`), Idempotent: true},
	))
	eq(t, got, src)
}

func TestReapplyOfARemoveIsRefusedWhenStrict(t *testing.T) {
	src := `provider "google" {
  project = "p"
}
`
	tl := list(
		hew.Transform{Op: hew.OpTest, Path: p(t, `/provider/"aws"`), Value: val(t, "region: x"), PatchLine: 6},
		hew.Transform{Op: hew.OpRemove, Path: p(t, `/provider/"aws"`), PatchLine: 6},
	)
	failWith(t, src, tl, hewerr.CodeAssertionFailed, `/provider/"aws"`, 6, "already applied")
}

func TestAddAfterAConvergedAssertReportsAlreadyApplied(t *testing.T) {
	src := `provider "aws" {
  region = "us-east-1"
}
`
	tl := list(
		hew.Transform{Op: hew.OpTest, Path: p(t, `/provider/"aws"/region`), Value: val(t, "gone"),
			Idempotent: true, PatchLine: 6},
		hew.Transform{Op: hew.OpAdd, Path: p(t, `/provider/"aws"/region`), Value: val(t, "us-east-1"), PatchLine: 7},
	)
	failWith(t, src, tl, hewerr.CodeAssertionFailed, `/provider/"aws"/region`, 7, "already applied")
}

func TestReplaceAfterAConvergedAssertReportsAlreadyApplied(t *testing.T) {
	src := `provider "aws" {
  region = "us-east-1"
}
`
	tl := list(
		hew.Transform{Op: hew.OpTest, Path: p(t, `/provider/"aws"/region`), Value: val(t, "gone"),
			Idempotent: true, PatchLine: 6},
		hew.Transform{Op: hew.OpReplace, Path: p(t, `/provider/"aws"/region`), Value: val(t, "us-east-1"), PatchLine: 7},
	)
	failWith(t, src, tl, hewerr.CodeAssertionFailed, `/provider/"aws"/region`, 7, "already applied")
}

// --- the other assert modes -------------------------------------------------

func TestAssertAbsent(t *testing.T) {
	got := apply(t, providers, list(hew.Transform{Op: hew.OpTest, Path: p(t, `/provider/"gcp"`), Absent: true}))
	eq(t, got, providers)

	tl := list(hew.Transform{Op: hew.OpTest, Path: p(t, `/provider/"aws"[0]`), Absent: true, PatchLine: 6})
	failWith(t, providers, tl, hewerr.CodeAssertionFailed, `/provider/"aws"[0]`, 6, "expected absent")
}

func TestAssertAbsentPropagatesAFinalError(t *testing.T) {
	tl := list(hew.Transform{Op: hew.OpTest, Path: p(t, `/provider/"aws"`), Absent: true, PatchLine: 6})
	failWith(t, providers, tl, hewerr.CodeAmbiguousMatch, `/provider/"aws"`, 6, "ambiguous-match")
}

func TestAssertCount(t *testing.T) {
	two := 2
	got := apply(t, providers, list(hew.Transform{Op: hew.OpTest, Path: p(t, `/provider/"aws"[0]`), Count: &two}))
	eq(t, got, providers)

	one := 1
	tl := list(hew.Transform{Op: hew.OpTest, Path: p(t, `/provider/"aws"[0]`), Count: &one, PatchLine: 6})
	he := failWith(t, providers, tl, hewerr.CodeAssertionFailed, `/provider/"aws"[0]`, 6, "count")
	if he.Want != "1" || he.Got != "2" {
		t.Errorf("want/got: %q / %q", he.Want, he.Got)
	}
}

func TestAssertCountOverASequenceAndOverAScalar(t *testing.T) {
	src := "tags = [\"a\", \"b\", \"c\"]\nname = \"x\"\n"
	three := 3
	got := apply(t, src, list(hew.Transform{Op: hew.OpTest, Path: p(t, "/tags"), Count: &three}))
	eq(t, got, src)

	tl := list(hew.Transform{Op: hew.OpTest, Path: p(t, "/name"), Count: &three, PatchLine: 4})
	failWith(t, src, tl, hewerr.CodeAssertionFailed, "/name", 4, "not a container")
}

func TestAssertExhaustive(t *testing.T) {
	two := 2
	tl := list(hew.Transform{Op: hew.OpTest, Path: p(t, `/provider/"aws"[1]`), Count: &two, Exhaustive: true})
	if _, err := Apply([]byte(providers), tl); err != nil {
		t.Fatalf("exhaustive over a 2-member body: %v", err)
	}
	one := 1
	bad := list(hew.Transform{Op: hew.OpTest, Path: p(t, `/provider/"aws"[1]`), Count: &one, Exhaustive: true, PatchLine: 9})
	failWith(t, providers, bad, hewerr.CodeAssertionFailed, `/provider/"aws"[1]`, 9, "exhaustive")
}

func TestAssertKind(t *testing.T) {
	src := `provider "aws" {
  tags   = ["a"]
  meta   = { x = 1 }
  region = "us-west-1"
}
`
	for _, tc := range []struct {
		path string
		kind hew.NodeKind
	}{
		{`/provider/"aws"`, hew.KindBlock},
		{`/provider/"aws"/tags`, hew.KindSeq},
		{`/provider/"aws"/meta`, hew.KindMap},
		{`/provider/"aws"/region`, hew.KindScalar},
		{`/`, hew.KindMap},
	} {
		kind := tc.kind
		tl := list(hew.Transform{Op: hew.OpTest, Path: p(t, tc.path), NodeKind: &kind})
		if _, err := Apply([]byte(src), tl); err != nil {
			t.Errorf("kind %s at %s: %v", kind, tc.path, err)
		}
	}
	scalar := hew.KindScalar
	tl := list(hew.Transform{Op: hew.OpTest, Path: p(t, `/provider/"aws"/tags`), NodeKind: &scalar, PatchLine: 6})
	he := failWith(t, src, tl, hewerr.CodeAssertionFailed, `/provider/"aws"/tags`, 6, "kind")
	if he.Want != "scalar" || he.Got != "seq" {
		t.Errorf("want/got: %q / %q", he.Want, he.Got)
	}
}

func TestOptionalTestOnAMissingNodeIsSatisfied(t *testing.T) {
	got := apply(t, providers, list(hew.Transform{Op: hew.OpTest,
		Path: p(t, `/provider/"aws"[0]/alias`), Value: val(t, "east"), Optional: true}))
	eq(t, got, providers)
}

func TestTestOverTheDocumentRoot(t *testing.T) {
	src := "a = 1\nb = \"two\"\n"
	got := apply(t, src, list(hew.Transform{Op: hew.OpTest, Path: p(t, "/"), Value: val(t, "{a: 1, b: two}")}))
	eq(t, got, src)
}

func TestBodyWithRepeatedNamesHasNoValueForm(t *testing.T) {
	src := `provider "aws" {}
provider "google" {}
`
	tl := list(hew.Transform{Op: hew.OpTest, Path: p(t, "/"), Value: val(t, "{}"), PatchLine: 3})
	failWith(t, src, tl, hewerr.CodeInexpressible, "/", 3, "no value form")
}

// --- copy edges -------------------------------------------------------------

func TestCopyAnAttribute(t *testing.T) {
	src := `provider "aws" {
  region = "us-west-1"
}
`
	got := apply(t, src, list(hew.Transform{Op: hew.OpCopy,
		From: p(t, `/provider/"aws"/region`), Path: p(t, `/provider/"aws"/backup_region`)}))
	eq(t, got, `provider "aws" {
  region        = "us-west-1"
  backup_region = "us-west-1"
}
`)
}

func TestCopyOntoAnExistingNode(t *testing.T) {
	tl := list(hew.Transform{Op: hew.OpCopy, From: p(t, `/provider/"aws"[0]`),
		Path: p(t, `/provider/"aws"[1]`), PatchLine: 4})
	failWith(t, providers, tl, hewerr.CodeAlreadyExists, `/provider/"aws"[1]`, 4, "already-exists")
}

func TestCopyFromAMissingSource(t *testing.T) {
	tl := list(hew.Transform{Op: hew.OpCopy, From: p(t, `/provider/"gcp"`), Path: p(t, `/provider/"new"`)})
	failWith(t, providers, tl, hewerr.CodeNoMatch, `/provider/"gcp"`, 0, "no-match")
}

func TestCopyFromTheRootIsRefused(t *testing.T) {
	tl := list(hew.Transform{Op: hew.OpCopy, From: p(t, "/"), Path: p(t, "/copy")})
	failWith(t, "a = 1\n", tl, hewerr.CodeInexpressible, "/", 0, "document root")
}

func TestCopyAnAttributeToALabelledDestinationIsRefused(t *testing.T) {
	src := `provider "aws" {
  region = "us-west-1"
}
`
	tl := list(hew.Transform{Op: hew.OpCopy, From: p(t, `/provider/"aws"/region`), Path: p(t, `/thing/"x"`)})
	failWith(t, src, tl, hewerr.CodeInexpressible, `/thing/"x"`, 0, "block labels")
}

func TestCopyReindentsIntoADeeperBody(t *testing.T) {
	src := `outer {
  inner {
    a = 1
  }
}
`
	got := apply(t, src, list(hew.Transform{Op: hew.OpCopy,
		From: p(t, "/outer/inner"), Path: p(t, "/outer/inner/nested")}))
	eq(t, got, `outer {
  inner {
    a = 1
    nested {
      a = 1
    }
  }
}
`)
}

// --- values (§8.5's source-text rule) ---------------------------------------

func TestExpressionsCompareAsSourceText(t *testing.T) {
	src := `x {
  interp    = "${var.x}"
  traversal = var.x
  call      = max(1, 2)
  wrapped   = "pre-${var.x}-post"
  n         = 8080
  f         = 1.5
  b         = true
  nul       = null
  list      = ["a", "b"]
  obj       = { a = 1, "b c" = 2 }
  spaced    = var . x
}
`
	for _, tc := range []struct{ path, value string }{
		{"/x/interp", "${var.x}"},
		{"/x/traversal", "var.x"},
		{"/x/call", "max(1, 2)"},
		{"/x/wrapped", "pre-${var.x}-post"},
		{"/x/n", "8080"},
		{"/x/f", "1.5"},
		{"/x/b", "true"},
		{"/x/nul", "null"},
		{"/x/list", "[a, b]"},
		{"/x/obj", "{a: 1, b c: 2}"},
		{"/x/spaced", "var . x"},
	} {
		tl := list(hew.Transform{Op: hew.OpTest, Path: p(t, tc.path), Value: val(t, tc.value)})
		if _, err := Apply([]byte(src), tl); err != nil {
			t.Errorf("%s == %s: %v", tc.path, tc.value, err)
		}
	}
	// The whole point: hew does not evaluate HCL.
	tl := list(hew.Transform{Op: hew.OpTest, Path: p(t, "/x/interp"), Value: val(t, "var.x")})
	failWith(t, src, tl, hewerr.CodeStaleTarget, "/x/interp", 0, "stale-target")
}

func TestWritingValuesBackAsHCL(t *testing.T) {
	for _, tc := range []struct{ value, want string }{
		{"plain", `"plain"`},
		{"8080", "8080"},
		{"1.5", "1.5"},
		{"true", "false"},
		{"~", "null"},
		{`'a "quoted" \ one'`, `"a \"quoted\" \\ one"`},
		{`"tab\ttext"`, `"tab\ttext"`},
		{"[a, 2]", `["a", 2]`},
		{"${var.x}", `"${var.x}"`},
	} {
		v := tc.value
		want := tc.want
		if v == "true" {
			v, want = "false", "false"
		}
		got := apply(t, "x = 0\n", list(hew.Transform{Op: hew.OpReplace, Path: p(t, "/x"), Value: val(t, v)}))
		eq(t, got, "x = "+want+"\n")
	}
}

func TestWritingANestedObjectValue(t *testing.T) {
	got := apply(t, "x = 0\n", list(hew.Transform{Op: hew.OpReplace, Path: p(t, "/x"),
		Value: val(t, "a:\n  bb: 1\n  c: 2")}))
	eq(t, got, `x = {
  a = {
    bb = 1
    c  = 2
  }
}
`)
}

func TestWritingAnEmptyObjectValue(t *testing.T) {
	got := apply(t, "x = 0\ny = 1\n", list(hew.Transform{Op: hew.OpReplace, Path: p(t, "/x"), Value: val(t, "{}")}))
	eq(t, got, "x = {}\ny = 1\n")
}

func TestQuotedObjectKeysDecode(t *testing.T) {
	src := "x = { \"a b\" = 1 }\n"
	tl := list(hew.Transform{Op: hew.OpTest, Path: p(t, "/x"), Value: val(t, "{\"a b\": 1}")})
	if _, err := Apply([]byte(src), tl); err != nil {
		t.Fatalf("quoted object key: %v", err)
	}
}

// --- alignment (§8.5) -------------------------------------------------------

func TestUntouchedBodiesStayByteIdenticalIncludingTheirMisalignment(t *testing.T) {
	src := `terraform {
  required_version     = ">= 1.6"
  experiments= []
}

provider "aws" {
  region = "us-west-1"
}
`
	got := apply(t, src, list(hew.Transform{Op: hew.OpReplace,
		Path: p(t, `/provider/"aws"/region`), Value: val(t, "us-east-1")}))
	eq(t, got, `terraform {
  required_version     = ">= 1.6"
  experiments= []
}

provider "aws" {
  region = "us-east-1"
}
`)
}

func TestAModifiedBodyAdoptsTheBackendAlignment(t *testing.T) {
	src := `x {
  a     = 1
  bbbbb = 2
}
`
	got := apply(t, src, list(hew.Transform{Op: hew.OpRemove, Path: p(t, "/x/bbbbb")}))
	eq(t, got, `x {
  a = 1
}
`)
}

func TestAlignmentGroupsBreakAtNonAttributeLines(t *testing.T) {
	src := `x {
  a = 1
  inner {
    z = 1
  }
  bbbbb = 2

  c = 3
  new_thing = 4
}
`
	got := apply(t, src, list(hew.Transform{Op: hew.OpReplace, Path: p(t, "/x/a"), Value: val(t, "9")}))
	eq(t, got, `x {
  a = 9
  inner {
    z = 1
  }
  bbbbb = 2

  c         = 3
  new_thing = 4
}
`)
}

func TestAlignmentLeavesNonSpaceGapsAlone(t *testing.T) {
	src := "x {\n  a /* mid */ = 1\n  bbb = 2\n}\n"
	got := apply(t, src, list(hew.Transform{Op: hew.OpReplace, Path: p(t, "/x/bbb"), Value: val(t, "9")}))
	eq(t, got, "x {\n  a /* mid */ = 1\n  bbb = 9\n}\n")
}

// --- small units ------------------------------------------------------------

func TestShiftIndent(t *testing.T) {
	eq(t, shiftIndent("\n  a\n", 0), "\n  a\n")
	eq(t, shiftIndent("\n  a\n", 2), "\n    a\n")
	eq(t, shiftIndent("\n  a\n  ", 2), "\n    a\n    ")
	eq(t, shiftIndent("\n    a\n  ", -2), "\n  a\n")
	eq(t, shiftIndent("\n  a\n", -8), "\na\n")
}

func TestAppendBlankSeparator(t *testing.T) {
	eq(t, string(appendBlankSeparator(nil)), "")
	eq(t, string(appendBlankSeparator([]byte("a"))), "a\n\n")
	eq(t, string(appendBlankSeparator([]byte("a\n"))), "a\n\n")
	eq(t, string(appendBlankSeparator([]byte("a\n\n"))), "a\n\n")
}

func TestOrdinalWord(t *testing.T) {
	for i, want := range []string{"1st", "2nd", "3rd", "4th", "5th"} {
		if got := ordinalWord(i); got != want {
			t.Errorf("ordinalWord(%d) = %s, want %s", i, got, want)
		}
	}
}

func TestTupleString(t *testing.T) {
	eq(t, tupleString("provider", nil), "provider")
	eq(t, tupleString("provider", []string{"aws", "x"}), `provider "aws" "x"`)
}

func TestIsBlank(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{{"", false}, {" ", true}, {"\n", true}, {" \t\r\n", true}, {" a ", false}} {
		if got := isBlank([]byte(tc.in)); got != tc.want {
			t.Errorf("isBlank(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestShiftMapsOffsetsThroughSplices(t *testing.T) {
	marks := []mark{{src: 0, delta: 0}, {src: 10, delta: 5}, {src: 20, delta: -2}}
	for _, tc := range []struct{ off, want int }{{0, 0}, {9, 9}, {10, 15}, {19, 24}, {20, 18}, {30, 28}} {
		if got := shift(marks, tc.off); got != tc.want {
			t.Errorf("shift(%d) = %d, want %d", tc.off, got, tc.want)
		}
	}
}

func TestNoTransformsLeavesTheTargetAlone(t *testing.T) {
	eq(t, apply(t, providers, list()), providers)
}

func TestFileWithoutATrailingNewline(t *testing.T) {
	got := apply(t, "a = 1", list(hew.Transform{Op: hew.OpReplace, Path: p(t, "/a"), Value: val(t, "2")}))
	eq(t, got, "a = 2")
}
