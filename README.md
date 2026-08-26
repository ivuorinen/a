# a — age encryption wrapper

A small CLI that encrypts and decrypts files with your SSH keys using the
[age](https://github.com/FiloSottile/age) format. age is built in as a Go
library, so **no external `age` binary is needed** — one self-contained tool.
It can pull recipients' public keys straight from GitHub, keeps settings in a
YAML config, and caches fetched keys locally. Files are fully interoperable with
the standard `age` CLI.

## Install

Builds for Linux, macOS, FreeBSD, OpenBSD, and NetBSD. Windows is not supported.
No runtime dependencies.

**Download a binary** — take the archive for your platform from the
[latest release](https://github.com/ivuorinen/a/releases/latest), unpack it, and
put `a` on your `PATH`.

**Linux packages** — `.deb`, `.rpm`, and `.apk` are attached to every release:

```bash
sudo dpkg -i a_<version>_linux_amd64.deb
sudo rpm -i a_<version>_linux_amd64.rpm
sudo apk add --allow-untrusted ./a_<version>_linux_amd64.apk
```

**Container** — `ghcr.io/ivuorinen/a`:

```bash
docker run --rm -u "$(id -u):$(id -g)" -e HOME=/tmp \
  -v "$PWD:/work" -w /work ghcr.io/ivuorinen/a encrypt message.txt
```

Both flags are needed for a bind mount. `-u` runs as you, so the container can
write to a directory you own and the output belongs to you; the image's own uid
(65532) cannot. `-e HOME=/tmp` gives that uid somewhere to create the config
directory, which `a` needs on every command — pointing it at `/work` instead
would leave `.config`, `.cache`, and `.local` in your working directory.

Config is therefore not persisted between container runs. To keep it, mount a
directory for it and set `-e XDG_CONFIG_HOME=/config`.

**With Go** (requires Go 1.25+):

```bash
go install github.com/ivuorinen/a@latest
```

**From source**:

```bash
go build -o a
sudo mv a /usr/local/bin/   # optional
```

### Verifying a download

Release checksums are keyless-signed with cosign via Sigstore:

```bash
cosign verify-blob checksums.txt \
  --certificate checksums.txt.pem \
  --signature checksums.txt.sig \
  --certificate-identity-regexp 'https://github.com/ivuorinen/a/.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

That authenticates `checksums.txt` itself. It says nothing about the archive you
downloaded until you check that against it:

```bash
sha256sum --ignore-missing --check checksums.txt
```

`--ignore-missing` skips the platforms you did not download; it still exits
nonzero if nothing was verified. Without GNU coreutils (macOS, BSD), check the
one archive you did download — substitute its own platform suffix for the
`darwin_arm64` in the example:

```bash
grep a_<version>_darwin_arm64.tar.gz checksums.txt | shasum -a 256 -c -
```

## Commands

| Command | Alias | Description |
| --- | --- | --- |
| `config [set\|rem\|show]` | `c` | View or change settings; bare `config` prints the commands and current config |
| `encrypt [input] [github-user]` | `e` | Encrypt a file; output defaults to `<input>.age` |
| `decrypt [input]` | `d` | Decrypt a file; output defaults to `<input>` without `.age` |
| `completion [bash\|zsh\|fish]` | | Print a shell-completion script |

Add `-v` for verbose (debug) logging, and `--version` to print the build version
(quote it in bug reports). The long flag form still works:
`encrypt -i in -o out -r key.pub`, `decrypt -i in -o out --ssh-key key`.

`encrypt` and `decrypt` refuse to replace an existing output file; pass `-f` /
`--force` to overwrite. Naming a GitHub user whose keys cannot be fetched is an
error, not a warning — `a` will not produce a file the requested recipient
cannot open.

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

Stored at `$XDG_CONFIG_HOME/a/config.yaml` (Linux and BSD, default
`~/.config/a/config.yaml`) or `~/.config/a/config.yaml` (macOS), and created with
defaults on first run. The file must not be group- or other-accessible: any bit
set in the group or other triplet — not just the read bits — makes `a` refuse to
run and print the `chmod` to apply.

| Key | Description |
| --- | --- |
| `ssh_key_path` | Private key used for decryption; if empty, `~/.ssh/id_*` keys are tried in turn |
| `github_user` | Default GitHub user whose published keys are added as recipients |
| `default_recipients` | Public-key files or key strings always added as recipients |
| `cache_ttl_minutes` | Lifetime of cached GitHub keys; `0` disables caching |
| `log_file_path` | JSON log file; defaults to `$XDG_STATE_HOME/a/cli.log` (`~/.local/state/a/cli.log`) |

Fetched GitHub keys are cached (mode `0600`) in the user cache dir
(`~/.cache/a/<user>.keys` on Linux) for `cache_ttl_minutes`, avoiding a network
request on every encryption.

## Development

```bash
go test ./...
```

## License

MIT — see [LICENSE.md](LICENSE.md).
