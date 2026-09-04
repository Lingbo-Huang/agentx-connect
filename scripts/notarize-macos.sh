#!/bin/sh
set -eu
# Run only on an isolated macOS release runner. Inputs are GitHub secret refs.
: "${APPLE_DEVELOPER_ID_P12:?Configure the Developer ID Application certificate secret}"
: "${APPLE_CERTIFICATE_PASSWORD:?Configure its import password}"
: "${APPLE_SIGNING_IDENTITY:?Configure the Developer ID Application identity}"
: "${APPLE_NOTARY_KEY_P8:?Configure the App Store Connect API private key}"
: "${APPLE_NOTARY_KEY_ID:?Configure the API key ID}"
: "${APPLE_NOTARY_ISSUER_ID:?Configure the API issuer ID}"
: "${RELEASE_TAG:?Select a reviewed release tag}"
printf '%s\n' "$RELEASE_TAG" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$' || exit 2
umask 077
work=$(mktemp -d)
keychain="$work/release.keychain-db"
cleanup() { security delete-keychain "$keychain" >/dev/null 2>&1 || true; rm -rf "$work"; }
trap cleanup EXIT HUP INT TERM
keychain_password=$(openssl rand -hex 32)
security create-keychain -p "$keychain_password" "$keychain"
security unlock-keychain -p "$keychain_password" "$keychain"
security set-keychain-settings -lut 3600 "$keychain"
printf '%s' "$APPLE_DEVELOPER_ID_P12" | base64 --decode > "$work/certificate.p12"
printf '%s' "$APPLE_NOTARY_KEY_P8" | base64 --decode > "$work/notary.p8"
security import "$work/certificate.p12" -k "$keychain" -P "$APPLE_CERTIFICATE_PASSWORD" -T /usr/bin/codesign >/dev/null
security set-key-partition-list -S apple-tool:,apple:,codesign: -s -k "$keychain_password" "$keychain" >/dev/null
mkdir -p dist/notarized
for arch in arm64 amd64; do
  for name in agentx-connect agentx-bridge-mcp; do
    file="dist/notarized/${name}_darwin_${arch}"
    CGO_ENABLED=0 GOOS=darwin GOARCH="$arch" go build -trimpath -buildvcs=false \
      -ldflags "-s -w -X main.buildVersion=${RELEASE_TAG#v}" -o "$file" "./cmd/$name"
    codesign --keychain "$keychain" --force --options runtime --timestamp --sign "$APPLE_SIGNING_IDENTITY" "$file"
    codesign --verify --strict "$file"
  done
done
ditto -c -k --keepParent dist/notarized "$work/macos.zip"
xcrun notarytool submit "$work/macos.zip" --key "$work/notary.p8" --key-id "$APPLE_NOTARY_KEY_ID" \
  --issuer "$APPLE_NOTARY_ISSUER_ID" --wait --timeout 20m --output-format json > "$work/notary-result.json"
jq -e '.status == "Accepted"' "$work/notary-result.json" >/dev/null
jq '{id,status}' "$work/notary-result.json" > dist/notarized/NOTARIZATION.json
(cd dist/notarized && shasum -a 256 agentx-* > SHA256SUMS)
