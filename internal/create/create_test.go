package create

import (
	"bufio"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jalendport/spark-cli/internal/manifest"
	"github.com/jalendport/spark-cli/internal/proc"
)

func TestBuildIntoSuccess(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "site")
	err := buildInto(dst, func(dir string) error {
		return os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hi"), 0o644)
	})
	if err != nil {
		t.Fatalf("buildInto: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dst, "hello.txt")); string(got) != "hi" {
		t.Errorf("built file = %q", got)
	}
}

func TestBuildIntoFailureLeavesNothing(t *testing.T) {
	parent := t.TempDir()
	dst := filepath.Join(parent, "site")
	err := buildInto(dst, func(dir string) error {
		// Write partial output, then fail mid-flow.
		if err := os.WriteFile(filepath.Join(dir, "partial.txt"), []byte("x"), 0o644); err != nil {
			return err
		}
		return errors.New("boom")
	})
	if err == nil {
		t.Fatal("expected buildInto to surface the mid-flow failure")
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Error("destination should not exist after a failed build")
	}
	// No temporary build directory should be left behind either.
	entries, _ := os.ReadDir(parent)
	if len(entries) != 0 {
		t.Errorf("stray entries left behind: %v", entries)
	}
}

func TestBuildIntoPreExistingEmptyDirSurvivesFailure(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "site")
	if err := os.Mkdir(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	err := buildInto(dst, func(dir string) error { return errors.New("boom") })
	if err == nil {
		t.Fatal("expected buildInto to surface the failure")
	}
	if _, err := os.Stat(dst); err != nil {
		t.Errorf("pre-existing empty destination did not survive: %v", err)
	}
}

func TestBuildIntoRejectsNonEmptyDir(t *testing.T) {
	dst := t.TempDir()
	writeFileT(t, filepath.Join(dst, "keep.txt"), "x")
	called := false
	err := buildInto(dst, func(string) error { called = true; return nil })
	if err == nil {
		t.Fatal("expected a non-empty destination to be rejected")
	}
	if called {
		t.Error("fn should not run when the destination is non-empty")
	}
}

func TestParseConfirm(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", true}, // a blank line defaults to yes
		{"y", true},
		{"Y", true},
		{"yes", true},
		{"YES", true},
		{"  y  ", true},
		{"n", false},
		{"no", false},
		{"nope", false},
		{"x", false},
	}
	for _, c := range cases {
		if got := parseConfirm(c.in); got != c.want {
			t.Errorf("parseConfirm(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestConfirm(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		// A bare newline is a deliberate blank answer, which defaults to yes.
		{"blank line yes default", "\n", true},
		{"explicit yes", "y\n", true},
		{"explicit no", "n\n", false},
		// Stdin that runs out with nothing typed is a decline, never an error.
		{"eof declines", "", false},
	}
	for _, c := range cases {
		rd := bufio.NewReader(strings.NewReader(c.in))
		if got := confirm(rd, io.Discard, "Set up?"); got != c.want {
			t.Errorf("%s: confirm(%q) = %v, want %v", c.name, c.in, got, c.want)
		}
	}
}

func TestNextSteps(t *testing.T) {
	dir := "/tmp/site"
	cases := []struct {
		name string
		o    setupOutcome
		want []string
	}{
		{
			// No setup section: cd then spark up (nothing has started the stack).
			name: "no setup section",
			o:    setupOutcome{hasSetup: false},
			want: []string{"cd /tmp/site", "spark up"},
		},
		{
			// Setup fully succeeded: just cd — the stack is already running.
			name: "setup succeeded",
			o:    setupOutcome{hasSetup: true, remaining: nil},
			want: []string{"cd /tmp/site"},
		},
		{
			// Declined: every setup command becomes a manual next step.
			name: "setup declined",
			o:    setupOutcome{hasSetup: true, remaining: []string{"docker compose up -d", "spark composer install"}},
			want: []string{"cd /tmp/site", "docker compose up -d", "spark composer install"},
		},
		{
			// Partially failed: only the failing command and everything after it.
			name: "setup partially failed",
			o:    setupOutcome{hasSetup: true, remaining: []string{"spark composer install"}},
			want: []string{"cd /tmp/site", "spark composer install"},
		},
	}
	for _, c := range cases {
		got := nextSteps(dir, c.o)
		if len(got) != len(c.want) {
			t.Errorf("%s: nextSteps = %v, want %v", c.name, got, c.want)
			continue
		}
		for i := range c.want {
			if got[i] != c.want[i] {
				t.Errorf("%s: nextSteps[%d] = %q, want %q", c.name, i, got[i], c.want[i])
			}
		}
	}
}

func TestNextStepsExpandsDeclinedSetup(t *testing.T) {
	answers := map[string]string{"project_name": "My Cool Site"}
	setup := expandCommands([]string{
		`docker compose exec -e SITE_NAME="{project_name}" php composer craft-setup`,
		"spark composer install {project_name:slug}",
	}, answers)
	got := nextSteps("my-cool-site", setupOutcome{hasSetup: true, remaining: setup})
	want := []string{
		"cd my-cool-site",
		`docker compose exec -e SITE_NAME="My Cool Site" php composer craft-setup`,
		"spark composer install my-cool-site",
	}
	if len(got) != len(want) {
		t.Fatalf("nextSteps = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("nextSteps[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRunSetupPropagatesExitCodeAndRemaining(t *testing.T) {
	// The first command succeeds, the second exits nonzero: the exit code must
	// ride up and the failing command plus the unrun tail must come back.
	setup := []string{"true", "exit 5", "echo never"}
	remaining, err := runSetup(t.TempDir(), setup)
	code, ok := proc.Code(err)
	if !ok || code != 5 {
		t.Fatalf("runSetup exit code = (%d, %v), want (5, true)", code, ok)
	}
	want := []string{"exit 5", "echo never"}
	if len(remaining) != len(want) {
		t.Fatalf("remaining = %v, want %v", remaining, want)
	}
	for i := range want {
		if remaining[i] != want[i] {
			t.Errorf("remaining[%d] = %q, want %q", i, remaining[i], want[i])
		}
	}
}

func TestRunSetupSuccess(t *testing.T) {
	remaining, err := runSetup(t.TempDir(), []string{"true", "true"})
	if err != nil {
		t.Fatalf("runSetup: %v", err)
	}
	if remaining != nil {
		t.Errorf("remaining = %v, want nil on success", remaining)
	}
}

func TestSetupCommands(t *testing.T) {
	if got := setupCommands(nil); got != nil {
		t.Errorf("setupCommands(nil) = %v, want nil", got)
	}
	if got := setupCommands(&manifest.Manifest{}); got != nil {
		t.Errorf("setupCommands(no create) = %v, want nil", got)
	}
	m := &manifest.Manifest{Create: &manifest.Create{Setup: []string{"true"}}}
	if got := setupCommands(m); len(got) != 1 || got[0] != "true" {
		t.Errorf("setupCommands = %v, want [true]", got)
	}
}

// A boilerplate with no create: section must still get a git repo.
func TestSetupProjectWithoutCreateStillInitsGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	m := &manifest.Manifest{} // Create is nil
	rd := bufio.NewReader(strings.NewReader(""))
	answers, err := setupProject(root, m, rd, io.Discard, nil)
	if err != nil {
		t.Fatalf("setupProject: %v", err)
	}
	if len(answers) != 0 {
		t.Errorf("answers = %v, want empty for a boilerplate with no create: section", answers)
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		t.Errorf("git init did not run without a create: section: %v", err)
	}
}
