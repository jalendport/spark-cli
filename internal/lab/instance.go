package lab

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// instanceMeta is the engine's per-instance record (see ENGINE.md §7). It is
// written to .lab/<name>/lab.instance.json at mint and read back by list, boot,
// port allocation, and teardown.
type instanceMeta struct {
	Name          string `json:"name"`
	Plugin        string `json:"plugin"`
	Handle        string `json:"handle"`
	CraftMajor    int    `json:"craftMajor"`
	CraftVersion  string `json:"craftVersion"`
	CraftResolved string `json:"craftResolved,omitempty"`
	PHPTag        string `json:"phpTag"`
	DB            string `json:"db"`
	WebPort       int    `json:"webPort"`
	MailpitPort   int    `json:"mailpitPort"`
}

// displayVersion is the Craft version shown in `list`: the exact version
// resolved from composer.lock once known (most useful for `latest` instances),
// falling back to the name token before the first boot records it.
func (m instanceMeta) displayVersion() string {
	if m.CraftResolved != "" {
		return m.CraftResolved
	}
	return m.CraftVersion
}

// dbSpec holds the per-driver connection details (ENGINE.md §6).
type dbSpec struct {
	key      string // internal key: mysql | pgsql
	service  string // compose service / server name
	port     string // in-container port
	user     string
	password string
	driver   string // Craft CRAFT_DB_DRIVER value
	suffix   string // instance-name suffix: mysql | pg
	image    string
	schema   string // CRAFT_DB_SCHEMA value; pgsql-only, empty for mysql
}

var dbSpecs = map[string]dbSpec{
	"mysql": {key: "mysql", service: "mysql", port: "3306", user: "root", password: "root", driver: "mysql", suffix: "mysql", image: "jalendport/spark-mysql:8.4", schema: ""},
	"pgsql": {key: "pgsql", service: "postgres", port: "5432", user: "postgres", password: "root", driver: "pgsql", suffix: "pg", image: "postgres:16", schema: "public"},
}

// schemaLine renders the {{DB_SCHEMA_LINE}} placeholder: the CRAFT_DB_SCHEMA
// line (with trailing newline) for pgsql, empty for mysql — the schema is a
// pgsql-only concept, so mysql instances omit it entirely.
func (s dbSpec) schemaLine() string {
	if s.schema == "" {
		return ""
	}
	return "CRAFT_DB_SCHEMA=" + s.schema + "\n"
}

// normalizeDB maps a user --db value to a dbSpec.
func normalizeDB(value string) (dbSpec, error) {
	switch strings.ToLower(value) {
	case "pg", "pgsql", "postgres", "postgresql":
		return dbSpecs["pgsql"], nil
	case "mysql", "my", "maria", "mariadb":
		return dbSpecs["mysql"], nil
	}
	return dbSpec{}, fmt.Errorf("unknown --db %q — use mysql or pg", value)
}

var (
	versionRe   = regexp.MustCompile(`^\d+(\.\d+){1,2}$`)
	bareMajorRe = regexp.MustCompile(`^\d+$`)
)

// phpByMajor maps a Craft major to its default PHP image tag (ENGINE.md §11).
// Its keys are also the set of supported Craft majors.
var phpByMajor = map[int]string{4: "8.1", 5: "8.2"}

// supportedMajor reports whether the engine ships a skeleton for this Craft
// major. Kept in sync with phpByMajor / the skeleton/craft-<major>/ dirs.
func supportedMajor(major int) bool {
	_, ok := phpByMajor[major]
	return ok
}

// resolveVersion validates a --craft value and returns the version token used
// in the instance name, the Craft major, and the composer pin ("" when the
// skeleton's ^major default should hold). Unsupported majors are rejected here
// — before any asset fetch — so a bad --craft fails fast (ENGINE.md §3).
//
// Accepted forms:
//   - "" or "latest"      → latest stable Craft 5 (skeleton default, token "latest")
//   - bare major "4"/"5"  → latest of that major (skeleton default, token "4"/"5")
//   - "5.10" / "4.16"     → latest patch of that minor (pin "5.10.*")
//   - "5.4.3"             → that exact release
func resolveVersion(craft string) (token string, major int, pin string, err error) {
	invalid := func() error {
		return fmt.Errorf("invalid --craft %q — use one of: latest, 4, 5, 5.10, 4.16, or 5.4.3", craft)
	}
	if craft == "" || craft == "latest" {
		return "latest", 5, "", nil
	}
	if bareMajorRe.MatchString(craft) {
		major, err = strconv.Atoi(craft)
		if err != nil {
			return "", 0, "", invalid()
		}
		if !supportedMajor(major) {
			return "", 0, "", fmt.Errorf("unsupported --craft major %q — the engine ships skeletons for Craft 4 and 5 only", craft)
		}
		// Bare major ⇒ latest of that major, so leave the pin empty and let the
		// skeleton's ^major constraint resolve the newest stable release.
		return craft, major, "", nil
	}
	if !versionRe.MatchString(craft) {
		return "", 0, "", invalid()
	}
	major, err = strconv.Atoi(strings.SplitN(craft, ".", 2)[0])
	if err != nil {
		return "", 0, "", invalid()
	}
	if !supportedMajor(major) {
		return "", 0, "", fmt.Errorf("unsupported --craft major in %q — the engine ships skeletons for Craft 4 and 5 only", craft)
	}
	pin = craft
	if strings.Count(craft, ".") == 1 {
		pin = craft + ".*"
	}
	return craft, major, pin, nil
}

// instanceName derives the instance/dir name from the version token and db
// suffix, e.g. 5.10-mysql, 4.16-pg, latest-mysql (ENGINE.md §2).
func instanceName(token string, spec dbSpec) string {
	return token + "-" + spec.suffix
}

// projectName is the compose project name: lab-<slug(handle-name)>-<hash6>
// (ENGINE.md §2/§6), lowercase with non-alphanumerics collapsed to hyphens. The
// trailing hash is the first 6 hex of sha256(resolved plugin path), so two
// checkouts/worktrees of the same plugin (same handle + name) get distinct
// compose projects and can't cross-wire each other's containers/volumes.
func projectName(handle, name, pluginDir string) string {
	slug := strings.ToLower(handle + "-" + name)
	slug = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	return "lab-" + slug + "-" + pathHash6(pluginDir)
}

// pathHash6 returns the first 6 hex characters of sha256(abs(dir)), a short
// stable per-checkout discriminator for the compose project name.
func pathHash6(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	sum := sha256.Sum256([]byte(abs))
	return hex.EncodeToString(sum[:])[:6]
}

// listInstances returns the metas of every minted instance under .lab/, sorted
// by name.
func listInstances(labDir string) ([]instanceMeta, error) {
	entries, err := os.ReadDir(labDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var metas []instanceMeta
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		meta, err := readMeta(filepath.Join(labDir, e.Name()))
		if err != nil {
			continue // not a fully-minted instance
		}
		metas = append(metas, meta)
	}
	return metas, nil
}

func readMeta(instanceDir string) (instanceMeta, error) {
	var meta instanceMeta
	data, err := os.ReadFile(filepath.Join(instanceDir, "lab.instance.json"))
	if err != nil {
		return meta, err
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return meta, err
	}
	if err := meta.validate(instanceDir); err != nil {
		return instanceMeta{}, err
	}
	return meta, nil
}

// validate guards against a tampered or corrupt lab.instance.json redirecting a
// command outside the instance it was read from (ENGINE.md §7): the recorded
// Name must equal the directory basename and carry no path separators, the db
// must be a known driver, and the ports must be in range. Callers derive paths
// from the resolved instance dir, not meta.Name, but this keeps a malicious meta
// from being trusted anywhere downstream.
func (m instanceMeta) validate(instanceDir string) error {
	base := filepath.Base(instanceDir)
	if m.Name != base {
		return fmt.Errorf("instance metadata name %q does not match its directory %q", m.Name, base)
	}
	if m.Name == "" || m.Name != filepath.Base(m.Name) || strings.ContainsAny(m.Name, `/\`) {
		return fmt.Errorf("instance metadata has an invalid name %q", m.Name)
	}
	if _, ok := dbSpecs[m.DB]; !ok {
		return fmt.Errorf("instance metadata has an unknown db %q", m.DB)
	}
	if !validPort(m.WebPort) || !validPort(m.MailpitPort) {
		return fmt.Errorf("instance metadata has out-of-range ports (web %d, mailpit %d)", m.WebPort, m.MailpitPort)
	}
	// Downstream commands trust these without re-checking: seed/boot pass Handle
	// to plugin/install, and list renders CraftVersion/PHPTag.
	if m.Plugin == "" || m.Handle == "" {
		return fmt.Errorf("instance metadata is missing its plugin identity (plugin %q, handle %q)", m.Plugin, m.Handle)
	}
	if _, ok := phpByMajor[m.CraftMajor]; !ok {
		return fmt.Errorf("instance metadata has an unsupported Craft major %d", m.CraftMajor)
	}
	if m.CraftVersion == "" || m.PHPTag == "" {
		return fmt.Errorf("instance metadata is missing version info (craft %q, php %q)", m.CraftVersion, m.PHPTag)
	}
	return nil
}

// validPort reports whether p is a usable, non-privileged TCP port.
func validPort(p int) bool {
	return p > 0 && p < 65536
}

func writeMeta(instanceDir string, meta instanceMeta) error {
	return writeJSON(filepath.Join(instanceDir, "lab.instance.json"), meta)
}

// pickPorts picks two free TCP ports (web, then mailpit) by bind-testing upward
// from 8100 on 127.0.0.1, skipping anything in used. It marks its picks in used
// so the two never coincide.
func pickPorts(used map[int]bool) (web, mailpit int, err error) {
	next := func(from int) (int, error) {
		for p := from; p < 65535; p++ {
			if used[p] || !portFree(p) {
				continue
			}
			used[p] = true
			return p, nil
		}
		return 0, fmt.Errorf("no free port found above %d", from)
	}
	if web, err = next(8100); err != nil {
		return 0, 0, err
	}
	if mailpit, err = next(8100); err != nil {
		return 0, 0, err
	}
	return web, mailpit, nil
}

// allocateLocal is the registry-free fallback (ENGINE.md §6): it seeds the used
// set only from sibling instances in the same .lab/ dir. Used when the global
// registry can't be locked.
func allocateLocal(existing []instanceMeta) (web, mailpit int, err error) {
	used := map[int]bool{}
	for _, m := range existing {
		used[m.WebPort] = true
		used[m.MailpitPort] = true
	}
	return pickPorts(used)
}

// portFree reports whether a TCP port can be bound on the loopback interface.
func portFree(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// dbBlock renders the full YAML service block for the instance's database,
// substituted into the compose template's {{DB_BLOCK}} placeholder. The block
// is 2-space indented and bakes in the auto-created `craft` database.
func dbBlock(spec dbSpec) string {
	if spec.key == "pgsql" {
		return `  postgres:
    image: ` + spec.image + `
    environment:
      POSTGRES_PASSWORD: root
      POSTGRES_DB: craft
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U postgres"]
      interval: 5s
      timeout: 3s
      retries: 10
      start_period: 10s
    init: true
    volumes:
      - db-data:/var/lib/postgresql/data
`
	}
	return `  mysql:
    image: ` + spec.image + `
    environment:
      MYSQL_ROOT_PASSWORD: root
      MYSQL_DATABASE: craft
    healthcheck:
      test: ["CMD", "mysqladmin", "ping", "-h", "localhost"]
      interval: 5s
      timeout: 3s
      retries: 10
      start_period: 10s
    init: true
    volumes:
      - db-data:/var/lib/mysql
`
}

// copyTree recursively copies src into dst, creating dst, preserving file
// modes, and following the source's directory structure. Symlinks are
// recreated as symlinks.
func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		}
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		}
		return copyFile(path, target, info.Mode().Perm())
	})
}

func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
