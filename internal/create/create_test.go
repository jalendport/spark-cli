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

// A boilerplate with no create: section must still get a git repo.
func TestSetupProjectWithoutCreateStillInitsGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	m := &manifest.Manifest{} // Create is nil
	rd := bufio.NewReader(strings.NewReader(""))
	if err := setupProject(root, m, rd, io.Discard); err != nil {
		t.Fatalf("setupProject: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		t.Errorf("git init did not run without a create: section: %v", err)
	}
}
