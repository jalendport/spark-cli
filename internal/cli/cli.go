package cli

import (
	"fmt"
	"os"

	"github.com/jalendport/spark-cli/internal/create"
	"github.com/jalendport/spark-cli/internal/lab"
	"github.com/jalendport/spark-cli/internal/manifest"
	"github.com/jalendport/spark-cli/internal/proc"
	"github.com/jalendport/spark-cli/internal/ui"
	"github.com/spf13/cobra"
)

// app holds the resolved run context: the version string, and — when the CWD
// is inside a project — the project root and its loaded manifest.
type app struct {
	version   string
	inProject bool
	root      string
	manifest  *manifest.Manifest
}

// Execute is the binary entry point. It detects the project, builds the
// command tree, and returns the process exit code (passed through from any
// underlying command).
func Execute(version string) int {
	a := &app{version: version}

	cwd, err := os.Getwd()
	if err != nil {
		ui.Errorf("%v", err)
		return 1
	}

	if root, ok := manifest.FindRoot(cwd); ok {
		m, err := manifest.Load(root)
		if err != nil {
			// A broken manifest shouldn't take down the global commands: warn,
			// then behave as if outside a project.
			ui.Warnf("ignoring broken manifest: %v", err)
		} else {
			for _, k := range m.UnknownKeys {
				ui.Warnf("unknown key %q in %s (ignored)", k, manifest.Filename)
			}
			a.inProject = true
			a.root = root
			a.manifest = m
		}
	}

	if err := a.build().Execute(); err != nil {
		if code, ok := proc.Code(err); ok {
			return code
		}
		ui.Errorf("%v", err)
		return 1
	}
	return 0
}

// build assembles the cobra command tree from globals, built-in verbs, and
// manifest commands. Manifest commands override same-named built-ins.
func (a *app) build() *cobra.Command {
	root := &cobra.Command{
		Use:           "spark",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.ArbitraryArgs, // let unknown args reach RunE for a friendly message
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				a.renderHelp()
				return nil
			}
			if !a.inProject {
				ui.Errorf("not in a spark project")
				ui.Fprintln(os.Stderr, ui.Dim.Render("Run 'spark create' to scaffold one, or cd into a project directory."))
			} else {
				ui.Errorf("unknown command %q — run 'spark help' to see what's available", args[0])
			}
			return &proc.ExitError{Code: 1}
		},
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetHelpFunc(func(_ *cobra.Command, _ []string) { a.renderHelp() })

	root.AddCommand(&cobra.Command{
		Use: "version",
		RunE: func(_ *cobra.Command, _ []string) error {
			fmt.Fprintln(os.Stdout, "spark "+a.version)
			return nil
		},
	})
	root.AddCommand(create.NewCommand())

	// `lab` is a global built-in: it runs inside Craft plugin repos (which have
	// no spark.yml), so it is available regardless of project detection.
	root.AddCommand(lab.NewCommand())

	if a.inProject {
		a.addProjectCommands(root)
	}
	return root
}

// addProjectCommands wires the built-in verbs and manifest commands, letting a
// manifest command replace the built-in that shares its name.
func (a *app) addProjectCommands(root *cobra.Command) {
	overridden := map[string]bool{}
	for _, mc := range a.manifest.Commands {
		overridden[mc.Name] = true
	}

	for _, b := range builtins {
		if overridden[b.name] {
			continue
		}
		b := b
		root.AddCommand(&cobra.Command{
			Use: b.name,
			// A built-in must surrender any alias a manifest command claims as
			// its name — cobra resolves aliases in insertion order, so keeping
			// it would make the manifest command unreachable.
			Aliases:            freeAliases(b.aliases, overridden),
			DisableFlagParsing: true, // pass flags like -d straight through
			RunE: func(_ *cobra.Command, args []string) error {
				return b.run(a.root, args)
			},
		})
	}

	for _, mc := range a.manifest.Commands {
		mc := mc
		root.AddCommand(&cobra.Command{
			Use: mc.Name,
			// Keep aliases when overriding a built-in, minus any claimed by
			// another manifest command.
			Aliases:            freeAliases(builtinAliases(mc.Name), overridden),
			DisableFlagParsing: true,
			RunE: func(_ *cobra.Command, args []string) error {
				return runShell(a.root, applyArgs(mc.Run, args))
			},
		})
	}
}

// builtinAliases returns the aliases of the built-in verb with the given name,
// so a manifest override inherits them (e.g. overriding `up` keeps u/start).
func builtinAliases(name string) []string {
	for _, b := range builtins {
		if b.name == name {
			return b.aliases
		}
	}
	return nil
}

// freeAliases returns aliases minus any name in taken, so a command never
// carries an alias that shadows a real command.
func freeAliases(aliases []string, taken map[string]bool) []string {
	var out []string
	for _, a := range aliases {
		if !taken[a] {
			out = append(out, a)
		}
	}
	return out
}
