# a — age encryption wrapper

A small CLI that encrypts and decrypts files with your SSH keys using the
[age](https://github.com/FiloSottile/age) format. age is built in as a Go
library, so **no external `age` binary is needed** — one self-contained tool.
It can pull recipients' public keys straight from GitHub, keeps settings in a
YAML config, and caches fetched keys locally. Files are fully interoperable with
the standard `age` CLI.

## Install

Requires Go 1.26+ to build. No runtime dependencies.

```bash
go build -o a
sudo mv a /usr/local/bin/   # optional
```

## Commands

| Command | Alias | Description |
| --- | --- | --- |
| `config [set\|rem\|show]` | `c` | View or change settings; bare `config` prints the commands and current config |
| `encrypt [input] [github-user]` | `e` | Encrypt a file; output defaults to `<input>.age` |
| `decrypt [input]` | `d` | Decrypt a file; output defaults to `<input>` without `.age` |
| `completion [bash\|zsh\|fish]` | | Print a shell-completion script |

Add `-v` for verbose (debug) logging. The long flag form still works:
`encrypt -i in -o out -r key.pub`, `decrypt -i in -o out --ssh-key key`.

## Example

```bash
# 1. Have an SSH key (create one if needed)
ssh-keygen -t ed25519 -f ~/.ssh/id_ed25519 -N ''

# 2. Configure: private key for decrypting, your public key as a default recipient
a config set ssh_key_path ~/.ssh/id_ed25519
a config set default_recipients ~/.ssh/id_ed25519.pub   # comma-separate for several

# 3. Encrypt to the configured recipients -> message.txt.age
a e message.txt
a e message.txt octocat        # also encrypt to github.com/octocat.keys

# 4. Decrypt -> message.txt (written 0600)
a d message.txt.age
```

`a c show` prints the current config; `a config rem <key>` resets one key.

## Configuration

Stored at `$XDG_CONFIG_HOME/a/config.yaml` (Linux, default `~/.config/a/config.yaml`),
`~/.config/a/config.yaml` (macOS), or `%AppData%\a\config.yaml` (Windows), and
created with defaults on first run.

| Key | Description |
| --- | --- |
| `ssh_key_path` | Private key used for decryption; if empty, `~/.ssh/id_*` keys are tried in turn |
| `github_user` | Default GitHub user whose published keys are added as recipients |
| `default_recipients` | Public-key files or key strings always added as recipients |
| `cache_ttl_minutes` | Lifetime of cached GitHub keys; `0` disables caching |
| `log_file_path` | JSON log file location |

Fetched GitHub keys are cached (mode `0600`) in the user cache dir
(`~/.cache/a/<user>.keys` on Linux) for `cache_ttl_minutes`, avoiding a network
request on every encryption.

## Development

```bash
go test ./...
```

## License

MIT — see [LICENSE.md](LICENSE.md).
