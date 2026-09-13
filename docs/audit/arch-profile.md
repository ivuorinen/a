# Architecture Profile

Generated: 2026-09-12

## Detected Patterns

No catalogued architectural pattern matches at Medium confidence or better.

The repository is a single-binary Go CLI of ~1160 non-test statements across
two packages. The catalogue's patterns all describe how a codebase partitions
responsibility once it is large enough to need partitioning; this one is not.
Naming the closest match anyway would invent rules `/nitpicker arch` would then
enforce against code that never agreed to them.

Signals scanned and absent: no `domain/`, `ports/`, `adapters/`, `entities/`,
`use-cases/`, `core/`, `infrastructure/`, `application/`, `commands/`,
`queries/`, `handlers/`, `events/`, `models/`, `views/`, `controllers/`,
`features/`, `services/`, `repositories/`, `plugins/`, or `pipeline/`
directory; no `*Entity`, `*Aggregate`, `*Repository`, `*Port`, `*Adapter`,
`*Handler`, `*Projection`, `*ViewModel`, or `*Presenter` identifier; no event
bus, no persistence layer, no service boundary.

### Detected: none

Confidence: none — manual review required.

## Detected Combination

None.

## Actual structure (evidence, not a pattern claim)

```text
a.go                     package main — wiring only: version stamp, globals,
                         cobra root, PersistentPreRunE, subcommand assembly
internal/cmd/            package cmd — one file per command plus shared config
  config_shared.go       Config type, path/IO primitives (Init/Load/Save, key scan)
  config.go              `config` command + key setter/formatter
  encrypt.go             `encrypt` command + recipient collection, GitHub fetch, cache
  decrypt.go             `decrypt` command + SSH identity parsing, key iteration
  completion.go          `completion` command
```

Dependency direction (from `go list`):

- `github.com/ivuorinen/a` imports `github.com/ivuorinen/a/internal/cmd`.
- `github.com/ivuorinen/a/internal/cmd` imports no package of this module.
- The boundary is one-way and acyclic, and `internal/` makes it enforceable by
  the compiler rather than by convention. Commit 28f8e35 moved the package
  under `internal/` for exactly that reason.

Composition is by constructor injection: `Encrypt`, `Decrypt`, `ConfigCmd`, and
`Completion` each take their collaborators (`*Config`, `*slog.Logger`, a
`saveConfig` callback, the root command) as arguments and return a
`*cobra.Command`. No package-level state lives in `internal/cmd`; the mutable
globals (`log`, `cfg`, `cfgFile`, `cacheDir`) are all in `main`.

Two package variables in `internal/cmd` exist as test seams and are documented
as such: `githubKeysURL` (encrypt.go:189) and `passphrasePrompt`
(decrypt.go:23).

## Inferred Structural Rules

None inferred from a pattern. Three boundaries the code does hold, recorded so
a later change that breaks one is visible as a change rather than as drift:

1. `internal/cmd` imports nothing from this module. Any import of `main` or of
   a future sibling package is a new dependency edge, not a refactor.
2. `main` owns all mutable process state. `internal/cmd` receives its
   collaborators as constructor arguments; a package-level `var` there that is
   not a documented test seam breaks that.
3. `main` mutates `*cfg` and `*log` in place rather than reassigning the
   pointers, because subcommands capture them at construction time
   (a.go:62-66, a.go:101-104). A subcommand that copies either value instead of
   holding the pointer silently stops observing the loaded config.

## Ambiguities & Contradictions

None. No conflicting pattern signals, because there are no pattern signals.
