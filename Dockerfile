# Pinned by digest so a rebuild of a release tag reproduces the same base layer;
# `alpine:latest` made ghcr.io/ivuorinen/a:vX.Y.Z non-reproducible, and
# .goreleaser.yml passes --pull=true, which guaranteed the drift. Renovate keeps
# this line current.
FROM alpine:3.24@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b
# ca-certificates is deliberately unpinned. Alpine's package index only carries
# the current version of each package, so `ca-certificates=<version>` starts
# failing the build the day alpine publishes a new one -- a pin that breaks on a
# schedule is worse than none. The base image's digest pin above is what holds
# the layer stable. The ignore must sit directly above the instruction; hadolint
# does not look past intervening comment lines.
# hadolint ignore=DL3018
RUN apk --no-cache add ca-certificates

# uid 65532 has no /etc/passwd entry, so HOME would default to "/" and every
# command would die in PersistentPreRunE on `mkdir /.config: permission denied`
# -- the config, cache and state dirs all derive from HOME. Give the uid a
# directory it owns and point HOME at it. `--version` is the only command that
# survived without this, because cobra answers it before PersistentPreRunE runs,
# so a smoke test must use a real subcommand.
ENV HOME=/home/nonroot
RUN mkdir -p /home/nonroot && chown 65532:65532 /home/nonroot

ARG TARGETPLATFORM
COPY $TARGETPLATFORM/a /usr/local/bin/
# Staged into the build context by .goreleaser.yml's dockers_v2.extra_files. The
# image redistributes the same BSD-3/Apache-2.0 deps as the archives, so it owes
# the same notices.
COPY THIRD_PARTY_NOTICES.md /usr/share/doc/a/
# a only reads and writes user files; it never needs privilege. Running as root
# would start any container escape at uid 0.
USER 65532:65532
# checkov:skip=CKV_DOCKER_2: a is a one-shot CLI that runs and exits; there is no
# long-running process for a HEALTHCHECK to probe.
ENTRYPOINT ["a"]
