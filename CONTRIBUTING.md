# Contributing

## Setup

`mise.toml` pins the tools the `Justfile` and CI run, and CI installs the same
versions from it. Two other files hold pins: `go.mod`'s `toolchain` line, which
must match the `go` entry in `mise.toml`, and `.pre-commit-config.yaml`, which
pins every hook by commit SHA. For markdownlint-cli, yamllint and yamlfmt, the
hook's `# frozen:` tag must match the `mise.toml` version; the comments in
`mise.toml` say which pins move together. Install the tools, then the git hooks:

```bash
mise install
prek install
```

The tools are on `PATH` only while mise is active in your shell (`mise activate`,
or mise's shims directory on `PATH`). Without that, prefix commands with
`mise exec --`, as in `mise exec -- prek install`.

Tasks live in the `Justfile`; `just --list` shows them all.

## Before opening a pull request

| Command | What it checks | Runs in CI |
| --- | --- | --- |
| `just test` | `go test ./...` | yes, with `-race` |
| `just lint` | golangci-lint, yamllint, markdownlint | golangci-lint |
| `just conditions` | every condition evaluated both true and false | yes |
| `just vuln` | govulncheck against the linked modules | yes |
| `just precommit` | every pre-commit hook on all files | no |

`just lint` rewrites files: `.golangci.yml` sets `issues.fix: true`, so the
formatters apply their changes in place. CI runs it with `--fix=false` and only
reports.

`just conditions` is the coverage gate. Statement coverage is not enough here:
a compound condition such as `a && b` counts as covered after one evaluation.
A new branch needs tests that drive each of its conditions both ways; the
script names any condition that has not been.

## Changing dependencies

After any change to `go.mod`, regenerate the license notices that ship with
every release and commit the result:

```bash
just notices
```

CI fails when `THIRD_PARTY_NOTICES.md` does not match the module set.

When you change a version in `mise.toml`, run `mise lock` and commit
`mise.lock` with it.

## Commit messages

Use [Conventional Commits](https://www.conventionalcommits.org/):
`fix: …`, `feat: …`, `refactor: …`, with an optional scope such as
`fix(config): …`. GoReleaser groups the release notes by these types and drops
`docs`, `test` and `chore` commits from them, so a behaviour change typed as
`chore` or `refactor` never reaches the changelog's fixes and features. Mark a
breaking change with `!` (`feat!: …`).

## Pull requests

`main` accepts changes only through pull requests with a linear history. The
`PR Lint` check runs MegaLinter (YAML, Markdown, JSON) and a dependency review;
the `Test` workflow runs the checks marked as CI above, verifies the notices,
and builds every release target.
