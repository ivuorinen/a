## Analysis Notes

## Coverage

| Metric | internal/cmd | main | Command | Gated in CI |
| --- | --- | --- | --- | --- |
| Statement | 100% | 100% | `just coverage` | no |
| Branch | 240/240 | 22/22 | `just branches` | no |
| Condition | 262/262 | 28/28 | `just conditions` | **yes** |

147 tests, no skips.

Statement coverage is the weakest of the three and the only one `go test`
reports: it counts executed lines, so a compound condition like `a && b` is
satisfied by a single evaluation and an `if` with no `else` has no block
representing its false path. This repo hit 100% statement coverage while two
conditions had still never been observed both ways, one of them a guard whose
removal writes the GitHub key cache into the process's working directory.

Condition coverage is therefore the gated one, via
`scripts/check-conditions.sh`, which `just conditions` and the CI job both run.
The script exists rather than a bare `gobco` call because gobco exits 0 even
when it reports gaps — a check that cannot fail — and because it panics when
handed more than one package. gobco is pinned in mise.toml and needs no entry in
go.mod; the script falls back to `go run` at the same version when the binary is
not on PATH, which is how CI runs it.

Branch coverage is not gated: condition coverage subsumes it, and `just
branches` is there for the coarser view when hunting an unreached arm rather
than an unexercised term.

### Known equivalent mutants

Three sub-conditions can be deleted without changing observable behavior, so no
test can catch their removal. They are not gaps; do not write tests chasing
them, and do not "simplify" them away without reading the reasoning:

- `buildVersion`: `info.Main.Version != "(devel)"` filters exactly the value the
  fallback returns anyway. Load-bearing only if the fallback constant changes —
  commented in `a.go`.
- `resolveIO`: `input != ""` in `if output == "" && input != ""`. With an empty
  input the derived output is non-empty (".age"/".dec"), but the next check
  returns "input file is required" either way.
- `collectRecipients`: `cfg.GitHubUser != ""` in
  `if ghUser == "" && cfg.GitHubUser != ""`. Assigning "" to an already-empty
  `ghUser` changes nothing, and the following early return is identical.

A fourth survivor was **not** equivalent and is now covered: dropping
`cfg.CacheDir != ""` from `fetchGitHubKeys` made `filepath.Join("", user+".keys")`
resolve to a bare relative path, writing the key cache into the process's
working directory. `TestFetchGitHubKeys_NoCacheDirDoesNotWriteToCwd` pins it.

`just coverage`, not `go test -coverprofile`: `main()` runs in the subprocess
`TestCLIIntegration` launches, so a plain profile reports it at 0% however
thoroughly the test drives it. The recipe collects the unit runs via
`-test.gocoverdir`, has the integration test build its binary with `-cover` via
`A_INTEGRATION_GOCOVERDIR`, and merges the two with `go tool covdata`. A plain
`go test` run is unaffected and still builds the binary the ordinary way.

Two conditions reduce the number, both by skipping rather than failing:

- **Running as root.** Several tests inject faults with permission bits (0500
  directories, 0000 files), which root ignores. `requireNonRoot` skips them.
- **No `ssh-keygen`.** Key generation is a build-environment dependency;
  `requireSSHKeygen` and `requireBinary` skip locally and **fail** under `CI`,
  because a skip reads as a pass and would silently retire the entire
  SSH-key surface.

### 100% here does not mean every line can fire in production

Four groups of statements were unreachable from a test until seams were added
for them, and two of those groups are still unreachable *in production*:

- **`yamlMarshal`** (`config_shared.go`). `yaml.Marshal` cannot fail for
  `Config` — every field is a string, `[]string` or `int`. The error branches in
  `SaveConfig` and `formatConfig` are dead in production and exist only because
  ignoring an error return is worse. The seam makes them executable in tests; it
  does not make them reachable in practice.
- **`tempFile` / `createTemp`** (`config_shared.go`). The `Write` and `Close`
  guards in `SaveConfig`, `encryptFile` and `tryDecrypt` fire on a full disk or
  an I/O error mid-write. Both are real, neither can be provoked against a file
  in a temp directory.
- **`userConfigDir(goos)`** (`config_shared.go`). The darwin branch is real on
  macOS and dead on every other platform. Parameterizing on `goos` makes it
  checkable from a linux runner.
- **`termIsTerminal` / `termReadPassword`** (`decrypt.go`). `passphrasePrompt`'s
  body needs a tty; under `go test` stdin is never one. The wrappers reach it
  without a PTY dependency.

These exist for testability and for nothing else. Keep them: deleting a seam
silently drops the branch under it back to uncovered, and the mutation checks
below are what would notice.

The response-body-close branch in `fetchGitHubKeys` needed **no** seam —
`keysHTTPClient` was already a package variable, so a stub `RoundTripper`
serving a body that fails to close reaches it.

## What the suite is checked against

Every critical path is mutation-checked rather than trusted for having a test
with a matching name. Across the 2026-09-13 runs, 15 mutations were applied to
critical-path functions; 10 survived at some point and all are now caught:

- `Encrypt` RunE dropping the `encryptFile` error check (exited 0 on a failed
  encryption) — `TestEncryptCmd_EncryptFileFailureSurfaces`.
- `Decrypt` RunE dropping `ensureWritableOutput` (clobbered a newer plaintext) —
  `TestDecryptCmd_RefusesExistingOutput`.
- `LoadConfig`'s permission mask narrowed to group-only or other-only — the mode
  table in `TestLoadConfig_RejectsGroupOtherPerms`.
- `tryDecrypt`'s empty-path guard deleted — `TestTryDecrypt_EmptyPath` asserts
  the message, not merely that an error occurred.
- `Completion` mapping bash to the zsh generator — `TestCompletion_ValidShells`
  asserts a per-shell marker in the captured script.
- `SaveConfig`'s error text replaced with an unrelated string — the ten formerly
  bare `assert.Error` calls assert messages.
- `setConfigKey` losing its `log_file_path` arm — `TestConfig_SetEachKey` is
  driven off `configKeys` and fails if a key has no case.
- `userConfigDir` never taking the darwin branch — `TestUserConfigDir`.
- `passphrasePrompt` dropping its tty guard — `TestPassphrasePrompt`.
- `createTemp` returning a nil `*os.File` inside a non-nil `tempFile` (the Go
  nil-interface trap) — the `createTemp`-failure tests.

When adding a branch to a critical path, mutate it and confirm a test fails.
A test that passes against the mutated code is not protecting the branch, and
coverage will not tell you: it counts executed lines, not verified ones.

`TestEncryptFile_TempFileWriteAndCloseErrors` calibrates age's header write
count at runtime rather than hardcoding it, so that an age version change moves
the target write instead of silently testing the wrong branch.
