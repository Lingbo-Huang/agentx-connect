#!/bin/sh
set -eu
# Explicit source installation. Uses an installed Go compiler, never system-policy changes.
fail() { printf 'agentx_connect: %s\n' "$1" >&2; exit 2; }
[ "$#" -ge 2 ] || fail 'usage: install-source.sh vX.Y.Z <install|upgrade|status|doctor|rollback|uninstall> --host <host> [options]'
version=$1
action=$2
shift 2
printf '%s\n' "$version" | LC_ALL=C grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.-]+)?$' || fail 'select an exact module version, never latest or a branch'
case "$action" in install|upgrade|status|doctor|rollback|uninstall) ;; *) fail 'unsupported action';; esac
for arg do
  case "$arg" in --version|--version=*|-version|-version=*|--local-binary|--local-binary=*|-local-binary|-local-binary=*) fail 'version and local binary are selected by this bootstrap';; esac
done
case "$(uname -s)" in Darwin) build_os=darwin;; Linux) build_os=linux;; *) fail 'source bootstrap supports macOS and Linux';; esac
case "$(uname -m)" in arm64|aarch64) build_arch=arm64;; x86_64|amd64) build_arch=amd64;; *) fail 'unsupported CPU';; esac
command -v go >/dev/null 2>&1 || fail 'install Go 1.26 or newer from https://go.dev/dl/ first, then rerun this command'
umask 077
stage=$(mktemp -d)
# Resolve macOS /var and /tmp aliases; the Go installer rejects symlink ancestors.
stage=$(CDPATH= cd -- "$stage" && pwd -P)
trap 'rm -rf "$stage"' EXIT
trap 'exit 130' INT
trap 'exit 143' HUP TERM
mkdir "$stage/bin"
# Do not inherit workspace replacements, toolexec/overlay flags, cross compilation
# or settings that disable module authentication. Proxy selection may be customized.
export GOENV=off GOWORK=off GO111MODULE=on GOFLAGS= CGO_ENABLED=0 GOTOOLCHAIN=local
export GOOS="$build_os" GOARCH="$build_arch" GOAMD64=v1 GOARM64=v8.0 GOCACHEPROG=
export GOSUMDB=sum.golang.org GONOSUMDB= GOPRIVATE= GOINSECURE= GOBIN="$stage/bin"
module=github.com/Lingbo-Huang/agentx-connect
printf 'agentx_connect: source_build_started %s\n' "$version" >&2
build() {
  if ! go install -trimpath -ldflags "-s -w -X main.buildVersion=${version#v}" "$@"; then
    fail 'source build failed; install Go 1.26 or newer and check module network access; Host installation was not changed'
  fi
}
case "$action" in
  install|upgrade)
    build "$module/cmd/agentx-connect@$version" "$module/cmd/agentx-bridge-mcp@$version"
    printf 'agentx_connect: source_build_ready %s\n' "$version" >&2
    "$stage/bin/agentx-connect" "$action" --version "$version" --local-binary "$stage/bin/agentx-bridge-mcp" "$@"
    ;;
  *)
    build "$module/cmd/agentx-connect@$version"
    printf 'agentx_connect: source_build_ready %s\n' "$version" >&2
    "$stage/bin/agentx-connect" "$action" "$@"
    ;;
esac
