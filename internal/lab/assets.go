package lab

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jalendport/spark-cli/internal/ui"
)

// assetsRepo is the public source of the asset bundle. It is shallow-cloned per
// `up` and discarded once the instance has copied what it needs.
const assetsRepo = "https://github.com/jalendport/spark-craft-lab"

// assetsEnv, when set to a local working copy, skips the clone — for developing
// the assets themselves against a live checkout.
const assetsEnv = "SPARK_LAB_ASSETS"

// resolveAssets returns a usable asset bundle directory plus a cleanup func the
// caller must defer. With SPARK_LAB_ASSETS set it uses that path directly
// (cleanup is a no-op); otherwise it shallow-clones the bundle into a temp dir
// (cleanup removes it).
func resolveAssets() (dir string, cleanup func(), err error) {
	noop := func() {}

	if override := os.Getenv(assetsEnv); override != "" {
		abs, err := filepath.Abs(override)
		if err != nil {
			return "", noop, err
		}
		if err := validateAssets(abs); err != nil {
			return "", noop, fmt.Errorf("%s=%s: %w", assetsEnv, override, err)
		}
		return abs, noop, nil
	}

	tmp, err := os.MkdirTemp("", "spark-lab-assets-")
	if err != nil {
		return "", noop, err
	}
	cleanup = func() { os.RemoveAll(tmp) }

	ui.Stepf("fetching lab assets")
	cmd := exec.Command("git", "clone", "--depth", "1", assetsRepo, tmp)
	if out, err := cmd.CombinedOutput(); err != nil {
		cleanup()
		return "", noop, fmt.Errorf(
			"could not clone the lab asset bundle from %s: %w\n%s\n"+
				"(the repo may still be private — set %s to a local checkout to develop against it)",
			assetsRepo, err, out, assetsEnv)
	}
	if err := validateAssets(tmp); err != nil {
		cleanup()
		return "", noop, err
	}
	return tmp, cleanup, nil
}

// engineContract is the asset-bundle contract revision this binary implements.
// A bundle that declares a different revision in a .spark-engine-contract file
// at its root is refused with a clear message instead of failing somewhere deep
// inside mint. Bundles without the file are accepted (the check activates once
// the asset repo starts declaring it).
const engineContract = "1"

// engineContractFile is the marker file at the bundle root declaring its
// contract revision.
const engineContractFile = ".spark-engine-contract"

// validateAssets checks the bundle has the directories the engine copies from,
// and that its declared contract revision (if any) matches this binary.
func validateAssets(dir string) error {
	for _, sub := range []string{"skeleton", "docker"} {
		if info, err := os.Stat(filepath.Join(dir, sub)); err != nil || !info.IsDir() {
			return fmt.Errorf("asset bundle at %s is missing %s/", dir, sub)
		}
	}
	if data, err := os.ReadFile(filepath.Join(dir, engineContractFile)); err == nil {
		if got := strings.TrimSpace(string(data)); got != engineContract {
			return fmt.Errorf(
				"asset bundle declares engine contract %q but this spark speaks %q — upgrade whichever is older (brew upgrade spark, or pull the asset repo)",
				got, engineContract)
		}
	}
	return nil
}
