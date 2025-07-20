# A CLI Wrapper for Age Encryption

A robust command-line interface (CLI) wrapper around the [age](https://github.com/FiloSottile/age)
encryption tool. This utility simplifies encryption and decryption using SSH keys,
with integrated support for fetching public keys from GitHub.

## Features

* **Secure Encryption/Decryption:** Utilize SSH and GitHub keys with `age` for strong encryption.
* **Configuration:** Easily configurable via a YAML file.
* **Structured Logging:** JSON-formatted logs with configurable paths.
* **Cross-platform:** Supports Linux, macOS, and Windows.
* **Shell Completion:** Auto-generated completion scripts for Bash, Zsh, and Fish.
* **Robust Error Handling:** Comprehensive and clear error messaging.

## Installation

### Prerequisites

* Go (1.21+)
* `age` encryption tool

### Build from source

```bash
git clone <repository-url>
cd <repository-directory>
go build -o a
```

### Move binary to path (optional)

```bash
sudo mv a /usr/local/bin/
```

## Usage

### Basic usage

```bash
a [command] [flags]
```

### Commands

* `config`: Manage application settings
* `encrypt`: Encrypt files
* `decrypt`: Decrypt files
* `completion`: Generate shell completion scripts

### Examples

#### Configure the CLI

```bash
a config --ssh-key ~/.ssh/id_rsa --github-user yourusername --default-recipients ~/.ssh/id_rsa.pub --cache-ttl 120
```

#### Encrypt a file

```bash
a encrypt -o encrypted_file.txt input.txt
```

#### Decrypt a file

```bash
a decrypt -o decrypted_file.txt encrypted_file.txt
```

## Generate shell completions

```bash
a completion bash > /etc/bash_completion.d/a
```

## Configuration File

Configuration is stored at `$HOME/.config/a/config.yaml`:

```yaml
ssh_key_path: "/home/user/.ssh/id_rsa"
github_user: "yourusername"
default_recipients:
  - "/home/user/.ssh/id_rsa.pub"
cache_ttl_minutes: 120
log_file_path: "/home/user/.state/a/cli.log"
```

## Logging

Structured JSON logs are written to a configurable log file (`cli.log`). Verbosity can be adjusted with the `-v` or `--verbose` flag.

## Testing

Run unit tests with:

```bash
go test ./...
```

## License

This project is licensed under the MIT License. See the [LICENSE](LICENSE) file for details.
