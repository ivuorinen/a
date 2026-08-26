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
