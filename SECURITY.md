# Security and release verification

Install only a reviewed release from `Lingbo-Huang/agentx-connect`. Installers
download a named release and require SHA256 for every executable. SHA256 detects
corruption; it does not independently authenticate a compromised release account.
Release CI also publishes GitHub artifact attestations tied to this repository
and workflow. With GitHub CLI installed, verify a downloaded binary:

```sh
gh attestation verify ./agentx-connect_darwin_arm64 --repo Lingbo-Huang/agentx-connect
```

The public Git repository is the reviewable trust entry point. Updates are
explicit and pinned to a tag; no startup auto-update, sudo, shell-profile edits,
Gatekeeper bypass or development-channel flags are used. macOS Developer ID and
notarization are distinct from build provenance; releases must state their actual
signing status. Do not describe an unattested/unsigned preview as store-approved.

Each Host uses its own credential issued by the selected AgentX server after the
user approves Device Authorization. Windows credentials require a current-user
ACL; Unix credentials require private file permissions. The installer does not
read credentials. MCP forwards minimal requests to that server; it does not
run an autonomous agent or accept completed work on the user's behalf.

Upgrade records contain executable and Skill backups, never credentials. File
recovery refuses unexpected local modifications and unsafe paths. Keep the
installation directory under your own account. Remove the native configuration
or Git plugin to stop local calls, and revoke the Host in AgentX to remove server
authority. Existing tasks remain recoverable through their durable references.

For a suspected vulnerability, use GitHub's private vulnerability reporting on
this repository. Do not attach credentials, approval codes or complete user tasks.
