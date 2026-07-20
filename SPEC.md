# spark — design spec (v1)

One global `spark` binary (Go) that adapts to whichever Spark boilerplate project it runs in.
Decided 2026-07-17; this file is the source of truth for the scaffold.

## Principles

- The binary is a **generic engine**; per-project behavior comes from a committed `spark.yml` manifest.
- The manifest's job is **routing, help, and cohesion — not logic**. Complex repos (e.g. spark-craft-lab) keep their own scripts and the manifest wraps them.
- Commands live in the repo, so old projects never skew against new binary versions.
- No runtime dependencies for users: single static binary, installed via brew tap or curl installer.

## Stack

- Go 1.24+, module path `github.com/jalendport/spark-cli`.
- [cobra](https://github.com/spf13/cobra) for the command tree.
- [charmbracelet/lipgloss](https://github.com/charmbracelet/lipgloss) for styled output; keep a small `internal/ui` package so styling stays in one place. Colors: use adaptive styles that work in light and dark terminals.
- `gopkg.in/yaml.v3` for the manifest.
- Binary name: `spark`. Repo layout: `main.go` + `internal/` packages (`internal/cli`, `internal/manifest`, `internal/ui`, `internal/compose`). No `pkg/`, no `cmd/` sprawl — this is a small tool.

## Project detection

Walk up from the CWD looking for `spark.yml`. The directory containing it is the **project root**; all commands run relative to it. Outside a project, only global commands (`create`, `version`, `help`, and a friendly "not in a spark project" message for everything else) are available.

## Manifest: `spark.yml`

```yaml
# identifies the boilerplate; shown in help header
name: spark-craft

# optional; free-form, shown in help
description: Craft CMS boilerplate

# override or extend commands
commands:
  # string form: shell one-liner, run via `sh -c` from the project root
  craft: docker compose exec php ./craft {args}

  # map form when help text or extras are needed
  composer:
    run: docker compose exec php composer {args}
    help: Run Composer inside the php container

# create-time setup, executed by `spark create` after tarball extraction (v1: schema
# defined here, implementation deferred — the loader should parse and ignore it)
create:
  prompts:
    - key: project_name
      label: Project name
  rename:
    composer.json.project: composer.json
  replace: # token replacement across text files
    __PROJECT_NAME__: "{project_name}"
  post: [] # optional shell commands run at the end
```

- `{args}` in a `run` string is replaced with the user's extra CLI args (shell-quoted, joined by spaces). If absent, args are appended.
- Unknown top-level keys: warn, don't fail (forward compatibility).
- A manifest command with the same name as a built-in **overrides** it (inheriting its aliases). This extends to aliases: a manifest command whose name collides with a built-in's alias wins — the built-in surrenders that alias.

## Built-in verbs (available in every project)

| Verb      | Aliases    | Default behavior (when not overridden)                          |
| --------- | ---------- | --------------------------------------------------------------- |
| `up`      | `u, start` | `docker compose up -d` (+ friendly status/URL summary if easy)  |
| `down`    | `d, stop`  | `docker compose stop`                                           |
| `restart` |            | `down` then `up`                                                |
| `logs`    |            | `docker compose logs -f {args}`                                 |
| `sh`      |            | `docker compose exec <first service or {args[0]}> sh`           |
| `run`     |            | `docker compose run --rm {args}`                                |

Defaults require a `docker-compose.yml`/`compose.yaml` at the project root; if none exists and the manifest doesn't override the verb, print a clear error.

Global commands: `version`, `help`, `create` (stub in v1 — prints "coming soon"; real implementation is a later phase using the registry below).

## Built-in `lab` command group

`spark lab` mints, boots, seeds, and tears down fully self-contained, disposable Craft CMS instances for testing the plugin in the current repo. Unlike the manifest verbs above, **`lab` is a native Go engine baked into the binary, not a manifest passthrough** — for two reasons:

- It runs from inside a **Craft plugin working copy** (detected by walking up for a `composer.json` with `"type": "craft-plugin"`), not a Spark boilerplate project. Plugin repos have no `spark.yml`, so there's nothing for a manifest to route to.
- Its behavior is real orchestration (compose stack minting, port allocation, readiness gating, per-driver DB setup, in-Craft seeding), not a shell one-liner — exactly the "complex logic" the manifest is explicitly *not* meant to carry.

Because it's a global built-in, `lab` is available regardless of project detection.

Commands (all operate on `<plugin>/.lab/<name>/`):

| Command | Behavior |
| ------- | -------- |
| `lab up [--craft <v>] [--db mysql\|pg] [--php <tag>]` | Mint (if needed) + boot + seed an instance. Idempotent. |
| `lab list` | List this plugin's instances (name, URL, Craft version, PHP, db, status). |
| `lab craft <name> <args…>` | Proxy a Craft console command into the instance's php container. |
| `lab seed <name>` | Ensure up, then re-run the seed flow. |
| `lab destroy <name>\|--all` | Tear down containers/volumes and remove the `.lab` data. |
| `lab prune` | Remove the shared composer cache volume and any orphaned `lab-*` compose projects. |

The Craft skeletons, Docker build context, generic templates, and the in-Craft "lab" module come from an **asset bundle** (`github.com/jalendport/spark-craft-lab`). There is no persistent cache: `up` shallow-clones the bundle into a temp dir, copies everything each instance needs into `.lab/<name>/`, and discards the clone, so instances are self-contained and the other verbs never touch the network. `SPARK_LAB_ASSETS=<path>` points at a local working copy to skip the clone (for developing the assets themselves). The full asset contract — template placeholders, compose shape, seed flow, plugin `lab/` convention — lives in that repo's `ENGINE.md`.

## Registry (for `spark create`, later phase)

`registry.json` at the root of this repo, fetched at runtime from raw.githubusercontent:

```json
{
  "boilerplates": [
    { "name": "craft", "repo": "jalendport/spark-craft", "description": "Craft CMS boilerplate" },
    { "name": "craft-lab", "repo": "jalendport/spark-craft-lab", "description": "Disposable Craft instances for plugin dev" }
  ]
}
```

## UX bar

Help output, errors, and command echo should look as polished as `gh` or modern charm-built tools: styled header with project name, aligned command list including manifest-defined commands, dim command echo before executing (e.g. `→ docker compose up -d`). Exit codes pass through from the underlying command.

## Out of scope for the scaffold

- Real `create` implementation, goreleaser/brew tap, self-update, spark-craft-lab manifest logic changes.
