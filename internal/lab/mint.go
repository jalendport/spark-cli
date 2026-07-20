package lab

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// mintParams carries everything needed to mint one instance.
type mintParams struct {
	name        string
	major       int
	token       string // version token for the name (e.g. 5.10, latest)
	craftPin    string // exact composer pin, "" for the skeleton default
	spec        dbSpec
	phpTag      string
	webPort     int
	mailpitPort int
}

// mint materializes a fresh, self-contained instance under .lab/<name>/ from the
// asset bundle: the skeleton (which already carries the in-Craft lab module, its
// registration, and the generic templates) plus the docker build context copied
// in as .docker/, with composer.json / .env / compose.yaml rendered (ENGINE.md
// §4–6). It writes lab.instance.json and returns the meta.
func mint(c *ctx, assets string, p mintParams) (instanceMeta, error) {
	skeleton := filepath.Join(assets, "skeleton", fmt.Sprintf("craft-%d", p.major))
	if info, err := os.Stat(skeleton); err != nil || !info.IsDir() {
		return instanceMeta{}, fmt.Errorf("asset bundle has no skeleton for Craft %d", p.major)
	}
	// Fail fast with a clear message rather than deep inside renderTemplate if
	// the bundle's skeleton doesn't carry what mint renders.
	for _, req := range []string{".env.tpl", "compose.yaml.tpl", "composer.json"} {
		if !fileExists(filepath.Join(skeleton, req)) {
			return instanceMeta{}, fmt.Errorf(
				"asset bundle skeleton craft-%d is missing %s — the bundle and this spark version may be incompatible", p.major, req)
		}
	}

	dest := c.instanceDir(p.name)
	if err := os.MkdirAll(c.labDir(), 0o755); err != nil {
		return instanceMeta{}, err
	}

	// 1. skeleton → instance dir.
	if err := copyTree(skeleton, dest); err != nil {
		return instanceMeta{}, fmt.Errorf("copy skeleton: %w", err)
	}

	// 2. Copy the docker build context into the instance as .docker/ so it stays
	// self-contained after the asset clone is discarded; compose builds the php
	// image relatively from ./.docker/php. Generic templates ride along in the
	// skeleton (skeleton/craft-<major>/templates/), served from /app/templates.
	if err := copyTree(filepath.Join(assets, "docker"), filepath.Join(dest, ".docker")); err != nil {
		return instanceMeta{}, fmt.Errorf("copy docker context: %w", err)
	}

	// 3. Rewrite composer.json (asset-packagist + path repo + require + platform).
	// The skeleton already ships the lab module's PSR-4 mapping, so mint leaves
	// autoload alone.
	pluginVersion := latestGitTag(c.pluginDir)
	if err := rewriteComposer(filepath.Join(dest, "composer.json"), c.pluginName, pluginVersion, p.craftPin, p.phpTag); err != nil {
		return instanceMeta{}, err
	}

	// 4. Render .env + compose.yaml from templates.
	key, err := securityKey()
	if err != nil {
		return instanceMeta{}, err
	}
	mapping := map[string]string{
		"{{PROJECT}}":        projectName(c.handle, p.name, c.pluginDir),
		"{{WEB_PORT}}":       strconv.Itoa(p.webPort),
		"{{MAILPIT_PORT}}":   strconv.Itoa(p.mailpitPort),
		"{{DB_BLOCK}}":       dbBlock(p.spec),
		"{{DB_SCHEMA_LINE}}": p.spec.schemaLine(),
		"{{DB_SERVER}}":      p.spec.service,
		"{{DB_DRIVER}}":      p.spec.driver,
		"{{DB_PORT}}":        p.spec.port,
		"{{DB_USER}}":        p.spec.user,
		"{{DB_PASSWORD}}":    p.spec.password,
		"{{DB_NAME}}":        "craft",
		"{{PHP_TAG}}":        p.phpTag,
		"{{NGINX_TAG}}":      "1.26",
		"{{UID}}":            strconv.Itoa(os.Getuid()),
		"{{GID}}":            strconv.Itoa(os.Getgid()),
		"{{APP_ID}}":         fmt.Sprintf("spark-craft-lab--%s--%s", c.handle, p.name),
		"{{SECURITY_KEY}}":   key,
	}
	for _, tpl := range []string{".env.tpl", "compose.yaml.tpl"} {
		if err := renderTemplate(filepath.Join(dest, tpl), mapping); err != nil {
			return instanceMeta{}, err
		}
	}

	// 5. Metadata.
	craftVersion := p.token
	meta := instanceMeta{
		Name:         p.name,
		Plugin:       c.pluginName,
		Handle:       c.handle,
		CraftMajor:   p.major,
		CraftVersion: craftVersion,
		PHPTag:       p.phpTag,
		DB:           p.spec.key,
		WebPort:      p.webPort,
		MailpitPort:  p.mailpitPort,
	}
	if err := writeMeta(dest, meta); err != nil {
		return instanceMeta{}, err
	}
	return meta, nil
}

// renderTemplate reads a .tpl file, substitutes the mapping, writes the result
// to the same path without the .tpl suffix, and removes the template.
func renderTemplate(path string, mapping map[string]string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	content := string(data)
	for k, v := range mapping {
		content = strings.ReplaceAll(content, k, v)
	}
	out := strings.TrimSuffix(path, ".tpl")
	if err := os.WriteFile(out, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Remove(path)
}

// composerVersionRe matches the shape Composer accepts as a `path` repo
// version: a 1–4 segment numeric core with an optional stability/build suffix
// (e.g. 3.2, 1.0.0, 2.4.1-beta.2). Tags that don't match — 2024.05, release-3,
// nightly — would make the path repo's `versions` pin invalid and fail late in
// composer install, so they fall back to 0.1.0.
var composerVersionRe = regexp.MustCompile(`^\d+(\.\d+){0,3}([-+][0-9A-Za-z.-]+)?$`)

// latestGitTag returns the plugin's latest git tag (leading v stripped) when it
// is a Composer-acceptable version, else the fallback 0.1.0, so the path repo's
// version pin always lets inter-package constraints resolve.
//
// `--abbrev=0` reports the most recent tag *reachable from HEAD*, so on a
// detached or behind checkout this can be an older release than the plugin's
// newest tag — acceptable, since the pin only needs to be a valid version that
// satisfies dependents, not the exact latest.
func latestGitTag(dir string) string {
	const fallback = "0.1.0"
	out, err := exec.Command("git", "-C", dir, "describe", "--tags", "--abbrev=0").Output()
	if err != nil {
		return fallback
	}
	tag := strings.TrimSpace(string(out))
	tag = strings.TrimPrefix(tag, "v")
	if !composerVersionRe.MatchString(tag) {
		return fallback
	}
	return tag
}

// securityKey returns a random 32-byte hex string for CRAFT_SECURITY_KEY.
func securityKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
