package lab

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mkBundle builds a minimal asset-bundle dir; contract "" omits the
// .spark-engine-contract file (the pre-handshake bundle shape).
func mkBundle(t *testing.T, contract string) string {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"skeleton", "docker"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if contract != "" {
		if err := os.WriteFile(filepath.Join(dir, engineContractFile), []byte(contract), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestValidateAssetsContract(t *testing.T) {
	if err := validateAssets(mkBundle(t, "")); err != nil {
		t.Errorf("bundle without a contract file rejected: %v", err)
	}
	if err := validateAssets(mkBundle(t, engineContract+"\n")); err != nil {
		t.Errorf("bundle with matching contract rejected: %v", err)
	}
	err := validateAssets(mkBundle(t, "999"))
	if err == nil {
		t.Fatal("bundle with mismatched contract accepted")
	}
	if !strings.Contains(err.Error(), `"999"`) || !strings.Contains(err.Error(), `"`+engineContract+`"`) {
		t.Errorf("mismatch error should name both revisions, got: %v", err)
	}

	if err := validateAssets(t.TempDir()); err == nil {
		t.Error("empty dir accepted as an asset bundle")
	}
}

// TestValidateAssetsRealBundle checks the local asset checkout (when present
// via SPARK_LAB_ASSETS) actually passes validation — i.e. the handshake and
// the real repo agree.
func TestValidateAssetsRealBundle(t *testing.T) {
	assets := os.Getenv(assetsEnv)
	if assets == "" {
		t.Skipf("%s not set", assetsEnv)
	}
	if err := validateAssets(assets); err != nil {
		t.Errorf("real asset bundle fails validation: %v", err)
	}
}
