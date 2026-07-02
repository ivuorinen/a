# Nitpicker Findings

Generated: 2026-06-26
Last validated: 2026-07-02

## Summary

- Total: 37 | Open: 0 | Fixed: 37 | Invalid: 0

Pass 1 fixed all Critical/High/Medium/Low defects. Pass 2 resolved the four
Advisory items (dead config and overly strict validation). Pass 3 was a full
adversarial codepath + test-coverage sweep: it fixed a silent completion no-op
and an over-strict permission check, removed a vestigial error path, and added a
test suite raising statement coverage from ~49% to ~88% (see Analysis Notes).
Pass 8 (2026-07-01) re-validated every fixed finding against the real binary end
to end and added a config viewer/confirmation. Pass 9 (2026-07-02) was an
adversarial security scan of a now-library-based (filippo.io/age) codebase: it
found and fixed a Critical plaintext-leak on failed decryption (N-027), then made
the encrypt path atomic to match (N-028). Pass 10 (2026-07-02) was a whole-repo
adversarial sweep across code, docs, and infra — govulncheck and gitleaks clean;
fixed broken release version injection, a failing brew smoke test, a
command-bricking log path, an unbounded/timeout-less network fetch, non-atomic
config writes, and two config/ignore gaps (N-029..N-035).

## Open Findings

(none)

## Fixed

### Pass 1 — 2026-06-26

#### [N-001] CLI panics at startup on every invocation

Fixed: 2026-06-26
Notes: `cfg` was nil when `rootCmd.AddCommand(...)` constructed the subcommands
(config/encrypt/decrypt captured the nil pointer); `PersistentPreRunE` then loaded
config into the package var but the subcommands kept the stale nil. `ConfigCmd`
dereferenced the typed-nil pointer at construction (`config.LogFilePath`), so
`a <anything>` SIGSEGV'd before running. Verified by reproduction. Fix: allocate
`cfg = &cmd.Config{}` up front and have `loadConfig` mutate it in place
(`*cfg = *loaded`) so the captured pointer observes loaded values.

#### [N-002] Bootstrap chicken-and-egg: new user can never create config

Fixed: 2026-06-26
Notes: `PersistentPreRunE` calls `loadConfig`, and the old `LoadConfig` errored when
the file was absent, so `a config` (the only way to create a config) failed in its
own PreRun. Fix: `InitConfigPaths` now materializes a default `config.yaml` (0600)
on first run, and `LoadConfig` returns a defaulted config when the file is missing.

#### [N-003] Decryption always fails (broken arg-validation loop)

Fixed: 2026-06-26
Notes: `tryDecrypt` validated `["-d","-i",keyPath,"-o",output,input]` by treating
indices 0/2/4 as flags, but index 2 is `keyPath` and 4 is `output`, so it always
returned "unexpected flag in age arguments". Decryption could never succeed. Fix:
removed the broken security-theater loop (args are built from literal flags
internally), kept the meaningful keyPath/output validations.

#### [N-004] Encrypting to public-key files always fails

Fixed: 2026-06-26
Notes: `buildAgeArgs` passed every recipient via `age -r`, but recipients are SSH
public-key file paths (per README and `default_recipients`). `-r` expects a key
string; file paths require `-R`. Only GitHub-fetched key strings worked. Fix:
classify each recipient — existing file → `-R`, otherwise `-r`.

#### [N-005] config command wipes unset fields

Fixed: 2026-06-26
Notes: `ConfigCmd` unconditionally overwrote every field from flag values, so
`a config --github-user x` reset ssh_key_path, recipients, and ttl to empty/zero.
Fix: only assign fields whose flag was `Changed()`. Also returned an error (instead
of silent `nil`) on a bad type assertion, and removed the now-redundant flag-default
branch.

#### [N-006] Decrypted plaintext left world/group-readable

Fixed: 2026-06-26
Notes: age writes output honoring umask (observed 0664), leaving decrypted secrets
readable by group/other. Fix: `tryDecrypt` chmods the output to 0600 after a
successful decrypt.

#### [N-007] LoadConfig path validation was wrong and broke legitimate use

Fixed: 2026-06-26
Notes: It recomputed `expectedDir` from `os.UserConfigDir()` and rejected any
cfgFile not under it via `strings.HasPrefix` — ignoring its own parameter contract,
using a weak prefix check (`/.config/a-evil` passes), and making the function
untestable. cfgFile is supplied by `InitConfigPaths` (trusted), so the check was
removed; the existing `#nosec G304` covers the read.

#### [N-008] Test helper key-name collision scanned the real ~/.ssh

Fixed: 2026-06-26
Notes: `generateSSHKeyPair` hardcoded `id_rsa`, so the second call in
`TestEncryptDecrypt_WrongKey` failed (ssh-keygen won't overwrite), returned an empty
path, and decrypt fell back to scanning the developer's real `~/.ssh`. Fix: place
each keypair in its own `os.MkdirTemp` subdirectory (filename stays `id_rsa`, which
decryption requires).

#### [N-009] README encrypt/decrypt examples use wrong invocation

Fixed: 2026-06-26
Notes: Examples were `a encrypt -o out.txt input.txt`, but the positional arg is
ignored and `-i/--input` is required, so the documented command errors with "input
file is required". Fixed to `-i input.txt -o out.txt` (and the decrypt analogue).

#### [N-010] README LICENSE link is broken

Fixed: 2026-06-26
Notes: Linked `[LICENSE](LICENSE)` but the file is `LICENSE.md`. Fixed the target.

#### [N-011] README states wrong Go version

Fixed: 2026-06-26
Notes: README said "Go (1.21+)" while go.mod requires `go 1.26.4`. Updated to 1.26+.

### Pass 2 — 2026-06-26

#### [N-012] CacheTTLMinutes was dead configuration

Fixed: 2026-06-26
Notes: `CacheTTLMinutes` and the `CacheDir` from `InitConfigPaths` were never read;
GitHub key fetches hit the network every time. Implemented the intended caching:
`fetchGitHubKeys` now stores the `.keys` response at `<CacheDir>/<user>.keys` (0600)
and reuses it while younger than `CacheTTLMinutes`. `CacheDir` is threaded into
`Config` (yaml:"-") via `a.go`, the first-run config now defaults `cache_ttl_minutes`
to 120, and `0` disables caching. Covered by new tests in cmd/encrypt_test.go and
verified end-to-end (cache file written, second run served from cache).

#### [N-013] Output restricted to .txt/.out extensions

Fixed: 2026-06-26
Notes: Removed the `.txt`/`.out` output allow-list from `buildAgeArgs` and
`tryDecrypt` so arbitrary output filenames (e.g. `foo.tar.age`) are accepted. The
restriction provided no security value (output name is caller-chosen). Covered by
`TestBuildAgeArgs`.

#### [N-014] Decryption key restricted to id_rsa/id_ed25519 suffix

Fixed: 2026-06-26
Notes: Removed the brittle filename-suffix check in `tryDecrypt`, which rejected
valid keys with non-standard names (e.g. `~/.ssh/work_ed25519`) supplied via
`--ssh-key`. age itself validates the key and unusable keys simply fail that attempt.

#### [N-015] Dead binary-name guard in runAgeEncrypt

Fixed: 2026-06-26
Notes: Removed `ageBin := "age"; if ageBin != "age" { ... }` (never true) and inlined
the `"age"` literal, matching the cleanup already done in `tryDecrypt`.

### Pass 3 — 2026-06-26

#### [N-016] completion command silently no-ops on an unknown shell

Fixed: 2026-06-26
Notes: `a completion powershell` exited 0 with no output and no error (the `switch`
had no default), and completion-generation errors were discarded (`_ =`). Converted
`Run` to `RunE`, returning an error for unsupported shells and propagating generator
errors; added `ValidArgs`. Covered by cmd/completion_test.go.

#### [N-017] LoadConfig required exactly 0600, rejecting stricter modes

Fixed: 2026-06-26
Notes: The check `info.Mode().Perm() != 0o600` rejected a more-restrictive `0400`
config, a footgun for users hardening their config. Replaced with `perm&0o077 != 0`
(reject only group/other access). Covered by `TestLoadConfig_AcceptsStricterPerms`
and `TestLoadConfig_RejectsGroupOtherPerms`.

#### [N-018] collectRecipients had a vestigial error return

Fixed: 2026-06-26
Notes: `collectRecipients` always returned a nil error, leaving an unreachable,
untestable `if err != nil` branch in `Encrypt`'s RunE. Dropped the error return and
the dead branch. Also made the GitHub fetch URL injectable (`githubKeysURL`) so the
network paths (200/404/connection error) are testable, removing the dead
"is-this-really-a-github-URL" self-check on a URL the code constructs itself.

### Pass 4 — 2026-06-28

Ponytail lens (over-engineering hunt).

#### [N-019] ConfigCmd used `any` parameters for same-package types

Fixed: 2026-06-28
Notes: `ConfigCmd(cfg any, saveConfig func(any) error)` erased types that live in the
same package as ConfigCmd — there is no import cycle to dodge — forcing a runtime
`cfg.(*Config)` assertion, a dead "internal error" branch, and a `c.(*cmd.Config)`
round-trip in a.go. Changed to `ConfigCmd(cfg *Config, saveConfig func(*Config) error)`.
A type mismatch is now a compile error, so the assertion, the error branch, the a.go
closure, and `TestConfigCmd_BadConfigType` were all deleted (net code removed).

### Pass 5 — 2026-06-28

Ponytail simplification sweep (5 iterations, race-tested each round).

#### [N-020] Justfile `format` target used the wrong yamlfmt flag

Fixed: 2026-06-28
Notes: `yamlfmt -c .yamlfmt.yml .` is invalid (`-c` is not a yamlfmt flag), so
`just format` aborted at that step with a usage dump. Changed to `-conf`. Now both
`just format` and `just lint` run clean.

#### [N-021] Simplification sweep across code and tests

Fixed: 2026-06-28
Notes: Net deletions, behavior unchanged (race + full suite green each round):
(1) Decrypt's "provided key" branch duplicated the log+tryDecrypt logic already in
`tryAllKeys`; collapsed it to a one-element slice through `tryAllKeys`.
(2) Extracted `requireInputOutput`, the input/output flag validation duplicated
verbatim in Encrypt and Decrypt RunE — single source of truth.
(3) `parseKeyLines` now uses `strings.SplitSeq` (iterator, no slice alloc) per the
linter; removed a needless temp var in `runAgeEncrypt` and a stale "import Config
from main" comment.
(4) Removed dead test scaffolding: the `writeTestResult` helper dumped unread files
into temp dirs (7 call sites), plus a redundant `os.Stat` and a pointless
`HasPrefix` assertion in the roundtrip test; dropped two now-unused imports.

### Pass 6 — 2026-06-28

Deep simplify + coverage loops (10 iterations, race-tested each round).

#### [N-022] Untested error branches now covered via fault injection

Fixed: 2026-06-28
Notes: Pushed coverage from ~88% to ~93% by testing the FS-error branches with
root-independent fault injection — ENOTDIR (a path under a regular file) and EISDIR
(a directory where a file is expected): `InitConfigPaths` mkdir failures and the
`UserConfigDir` error, `LoadConfig` stat/read/defaults errors, `applyConfigDefaults`
mkdir error, `readKeyCache` unreadable-cache, `fetchGitHubKeys` body-read error (via
a hijacked short HTTP response), `Encrypt`'s age-failure path, and the two main-pkg
wrapper error paths. Residual uncovered: `main()` (integration-covered), the darwin
branch (GOOS-gated), and a handful of unreachable errors (`yaml.Marshal` of a fixed
struct, `UserCacheDir` while config succeeds, a `Body.Close` defer).

#### [N-023] Replaced logrus with stdlib log/slog

Fixed: 2026-06-28
Notes: The app used logrus only for JSON logging with levels — exactly what stdlib
`log/slog` does (Go 1.21+; this module is 1.26). Converted all logging to `slog`,
removing the `github.com/sirupsen/logrus` dependency and its transitive
`golang.org/x/sys`. Direct deps are now cobra, testify, yaml. Also deleted
`TestSetupLogging`, which tested the logging library rather than our code (our
`setupLogging` is covered by `TestConfigWrappers`).

#### [N-024] Removed dead ConfigPaths.ConfigDir field

Fixed: 2026-06-28
Notes: `ConfigPaths.ConfigDir` was set by `InitConfigPaths` but never read in
production (only two test assertions used it). Removed the field; tests now assert on
`filepath.Dir(paths.ConfigFile)`.

### Pass 7 — 2026-06-28

Loops 11–15: the final deeper simplification round (race-tested each).

#### [N-025] Deeper simplification: stdlib idioms, inlining, idiomatic signatures

Fixed: 2026-06-28
Notes: Five iterations, behavior unchanged, coverage steady (~93%):
(11) `collectRecipients` builds its recipient list with `slices.Concat` instead of a
two-step manual `append`.
(12) Inlined the single-use `defaultConfig()` constructor at its one call site (the
bootstrap default is still verified by `TestInitConfigPaths_Full`).
(13) `tryAllKeys` now returns `(tried []string, ok bool)` instead of mutating a
`*[]string` out-param — idiomatic Go; the caller drops its pre-declared accumulator.
(14) Deleted three pure-`NotNil` smoke tests (`TestCmdEncryptPlaceholder`,
`TestCmdDecryptPlaceholder`, `TestCmdCompletion`) redundant with the functional and
cmd-package tests; removed the now-unused cobra import from the main test file.
(15) Hoisted the GitHub-username regex to a package-level `githubUsernameRE` so it is
compiled once rather than on every `collectRecipients` call.

### Pass 8 — 2026-07-01

Empirical end-to-end re-validation (real `age` v1.3.1 binary, isolated `HOME` +
`XDG_*`) plus a usage improvement.

Re-validation (all hold): full `config`→`encrypt`→`decrypt` round trip recovers
plaintext (N-002/N-003); file recipients encrypt via `-R` (N-004); decrypted
output is `0600` (N-006); first-run `cache_ttl_minutes` materializes as `120`
(N-012); a failed decrypt with a wrong key leaves no stale output file and the
subsequent correct key succeeds, so multi-key scanning is safe. No regressions,
no new correctness defects.

#### [N-026] `config` command gave no feedback and had no way to view settings

Fixed: 2026-07-01
Notes: `a config ...` saved silently (no output, no confirmation) and there was no
command to inspect current settings — a typo'd path failed only later at
encrypt/decrypt time. `ConfigCmd` now marshals the resulting config and echoes it
(`Configuration saved:\n<yaml>`), so a save confirms exactly what was written and
`a config` with no flags doubles as a viewer. Covered by
`TestConfigCmd_EchoesResultingConfig`. README rewritten with one complete
setup→encrypt→decrypt walkthrough and a corrected XDG-aware config path.

### Pass 9 — 2026-07-02

Adversarial security scan (secrets-leak focus) of the library-based codebase.

#### [N-027] Failed decrypt leaked group/world-readable plaintext to disk

Fixed: 2026-07-02
Notes: CRITICAL. `tryDecrypt` wrote decrypted plaintext straight to the output via
`os.OpenFile(..., O_CREATE|O_WRONLY|O_TRUNC, 0o600)` and only `chmod`ed to 0600
*after* `io.Copy`. age authenticates its stream incrementally (64 KiB chunks), so a
tampered/truncated ciphertext — or any mid-stream failure — wrote one or more
plaintext chunks and then errored, skipping the chmod. Proven end to end: decrypting
a truncated 198 KB ciphertext onto a pre-existing `0644` file exited non-zero yet
left 65536 bytes of plaintext on disk at mode **0664 (group/world-readable)** and
destroyed the pre-existing file. Because `tryAllKeys` reuses one output path, a
header-matching-but-corrupt attempt could also leave fragments there. Fix: decrypt
to a temp file created 0600 (`os.CreateTemp`) in the target directory and
`os.Rename` onto the output only after a fully successful decrypt; remove the temp on
any failure. Plaintext now never reaches the target path — nor any looser-than-0600
mode — on a failed or partial decrypt, and a pre-existing file is untouched unless
decryption succeeds (rename also replaces rather than follows a symlink at the
target). Covered by `TestTryDecrypt_FailureLeavesNoPlaintext`; full round trip and
0600 output re-verified.

#### [N-028] encryptFile was not atomic (clobber/partial output on failure)

Fixed: 2026-07-02
Notes: `encryptFile` opened output with `O_CREATE|O_WRONLY|O_TRUNC` and wrote
incrementally, so a mid-stream failure truncated any pre-existing file at that path
and left a partial `.age`. Not a secrets leak (the output is ciphertext, and a
truncated age file fails its integrity check on decrypt), but data-loss-prone.
Mirrored the N-027 fix: encrypt to a 0600 temp file in the target dir and
`os.Rename` onto output only after `age.Encrypt`/copy/close all succeed; remove the
temp on any failure. Covered by `TestEncryptFile_FailureLeavesPreexistingIntact`
(a failed encrypt leaves the pre-existing file intact and no temp residue).

### Pass 10 — 2026-07-02

Whole-repo adversarial sweep (code, docs, infra). govulncheck: no vulnerabilities;
gitleaks: no leaks across 131 commits.

#### [N-029] Release version injection was a no-op

Fixed: 2026-07-02
Notes: `.goreleaser.yml` set `-X github.com/ivuorinen/a/cmd.version` (and .commit/
.date/.builtBy) — symbols that do not exist in package `cmd`; the linker silently
ignores `-X` on missing symbols. The real `version` was a `const` in `main`, which
`-X` also cannot set. Proven: `go build -ldflags "-X ...cmd.version=9.9.9"` and
`-X main.version=9.9.9` both still printed `v0.3.0`, so every release would misreport
its version. Fix: made `version` a package-level `var` in `main` and pointed the
single ldflag at `-X main.version={{.Version}}`; dropped the three unused
commit/date/builtBy flags. Verified `-X main.version=v1.2.3` now yields `v1.2.3`.

#### [N-030] Homebrew smoke test invoked a nonexistent subcommand

Fixed: 2026-07-02
Notes: the goreleaser brew `test` block ran `system "#{bin}/a", "version"`, but there
is no `version` subcommand — cobra exposes only the `--version` flag, so `a version`
exits non-zero ("unknown command"). The formula test would fail on every release.
Proven by running `a version`. Fix: changed the test to `a --version`.

#### [N-031] A bad log_file_path bricked every command

Fixed: 2026-07-02
Notes: `setupLogging` ran in `PersistentPreRunE` for all commands and returned an
error if the log file could not be opened, so a `log_file_path` pointing at an
unwritable/nonexistent location (or a directory) made every command fail — including
`config`, the only in-tool way to fix the path. Logging carries no secrets and
encrypt/decrypt do not depend on it, so it now degrades to stderr with a warning
instead of erroring. Test updated (`TestSetupLoggingFallback`).

#### [N-032] GitHub key fetch had no timeout and unbounded read

Fixed: 2026-07-02
Notes: `fetchGitHubKeys` used `http.Get` (no timeout — a slow/hostile server hangs
the CLI forever) and `io.ReadAll` with no size cap (a huge/hostile response exhausts
memory). Fix: a package `http.Client{Timeout: 30s}` and
`io.ReadAll(io.LimitReader(body, 1<<20))`. Not a leak, but a reliability/DoS hole in
the one network path of a security tool.

#### [N-033] SaveConfig was not atomic — config loss on partial write

Fixed: 2026-07-02
Notes: `SaveConfig` used `os.WriteFile` (O_TRUNC then write), so an interrupted or
disk-full `config set` truncated the config, silently losing all settings
(ssh_key_path, recipients, ...). It also inherited a pre-existing file's mode, which
could leave the config not-0600 and then rejected by LoadConfig. Fix: write to a
0600 temp file in the config dir and `os.Rename` over the target — atomic, always
0600, original preserved on failure. Covered by `TestLoadAndSaveConfig` (round trip
through LoadConfig, which requires 0600).

#### [N-034] golangci-lint config stale and copied from another project

Fixed: 2026-07-02
Notes: `.golangci.yml` pinned `run.go: "1.21"` while go.mod is `1.26.4` (analysis
ran against the wrong language version), and its header comments referenced an
unrelated "f2b"/"fail2ban" project. Fix: set `go: "1.26"` and corrected the header.

#### [N-035] dist/ not gitignored

Fixed: 2026-07-02
Notes: goreleaser writes build artifacts to `dist/`, which was not in `.gitignore`
(only `coverage*`, `out/`, `a` were), so a `just release` followed by `git add`
could commit built binaries. Fix: added `dist/`.

### Pass 11 — 2026-07-02

#### [N-036] Tests never used testify's require, risking cascade failures

Fixed: 2026-07-02
Notes: All ~257 assertions across the 8 test files used `assert`, which continues
after a failure. When a precondition failed — a setup step (`WriteFile`/`MkdirAll`/
`Flags().Set`), a value-returning helper (`makeSSHKey`), or an `err` checked right
before its value is used (`got, err := ReadFile; assert.NoError; assert.Equal(got)`)
— the test kept running and produced nil-panics or confusing secondary failures that
masked the real cause. Converted preconditions and err-before-use sites to
`require.*` (fail-fast) while leaving terminal, independent behavioral checks as
`assert.*` (so all are reported). Split is now ~128 require / ~129 assert. Also
fixed a stale `age -r` comment in `TestEncryptCmd_AgeFailure`. Behavior/coverage
unchanged (85.2%); race + lint clean.

### Pass 12 — 2026-07-02

#### [N-037] GitHub Actions hardening (zizmor --persona auditor)

Fixed: 2026-07-02
Notes: `zizmor 1.26.1 --persona auditor` reported 10 findings (3 medium, 7 low)
across the 4 workflows. Autofixer applied the one available fix (artipacked:
`persist-credentials: false` on the sync-labels checkout). Fixed the rest manually:
`excessive-permissions` — pr-lint.yml and sync-labels.yml used top-level
`permissions: read-all`; narrowed both to `permissions: {}` (each job already
declares its own least-privilege set). `concurrency-limits` — added a
`concurrency` group (cancel-in-progress) to codeql.yml and stale.yml.
`undocumented-permissions` — added an explanatory comment to every permission
grant in codeql/pr-lint/stale/sync-labels; stale.yml's redundant top-level reads
were replaced with `{}` since its single job overrides them. Re-run is clean ("No
findings to report"); yamllint and actionlint both pass.

## Invalid

(none)

## Analysis Notes

Pass 3 added test files: cmd/fetch_test.go, cmd/decrypt_test.go,
cmd/config_shared_test.go, cmd/completion_test.go, cmd/config_test.go, expanded
cmd/encrypt_test.go, and main-package tests (config wrappers, setupLogging error,
and a TestCLIIntegration that builds the real binary and runs a full encrypt/decrypt
roundtrip — exercising main() and PersistentPreRunE end to end).

Statement coverage is ~88%. Every reachable logic branch is tested. The residual
uncovered lines are deliberately left, as they cannot be exercised by a hermetic
unit test without fault injection:

- `main()` — covered behaviorally by TestCLIIntegration (separate process, so not
  credited in the unit coverage profile).
- `InitConfigPaths` darwin branch — OS-gated (`runtime.GOOS == "darwin"`).
- Syscall-error branches that need an unwritable/unreadable FS at a path the code
  controls: `os.UserConfigDir`/`os.UserCacheDir`/`MkdirAll` errors in
  `InitConfigPaths`, the non-`NotExist` `os.Stat` error in `LoadConfig`,
  `applyConfigDefaults` `MkdirAll` error, `io.ReadAll`/`Body.Close` errors in
  `fetchGitHubKeys`, and the `os.ReadFile` error after a successful `Stat` in
  `readKeyCache`.
- `SaveConfig`'s `yaml.Marshal` error — unreachable for the `Config` struct (no
  non-marshalable fields).
