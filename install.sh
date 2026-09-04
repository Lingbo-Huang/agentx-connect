#!/bin/sh
set -eu
# Download first, verify the release checksum, then execute. No sudo or shell-profile edits.
version=v0.2.1
case "$(uname -s)" in Darwin) os=darwin;; Linux) os=linux;; *) printf 'Use install.ps1 on Windows\n' >&2; exit 2;; esac
case "$(uname -m)" in arm64|aarch64) arch=arm64;; x86_64|amd64) arch=amd64;; *) printf 'Unsupported CPU\n' >&2; exit 2;; esac
umask 077
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT HUP INT TERM
base="https://github.com/Lingbo-Huang/agentx-connect/releases/download/$version"
asset="agentx-connect_${os}_${arch}"
download() { curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --max-redirs 3 --connect-timeout 15 --max-time 180 "$1" -o "$2"; }
download "$base/SHA256SUMS" "$stage/SHA256SUMS"
download "$base/$asset" "$stage/$asset"
expected=$(awk -v name="$asset" '$2 == name {print $1}' "$stage/SHA256SUMS")
[ "${#expected}" -eq 64 ] || { printf 'Missing or ambiguous checksum\n' >&2; exit 1; }
if command -v sha256sum >/dev/null 2>&1; then actual=$(sha256sum "$stage/$asset" | cut -d ' ' -f 1); else actual=$(shasum -a 256 "$stage/$asset" | cut -d ' ' -f 1); fi
[ "$actual" = "$expected" ] || { printf 'Checksum mismatch\n' >&2; exit 1; }
chmod 0700 "$stage/$asset"
"$stage/$asset" "$@"
