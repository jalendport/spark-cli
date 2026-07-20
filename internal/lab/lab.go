// Package lab implements the built-in `spark lab` command group: a native Go
// engine that mints, boots, seeds, and tears down fully self-contained,
// disposable Craft CMS instances for testing the plugin in the current repo.
//
// It runs from inside a Craft plugin working copy (detected by walking up for a
// composer.json with "type": "craft-plugin"), so it is a global command group —
// available regardless of whether a spark.yml project is present. All state
// lives under <plugin>/.lab/<name>/. The Craft skeletons, docker build context,
// generic templates, and in-Craft "lab" module come from an asset bundle
// (github.com/jalendport/spark-craft-lab). There is no persistent cache: `up`
// shallow-clones the bundle into a temp dir, copies everything each instance
// needs into .lab/<name>/, and discards the clone — so instances are fully
// self-contained and list/craft/seed/destroy never touch the assets. Setting
// SPARK_LAB_ASSETS to a local working copy skips the clone (for developing the
// assets themselves). See that repo's ENGINE.md for the contract.
package lab

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// ctx is the resolved run context for a lab command: the plugin under test and
// the asset bundle backing the engine.
type ctx struct {
	pluginDir  string // absolute path to the plugin working copy
	pluginName string // composer package name, e.g. jalendport/craft-metronome
	handle     string // plugin handle from composer extra.handle
}

func (c *ctx) labDir() string              { return filepath.Join(c.pluginDir, ".lab") }
func (c *ctx) instanceDir(n string) string { return filepath.Join(c.labDir(), n) }

// pluginDirFlag holds the value of the persistent --plugin-dir override.
var pluginDirFlag string

// NewCommand builds the `lab` cobra command group. It is added to the root
// command unconditionally (lab runs in plugin repos, which have no spark.yml).
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lab",
		Short: "Mint and manage disposable Craft instances for the current plugin",
		Long:  "Mint, boot, seed, and tear down self-contained Craft CMS instances for testing the plugin in the current repo.",
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.PersistentFlags().StringVar(&pluginDirFlag, "plugin-dir", "", "path to the plugin working copy (overrides detection)")
	// The root command installs a custom help func that propagates to children;
	// restore cobra's own usage output for the lab subtree so `spark lab --help`
	// and `spark lab <cmd> --help` list subcommands and flags.
	cmd.SetHelpFunc(func(c *cobra.Command, _ []string) { _ = c.Usage() })

	cmd.AddCommand(upCommand())
	cmd.AddCommand(listCommand())
	cmd.AddCommand(craftCommand())
	cmd.AddCommand(seedCommand())
	cmd.AddCommand(destroyCommand())
	cmd.AddCommand(pruneCommand())
	return cmd
}

// resolveContext detects the plugin repo. Only `up` additionally resolves the
// asset bundle; list/craft/seed/destroy operate purely on already-minted
// instances under .lab/ and never touch the assets.
func resolveContext() (*ctx, error) {
	dir, name, handle, err := detectPlugin(pluginDirFlag)
	if err != nil {
		return nil, err
	}
	return &ctx{pluginDir: dir, pluginName: name, handle: handle}, nil
}

// detectPlugin walks up from the CWD (or the --plugin-dir override) for the
// first composer.json with "type": "craft-plugin", returning its directory,
// package name, and handle.
func detectPlugin(override string) (dir, name, handle string, err error) {
	start := override
	if start == "" {
		start, err = os.Getwd()
		if err != nil {
			return "", "", "", err
		}
	}
	start, err = filepath.Abs(start)
	if err != nil {
		return "", "", "", err
	}

	found := ""
	cur := start
	for {
		cj := filepath.Join(cur, "composer.json")
		if data, e := os.ReadFile(cj); e == nil {
			meta, e := parseComposer(data)
			if e == nil && stringField(meta, "type") == "craft-plugin" {
				found = cur
				break
			}
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	if found == "" {
		return "", "", "", fmt.Errorf(
			"not inside a Craft plugin repo — run this from a plugin working copy (a composer.json with \"type\": \"craft-plugin\"), or pass --plugin-dir")
	}

	data, err := os.ReadFile(filepath.Join(found, "composer.json"))
	if err != nil {
		return "", "", "", err
	}
	meta, err := parseComposer(data)
	if err != nil {
		return "", "", "", fmt.Errorf("parse %s/composer.json: %w", found, err)
	}
	name = stringField(meta, "name")
	if name == "" {
		return "", "", "", fmt.Errorf("plugin composer.json is missing a package name")
	}
	// A missing extra.handle is not an error here: only `up` needs it (to mint
	// and serve /test/<handle>). list/destroy/prune must keep working so
	// existing instances can always be inspected and cleaned up.
	return found, name, pluginHandle(meta), nil
}

// ensureLabExcluded appends a ".lab/" line to the plugin repo's
// .git/info/exclude (never its tracked .gitignore) so minted instances stay
// invisible to git. No-ops when already present or when the dir isn't a git repo.
func ensureLabExcluded(pluginDir string) error {
	out, err := exec.Command("git", "-C", pluginDir, "rev-parse", "--git-path", "info/exclude").Output()
	if err != nil {
		return nil // not a git repo (or git missing) — nothing to exclude
	}
	p := strings.TrimSpace(string(out))
	if !filepath.IsAbs(p) {
		p = filepath.Join(pluginDir, p)
	}

	if data, err := os.ReadFile(p); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(line) == ".lab/" {
				return nil
			}
		}
	}

	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	// Ensure the appended line starts on its own row.
	if info, err := f.Stat(); err == nil && info.Size() > 0 {
		if _, err := f.WriteString("\n"); err != nil {
			return err
		}
	}
	_, err = f.WriteString(".lab/\n")
	return err
}
