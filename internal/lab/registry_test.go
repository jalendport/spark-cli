package lab

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// readRegistryFile decodes ~/.spark/lab-state.json (under the test HOME).
func readRegistryFile(t *testing.T) registryFile {
	t.Helper()
	path, err := registryPath()
	if err != nil {
		t.Fatalf("registryPath: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	var reg registryFile
	if err := json.Unmarshal(data, &reg); err != nil {
		t.Fatalf("decode registry: %v", err)
	}
	return reg
}

// mkInstanceDir creates a fake instance dir the registry can key a
// reservation by.
func mkInstanceDir(t *testing.T, root, name string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestAllocatePortsReservesAcrossDirs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	dirA := mkInstanceDir(t, root, "a")
	dirB := mkInstanceDir(t, root, "b")

	webA, mailA, err := allocatePorts(dirA, nil)
	if err != nil {
		t.Fatalf("allocate a: %v", err)
	}
	webB, mailB, err := allocatePorts(dirB, nil)
	if err != nil {
		t.Fatalf("allocate b: %v", err)
	}

	seen := map[int]bool{}
	for _, p := range []int{webA, mailA, webB, mailB} {
		if seen[p] {
			t.Fatalf("port %d handed out twice (a: %d/%d, b: %d/%d)", p, webA, mailA, webB, mailB)
		}
		seen[p] = true
	}

	// The reservation must survive while its dir exists — this is the invariant
	// `up` relies on by creating the dir before allocating (the GC-before-mint
	// race fix).
	reg := readRegistryFile(t)
	if _, ok := reg.Allocations[dirA]; !ok {
		t.Errorf("dir-backed reservation for %s was lost", dirA)
	}
	if _, ok := reg.Allocations[dirB]; !ok {
		t.Errorf("dir-backed reservation for %s was lost", dirB)
	}
}

func TestAllocatePortsGCsMissingDirsOnly(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	live := mkInstanceDir(t, root, "live")
	gone := filepath.Join(root, "gone") // never created

	if _, _, err := allocatePorts(live, nil); err != nil {
		t.Fatalf("allocate live: %v", err)
	}
	// Simulate the old bug's shape: a reservation keyed by a missing dir.
	f, unlock, err := lockRegistry()
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	reg := readRegistry(f)
	reg.Allocations[gone] = portAlloc{WebPort: 60001, MailpitPort: 60002}
	if err := writeRegistry(f, reg); err != nil {
		t.Fatalf("write: %v", err)
	}
	unlock()

	other := mkInstanceDir(t, root, "other")
	if _, _, err := allocatePorts(other, nil); err != nil {
		t.Fatalf("allocate other: %v", err)
	}

	after := readRegistryFile(t)
	if _, ok := after.Allocations[gone]; ok {
		t.Errorf("missing-dir reservation was not GC'd")
	}
	if _, ok := after.Allocations[live]; !ok {
		t.Errorf("live reservation was GC'd")
	}
}

func TestReleasePorts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := mkInstanceDir(t, t.TempDir(), "x")

	if _, _, err := allocatePorts(dir, nil); err != nil {
		t.Fatalf("allocate: %v", err)
	}
	releasePorts(dir)
	if _, ok := readRegistryFile(t).Allocations[dir]; ok {
		t.Errorf("reservation survived releasePorts")
	}
	// Releasing an unknown dir must be a no-op, not a panic or corruption.
	releasePorts(filepath.Join(dir, "never-existed"))
}
