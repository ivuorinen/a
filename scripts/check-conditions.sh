#!/usr/bin/env bash
# Fail when any condition has not been evaluated both true and false.
#
# `go test -cover` measures *statements*. A compound condition like `a && b` is
# one block, so a single evaluation marks it covered whichever way each term
# went, and an `if` with no `else` has no block representing its false path. The
# suite reached 100% statement coverage while two conditions had still never been
# observed both ways -- one of them a guard whose removal writes the GitHub key
# cache into the process's working directory. gobco counts each condition's true
# and false outcomes separately and is what catches that.
#
# This is a script rather than a Justfile line for two reasons, both gobco
# behaviors that would otherwise produce a check that cannot fail:
#
#   - gobco exits 0 even when it reports uncovered conditions, so the exit status
#     has to be derived from its summary line.
#   - gobco panics when given more than one package, so each is run separately.
#
# A missing or unparseable summary is treated as failure, not as a pass: silence
# from a checker is the failure mode this exists to prevent.
set -euo pipefail

cd "$(dirname "$0")/.."

# Keep in sync with the gobco pin in mise.toml.
GOBCO_VERSION=1.3.4

if command -v gobco >/dev/null 2>&1; then
  gobco=(gobco)
else
  # No mise-provided binary (CI): run the pinned version straight from the module
  # proxy. Nothing is added to go.mod.
  gobco=(go run "github.com/rillig/gobco@v${GOBCO_VERSION}")
fi

# gobco copies the module to a temp directory and runs `go test` there, and the
# copy includes mise.toml. On a machine with mise active that config sits at an
# untrusted path, so mise refuses to load it and the test run dies before gobco
# sees any coverage -- reported as "gobco failed to run", which is true but
# unhelpful. Giving gobco a directory we just created and trusting exactly that
# one keeps the check working under mise without trusting all of /tmp.
workdir=$(mktemp -d)
trap 'rm -rf "$workdir"' EXIT
export TMPDIR="$workdir" MISE_TRUSTED_CONFIG_PATHS="$workdir"

status=0

for pkg in . ./internal/cmd; do
  echo "==> conditions: ${pkg}"
  out=$("${gobco[@]}" "$pkg" 2>&1) || {
    printf '%s\n' "$out"
    echo "::error::gobco failed to run for ${pkg}" >&2
    status=1
    continue
  }
  printf '%s\n' "$out"

  summary=$(printf '%s\n' "$out" | grep -E '^Condition coverage: [0-9]+/[0-9]+$' || true)
  if [ -z "$summary" ]; then
    echo "::error::gobco printed no coverage summary for ${pkg}; treating as failure" >&2
    status=1
    continue
  fi

  ratio=${summary##*: }
  covered=${ratio%/*}
  total=${ratio#*/}
  if [ "$covered" != "$total" ]; then
    echo "::error::${pkg}: ${covered}/${total} conditions covered." \
      "Every condition must be evaluated both true and false; the lines above name the gaps." >&2
    status=1
  fi
done

if [ "$status" -eq 0 ]; then
  echo "All conditions evaluated both ways."
fi

exit "$status"
