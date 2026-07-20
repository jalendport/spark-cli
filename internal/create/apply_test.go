package create

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jalendport/spark-cli/internal/manifest"
	"github.com/jalendport/spark-cli/internal/proc"
)

func writeFileT(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestApplyRename(t *testing.T) {
	root := t.TempDir()
	writeFileT(t, filepath.Join(root, "composer.json.project"), "{}")

	if err := applyRename(root, map[string]string{"composer.json.project": "composer.json"}); err != nil {
		t.Fatalf("applyRename: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "composer.json")); err != nil {
		t.Errorf("renamed file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "composer.json.project")); !os.IsNotExist(err) {
		t.Error("source file still present after rename")
	}
}

func TestApplyRenameMissingSource(t *testing.T) {
	root := t.TempDir()
	if err := applyRename(root, map[string]string{"nope": "dst"}); err == nil {
		t.Error("expected an error renaming a missing source")
	}
}

func TestApplyReplace(t *testing.T) {
	root := t.TempDir()
	writeFileT(t, filepath.Join(root, "composer.json"), `{"name":"__VENDOR__/__PROJECT__"}`)
	writeFileT(t, filepath.Join(root, "vendor", "keep.txt"), "__VENDOR__ untouched")
	// A binary file that also contains the token must be left byte-for-byte alone.
	writeFileT(t, filepath.Join(root, "logo.bin"), "pre\x00__VENDOR__post")

	answers := map[string]string{"vendor": "acme", "project": "widget"}
	replace := map[string]string{"__VENDOR__": "{vendor}", "__PROJECT__": "{project}"}
	if err := applyReplace(root, replace, answers); err != nil {
		t.Fatalf("applyReplace: %v", err)
	}

	got, _ := os.ReadFile(filepath.Join(root, "composer.json"))
	if string(got) != `{"name":"acme/widget"}` {
		t.Errorf("composer.json = %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "vendor", "keep.txt")); string(got) != "__VENDOR__ untouched" {
		t.Errorf("vendor/ file was rewritten: %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "logo.bin")); string(got) != "pre\x00__VENDOR__post" {
		t.Errorf("binary file was rewritten: %q", got)
	}
}

func TestApplyReplaceSkipsEmptyToken(t *testing.T) {
	root := t.TempDir()
	writeFileT(t, filepath.Join(root, "file.txt"), "abc")
	// An empty token must not splice the replacement between every character.
	if err := applyReplace(root, map[string]string{"": "X"}, nil); err != nil {
		t.Fatalf("applyReplace: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "file.txt")); string(got) != "abc" {
		t.Errorf("empty token corrupted the file: %q", got)
	}
}

func TestRunPrompts(t *testing.T) {
	c := &manifest.Create{
		Prompts: []struct {
			Key   string `yaml:"key"`
			Label string `yaml:"label"`
		}{
			{Key: "vendor", Label: "Vendor"},
			{Key: "project", Label: "Project"},
		},
	}
	rd := bufio.NewReader(strings.NewReader("acme\nwidget\n"))
	answers, err := runPrompts(c, rd, io.Discard)
	if err != nil {
		t.Fatalf("runPrompts: %v", err)
	}
	if answers["vendor"] != "acme" || answers["project"] != "widget" {
		t.Errorf("answers = %v", answers)
	}
}

func TestApplyRenameDestinationCollision(t *testing.T) {
	root := t.TempDir()
	writeFileT(t, filepath.Join(root, "a.txt"), "a")
	writeFileT(t, filepath.Join(root, "b.txt"), "b")
	// Two sources targeting the same destination must be rejected up front.
	if err := applyRename(root, map[string]string{"a.txt": "out", "b.txt": "out"}); err == nil {
		t.Fatal("expected colliding rename destinations to be rejected")
	}
}

func TestApplyRenameChainRejected(t *testing.T) {
	root := t.TempDir()
	writeFileT(t, filepath.Join(root, "a"), "a")
	writeFileT(t, filepath.Join(root, "b"), "b")
	// b is both a destination and a source, so the result would depend on order.
	if err := applyRename(root, map[string]string{"a": "b", "b": "c"}); err == nil {
		t.Fatal("expected a rename whose destination is another rename's source to be rejected")
	}
}

func TestApplyRenameOverwritesShippedFile(t *testing.T) {
	root := t.TempDir()
	// Overwriting an ordinary shipped file (not a rename participant) is the
	// documented purpose of rename and must keep working.
	writeFileT(t, filepath.Join(root, "composer.json.project"), "template")
	writeFileT(t, filepath.Join(root, "composer.json"), "placeholder")
	if err := applyRename(root, map[string]string{"composer.json.project": "composer.json"}); err != nil {
		t.Fatalf("applyRename: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "composer.json")); string(got) != "template" {
		t.Errorf("composer.json = %q, want the renamed template content", got)
	}
}

func TestApplyReplaceNoCascade(t *testing.T) {
	root := t.TempDir()
	writeFileT(t, filepath.Join(root, "file.txt"), "A B")
	// A→B and B→C must resolve in a single pass, so "A" becomes "B" (not "C").
	replace := map[string]string{"A": "B", "B": "C"}
	if err := applyReplace(root, replace, nil); err != nil {
		t.Fatalf("applyReplace: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "file.txt")); string(got) != "B C" {
		t.Errorf("cascading replacement: file.txt = %q, want %q", got, "B C")
	}
}

func TestRunPromptsTruncatedInput(t *testing.T) {
	c := &manifest.Create{
		Prompts: []struct {
			Key   string `yaml:"key"`
			Label string `yaml:"label"`
		}{
			{Key: "vendor", Label: "Vendor"},
			{Key: "project", Label: "Project"},
		},
	}
	// Input ends after the first answer; the second prompt gets no text.
	rd := bufio.NewReader(strings.NewReader("acme\n"))
	if _, err := runPrompts(c, rd, io.Discard); err == nil {
		t.Fatal("expected an error when input ends before all prompts are answered")
	}
}

func TestRunPostPropagatesExitCode(t *testing.T) {
	err := runPost(t.TempDir(), []string{"exit 3"})
	code, ok := proc.Code(err)
	if !ok || code != 3 {
		t.Fatalf("runPost exit code = (%d, %v), want (3, true)", code, ok)
	}
}

func TestExecuteCreate(t *testing.T) {
	root := t.TempDir()
	writeFileT(t, filepath.Join(root, "composer.json.project"), `{"name":"__VENDOR__/__PROJECT__"}`)

	c := &manifest.Create{
		Prompts: []struct {
			Key   string `yaml:"key"`
			Label string `yaml:"label"`
		}{
			{Key: "vendor", Label: "Vendor"},
			{Key: "project", Label: "Project"},
		},
		Rename:  map[string]string{"composer.json.project": "composer.json"},
		Replace: map[string]string{"__VENDOR__": "{vendor}", "__PROJECT__": "{project}"},
		Post:    []string{"echo done > POST.txt"},
	}
	rd := bufio.NewReader(strings.NewReader("acme\nwidget\n"))
	if err := executeCreate(root, c, rd, io.Discard); err != nil {
		t.Fatalf("executeCreate: %v", err)
	}

	got, _ := os.ReadFile(filepath.Join(root, "composer.json"))
	if string(got) != `{"name":"acme/widget"}` {
		t.Errorf("composer.json = %q", got)
	}
	if _, err := os.Stat(filepath.Join(root, "POST.txt")); err != nil {
		t.Errorf("post command did not run: %v", err)
	}
}
