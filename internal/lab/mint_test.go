package lab

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// writeComposer writes a minimal composer.json into dir, optionally with a
// "version" field ("" omits it entirely).
func writeComposer(t *testing.T, dir, version string) {
	t.Helper()
	m := map[string]any{"name": "jalendport/craft-example"}
	if version != "" {
		m["version"] = version
	}
	if err := writeJSON(filepath.Join(dir, "composer.json"), m); err != nil {
		t.Fatal(err)
	}
}

func TestPluginVersionPrefersComposerJSON(t *testing.T) {
	dir := t.TempDir()
	writeComposer(t, dir, "1.0.0")

	if got := pluginVersion(dir); got != "1.0.0" {
		t.Errorf("pluginVersion() = %q, want %q", got, "1.0.0")
	}
}

func TestPluginVersionIgnoresInvalidComposerVersion(t *testing.T) {
	dir := t.TempDir()
	writeComposer(t, dir, "not-a-version")

	// No git repo either, so this should fall through to the 0.1.0 stub rather
	// than the invalid literal from composer.json.
	if got := pluginVersion(dir); got != "0.1.0" {
		t.Errorf("pluginVersion() = %q, want fallback %q", got, "0.1.0")
	}
}

func TestPluginVersionFallsBackToGitTagWhenComposerHasNone(t *testing.T) {
	dir := t.TempDir()
	writeComposer(t, dir, "")
	initGitRepoWithTag(t, dir, "v2.3.1")

	if got := pluginVersion(dir); got != "2.3.1" {
		t.Errorf("pluginVersion() = %q, want %q", got, "2.3.1")
	}
}

func TestPluginVersionFallsBackToStubWhenNothingResolves(t *testing.T) {
	dir := t.TempDir()
	writeComposer(t, dir, "")

	if got := pluginVersion(dir); got != "0.1.0" {
		t.Errorf("pluginVersion() = %q, want %q", got, "0.1.0")
	}
}

// initGitRepoWithTag makes dir a git repo with one commit tagged tag, so
// latestGitTag has something to find.
func initGitRepoWithTag(t *testing.T, dir, tag string) {
	t.Helper()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=spark-cli tests",
			"GIT_AUTHOR_EMAIL=tests@example.com",
			"GIT_COMMITTER_NAME=spark-cli tests",
			"GIT_COMMITTER_EMAIL=tests@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("commit", "--allow-empty", "-q", "-m", "init")
	run("tag", tag)
}
