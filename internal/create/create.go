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
	// behind (and a pre-existing empty destination untouched).
	err = buildInto(abs, func(dir string) error {
		ui.Stepf("downloading %s", bp.Repo)
		if err := downloadBoilerplate(bp.Repo, dir); err != nil {
			return err
		}
		m, err := manifest.Load(dir)
		if err != nil {
			return fmt.Errorf("the boilerplate has no readable %s: %w", manifest.Filename, err)
		}
		return setupProject(dir, m, rd, os.Stdout)
	})
	if err != nil {
		return err
	}

	summary(bp, target)
	return nil
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

// summary prints the styled success block with the next steps.
func summary(bp Boilerplate, dir string) {
	ui.Fprintln(os.Stdout, "")
	ui.Successf("created %s in %s", bp.Name, dir)
	ui.Fprintln(os.Stdout, "")
	ui.Fprintln(os.Stdout, ui.Section.Render("NEXT STEPS"))
	ui.Fprintln(os.Stdout, "  "+ui.CommandName.Render("cd "+dir))
	ui.Fprintln(os.Stdout, "  "+ui.CommandName.Render("spark up"))
}

var slugNonWord = regexp.MustCompile(`[^a-z0-9]+`)

// slugify lowercases s and collapses runs of non-alphanumeric characters into
// single hyphens, trimming leading/trailing hyphens.
func slugify(s string) string {
	s = slugNonWord.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "-")
	return strings.Trim(s, "-")
}
