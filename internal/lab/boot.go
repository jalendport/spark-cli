package lab

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jalendport/spark-cli/internal/ui"
)

// composerCacheVolume is the one persistent cross-instance artifact: a build
// cache mounted into every instance's php container to keep composer fast.
const composerCacheVolume = "spark-craft-lab-composer"

const (
	adminUsername = "admin"
	adminPassword = "password"
	adminEmail    = "admin@lab.test"
)

// composeCmd builds a `docker compose <args>` command rooted at an instance dir.
func composeCmd(instanceDir string, args ...string) *exec.Cmd {
	cmd := exec.Command("docker", append([]string{"compose"}, args...)...)
	cmd.Dir = instanceDir
	return cmd
}

// streamCompose runs a compose command, echoing it and passing through stdio.
func streamCompose(instanceDir string, args ...string) error {
	ui.Echo("docker compose " + strings.Join(args, " "))
	cmd := composeCmd(instanceDir, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// captureCompose runs a compose command silently and returns its combined output.
func captureCompose(instanceDir string, args ...string) (string, error) {
	cmd := composeCmd(instanceDir, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// craftArgs builds the argv for a Craft console command inside the php
// container. Without a tty it adds -T so docker doesn't demand one.
func craftArgs(tty bool, args []string) []string {
	base := []string{"exec"}
	if !tty {
		base = append(base, "-T")
	}
	base = append(base, "php", "php", "craft")
	return append(base, args...)
}

// bootInstance brings an instance up and makes it usable: creates the composer
// cache volume, builds+starts the stack, waits for the entrypoint's composer
// install, installs Craft if needed, and installs the plugin under test
// (ENGINE.md §8 steps 1–7). The resolved instance dir is passed in (not derived
// from meta.Name) so a tampered name can't redirect compose at another dir; meta
// is a pointer because a port-bind retry can reallocate ports and update it.
// fresh reports whether the instance was minted in this run — only then may a
// port-bind failure reallocate ports, because Craft bakes the site URL into its
// database at install time and a later reallocation would silently diverge.
func bootInstance(c *ctx, dir string, meta *instanceMeta, fresh bool) error {
	// A running php container means the stack is already built and up (e.g. a
	// reseed right after an up): skip the volume-create + compose up --build and
	// go straight to ensuring Craft + the plugin are ready.
	if phpRunning(dir) {
		ui.Stepf("containers already running")
	} else {
		if out, err := exec.Command("docker", "volume", "create", composerCacheVolume).CombinedOutput(); err != nil {
			return fmt.Errorf("create composer cache volume: %w\n%s", err, out)
		}

		ui.Stepf("building and starting containers")
		if err := composeUpWithPortRetry(c, dir, meta, fresh); err != nil {
			return err
		}
	}

	if err := waitForVendor(dir); err != nil {
		return err
	}

	if _, err := captureCompose(dir, "exec", "-T", "php", "composer", "dump-autoload"); err != nil {
		return fmt.Errorf("composer dump-autoload failed: %w", err)
	}

	if err := ensureCraftInstalled(dir, *meta); err != nil {
		return err
	}

	if err := installPlugin(dir, meta.Handle); err != nil {
		return err
	}

	// Record the exact Craft version composer resolved so `list` can show it
	// (most useful for `latest` instances). Best-effort and cosmetic.
	recordResolvedCraft(dir)
	return nil
}

// composeUpWithPortRetry runs `docker compose up` and, if it fails because a
// published host port was grabbed by another process between allocation and
// bind (a residual TOCTOU window the registry can't fully close), reallocates
// fresh ports, rewrites them into the instance's compose.yaml/.env/meta, and
// retries. Any non-bind failure returns immediately. Reallocation only happens
// for a freshly-minted instance: an installed one has its original port baked
// into Craft's database as the site URL, so a bind clash there is reported
// instead of silently moving the instance.
func composeUpWithPortRetry(c *ctx, dir string, meta *instanceMeta, fresh bool) error {
	const maxAttempts = 4
	for attempt := 1; ; attempt++ {
		out, err := streamCaptureCompose(dir, "up", "-d", "--build", "--wait")
		if err == nil {
			return nil
		}
		if !fresh && isPortBindError(out) {
			return fmt.Errorf(
				"host port %d or %d is in use by another process, and this instance's site URL is already bound to it — free the port, or run: spark lab destroy %s && spark lab up",
				meta.WebPort, meta.MailpitPort, meta.Name)
		}
		if attempt >= maxAttempts || !isPortBindError(out) {
			return fmt.Errorf("docker compose up failed: %w", err)
		}
		ui.Warnf("host port %d or %d is already in use — reallocating and retrying (attempt %d/%d)",
			meta.WebPort, meta.MailpitPort, attempt, maxAttempts)
		// Tear down whatever partially came up so the retry rebinds cleanly; keep
		// volumes (nothing important has been written yet on a fresh boot).
		_, _ = captureCompose(dir, "down", "--remove-orphans")

		var existing []instanceMeta
		if metas, lerr := listInstances(c.labDir()); lerr == nil {
			existing = metas // includes self (old ports), so we move off them
		}
		web, mailpit, aerr := allocatePorts(dir, existing)
		if aerr != nil {
			return fmt.Errorf("reallocate ports after bind failure: %w", aerr)
		}
		if rerr := rewriteInstancePorts(dir, meta, web, mailpit); rerr != nil {
			return rerr
		}
	}
}

// streamCaptureCompose runs a compose command, passing stdio through to the user
// (so build/up progress stays live) while also capturing combined output so the
// caller can classify the failure.
func streamCaptureCompose(dir string, args ...string) (string, error) {
	ui.Echo("docker compose " + strings.Join(args, " "))
	var buf bytes.Buffer
	cmd := composeCmd(dir, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = io.MultiWriter(os.Stdout, &buf)
	cmd.Stderr = io.MultiWriter(os.Stderr, &buf)
	err := cmd.Run()
	return buf.String(), err
}

// isPortBindError reports whether compose output indicates a host-port bind
// clash (as opposed to a build error, image pull failure, etc.).
func isPortBindError(out string) bool {
	l := strings.ToLower(out)
	for _, marker := range []string{
		"port is already allocated",
		"address already in use",
		"failed to bind host port",
		"ports are not available",
	} {
		if strings.Contains(l, marker) {
			return true
		}
	}
	return false
}

// rewriteInstancePorts swaps the instance's old published ports for new ones in
// the already-rendered compose.yaml and .env, and updates meta (persisting it).
// Called only during the fresh-mint bind retry (enforced by the fresh gate in
// composeUpWithPortRetry), before Craft is installed, so no baked-in URL has
// been written to the database yet.
func rewriteInstancePorts(dir string, meta *instanceMeta, newWeb, newMailpit int) error {
	oldWeb, oldMailpit := meta.WebPort, meta.MailpitPort
	compose := filepath.Join(dir, "compose.yaml")
	if err := replaceInFile(compose,
		fmt.Sprintf("127.0.0.1:%d:80", oldWeb), fmt.Sprintf("127.0.0.1:%d:80", newWeb)); err != nil {
		return err
	}
	if err := replaceInFile(compose,
		fmt.Sprintf("127.0.0.1:%d:8025", oldMailpit), fmt.Sprintf("127.0.0.1:%d:8025", newMailpit)); err != nil {
		return err
	}
	if err := replaceInFile(filepath.Join(dir, ".env"),
		fmt.Sprintf("APP_URL=http://localhost:%d", oldWeb), fmt.Sprintf("APP_URL=http://localhost:%d", newWeb)); err != nil {
		return err
	}
	meta.WebPort = newWeb
	meta.MailpitPort = newMailpit
	return writeMeta(dir, *meta)
}

// replaceInFile rewrites path with every occurrence of old replaced by repl.
func replaceInFile(path, old, repl string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strings.ReplaceAll(string(data), old, repl)), 0o644)
}

// recordResolvedCraft reads the exact craftcms/cms version from the instance's
// composer.lock and persists it to lab.instance.json, so `list` can show the
// resolved version rather than the name token. Best-effort: any failure (no
// lock yet, parse error) leaves the meta untouched.
func recordResolvedCraft(instanceDir string) {
	data, err := os.ReadFile(filepath.Join(instanceDir, "composer.lock"))
	if err != nil {
		return
	}
	var lock struct {
		Packages []struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		return
	}
	version := ""
	for _, pkg := range lock.Packages {
		if pkg.Name == "craftcms/cms" {
			version = strings.TrimPrefix(pkg.Version, "v")
			break
		}
	}
	if version == "" {
		return
	}
	meta, err := readMeta(instanceDir)
	if err != nil || meta.CraftResolved == version {
		return
	}
	meta.CraftResolved = version
	_ = writeMeta(instanceDir, meta)
}

// waitForVendor blocks until the php entrypoint's `composer install` has
// produced vendor/autoload.php and composer.lock, failing fast if the php
// container exits first (ENGINE.md §8.2).
func waitForVendor(dir string) error {
	autoload := filepath.Join(dir, "vendor", "autoload.php")
	lock := filepath.Join(dir, "composer.lock")

	ready := func() bool {
		return fileExists(autoload) && fileExists(lock)
	}
	if ready() {
		return nil
	}

	ui.Stepf("waiting for composer install inside the php container")
	deadline := time.Now().Add(15 * time.Minute)
	for !ready() {
		if time.Now().After(deadline) {
			return fmt.Errorf("composer install timed out — check: docker compose logs php (in %s)", dir)
		}
		if !phpRunning(dir) {
			logs, _ := captureCompose(dir, "logs", "--tail", "40", "php")
			return fmt.Errorf("php container stopped during composer install:\n%s", logs)
		}
		time.Sleep(3 * time.Second)
	}
	return nil
}

// phpRunning reports whether the instance's php service is up.
func phpRunning(instanceDir string) bool {
	out, err := captureCompose(instanceDir, "ps", "--status", "running", "-q", "php")
	return err == nil && strings.TrimSpace(out) != ""
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// ensureCraftInstalled runs Craft's installer when the instance isn't installed
// yet (ENGINE.md §8.4).
func ensureCraftInstalled(instanceDir string, meta instanceMeta) error {
	if _, err := captureCompose(instanceDir, craftArgs(false, []string{"install/check"})...); err == nil {
		return nil // already installed
	}
	ui.Stepf("installing Craft")
	args := craftArgs(false, []string{
		"install/craft", "--interactive=0",
		"--username=" + adminUsername,
		"--password=" + adminPassword,
		"--email=" + adminEmail,
		fmt.Sprintf("--siteName=Spark Craft Lab (%s)", meta.Name),
		fmt.Sprintf("--siteUrl=http://localhost:%d", meta.WebPort),
		"--language=en-US",
	})
	if out, err := captureCompose(instanceDir, args...); err != nil {
		return fmt.Errorf("craft install failed: %w\n%s", err, out)
	}
	return nil
}

// installPlugin installs the plugin under test, treating an already-installed
// plugin as success (ENGINE.md §8.5).
func installPlugin(instanceDir, handle string) error {
	ui.Stepf("installing plugin %s", handle)
	out, err := captureCompose(instanceDir, craftArgs(false, []string{"plugin/install", handle, "--interactive=0"})...)
	if err == nil || strings.Contains(out, "already installed") {
		return nil
	}
	return fmt.Errorf("plugin install failed for %q:\n%s", handle, out)
}

// runSeed runs the in-Craft seeder (generic model + the plugin's seed hook).
func runSeed(instanceDir string) error {
	ui.Stepf("seeding content")
	if err := streamCompose(instanceDir, craftArgs(false, []string{"lab/seed"})...); err != nil {
		return fmt.Errorf("seed failed: %w", err)
	}
	return nil
}
