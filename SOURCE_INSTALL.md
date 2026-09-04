# Install without an Apple Developer account

An Apple Developer membership is not required to connect to AgentX. Developer ID
and notarization concern the publisher's macOS download distribution. Linux and
Windows do not use Apple's signing system. Git plugins still need the Bridge
process; installing a plugin does not make an unsigned downloaded binary notarized.

macOS and Linux users who trust the selected public source can compile it locally.
This route requires an existing [Go 1.26 or newer](https://go.dev/dl/) installation
and module network access. It builds both commands from one explicit public module
version and then uses the same managed installer, credentials and recovery logic.
It never modifies Gatekeeper, quarantine attributes or administrator policy.
Managed computers can have additional execution policies; this is not a promise
that a source build overrides those policies.

## Source bootstrap

This is a new source-code candidate, introduced after v0.2.2. Until a reviewed
release containing `install-source.sh` is published, keep using the existing
release instructions or review/build this candidate locally. Do not substitute
v0.2.2 into the commands below: its installer predates source import support.

After publication, replace **both** `vX.Y.Z` placeholders with that same reviewed
release tag. Download and inspect the script before running it:

```sh
curl -fL --proto '=https' --proto-redir '=https' https://raw.githubusercontent.com/Lingbo-Huang/agentx-connect/vX.Y.Z/install-source.sh -o /tmp/agentx-install-source.sh
sh /tmp/agentx-install-source.sh vX.Y.Z install --host codex --server https://YOUR-AGENTX-SERVER
```

Select your actual Host and server; add `--plugin` for a Codex/Claude Git plugin,
or `--no-connect` to prepare files without authorization. The user still approves
their own Device Authorization. A build does not grant access to any account.

Go's [versioned install](https://go.dev/ref/mod#go-install) ignores the surrounding
project. The bootstrap disables workspace/build overrides and automatic toolchain
download, requires module checksum verification, and compiles with CGO disabled.
It uses temporary binaries, cleans up on exit, and leaves installed files unchanged
if compilation fails. Go may keep its normal module/build cache.

The installer checks embedded module, version, OS and CPU before executing a staged
candidate. Those fields are compatibility checks, not signatures. `status` records
`LOCAL_BUILD` plus the actual installed SHA256; it does not claim GitHub build
attestation or Apple notarization for locally built bytes.

## Updates and removal

Use the new release's source bootstrap and version for `upgrade`:

```sh
sh /tmp/agentx-install-source.sh vX.Y.Z upgrade --host codex --no-connect
sh /tmp/agentx-install-source.sh vX.Y.Z status --host codex
sh /tmp/agentx-install-source.sh vX.Y.Z rollback --host codex
sh /tmp/agentx-install-source.sh vX.Y.Z uninstall --host codex
```

Retain `--plugin` and your custom `--root`, if used. Maintenance actions build only
the installer. They do not download or install a new Bridge. Use a cached reviewed
installer for offline maintenance. Rebuilding the same version can change bytes
when the compiler changes; `install` refuses that replacement, while explicit
`upgrade` preserves the previous bytes and source for rollback. Authorization is
retained across updates. Local uninstall and server-side revocation remain separate.

## Collaborating with an Apple team

For trusted binary distribution, an organization enrolled in the Apple Developer
Program can invite developers and assign certificate-related access. The team
that signs the build is its signing publisher and must own that release process.
Use team roles, not shared Apple Account passwords or borrowed private keys.
Adding a user to an individual's App Store Connect account does **not** extend the
individual's Developer Program membership benefits to that user.
See [Apple's team rules](https://developer.apple.com/help/app-store-connect/manage-your-team/add-and-edit-users/).

For a personally trusted unsigned download, Apple documents a manual Privacy &
Security exception; availability depends on system policy. AgentX does not perform
that override automatically. See [Apple's instructions](https://support.apple.com/guide/mac-help/mh40616/mac).
Obtaining our own Developer ID and notarization remains the preferred public
macOS binary distribution route; see [RELEASING.md](RELEASING.md).
