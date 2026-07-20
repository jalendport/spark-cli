# Spark

Spark is a single, generic CLI that adapts itself to whatever project you run it in. There is one binary; each project ships a committed `spark.yml` manifest that names the project, defines its commands, and describes how to scaffold a fresh copy. Run `spark` inside a project and you get that project's commands; run `spark create` anywhere and it scaffolds a new project from a published boilerplate.

## Install

### Homebrew

```sh
brew install jalendport/tap/spark
```

### Install script

```sh
curl -fsSL https://raw.githubusercontent.com/jalendport/spark-cli/master/install.sh | sh
```

The script downloads the latest release binary for your OS and architecture and installs it to `/usr/local/bin` (falling back to `~/.local/bin` when that isn't writable).

## How it works

Spark walks up from the current directory looking for a `spark.yml`. When it finds one, the directory containing it is the project root, and the manifest's commands become available on top of a set of built-in verbs. Outside a project, only the global commands (`create`, `lab`, `version`, `help`) are available.

The manifest is forward-compatible: an older binary reading a newer manifest warns about keys it doesn't recognize rather than failing, so a project and the CLI can evolve independently.

## Command reference

### Built-in verbs

These run in any project and drive its Docker Compose stack. A manifest command of the same name overrides the built-in (and inherits its aliases). Each verb passes any extra arguments straight through to the underlying command.

| Command | Aliases | Description |
| --- | --- | --- |
| `spark up` | `u`, `start` | Start the stack (`docker compose up -d`) |
| `spark down` | `d`, `stop` | Stop the stack |
| `spark restart` | | Restart the stack (down, then up) |
| `spark logs` | | Tail service logs (`docker compose logs -f`) |
| `spark sh` | | Open a shell in a service (defaults to the first service) |
| `spark run` | | Run a one-off container (`docker compose run --rm`) |

A built-in that needs a compose file but can't find one at the project root fails with a message pointing you at either adding a `docker-compose.yml` or overriding the verb in `spark.yml`.

### Manifest commands

Any command defined under `commands:` in `spark.yml` becomes a top-level verb. Manifest commands run as shell one-liners via `sh -c` from the project root, so pipes and redirects work as written. Extra arguments are substituted for a `{args}` placeholder, or appended when there is no placeholder.

```sh
spark craft migrate/all      # runs the manifest's `craft` command with `migrate/all`
```

### `spark create`

Scaffold a new project from a published boilerplate.

```sh
spark create [boilerplate] [directory]
```

With no boilerplate name, Spark fetches the [registry](registry.json) and shows a picker. With a name, it resolves that name against the registry. With no directory, it prompts for a project name and slugifies it into the target directory; the target must not already exist as a non-empty directory.

Spark downloads the boilerplate repo as a tarball from `codeload.github.com`, extracts it (stripping the archive's top-level directory), then runs the boilerplate's `create:` section: it asks the configured prompts, renames files, substitutes tokens, runs the post commands, and initializes a fresh git repository (with no initial commit, so the first commit is yours). It finishes by printing the next steps.

### `spark lab`

`spark lab` mints and manages disposable, self-contained Craft CMS instances for testing a plugin in the current repo. It is a global command group that runs from inside a Craft plugin working copy, independent of any `spark.yml`. Its subcommands (`up`, `list`, `craft`, `seed`, `destroy`, `prune`) and the engine contract they implement are documented in the [`spark-craft-lab`](https://github.com/jalendport/spark-craft-lab) asset repo's `ENGINE.md`.

### `spark version` / `spark help`

`spark version` prints the installed version. `spark help` (or `spark` with no arguments) renders the styled help screen, including the current project's commands when run inside a project.

## The `spark.yml` schema

`spark.yml` lives at the project root. Every key is optional. Unknown top-level keys are ignored with a warning.

```yaml
name: spark-craft
description: Craft CMS 5 starter with Docker, Vite, Twig, and Vue

commands:
  craft:
    run: docker compose exec php ./craft {args}
    help: Run Craft console commands inside the php container
  composer: docker compose exec php composer {args}

create:
  prompts:
    - key: vendor
      label: Vendor name
    - key: project
      label: Project name
  rename:
    composer.json.project: composer.json
  replace:
    __VENDOR__: "{vendor}"
    __PROJECT__: "{project}"
  post:
    - npm install
```

### `name` and `description`

Free-form strings shown in the help header. They have no behavioral effect.

### `commands`

A mapping of command name to definition. Each value is either a bare string (the shell one-liner to run) or a map with `run` (required) and `help` (optional, shown in help output). Commands appear in help in the order written. A `{args}` placeholder in `run` is replaced with the user's extra arguments; without it, arguments are appended.

### `create`

Consumed by `spark create` after the boilerplate is extracted. Every field is optional:

- `prompts` — an ordered list of `{ key, label }` entries. Each is asked interactively; the answer is stored under `key` for use in `replace`. When `label` is omitted, the `key` is shown.
- `rename` — a map of source path to destination path, both relative to the project root. Each source file is moved to its destination, letting a boilerplate ship a template under a name that would otherwise conflict (for example `composer.json.project` becomes `composer.json`).
- `replace` — a map of literal token to replacement. The replacement may reference prompt answers with `{key}` placeholders. Each token is substituted across every text file in the project, skipping binary files and the `vendor/`, `node_modules/`, and `.git/` directories.
- `post` — a list of shell commands run in order via `sh -c` from the new project root, for setup steps like installing dependencies.
