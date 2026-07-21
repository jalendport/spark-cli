package manifest

import (
	"os"
	"path/filepath"
	"testing"
)

// writeManifest drops a spark.yml with the given body into a fresh dir.
func writeManifest(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, Filename), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadCreateSetup(t *testing.T) {
	dir := writeManifest(t, `
name: demo
create:
  setup:
    - docker compose up -d
    - spark composer install
`)
	m, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.Create == nil {
		t.Fatal("create section did not parse")
	}
	want := []string{"docker compose up -d", "spark composer install"}
	if len(m.Create.Setup) != len(want) {
		t.Fatalf("setup = %v, want %v", m.Create.Setup, want)
	}
	for i, c := range want {
		if m.Create.Setup[i] != c {
			t.Errorf("setup[%d] = %q, want %q", i, m.Create.Setup[i], c)
		}
	}
}

// A create: section without setup: must leave Setup empty, never nil-dereference.
func TestLoadCreateWithoutSetup(t *testing.T) {
	dir := writeManifest(t, `
name: demo
create:
  post:
    - echo hi
`)
	m, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.Create == nil {
		t.Fatal("create section did not parse")
	}
	if len(m.Create.Setup) != 0 {
		t.Errorf("setup = %v, want empty", m.Create.Setup)
	}
}
