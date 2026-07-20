package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jalendport/spark-cli/internal/ui"
)

// helpEntry is one row in the aligned command list.
type helpEntry struct {
	name    string
	aliases []string
	help    string
}

// renderHelp writes the styled help screen: a project header, usage line, and
// aligned command list including any manifest-defined commands.
func (a *app) renderHelp() {
	w := os.Stdout

	title := "spark"
	if a.inProject && a.manifest.Name != "" {
		title = a.manifest.Name
	}
	ui.Fprintln(w, ui.Header.Render(title)+" "+ui.Dim.Render(a.version))

	if a.inProject && a.manifest.Description != "" {
		ui.Fprintln(w, ui.Description.Render(a.manifest.Description))
	} else if !a.inProject {
		ui.Fprintln(w, ui.Description.Render("A generic engine for Spark boilerplate projects."))
	}

	ui.Fprintln(w, "")
	ui.Fprintln(w, ui.Section.Render("USAGE"))
	ui.Fprintln(w, "  spark <command> [args]")

	if a.inProject {
		ui.Fprintln(w, "")
		ui.Fprintln(w, ui.Section.Render("COMMANDS"))
		renderEntries(w, a.projectEntries())
	}

	ui.Fprintln(w, "")
	ui.Fprintln(w, ui.Section.Render("GLOBAL"))
	renderEntries(w, globalEntries())

	if !a.inProject {
		ui.Fprintln(w, "")
		ui.Fprintln(w, ui.Dim.Render("Not in a spark project — only global commands are available here."))
	}
}

// projectEntries lists the built-in verbs (respecting manifest overrides)
// followed by any additional manifest commands, in a stable order.
func (a *app) projectEntries() []helpEntry {
	var entries []helpEntry
	overridden := map[string]bool{}
	manifestNames := map[string]bool{}
	for _, mc := range a.manifest.Commands {
		manifestNames[mc.Name] = true
	}

	for _, b := range builtins {
		// Mirror the routing in addProjectCommands: aliases claimed by a
		// manifest command aren't reachable as aliases, so don't advertise them.
		e := helpEntry{name: b.name, aliases: freeAliases(b.aliases, manifestNames), help: b.help}
		if mc, ok := a.manifest.Lookup(b.name); ok {
			overridden[b.name] = true
			if mc.Help != "" {
				e.help = mc.Help
			}
		}
		entries = append(entries, e)
	}

	for _, mc := range a.manifest.Commands {
		if overridden[mc.Name] {
			continue // already shown in its built-in slot
		}
		help := mc.Help
		if help == "" {
			help = mc.Run
		}
		entries = append(entries, helpEntry{name: mc.Name, help: help})
	}
	return entries
}

func globalEntries() []helpEntry {
	return []helpEntry{
		{name: "lab", help: "Mint disposable Craft instances for the current plugin"},
		{name: "create", help: "Scaffold a new project from a boilerplate"},
		{name: "version", help: "Print the spark version"},
		{name: "help", help: "Show this help"},
	}
}

// renderEntries prints an aligned two-column list with dim aliases.
func renderEntries(w io.Writer, entries []helpEntry) {
	width := 0
	for _, e := range entries {
		if len(e.name) > width {
			width = len(e.name)
		}
	}
	for _, e := range entries {
		line := "  " + ui.CommandName.Render(e.name) + strings.Repeat(" ", width-len(e.name)) + "   " + ui.CommandHelp.Render(e.help)
		if len(e.aliases) > 0 {
			line += "  " + ui.Dim.Render("("+strings.Join(e.aliases, ", ")+")")
		}
		fmt.Fprintln(w, line)
	}
}
