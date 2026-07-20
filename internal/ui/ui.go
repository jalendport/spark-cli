// Package ui centralizes all styled terminal output so the look stays
// consistent and lives in one place. Styles are adaptive: they resolve to
// sensible colors on both light and dark terminals.
package ui

import (
	"fmt"
	"io"
	"os"

	"github.com/charmbracelet/lipgloss"
)

var (
	// Header is the project name shown at the top of help output.
	Header = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "#7C3AED", Dark: "#C4B5FD"})

	// Description dims the free-form project description.
	Description = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#9CA3AF"})

	// Section labels a group in help output (e.g. "COMMANDS").
	Section = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "#374151", Dark: "#E5E7EB"})

	// CommandName styles a command's name in the aligned help list.
	CommandName = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#2563EB", Dark: "#93C5FD"})

	// CommandHelp styles the description column of the help list.
	CommandHelp = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#374151", Dark: "#D1D5DB"})

	// Dim is used for aliases and other secondary detail.
	Dim = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#9CA3AF", Dark: "#6B7280"})

	// echoStyle prints the command about to run, e.g. → docker compose up -d.
	echoStyle = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#9CA3AF", Dark: "#6B7280"})

	// warnStyle and errStyle prefix advisory and failure messages.
	warnStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "#B45309", Dark: "#FBBF24"})
	errStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "#B91C1C", Dark: "#FCA5A5"})

	// successStyle marks a completed action (e.g. an instance booting).
	successStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "#047857", Dark: "#6EE7B7"})
)

// Echo prints the dim "→ <command>" line before a command is executed.
// It goes to stderr so it never pollutes piped command output.
func Echo(command string) {
	fmt.Fprintln(os.Stderr, echoStyle.Render("→ "+command))
}

// Warnf prints a styled warning to stderr (used for forward-compatible
// manifest keys we don't recognize yet).
func Warnf(format string, a ...any) {
	fmt.Fprintln(os.Stderr, warnStyle.Render("warning:")+" "+fmt.Sprintf(format, a...))
}

// Errorf prints a styled error to stderr.
func Errorf(format string, a ...any) {
	fmt.Fprintln(os.Stderr, errStyle.Render("error:")+" "+fmt.Sprintf(format, a...))
}

// Fprintln writes a plain line to the given writer; a thin wrapper so callers
// don't import fmt just for help rendering.
func Fprintln(w io.Writer, s string) {
	fmt.Fprintln(w, s)
}

// Stepf prints a dim progress line to stderr so it stays out of piped stdout.
func Stepf(format string, a ...any) {
	fmt.Fprintln(os.Stderr, Dim.Render("• "+fmt.Sprintf(format, a...)))
}

// Successf prints a styled success line to stdout.
func Successf(format string, a ...any) {
	fmt.Fprintln(os.Stdout, successStyle.Render("✓")+" "+fmt.Sprintf(format, a...))
}
