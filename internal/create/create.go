// Package create implements the global `spark create` command: scaffolding a
// new project from a published boilerplate. It fetches the boilerplate registry,
// downloads the chosen repo as a tarball from codeload.github.com, extracts it
// stripping the top-level directory, then runs the boilerplate's spark.yml
// create: section — prompts, renames, token replacement, post commands, and a
// fresh git repo.
package create

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/jalendport/spark-cli/internal/manifest"
	"github.com/jalendport/spark-cli/internal/ui"
	"github.com/spf13/cobra"
)

// NewCommand builds the `create` cobra command. It is a global built-in: it runs
// outside any project to scaffold a new one.
func NewCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "create [boilerplate] [directory]",
		Short: "Scaffold a new project from a boilerplate",
		Args:  cobra.MaximumNArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			var boilerplate, directory string
			if len(args) > 0 {
				boilerplate = args[0]
			}
			if len(args) > 1 {
				directory = args[1]
			}
			return Run(boilerplate, directory)
		},
	}
}

// Run performs the full scaffold. With no boilerplate name it shows a picker;
// otherwise it resolves the name against the registry. With no directory it
// prompts for a project name and slugifies it.
func Run(boilerplate, directory string) error {
	rd := bufio.NewReader(os.Stdin)

	reg, err := fetchRegistry()
	if err != nil {
		return err
	}

	var bp Boilerplate
	if boilerplate != "" {
		b, ok := reg.find(boilerplate)
		if !ok {
			return fmt.Errorf("no boilerplate named %q — available: %s", boilerplate, strings.Join(reg.names(), ", "))
		}
		bp = b
	} else {
		b, err := pick(reg, rd, os.Stdout)
		if err != nil {
			return err
		}
		bp = b
	}

	target := directory
	if target == "" {
		name, err := promptLine(rd, os.Stdout, "Project name")
		if err != nil {
			return err
		}
		target = slugify(name)
		if target == "" {
			return fmt.Errorf("a project name is required to name the new directory")
		}
	}

	abs, err := filepath.Abs(target)
	if err != nil {
		return err
	}

	// Assemble everything in a temporary sibling directory and move it into place
	// only once every step succeeds, so any failure leaves nothing half-built
	// behind (and a pre-existing empty destination untouched). The manifest is
	// captured here so the setup: commands and the final summary can consult it
	// after the build closure returns.
	var m *manifest.Manifest
	err = buildInto(abs, func(dir string) error {
		ui.Stepf("downloading %s", bp.Repo)
		if err := downloadBoilerplate(bp.Repo, dir); err != nil {
			return err
		}
		loaded, err := manifest.Load(dir)
		if err != nil {
			return fmt.Errorf("the boilerplate has no readable %s: %w", manifest.Filename, err)
		}
		m = loaded
		return setupProject(dir, m, rd, os.Stdout)
	})
	if err != nil {
		return err
	}

	// Setup commands run only now that the project sits in its final home: they
	// may start docker compose, whose project name and bind-mount paths derive
	// from the working directory, so they must never see the temporary build
	// dir. A failure prints its own notice and falls through to the summary with
	// the unfinished commands, but is still returned so the exit code rides up.
	setup := setupCommands(m)
	outcome := setupOutcome{hasSetup: len(setup) > 0}
	var runErr error
	switch {
	case outcome.hasSetup && confirm(rd, os.Stdout, "Set up the project now? [Y/n]"):
		if remaining, err := runSetup(abs, setup); err != nil {
			outcome.remaining = remaining
			ui.Errorf("setup did not finish — run the remaining steps below by hand")
			runErr = err
		}
	case outcome.hasSetup:
		// Declined: every setup command becomes a manual next step.
		outcome.remaining = setup
	}

	summary(bp, target, outcome)
	return runErr
}

// setupCommands returns the manifest's create: setup commands, or nil when the
// boilerplate declares none.
func setupCommands(m *manifest.Manifest) []string {
	if m == nil || m.Create == nil {
		return nil
	}
	return m.Create.Setup
}

// runSetup runs the setup commands from the final project directory dir. On the
// first failure it returns the commands still needing a hand — the one that
// failed and everything after it — alongside the error (a proc.ExitError
// carrying the child's code), so the caller can both print an accurate NEXT
// STEPS block and propagate the exit code. On success it returns (nil, nil).
func runSetup(dir string, setup []string) (remaining []string, err error) {
	if i, err := runScripts(dir, setup); err != nil {
		return setup[i:], err
	}
	return nil, nil
}

// setupProject runs the manifest's create: section (when present) against the
// freshly extracted project at root, then always initializes a git repo — even
// for a boilerplate with no create: section.
func setupProject(root string, m *manifest.Manifest, rd *bufio.Reader, out io.Writer) error {
	if m.Create != nil {
		if err := executeCreate(root, m.Create, rd, out); err != nil {
			return err
		}
	}
	return gitInit(root)
}

// buildInto validates abs as a fresh project directory, then runs fn against a
// temporary sibling directory and renames it into place only if fn succeeds. On
// any failure the temporary directory is removed and abs is left untouched, so a
// pre-existing empty destination survives.
func buildInto(abs string, fn func(dir string) error) error {
	preExisting, err := checkEmptyDir(abs)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(abs), ".spark-create-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp) // a no-op once we've renamed tmp into place

	if err := fn(tmp); err != nil {
		return err
	}

	if preExisting {
		// Clear the empty destination so the rename can take its place.
		if err := os.Remove(abs); err != nil {
			return err
		}
	}
	return os.Rename(tmp, abs)
}

// pick renders the styled boilerplate list and reads a numbered selection.
func pick(reg Registry, rd *bufio.Reader, out io.Writer) (Boilerplate, error) {
	ui.Fprintln(out, ui.Section.Render("BOILERPLATES"))
	width := 0
	for _, b := range reg.Boilerplates {
		if len(b.Name) > width {
			width = len(b.Name)
		}
	}
	for i, b := range reg.Boilerplates {
		num := ui.Dim.Render(fmt.Sprintf("%d)", i+1))
		pad := strings.Repeat(" ", width-len(b.Name))
		ui.Fprintln(out, "  "+num+" "+ui.CommandName.Render(b.Name)+pad+"   "+ui.CommandHelp.Render(b.Description))
	}
	ui.Fprintln(out, "")

	choice, err := promptLine(rd, out, fmt.Sprintf("Select a boilerplate [1-%d]", len(reg.Boilerplates)))
	if err != nil {
		return Boilerplate{}, err
	}
	n, err := strconv.Atoi(choice)
	if err != nil || n < 1 || n > len(reg.Boilerplates) {
		return Boilerplate{}, fmt.Errorf("invalid selection %q", choice)
	}
	return reg.Boilerplates[n-1], nil
}

// promptLine prints a styled prompt and reads one trimmed line of input.
func promptLine(rd *bufio.Reader, out io.Writer, label string) (string, error) {
	fmt.Fprint(out, ui.CommandName.Render(label)+ui.Dim.Render(" › "))
	line, err := rd.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// confirm prints a styled yes/no prompt and reads one line, defaulting to yes.
// A read failure — including EOF on a closed or empty stdin — is treated as a
// decline rather than an error, so an unattended run never blocks or crashes on
// the optional setup step; it simply skips it.
func confirm(rd *bufio.Reader, out io.Writer, label string) bool {
	fmt.Fprint(out, ui.CommandName.Render(label)+ui.Dim.Render(" › "))
	line, err := rd.ReadString('\n')
	if err != nil && err != io.EOF {
		return false
	}
	// Distinguish a deliberate blank answer (Enter → "\n", defaults to yes) from
	// input that ran out before anything was typed (EOF with nothing → decline).
	if err == io.EOF && strings.TrimSpace(line) == "" {
		return false
	}
	return parseConfirm(line)
}

// parseConfirm reads a yes/no answer where a blank line defaults to yes. It
// accepts y/yes in any case as yes and treats everything else as no. Kept pure
// so the confirm logic is testable without a reader.
func parseConfirm(answer string) bool {
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "", "y", "yes":
		return true
	default:
		return false
	}
}

// checkEmptyDir validates target as a fresh project directory without creating
// anything: it must be absent or an existing empty directory (a non-empty one is
// refused). It reports whether target already exists so the caller can preserve
// it on failure.
func checkEmptyDir(target string) (exists bool, err error) {
	entries, err := os.ReadDir(target)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if len(entries) > 0 {
		return false, fmt.Errorf("%s already exists and is not empty", target)
	}
	return true, nil
}

// setupOutcome records what became of a boilerplate's setup: commands so the
// summary can print accurate next steps. A boilerplate with no setup: section
// is the zero value (hasSetup false).
type setupOutcome struct {
	hasSetup  bool     // the manifest declared setup commands
	remaining []string // commands still to run by hand: all when declined, the failing tail after an error, none on success
}

// nextSteps returns the NEXT STEPS command lines for dir given the setup
// outcome. Kept pure so the selection is testable without any terminal:
//
//   - no setup section: cd then `spark up` (nothing has started the stack).
//   - setup fully succeeded: just cd (setup already started the stack, so
//     printing `spark up` would be wrong).
//   - setup declined or partially failed: cd then every command still left to
//     run, so the manifest list doubles as copy-paste recovery instructions.
func nextSteps(dir string, o setupOutcome) []string {
	steps := []string{"cd " + dir}
	if !o.hasSetup {
		return append(steps, "spark up")
	}
	return append(steps, o.remaining...)
}

// summary prints the styled success block with the next steps derived from the
// setup outcome. The project is on disk in every path this runs, so the created
// line always prints; any setup failure has already announced itself via
// ui.Errorf before this point.
func summary(bp Boilerplate, dir string, o setupOutcome) {
	ui.Fprintln(os.Stdout, "")
	ui.Successf("created %s in %s", bp.Name, dir)
	ui.Fprintln(os.Stdout, "")
	ui.Fprintln(os.Stdout, ui.Section.Render("NEXT STEPS"))
	for _, step := range nextSteps(dir, o) {
		ui.Fprintln(os.Stdout, "  "+ui.CommandName.Render(step))
	}
}

var slugNonWord = regexp.MustCompile(`[^a-z0-9]+`)

// slugify lowercases s and collapses runs of non-alphanumeric characters into
// single hyphens, trimming leading/trailing hyphens.
func slugify(s string) string {
	s = slugNonWord.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "-")
	return strings.Trim(s, "-")
}
