package lab

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/jalendport/spark-cli/internal/proc"
	"github.com/jalendport/spark-cli/internal/ui"
	"github.com/spf13/cobra"
)

// upCommand mints (if needed), boots, and seeds an instance.
func upCommand() *cobra.Command {
	var craft, db, php string
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Mint, boot, and seed a Craft instance for this plugin",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := resolveContext()
			if err != nil {
				return err
			}
			if err := ensureLabExcluded(c.pluginDir); err != nil {
				return err
			}

			token, major, pin, err := resolveVersion(craft)
			if err != nil {
				return err
			}
			spec, err := normalizeDB(db)
			if err != nil {
				return err
			}
			// major is validated to a supported Craft major in resolveVersion, so
			// phpByMajor always has an entry — no silent fallback for unknown majors.
			phpTag := php
			if phpTag == "" {
				phpTag = phpByMajor[major]
			}
			name := instanceName(token, spec)
			dest := c.instanceDir(name)

			// Minting serves /test/<handle>; the other lab commands operate on
			// already-minted instances and don't need a handle.
			if c.handle == "" {
				return fmt.Errorf("plugin composer.json is missing extra.handle (required to serve /test/<handle>)")
			}

			fresh := false
			meta, err := readMeta(dest)
			switch {
			case err == nil:
				// PHP isn't part of the instance name, so an explicit --php can
				// disagree with what the instance was minted with — say so
				// instead of silently booting the old one.
				if php != "" && php != meta.PHPTag {
					ui.Warnf("instance %s was minted with PHP %s — ignoring --php %s (run: spark lab destroy %s, then up, to change)",
						name, meta.PHPTag, php, name)
				}
				ui.Stepf("booting existing instance %s", name)
			case errors.Is(err, os.ErrNotExist):
				// New instance: clear any half-minted leftovers, resolve assets,
				// allocate ports, mint. A mint failure removes the dir again so
				// the next up starts clean.
				if _, statErr := os.Stat(dest); statErr == nil {
					ui.Warnf("removing incomplete instance dir %s", dest)
					if err := os.RemoveAll(dest); err != nil {
						return err
					}
				}
				fresh = true
				existing, err := listInstances(c.labDir())
				if err != nil {
					return err
				}
				// The registry keys reservations by instance dir and GCs entries
				// whose dir doesn't exist, so the dir must exist BEFORE the ports
				// are reserved — otherwise a concurrent `up` in another repo can
				// collect this reservation during the asset clone below.
				if err := os.MkdirAll(dest, 0o755); err != nil {
					return err
				}
				webPort, mailpitPort, err := allocatePorts(dest, existing)
				if err != nil {
					_ = os.Remove(dest)
					return err
				}
				assets, cleanup, err := resolveAssets()
				if err != nil {
					_ = os.RemoveAll(dest)
					return err
				}
				defer cleanup()

				ui.Stepf("minting %s (Craft %s, PHP %s, %s, port %d)", name, token, phpTag, spec.suffix, webPort)
				meta, err = mint(c, assets, mintParams{
					name: name, major: major, token: token, craftPin: pin,
					spec: spec, phpTag: phpTag, webPort: webPort, mailpitPort: mailpitPort,
				})
				if err != nil {
					_ = os.RemoveAll(dest)
					return err
				}
			default:
				return fmt.Errorf("instance %s exists but its metadata is unreadable (%v) — run: spark lab destroy %s", name, err, name)
			}

			if err := bootInstance(c, dest, &meta, fresh); err != nil {
				return err
			}
			if err := runSeed(dest); err != nil {
				return err
			}
			summarize(meta)
			return nil
		},
	}
	cmd.Flags().StringVar(&craft, "craft", "", "Craft version to pin (e.g. 5.10, 4.16, 5.4.3); latest stable when omitted")
	cmd.Flags().StringVar(&db, "db", "mysql", "database backend: mysql or pg")
	cmd.Flags().StringVar(&php, "php", "", "override the PHP image tag (e.g. 8.3)")
	return cmd
}

// listCommand prints this plugin's instances.
func listCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List this plugin's instances",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := resolveContext()
			if err != nil {
				return err
			}
			metas, err := listInstances(c.labDir())
			if err != nil {
				return err
			}
			if len(metas) == 0 {
				ui.Fprintln(os.Stdout, ui.Dim.Render("no instances — run: spark lab up"))
				return nil
			}
			for _, m := range metas {
				status := "stopped"
				if phpRunning(c.instanceDir(m.Name)) {
					status = "running"
				}
				line := fmt.Sprintf("%-16s http://localhost:%-5d  craft %-8s php %-4s %-6s %s",
					m.Name, m.WebPort, m.displayVersion(), m.PHPTag, dbSpecs[m.DB].driver, status)
				ui.Fprintln(os.Stdout, line)
			}
			return nil
		},
	}
}

// craftCommand proxies a Craft console command into an instance's php container.
func craftCommand() *cobra.Command {
	return &cobra.Command{
		Use:                "craft [name] <args...>",
		Short:              "Run a Craft console command inside an instance",
		DisableFlagParsing: true, // pass craft's own flags straight through
		RunE: func(_ *cobra.Command, args []string) error {
			// DisableFlagParsing means cobra won't parse the lab-level persistent
			// --plugin-dir for this subcommand, so pull it out of the passthrough
			// argv ourselves before the rest goes to Craft.
			args = extractPluginDir(args)
			c, err := resolveContext()
			if err != nil {
				return err
			}
			// The instance name is optional: a leading arg is treated as a name
			// only when it resolves to an existing instance, otherwise the whole
			// arg list is the Craft command and the sole instance is inferred.
			name, rest := "", args
			if len(args) > 0 {
				if _, statErr := os.Stat(c.instanceDir(args[0])); statErr == nil {
					name, rest = args[0], args[1:]
				}
			}
			name, err = resolveInstanceName(c, name)
			if err != nil {
				return err
			}
			if len(rest) == 0 {
				return fmt.Errorf("usage: spark lab craft [name] <command> [args...]")
			}
			cmd := composeCmd(c.instanceDir(name), craftArgs(interactiveTTY(), rest)...)
			cmd.Stdin = os.Stdin
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			ui.Echo("docker compose exec php php craft " + proc.Quote(rest))
			if err := cmd.Run(); err != nil {
				// Only a real child exit becomes a silent exit-code passthrough;
				// infrastructure failures (docker missing, exec errors) must
				// surface their message.
				var ee *exec.ExitError
				if errors.As(err, &ee) {
					return &proc.ExitError{Code: ee.ExitCode()}
				}
				return err
			}
			return nil
		},
	}
}

// seedCommand ensures an instance is up and re-runs the seeder.
func seedCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "seed [name]",
		Short: "Re-run the seed flow for an instance",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := resolveContext()
			if err != nil {
				return err
			}
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			name, err = resolveInstanceName(c, name)
			if err != nil {
				return err
			}
			dir := c.instanceDir(name)
			meta, err := readMeta(dir)
			if err != nil {
				return fmt.Errorf("no such instance %q — run: spark lab list", name)
			}
			if err := bootInstance(c, dir, &meta, false); err != nil {
				return err
			}
			if err := runSeed(dir); err != nil {
				return err
			}
			ui.Successf("reseeded %s: http://localhost:%d/test/%s", name, meta.WebPort, meta.Handle)
			return nil
		},
	}
}

// destroyCommand tears down one instance or all of them.
func destroyCommand() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "destroy [name]|--all",
		Short: "Tear down an instance (or all) and remove its .lab data",
		RunE: func(_ *cobra.Command, args []string) error {
			// Reject the ambiguous combination up front: a user typing
			// `destroy --all <name>` almost certainly didn't mean "everything".
			if all && len(args) > 0 {
				return fmt.Errorf("destroy takes a name or --all, not both")
			}
			c, err := resolveContext()
			if err != nil {
				return err
			}
			if all {
				metas, err := listInstances(c.labDir())
				if err != nil {
					return err
				}
				for _, m := range metas {
					if err := destroyInstance(c, m.Name); err != nil {
						return err
					}
				}
				if err := os.RemoveAll(c.labDir()); err != nil {
					return err
				}
				ui.Successf("destroyed all instances")
				return nil
			}
			if len(args) > 1 {
				return fmt.Errorf("usage: spark lab destroy [name] | --all")
			}
			// The name is optional: infer the sole instance when omitted.
			// resolveInstanceName gates on the directory, not the metadata, so a
			// half-minted instance (no lab.instance.json) can still be named and
			// cleaned up.
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			name, err = resolveInstanceName(c, name)
			if err != nil {
				return err
			}
			if err := destroyInstance(c, name); err != nil {
				return err
			}
			// Remove .lab/ when the last instance is gone.
			if entries, err := os.ReadDir(c.labDir()); err == nil && len(entries) == 0 {
				_ = os.Remove(c.labDir())
			}
			ui.Successf("destroyed %s", name)
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "destroy every instance and remove .lab/")
	return cmd
}

// destroyInstance stops and removes an instance's containers/volumes and its
// on-disk directory.
func destroyInstance(c *ctx, name string) error {
	dir := c.instanceDir(name)
	ui.Stepf("tearing down %s", name)
	if _, err := captureCompose(dir, "down", "-v", "--remove-orphans"); err != nil {
		// Log but continue — the containers may already be gone; we still want
		// the directory removed.
		ui.Warnf("compose down for %s reported: %v", name, err)
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	// Release the port reservation promptly rather than waiting for the next
	// allocation's GC to notice the dir is gone.
	releasePorts(dir)
	return nil
}

// summarize prints the friendly post-up status block.
func summarize(meta instanceMeta) {
	ui.Successf("%s is up", meta.Name)
	base := fmt.Sprintf("http://localhost:%d", meta.WebPort)
	lines := []string{
		fmt.Sprintf("  site:    %s", base),
		fmt.Sprintf("  cp:      %s/admin  (%s / %s)", base, adminUsername, adminPassword),
		fmt.Sprintf("  test:    %s/test/%s", base, meta.Handle),
		fmt.Sprintf("  mailpit: http://localhost:%d", meta.MailpitPort),
		fmt.Sprintf("  craft:   spark lab craft %s <command>", meta.Name),
	}
	for _, l := range lines {
		ui.Fprintln(os.Stdout, ui.Dim.Render(l))
	}
}

// resolveInstanceName resolves the instance a name-optional command targets.
// With a non-empty name it confirms the instance dir exists (gating on the
// directory, not the metadata, so half-minted instances can still be named);
// with an empty name it infers the sole instance, erroring when zero or many
// exist so the target is never ambiguous.
func resolveInstanceName(c *ctx, name string) (string, error) {
	if name != "" {
		if _, err := os.Stat(c.instanceDir(name)); err != nil {
			return "", fmt.Errorf("no such instance %q — run: spark lab list", name)
		}
		return name, nil
	}
	metas, err := listInstances(c.labDir())
	if err != nil {
		return "", err
	}
	switch len(metas) {
	case 0:
		return "", fmt.Errorf("no instances — run: spark lab up")
	case 1:
		return metas[0].Name, nil
	default:
		names := make([]string, len(metas))
		for i, m := range metas {
			names[i] = m.Name
		}
		return "", fmt.Errorf("multiple instances — name one explicitly: %s", strings.Join(names, ", "))
	}
}

// pruneCommand reclaims cross-instance lab residue that teardown leaves behind:
// the shared composer cache volume (ENGINE.md §6/§12) and any orphaned lab-*
// compose projects — containers/networks/volumes whose compose file is gone
// (e.g. a .lab dir deleted by hand instead of `destroy`).
func pruneCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "prune",
		Short: "Remove the shared composer cache volume and orphaned lab projects",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			pruneOrphanProjects()

			ui.Stepf("removing composer cache volume %s", composerCacheVolume)
			out, err := exec.Command("docker", "volume", "rm", composerCacheVolume).CombinedOutput()
			if err == nil {
				ui.Successf("removed composer cache volume %s", composerCacheVolume)
				return nil
			}
			s := strings.ToLower(string(out))
			switch {
			case strings.Contains(s, "no such volume"):
				ui.Stepf("composer cache volume already gone")
			case strings.Contains(s, "in use"):
				ui.Warnf("composer cache volume is still in use — destroy running instances first, then prune")
			default:
				ui.Warnf("could not remove composer cache volume: %v\n%s", err, out)
			}
			return nil
		},
	}
}

// pruneOrphanProjects tears down any compose project named lab-* whose compose
// file no longer exists on disk. Best-effort: docker errors are swallowed since
// prune is a cleanup convenience.
func pruneOrphanProjects() {
	out, err := exec.Command("docker", "compose", "ls", "--all", "--format", "json").Output()
	if err != nil {
		return
	}
	var projects []struct {
		Name        string `json:"Name"`
		ConfigFiles string `json:"ConfigFiles"`
	}
	if err := json.Unmarshal(out, &projects); err != nil {
		return
	}
	for _, p := range projects {
		if !strings.HasPrefix(p.Name, "lab-") {
			continue
		}
		if p.ConfigFiles == "" {
			continue // can't attribute a compose file — don't guess on a destructive op
		}
		if anyPathExists(p.ConfigFiles) {
			continue // still backed by a live instance dir — leave it alone
		}
		ui.Stepf("removing orphaned lab project %s", p.Name)
		removeComposeProject(p.Name)
	}
}

// anyPathExists reports whether any of docker's comma-separated ConfigFiles
// paths still exists.
func anyPathExists(configFiles string) bool {
	for _, p := range strings.Split(configFiles, ",") {
		if p = strings.TrimSpace(p); p != "" {
			if _, err := os.Stat(p); err == nil {
				return true
			}
		}
	}
	return false
}

// removeComposeProject removes every container, network, and volume labeled with
// the given compose project name — a teardown that works even when the project's
// compose file is gone (so `compose down` no longer would).
func removeComposeProject(name string) {
	label := "label=com.docker.compose.project=" + name
	for _, step := range []struct {
		list []string
		rm   []string
	}{
		{[]string{"ps", "-aq", "--filter", label}, []string{"rm", "-f"}},
		{[]string{"network", "ls", "-q", "--filter", label}, []string{"network", "rm"}},
		{[]string{"volume", "ls", "-q", "--filter", label}, []string{"volume", "rm"}},
	} {
		out, err := exec.Command("docker", step.list...).Output()
		if err != nil {
			continue
		}
		for _, id := range strings.Fields(string(out)) {
			_ = exec.Command("docker", append(append([]string{}, step.rm...), id)...).Run()
		}
	}
}

// extractPluginDir pulls a --plugin-dir flag (space- or =-separated) out of a
// passthrough argv, setting pluginDirFlag and returning the remaining args. Used
// by `lab craft`, whose DisableFlagParsing otherwise sends --plugin-dir to Craft.
func extractPluginDir(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--plugin-dir":
			if i+1 < len(args) {
				pluginDirFlag = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "--plugin-dir="):
			pluginDirFlag = strings.TrimPrefix(a, "--plugin-dir=")
		default:
			out = append(out, a)
		}
	}
	return out
}

// interactiveTTY reports whether both stdin and stdout are terminals, so `lab
// craft` only asks docker for a tty when one can actually be allocated (a piped
// stdin with -t would fail with "the input device is not a TTY").
func interactiveTTY() bool {
	for _, f := range []*os.File{os.Stdin, os.Stdout} {
		info, err := f.Stat()
		if err != nil || info.Mode()&os.ModeCharDevice == 0 {
			return false
		}
	}
	return true
}
