package cli

import (
	"fmt"

	"github.com/jalendport/spark-cli/internal/compose"
)

// builtin describes a verb available in every project unless the manifest
// overrides it by defining a command of the same name.
type builtin struct {
	name    string
	aliases []string
	help    string
	run     func(root string, args []string) error
}

// builtins is the ordered set of default verbs, matching the spec table.
var builtins = []builtin{
	{
		name:    "up",
		aliases: []string{"u", "start"},
		help:    "Start the stack (docker compose up -d)",
		run: func(root string, args []string) error {
			if !hasCompose(root) {
				return errNoCompose("up")
			}
			argv := append(compose.Base(), "up", "-d")
			return runArgv(root, append(argv, args...))
		},
	},
	{
		name:    "down",
		aliases: []string{"d", "stop"},
		help:    "Stop the stack",
		run: func(root string, args []string) error {
			if !hasCompose(root) {
				return errNoCompose("down")
			}
			argv := append(compose.Base(), "stop")
			return runArgv(root, append(argv, args...))
		},
	},
	{
		name: "restart",
		help: "Restart the stack (down, then up)",
		run: func(root string, args []string) error {
			if !hasCompose(root) {
				return errNoCompose("restart")
			}
			if err := runArgv(root, append(compose.Base(), "stop")); err != nil {
				return err
			}
			return runArgv(root, append(compose.Base(), "up", "-d"))
		},
	},
	{
		name: "logs",
		help: "Tail service logs (docker compose logs -f)",
		run: func(root string, args []string) error {
			if !hasCompose(root) {
				return errNoCompose("logs")
			}
			argv := append(compose.Base(), "logs", "-f")
			return runArgv(root, append(argv, args...))
		},
	},
	{
		name: "sh",
		help: "Open a shell in a service (defaults to the first service)",
		run: func(root string, args []string) error {
			if !hasCompose(root) {
				return errNoCompose("sh")
			}
			service := ""
			if len(args) > 0 {
				service = args[0]
			} else {
				s, err := compose.FirstService(root)
				if err != nil {
					return err
				}
				service = s
			}
			return runArgv(root, append(compose.Base(), "exec", service, "sh"))
		},
	},
	{
		name: "run",
		help: "Run a one-off container (docker compose run --rm)",
		run: func(root string, args []string) error {
			if !hasCompose(root) {
				return errNoCompose("run")
			}
			argv := append(compose.Base(), "run", "--rm")
			return runArgv(root, append(argv, args...))
		},
	},
}

func hasCompose(root string) bool {
	_, ok := compose.File(root)
	return ok
}

func errNoCompose(verb string) error {
	return fmt.Errorf("no compose file at the project root, so the default %q command can't run — add a docker-compose.yml or override %q in spark.yml", verb, verb)
}
