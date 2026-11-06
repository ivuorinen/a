# Contributing

## Setup

Tool versions live in `mise.toml`, except Go's, which mise reads from the
`toolchain` line in `go.mod`. The tasks, the git hooks and CI all run those
binaries through mise, so no other file pins a version. markdownlint and
yamllint run on the `node`, `python` and `uv` that `mise.toml` pins, not on the
ones your system has. Install the tools, then the git hook:

```bash
mise install
mise exec -- prek install -f
```

`-f` replaces a hook written by an older prek or by pre-commit. The hook calls
each tool through `mise exec --`, so it needs `mise` on `PATH`, not an activated
shell. For your own commands, the tools are on `PATH` only while mise is active
in your shell (`mise activate`, or mise's shims directory on `PATH`); without
that, prefix them with `mise exec --`.

Tasks live in `.mise/tasks/`. `mise tasks ls` lists them; `mise run <task>`
runs one from anywhere in the repository.

## Before opening a pull request

| Command | What it checks | Runs in CI |
| --- | --- | --- |
| `mise run lint` | every git hook on all files: golangci-lint, yamllint, markdownlint, shellcheck, actionlint, checkov and the formatters | yes, `Lint` job |
| `mise run test` | `go test -race ./...` | yes |
| `mise run conditions` | every condition evaluated both true and false | yes |
| `mise run vuln` | govulncheck against the linked modules | yes |

`mise run lint` rewrites files: the formatters, and golangci-lint through
`issues.fix: true` in `.golangci.yml`, apply their changes in place. In CI a
rewritten file fails the job, and golangci-lint runs with `--fix=false` and only
reports. `mise run test` uses the race detector, which needs a C compiler.

`mise run conditions` is the coverage gate. Statement coverage is not enough
here: a compound condition such as `a && b` counts as covered after one
evaluation. A new branch needs tests that drive each of its conditions both
ways; the task names any condition that has not been.

## Changing dependencies

After any change to `go.mod`, regenerate the license notices that ship with
every release and commit the result:

```bash
mise run notices
```

CI fails when `THIRD_PARTY_NOTICES.md` does not match the module set.

After changing a version in `mise.toml`, or the `toolchain` line in `go.mod`,
run `mise lock` and commit `mise.lock` with it. CI installs with `--locked` and
fails on a version the lockfile does not list. Keep a `toolchain goX.Y.Z` line
in `go.mod`: mise reads it as Go's exact version. Without it, mise falls back to
the `go` directive only when that carries a patch level (`go X.Y.Z`). On
Renovate's branches, the `Renovate artifacts` workflow regenerates both files.

## Commit messages

Use [Conventional Commits](https://www.conventionalcommits.org/):
`fix: …`, `feat: …`, `refactor: …`, with an optional scope such as
`fix(config): …`. GoReleaser groups the release notes by these types and drops
`docs`, `test` and `chore` commits from them, so a behaviour change typed as
`chore` or `refactor` never reaches the changelog's fixes and features. Mark a
breaking change with `!` (`feat!: …`).

## Pull requests

`main` accepts changes only through pull requests with a linear history. The
`PR Lint` check runs MegaLinter's Prettier checks for YAML and JSON, and a
dependency review. The `Test` workflow's `Test` job runs the tests, `conditions`
and `vuln`, verifies the notices, and builds every release target; its `Lint`
job runs every git hook.
