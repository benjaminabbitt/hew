package harness

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// fixtureBuilders maps a manifest `requires:` token to a coded builder run in
// the scratch copy before the seam. The manifest's free-text `fixture:` field
// is documentation for humans — it is NEVER executed as shell (the corpus is
// remote-shared data; shelling its text would be an injection surface).
var fixtureBuilders = map[string]func(dir string) error{
	"git-fixture": buildGitFixture,
}

// BuildFixture runs the coded builder for a case's `requires:` token, if any.
// An unknown token was already rejected at Validate time; git being absent
// from PATH is an infrastructure failure, not a skip.
func BuildFixture(m *Manifest, dir string) error {
	if m.Requires == "" {
		return nil
	}
	build, ok := fixtureBuilders[m.Requires]
	if !ok {
		return fmt.Errorf("corpus error: unknown requires %q", m.Requires)
	}
	return build(dir)
}

// buildGitFixture reproduces cli/diff-git-anchor's documented setup: commit
// committed.yaml as config.yaml, then overwrite the working tree copy with
// worktree.yaml. Identity is pinned and host git config is masked so the
// fixture is hermetic on any machine.
func buildGitFixture(dir string) error {
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("infrastructure: git required by git-fixture cases but not on PATH: %w", err)
	}
	cp := func(from, to string) error {
		b, err := os.ReadFile(filepath.Join(dir, from))
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, to), b, 0o644)
	}
	git := func(args ...string) error {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL="+os.DevNull,
			"GIT_CONFIG_SYSTEM="+os.DevNull,
			"GIT_AUTHOR_NAME=hew-corpus", "GIT_AUTHOR_EMAIL=corpus@hew.invalid",
			"GIT_COMMITTER_NAME=hew-corpus", "GIT_COMMITTER_EMAIL=corpus@hew.invalid",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %v: %w: %s", args, err, out)
		}
		return nil
	}
	if err := cp("committed.yaml", "config.yaml"); err != nil {
		return err
	}
	if err := git("init", "-q", "."); err != nil {
		return err
	}
	if err := git("add", "config.yaml"); err != nil {
		return err
	}
	if err := git("commit", "-q", "-m", "base"); err != nil {
		return err
	}
	return cp("worktree.yaml", "config.yaml")
}
