<p align="center"><img src=".github/icon.svg" alt="Spark CLI" width="80" height="80"></p>

<h1 align="center">Spark CLI</h1>

<p align="center"><em>One CLI for every Spark project: scaffold, run, and test from a single binary.</em></p>

Every project accumulates its own pile of commands: the docker compose incantations, the container names, the scripts you have to remember. Spark replaces the pile with one binary that adapts to whatever project you run it in. Each project ships a committed `spark.yml` manifest that names the project and defines its commands; run `spark` inside a [Spark Craft](https://github.com/jalendport/spark-craft) checkout and you get that project's commands, run `spark create` anywhere and it scaffolds a fresh project from a published boilerplate.

## Features

- **Adapts to the project** — Spark walks up to the nearest `spark.yml` and serves that project's commands on top of a set of built-in verbs
- **Built-in Docker verbs** — `up`, `down`, `restart`, `logs`, `sh`, and `run` drive any Compose stack with zero configuration
- **Project scaffolding** — `spark create` turns a boilerplate repo into a running project: prompts, renames, token replacement, and `git init` included
- **Disposable Craft instances** — `spark lab` mints throwaway [Craft CMS](https://craftcms.com) installs for plugin development, powered by [Spark Craft Lab](https://github.com/jalendport/spark-craft-lab)
- **Single static binary** — no npm, no Ruby, no runtime dependencies; install once and every project just works

## Installation

Install with [Homebrew](https://brew.sh):

```sh
brew install jalendport/tap/spark
```

Or with the install script (macOS and Linux):

```sh
curl -fsSL https://raw.githubusercontent.com/jalendport/spark-cli/master/install.sh | sh
```

The script downloads the latest release binary for your OS and architecture and installs it to `/usr/local/bin`, falling back to `~/.local/bin` when that isn't writable.

## Usage

### Built-in verbs

Inside any project with a `spark.yml`, these drive the Docker Compose stack. Extra arguments pass straight through to the underlying command.

| Command | Aliases | Description |
| --- | --- | --- |
| `spark up` | `u`, `start` | Start the stack (`docker compose up -d`) |
| `spark down` | `d`, `stop` | Stop the stack |
| `spark restart` | | Restart the stack (down, then up) |
| `spark logs` | | Tail service logs (`docker compose logs -f`) |
| `spark sh` | | Open a shell in a service (defaults to the first service) |
| `spark run` | | Run a one-off container (`docker compose run --rm`) |

A manifest command with the same name as a built-in overrides it (and inherits its aliases); a built-in that can't find a compose file says so and points you at the fix.

### Manifest commands

Any command defined under `commands:` in `spark.yml` becomes a top-level verb. Manifest commands run as shell one-liners from the project root, so pipes and redirects work as written. Extra arguments are substituted for a `{args}` placeholder, or appended when there is no placeholder.

```sh
spark craft migrate/all    # runs the manifest's `craft` command with `migrate/all`
```

### Creating a project

```sh
spark create                   # pick a boilerplate interactively
spark create craft my-site     # scaffold straight into ./my-site
```

`spark create` downloads the boilerplate, asks its questions, applies its setup (file renames, token replacement, post commands), and initializes a fresh git repository. A failed create leaves nothing behind.

### Testing Craft plugins

From inside any Craft plugin working copy:

```sh
spark lab up                       # mint + boot + seed a disposable Craft instance
spark lab up --craft 4.16 --db pg  # pin the Craft version and database
spark lab destroy --all            # tear everything down without a trace
```

See the [Spark Craft Lab](https://github.com/jalendport/spark-craft-lab) repo for the full lab workflow, including the `lab/test.twig` smoke page every instance serves at `/lab-test`.

## Configuration

A project's entire Spark surface lives in its committed `spark.yml`:

```yaml
name: spark-craft
description: Craft CMS 5 starter with Docker, Vite, Twig, and Vue

commands:
  craft: docker compose exec php ./craft {args}
  composer:
    run: docker compose exec php composer {args}
    help: Run Composer inside the php container

create:
  prompts:
    - key: project_name
      label: Project name
  rename:
    composer.json.project: composer.json
  replace:
    __PROJECT_NAME__: "{project_name}"
  post: []
```

- `name` / `description` — shown in the styled help header
- `commands` — string form for a bare shell one-liner, or map form with `run` and `help`; `{args}` marks where extra CLI arguments land
- `create` — executed by `spark create` after download: `prompts` are asked interactively and their answers fill `{key}` placeholders in `replace`, `rename` moves shipped template files into place, and `post` runs shell commands from the new project root

Unknown manifest keys warn instead of failing, so older binaries keep working with newer manifests.

## Support

Found a bug or need help? Open an [issue](https://github.com/jalendport/spark-cli/issues).

<hr>

<p align="center">Made by <a href="https://jalendport.com">Jalen Davenport</a></p>
