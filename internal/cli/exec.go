package cli

import (
	"errors"
	"os"
	"os/exec"
	"strings"

	"github.com/jalendport/spark-cli/internal/proc"
	"github.com/jalendport/spark-cli/internal/ui"
)

// runArgv runs a resolved argv directly (no shell), echoing it first and
// passing through stdio and the exit code.
func runArgv(dir string, argv []string) error {
	ui.Echo(proc.Quote(argv))
	cmd := exec.Command(argv[0], argv[1:]...)
	return runCmd(dir, cmd)
}

// runShell runs a shell one-liner via `sh -c` from the project root. Manifest
// string commands use this form so pipes and redirects behave as written.
func runShell(dir, script string) error {
	ui.Echo(script)
	cmd := exec.Command("sh", "-c", script)
	return runCmd(dir, cmd)
}

func runCmd(dir string, cmd *exec.Cmd) error {
	cmd.Dir = dir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return &proc.ExitError{Code: ee.ExitCode()}
		}
		return err
	}
	return nil
}

// applyArgs substitutes {args} in a shell command with the user's extra args
// (shell-quoted). When the template has no {args} placeholder, the args are
// appended instead, matching the manifest spec.
func applyArgs(run string, args []string) string {
	quoted := proc.Quote(args)
	if strings.Contains(run, "{args}") {
		return strings.ReplaceAll(run, "{args}", quoted)
	}
	if quoted == "" {
		return run
	}
	return run + " " + quoted
}
