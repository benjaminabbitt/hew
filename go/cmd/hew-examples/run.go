package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// execution is one scenario played out: everything the renderer needs, and
// nothing that came from the machine it ran on.
type execution struct {
	Scenario *scenario
	// Start holds the fixture files as they were before the first step, keyed
	// by the path the scenario named.
	Start map[string]string
	Steps []stepResult
}

// stepResult is one step's real, captured outcome.
type stepResult struct {
	Step step
	// Cmds holds every command the step ran, in order — git scaffolding
	// first, then the hew invocation the step is about.
	Cmds []cmdResult
	// Writes mirrors Step.Write with the content actually written.
	Writes []write
	// After holds the post-step content of every Watch and Show file; Before
	// holds the Watch files' content as of the step's start.
	Before map[string]string
	After  map[string]string
}

// cmdResult is one executed command.
type cmdResult struct {
	// Argv is the command as displayed, argv0 included.
	Argv     []string
	Stdout   string
	Stderr   string
	ExitCode int
}

// subject returns the command whose exit code the page reports: the last one,
// which is the hew invocation whenever the step has one.
func (r stepResult) subject() (cmdResult, bool) {
	if len(r.Cmds) == 0 {
		return cmdResult{}, false
	}
	return r.Cmds[len(r.Cmds)-1], true
}

// appliedAt matches the RFC 3339 stamp §9.7's application record carries. It
// is the only value hew emits that a second run cannot reproduce, so the
// generator replaces it rather than letting a wall clock into a committed-by-CI
// comparison. Nothing else is rewritten: every other byte in a transcript is
// what the CLI actually printed.
var appliedAt = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?Z`)

const appliedAtPlaceholder = "2026-01-01T00:00:00Z"

func redact(s string) string {
	return appliedAt.ReplaceAllString(s, appliedAtPlaceholder)
}

// runScenario copies the scenario's fixtures into a scratch directory and
// executes its steps there. Every command runs with the scratch directory as
// its working directory and is given relative paths only, so no temporary path
// can reach the transcript.
func runScenario(sc *scenario, hewBin string) (*execution, error) {
	scratch, err := os.MkdirTemp("", "hew-example-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(scratch)

	if err := copyFixtures(sc.Dir, scratch); err != nil {
		return nil, err
	}

	ex := &execution{Scenario: sc, Start: map[string]string{}}
	for _, name := range sc.Show {
		body, err := os.ReadFile(filepath.Join(scratch, name))
		if err != nil {
			return nil, fmt.Errorf("%s: show: %w", sc.Name, err)
		}
		ex.Start[name] = string(body)
	}

	for i, st := range sc.Steps {
		res, err := runStep(st, scratch, hewBin)
		if err != nil {
			return nil, fmt.Errorf("%s: step %d: %w", sc.Name, i+1, err)
		}
		ex.Steps = append(ex.Steps, *res)
	}
	return ex, nil
}

func runStep(st step, scratch, hewBin string) (*stepResult, error) {
	res := &stepResult{
		Step:   st,
		Before: map[string]string{},
		After:  map[string]string{},
	}

	// Snapshot before the writes, not after: a step that rewrites a file and
	// then patches it should show the whole move, not just the patch's half.
	for _, name := range append(append([]string{}, st.Watch...), st.Unchanged...) {
		body, err := os.ReadFile(filepath.Join(scratch, name))
		if err == nil {
			res.Before[name] = string(body)
		}
	}

	for _, w := range st.Write {
		if err := os.WriteFile(filepath.Join(scratch, w.Path), []byte(w.Content), 0o644); err != nil {
			return nil, err
		}
		res.Writes = append(res.Writes, w)
	}

	for _, argv := range st.Git {
		c := cmdResult{Argv: append([]string{"git"}, argv...)}
		c.Stdout, c.Stderr, c.ExitCode = capture("git", argv, scratch, gitEnv())
		if c.ExitCode != 0 {
			return nil, fmt.Errorf("%s: exit %d\n%s%s", strings.Join(c.Argv, " "), c.ExitCode, c.Stdout, c.Stderr)
		}
		res.Cmds = append(res.Cmds, c)
	}
	if len(st.Run) > 0 {
		c := cmdResult{Argv: append([]string{"hew"}, st.Run...)}
		c.Stdout, c.Stderr, c.ExitCode = capture(hewBin, st.Run, scratch, nil)
		if c.ExitCode != st.ExpectExit {
			return nil, fmt.Errorf("%s: expected exit %d, got %d\nstdout:\n%s\nstderr:\n%s",
				strings.Join(c.Argv, " "), st.ExpectExit, c.ExitCode, c.Stdout, c.Stderr)
		}
		res.Cmds = append(res.Cmds, c)
	}
	for i := range res.Cmds {
		res.Cmds[i].Stdout = redact(res.Cmds[i].Stdout)
		res.Cmds[i].Stderr = redact(res.Cmds[i].Stderr)
	}

	for _, pair := range st.Identical {
		parts := strings.SplitN(pair, " == ", 2)
		left, lerr := os.ReadFile(filepath.Join(scratch, parts[0]))
		right, rerr := os.ReadFile(filepath.Join(scratch, parts[1]))
		if lerr != nil || rerr != nil {
			return nil, fmt.Errorf("identical %q: %v %v", pair, lerr, rerr)
		}
		if !bytes.Equal(left, right) {
			return nil, fmt.Errorf("identical %q: the two files differ", pair)
		}
	}

	for _, name := range append(append([]string{}, st.Watch...), st.Show...) {
		body, err := os.ReadFile(filepath.Join(scratch, name))
		if err != nil {
			return nil, fmt.Errorf("after the step, reading %s: %w", name, err)
		}
		res.After[name] = redact(string(body))
	}
	for _, name := range st.Unchanged {
		body, err := os.ReadFile(filepath.Join(scratch, name))
		if err != nil {
			return nil, fmt.Errorf("unchanged %s: %w", name, err)
		}
		if before, ok := res.Before[name]; ok && before != string(body) {
			return nil, fmt.Errorf("%s: declared unchanged but the step modified it", name)
		}
		res.After[name] = string(body)
	}
	return res, nil
}

func capture(bin string, args []string, dir string, extraEnv []string) (stdout, stderr string, code int) {
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	if extraEnv != nil {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code = 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		return out.String(), errb.String() + err.Error() + "\n", -1
	}
	return out.String(), errb.String(), code
}

// gitEnv pins git's identity, clock and configuration. A scenario's repository
// is scaffolding, and scaffolding built from the developer's ~/.gitconfig (or
// today's date) is how a transcript stops being reproducible.
func gitEnv() []string {
	return []string{
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=hew examples",
		"GIT_AUTHOR_EMAIL=examples@hew.invalid",
		"GIT_COMMITTER_NAME=hew examples",
		"GIT_COMMITTER_EMAIL=examples@hew.invalid",
		"GIT_AUTHOR_DATE=2026-01-01T00:00:00+00:00",
		"GIT_COMMITTER_DATE=2026-01-01T00:00:00+00:00",
	}
}

// copyFixtures copies everything in the scenario directory except its manifest
// into dst, in sorted order.
func copyFixtures(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Name() == "scenario.yaml" {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		from, to := filepath.Join(src, name), filepath.Join(dst, name)
		info, err := os.Stat(from)
		if err != nil {
			return err
		}
		if info.IsDir() {
			if err := os.MkdirAll(to, 0o755); err != nil {
				return err
			}
			if err := copyFixtures(from, to); err != nil {
				return err
			}
			continue
		}
		body, err := os.ReadFile(from)
		if err != nil {
			return err
		}
		if err := os.WriteFile(to, body, 0o644); err != nil {
			return err
		}
	}
	return nil
}
