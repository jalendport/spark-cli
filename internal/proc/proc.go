// Package proc holds the tiny helpers shared by every command that runs a
// child process: the exit-code error that rides up to main, and shell quoting
// for faithful command echoes.
package proc

import (
	"errors"
	"strings"
)

// ExitError carries a child process's exit code up to Execute so the binary
// can pass it through unchanged. It renders as an empty string because the
// child already wrote its own error output.
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return "" }

// Code extracts the process exit code from an error. Returns (code, true) for
// an ExitError, else (0, false).
func Code(err error) (int, bool) {
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code, true
	}
	return 0, false
}

// Quote POSIX-quotes each arg and joins them with spaces so a shell (or a
// human copy-pasting an echoed command) sees exactly the original tokens.
func Quote(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = quoteOne(a)
	}
	return strings.Join(parts, " ")
}

// quoteOne wraps a value in single quotes, escaping any embedded single
// quotes the standard POSIX way ('\”).
func quoteOne(s string) string {
	if s == "" {
		return "''"
	}
	// Fast path: nothing that needs quoting.
	if !strings.ContainsAny(s, " \t\n'\"\\$`&|;<>()*?![]{}#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
