package create

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

// tarEntry is one file, directory, or symlink written into a fixture archive.
type tarEntry struct {
	name string // path as stored in the archive, including the top-level wrapper
	body string // "" marks a directory (name should end in /)
	link string // non-empty marks a symlink; link is its target
}

// buildTarGz assembles a gzipped tar archive in memory from the given entries,
// mirroring the shape GitHub's codeload tarballs take (a single top-level dir).
func buildTarGz(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.body))}
		switch {
		case e.link != "":
			hdr.Typeflag = tar.TypeSymlink
			hdr.Linkname = e.link
			hdr.Size = 0
		case e.body == "":
			hdr.Typeflag = tar.TypeDir
			hdr.Mode = 0o755
		default:
			hdr.Typeflag = tar.TypeReg
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if e.body != "" {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractTarballStripsTopLevel(t *testing.T) {
	archive := buildTarGz(t, []tarEntry{
		{name: "spark-craft-abc123/", body: ""},
		{name: "spark-craft-abc123/spark.yml", body: "name: demo\n"},
		{name: "spark-craft-abc123/src/", body: ""},
		{name: "spark-craft-abc123/src/index.php", body: "<?php echo 'hi';\n"},
	})

	dest := t.TempDir()
	if err := extractTarball(bytes.NewReader(archive), dest); err != nil {
		t.Fatalf("extractTarball: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dest, "spark.yml"))
	if err != nil {
		t.Fatalf("spark.yml not extracted at the root: %v", err)
	}
	if string(got) != "name: demo\n" {
		t.Errorf("spark.yml body = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dest, "src", "index.php")); err != nil {
		t.Errorf("nested file not extracted: %v", err)
	}
	// The wrapper directory must not survive the strip.
	if _, err := os.Stat(filepath.Join(dest, "spark-craft-abc123")); !os.IsNotExist(err) {
		t.Error("top-level wrapper directory leaked into the target")
	}
}

func TestExtractTarballRejectsTraversal(t *testing.T) {
	archive := buildTarGz(t, []tarEntry{
		{name: "repo-sha/", body: ""},
		{name: "repo-sha/../escape.txt", body: "nope"},
	})
	dest := t.TempDir()
	if err := extractTarball(bytes.NewReader(archive), dest); err == nil {
		t.Fatal("expected a tar-slip entry to be rejected")
	}
}

func TestExtractTarballRejectsAbsoluteSymlink(t *testing.T) {
	archive := buildTarGz(t, []tarEntry{
		{name: "repo-sha/", body: ""},
		{name: "repo-sha/link", link: "/etc/passwd"},
	})
	if err := extractTarball(bytes.NewReader(archive), t.TempDir()); err == nil {
		t.Fatal("expected an absolute symlink target to be rejected")
	}
}

func TestExtractTarballRejectsEscapingSymlink(t *testing.T) {
	archive := buildTarGz(t, []tarEntry{
		{name: "repo-sha/", body: ""},
		{name: "repo-sha/link", link: "../../escape"},
	})
	if err := extractTarball(bytes.NewReader(archive), t.TempDir()); err == nil {
		t.Fatal("expected an escaping symlink target to be rejected")
	}
}

// A malicious archive that plants a symlink pointing outside dest and then
// writes a regular file "through" it must never touch anything outside dest.
func TestExtractTarballSymlinkThenWrite(t *testing.T) {
	outside := t.TempDir()
	archive := buildTarGz(t, []tarEntry{
		{name: "repo-sha/", body: ""},
		{name: "repo-sha/link", link: outside},
		{name: "repo-sha/link/pwned", body: "owned"},
	})
	// Extraction is expected to fail (the symlink target escapes dest); what
	// matters is that the write never lands outside dest.
	_ = extractTarball(bytes.NewReader(archive), t.TempDir())
	if _, err := os.Stat(filepath.Join(outside, "pwned")); !os.IsNotExist(err) {
		t.Fatal("regular entry escaped through a planted symlink")
	}
}

func TestExtractTarballCorruptGzipTrailer(t *testing.T) {
	archive := buildTarGz(t, []tarEntry{
		{name: "repo-sha/", body: ""},
		{name: "repo-sha/spark.yml", body: "name: demo\n"},
	})
	// Corrupt the gzip trailer (its final CRC32/ISIZE byte), which only surfaces
	// once the stream is drained past the tar EOF.
	archive[len(archive)-1] ^= 0xff
	if err := extractTarball(bytes.NewReader(archive), t.TempDir()); err == nil {
		t.Fatal("expected a corrupt gzip trailer to surface as an error")
	}
}

func TestExtractTarballCapsSize(t *testing.T) {
	orig := maxExtractBytes
	maxExtractBytes = 4
	defer func() { maxExtractBytes = orig }()

	archive := buildTarGz(t, []tarEntry{
		{name: "repo-sha/", body: ""},
		{name: "repo-sha/big.txt", body: "way more than four bytes"},
	})
	if err := extractTarball(bytes.NewReader(archive), t.TempDir()); err == nil {
		t.Fatal("expected extraction to be rejected past the size cap")
	}
}

func TestStripTop(t *testing.T) {
	cases := map[string]string{
		"repo-sha/":              "",
		"repo-sha":               "",
		"repo-sha/spark.yml":     "spark.yml",
		"repo-sha/src/":          "src",
		"./repo-sha/src/app.php": "src/app.php",
	}
	for in, want := range cases {
		if got := stripTop(in); got != want {
			t.Errorf("stripTop(%q) = %q, want %q", in, got, want)
		}
	}
}
