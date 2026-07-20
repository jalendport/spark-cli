package create

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// maxExtractBytes caps the total decompressed size of a boilerplate tarball, so
// a malicious or corrupt archive can't fill the disk. It is a var only so tests
// can lower it. 512MB is far larger than any real boilerplate.
var maxExtractBytes int64 = 512 << 20

// codeloadURL builds the GitHub tarball URL for a repo's default branch. All
// boilerplate repos are public, so no auth token is required.
func codeloadURL(repo string) string {
	return fmt.Sprintf("https://codeload.github.com/%s/tar.gz/HEAD", repo)
}

// downloadBoilerplate streams the repo tarball and extracts it into dest,
// stripping the archive's top-level directory. Network and HTTP failures
// surface as clear errors.
func downloadBoilerplate(repo, dest string) error {
	url := codeloadURL(repo)
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("could not download %s: %w", repo, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading %s from %s returned %s", repo, url, resp.Status)
	}
	if err := extractTarball(resp.Body, dest); err != nil {
		return fmt.Errorf("extract %s: %w", repo, err)
	}
	return nil
}

// extractTarball reads a gzipped tar stream and writes its contents into dest,
// stripping the single top-level directory GitHub wraps the archive in. Entries
// that would escape dest are refused (tar-slip protection). Symlinks are created
// only after every file and directory exists, and only when their target stays
// inside dest, so an archive can't plant a symlink and then write through it to
// escape.
func extractTarball(r io.Reader, dest string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("open gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	var links []*tar.Header // symlinks, deferred until all regular entries exist
	var total int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		rel := stripTop(hdr.Name)
		if rel == "" {
			continue // the top-level wrapper directory itself
		}
		target := filepath.Join(dest, rel)
		if !within(dest, target) {
			return fmt.Errorf("archive entry %q escapes the target directory", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			total += hdr.Size
			if total > maxExtractBytes {
				return fmt.Errorf("archive is larger than the %d-byte limit", maxExtractBytes)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := writeFile(tr, target, os.FileMode(hdr.Mode).Perm()); err != nil {
				return err
			}
		case tar.TypeSymlink:
			links = append(links, hdr)
		}
	}

	// Create symlinks last, once every real file and directory is in place, so a
	// planted symlink can never be traversed by an earlier-extracted entry.
	for _, hdr := range links {
		target := filepath.Join(dest, stripTop(hdr.Name))
		resolved := hdr.Linkname
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(filepath.Dir(target), resolved)
		}
		if filepath.IsAbs(hdr.Linkname) || !within(dest, resolved) {
			return fmt.Errorf("archive symlink %q → %q escapes the target directory", hdr.Name, hdr.Linkname)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.Symlink(hdr.Linkname, target); err != nil {
			return err
		}
	}

	// Drain the rest of the gzip stream so a corrupted trailer (bad CRC or size)
	// surfaces as an error instead of yielding a silently truncated project.
	if _, err := io.Copy(io.Discard, gz); err != nil {
		return fmt.Errorf("read gzip stream: %w", err)
	}
	return nil
}

// stripTop removes the first path segment (GitHub's <repo>-<sha>/ wrapper) from
// a tar entry name, returning "" for the wrapper directory itself.
func stripTop(name string) string {
	name = strings.TrimPrefix(name, "./")
	i := strings.IndexByte(name, '/')
	if i < 0 {
		return ""
	}
	return strings.TrimSuffix(name[i+1:], "/")
}

// within reports whether path stays inside dir once cleaned.
func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// writeFile copies r into path with the given permissions (defaulting to 0644).
func writeFile(r io.Reader, path string, perm os.FileMode) error {
	if perm == 0 {
		perm = 0o644
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
