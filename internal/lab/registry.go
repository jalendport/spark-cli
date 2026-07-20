package lab

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// The lab port registry coordinates host-port allocation across *all* plugin
// repos and checkouts on a machine. Without it, allocation only saw siblings in
// one .lab/ dir, so two concurrent `up` runs in different repos/worktrees both
// started probing at 8100 and could pick the same ports (ENGINE.md §6). The
// registry is a single per-user JSON file guarded by an advisory file lock;
// each allocation reserves ports globally before `docker compose up` runs.

// registryFile is the per-user allocation registry (see registryPath).
type registryFile struct {
	Allocations map[string]portAlloc `json:"allocations"`
}

// portAlloc is one instance's reserved host ports, keyed by its absolute
// instance directory so stale entries can be garbage-collected by existence.
type portAlloc struct {
	WebPort     int `json:"webPort"`
	MailpitPort int `json:"mailpitPort"`
}

// registryPath is ~/.spark/lab-state.json — a stable per-user location
// independent of any single plugin repo.
func registryPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".spark", "lab-state.json"), nil
}

// lockRegistry opens (creating if needed) the registry file and takes an
// exclusive advisory lock on it, returning the open file and an unlock func the
// caller must call. The lock auto-releases if the process dies, so a crash can't
// wedge allocation for other repos.
func lockRegistry() (*os.File, func(), error) {
	path, err := registryPath()
	if err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	unlock := func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}
	return f, unlock, nil
}

// readRegistry decodes the registry from an already-locked, open file. An empty
// or malformed file yields an empty registry rather than an error, so a
// corrupted state file self-heals on the next write.
func readRegistry(f *os.File) registryFile {
	reg := registryFile{Allocations: map[string]portAlloc{}}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return reg
	}
	data, err := io.ReadAll(f)
	if err != nil || len(data) == 0 {
		return reg
	}
	if err := json.Unmarshal(data, &reg); err != nil || reg.Allocations == nil {
		return registryFile{Allocations: map[string]portAlloc{}}
	}
	return reg
}

// writeRegistry truncates the locked file and writes reg back to it.
func writeRegistry(f *os.File, reg registryFile) error {
	data, err := json.MarshalIndent(reg, "", "\t")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	_, err = f.Write(data)
	return err
}

// allocatePorts reserves two free host ports for instanceDir, coordinating
// across every plugin repo on the machine via the per-user registry. It locks
// the registry, garbage-collects allocations whose instance dir no longer
// exists, unions the surviving registry ports with this repo's sibling metas,
// bind-tests upward from 8100 for two free ports, records them, and writes the
// registry back. When the registry can't be locked (e.g. no home dir) it falls
// back to the registry-free, sibling-only allocation (ENGINE.md §6).
//
// instanceDir must be the absolute instance directory and MUST already exist
// on disk before this is called: reservations are GC'd by dir existence, so a
// reservation for a not-yet-created dir would be collected by any concurrent
// allocation. A later re-allocation (the port-bind retry in boot) overwrites
// the same key and avoids the ports it previously lost.
func allocatePorts(instanceDir string, existing []instanceMeta) (web, mailpit int, err error) {
	abs, aerr := filepath.Abs(instanceDir)
	if aerr != nil {
		abs = instanceDir
	}

	f, unlock, lerr := lockRegistry()
	if lerr != nil {
		return allocateLocal(existing)
	}
	defer unlock()

	reg := readRegistry(f)
	used := map[int]bool{}
	for dir, a := range reg.Allocations {
		if !dirExists(dir) {
			delete(reg.Allocations, dir) // GC: instance is gone
			continue
		}
		used[a.WebPort] = true
		used[a.MailpitPort] = true
	}
	// Sibling metas are redundant with the registry once everything goes through
	// it, but kept as a belt-and-braces guard for instances minted before the
	// registry existed (or via the fallback path).
	for _, m := range existing {
		used[m.WebPort] = true
		used[m.MailpitPort] = true
	}

	web, mailpit, err = pickPorts(used)
	if err != nil {
		return 0, 0, err
	}
	reg.Allocations[abs] = portAlloc{WebPort: web, MailpitPort: mailpit}
	if werr := writeRegistry(f, reg); werr != nil {
		return 0, 0, werr
	}
	return web, mailpit, nil
}

// releasePorts drops instanceDir's reservation from the registry. Best-effort:
// the GC in allocatePorts would reclaim it eventually; releasing on destroy
// just keeps the registry tight.
func releasePorts(instanceDir string) {
	abs, err := filepath.Abs(instanceDir)
	if err != nil {
		abs = instanceDir
	}
	f, unlock, err := lockRegistry()
	if err != nil {
		return
	}
	defer unlock()
	reg := readRegistry(f)
	if _, ok := reg.Allocations[abs]; !ok {
		return
	}
	delete(reg.Allocations, abs)
	_ = writeRegistry(f, reg)
}

// dirExists reports whether p is an existing directory.
func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}
