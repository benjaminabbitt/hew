package harness

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// withBuilder registers a temporary fixture builder so BuildFixture's dispatch
// can be exercised without shelling out.
func withBuilder(t *testing.T, token string, fn func(dir string) error) {
	t.Helper()
	if _, exists := fixtureBuilders[token]; exists {
		t.Fatalf("%q already registered; pick another token", token)
	}
	fixtureBuilders[token] = fn
	t.Cleanup(func() { delete(fixtureBuilders, token) })
}

func TestBuildFixtureNoRequires(t *testing.T) {
	dir := t.TempDir()
	if err := BuildFixture(&Manifest{}, dir); err != nil {
		t.Fatalf("BuildFixture with no requires: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("a case with no requires must not have its scratch dir touched: %v", entries)
	}
}

func TestBuildFixtureUnknownRequires(t *testing.T) {
	err := BuildFixture(&Manifest{Requires: "docker-compose"}, t.TempDir())
	if err == nil {
		t.Fatal("an unknown requires token must fail")
	}
	if !strings.Contains(err.Error(), "corpus error") || !strings.Contains(err.Error(), "docker-compose") {
		t.Errorf("error %q must name the unknown token as a corpus error", err)
	}
}

func TestBuildFixtureDispatchesToBuilder(t *testing.T) {
	var gotDir string
	withBuilder(t, "test-fixture", func(dir string) error {
		gotDir = dir
		return os.WriteFile(filepath.Join(dir, "built.txt"), []byte("yes\n"), 0o644)
	})
	dir := t.TempDir()
	if err := BuildFixture(&Manifest{Requires: "test-fixture"}, dir); err != nil {
		t.Fatalf("BuildFixture: %v", err)
	}
	if gotDir != dir {
		t.Errorf("builder ran in %q, want the scratch dir %q", gotDir, dir)
	}
	if _, err := os.Stat(filepath.Join(dir, "built.txt")); err != nil {
		t.Errorf("builder side effect missing: %v", err)
	}
}

func TestBuildFixturePropagatesBuilderError(t *testing.T) {
	sentinel := errors.New("builder exploded")
	withBuilder(t, "test-fixture-fail", func(string) error { return sentinel })
	err := BuildFixture(&Manifest{Requires: "test-fixture-fail"}, t.TempDir())
	if !errors.Is(err, sentinel) {
		t.Errorf("BuildFixture = %v, want the builder's own error", err)
	}
}

func TestFixtureBuildersRegistry(t *testing.T) {
	if _, ok := fixtureBuilders["git-fixture"]; !ok {
		t.Error("git-fixture must stay registered: manifests declare it")
	}
	if len(fixtureBuilders) != 1 {
		t.Errorf("fixtureBuilders = %d entries; a new builder needs a Validate test too", len(fixtureBuilders))
	}
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		// Infrastructure, not corpus semantics: the harness treats missing git
		// as a hard failure at run time, but the unit test has nothing to say.
		t.Skip("git not on PATH")
	}
}

// TestBuildGitFixture builds a real repository in t.TempDir and asserts the
// two states cli/diff-git-anchor depends on: the worktree holds worktree.yaml
// and HEAD holds committed.yaml.
func TestBuildGitFixture(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	committed := "server:\n  timeout: 30\n"
	worktree := "server:\n  timeout: 45\n"
	if err := os.WriteFile(filepath.Join(dir, "committed.yaml"), []byte(committed), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "worktree.yaml"), []byte(worktree), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := BuildFixture(&Manifest{Requires: "git-fixture"}, dir); err != nil {
		t.Fatalf("BuildFixture: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatalf("config.yaml: %v", err)
	}
	if string(got) != worktree {
		t.Errorf("config.yaml = %q, want the worktree.yaml bytes %q", got, worktree)
	}

	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Fatalf("no repository was created: %v", err)
	}

	show := exec.Command("git", "show", "HEAD:config.yaml")
	show.Dir = dir
	show.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull)
	out, err := show.Output()
	if err != nil {
		t.Fatalf("git show: %v", err)
	}
	if string(out) != committed {
		t.Errorf("git show HEAD:config.yaml = %q, want the committed.yaml bytes %q", out, committed)
	}
}

// TestBuildGitFixtureIdentityIsPinned: the commit must not pick up host git
// identity, or the fixture is not hermetic.
func TestBuildGitFixtureIdentityIsPinned(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	for name, content := range map[string]string{"committed.yaml": "a: 1\n", "worktree.yaml": "a: 2\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := buildGitFixture(dir); err != nil {
		t.Fatalf("buildGitFixture: %v", err)
	}
	log := exec.Command("git", "log", "-1", "--format=%an <%ae> %cn <%ce> %s")
	log.Dir = dir
	log.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull)
	out, err := log.Output()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	got := strings.TrimSpace(string(out))
	want := "hew-corpus <corpus@hew.invalid> hew-corpus <corpus@hew.invalid> base"
	if got != want {
		t.Errorf("commit identity = %q, want %q", got, want)
	}
}

func TestBuildGitFixtureMissingInputs(t *testing.T) {
	requireGit(t)
	tests := []struct {
		name  string
		files map[string]string
	}{
		{name: "no committed.yaml", files: map[string]string{"worktree.yaml": "a: 2\n"}},
		{name: "no worktree.yaml", files: map[string]string{"committed.yaml": "a: 1\n"}},
		{name: "neither", files: map[string]string{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, content := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := buildGitFixture(dir); err == nil {
				t.Fatal("a missing fixture input must fail the build, not build half a repo")
			}
		})
	}
}

// TestBuildGitFixtureGitFailureIsReported: git must fail loudly, carrying its
// own output, when the directory cannot host a repository.
func TestBuildGitFixtureGitFailureIsReported(t *testing.T) {
	requireGit(t)
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	for name, content := range map[string]string{"committed.yaml": "a: 1\n", "worktree.yaml": "a: 2\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// config.yaml is written first; make the repo un-initializable afterwards.
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("a: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, ".git"), 0o755) })
	err := buildGitFixture(dir)
	if err == nil {
		t.Fatal("git init into an unusable .git must fail")
	}
	if !strings.Contains(err.Error(), "git [") {
		t.Errorf("error %q must name the git invocation", err)
	}
}
