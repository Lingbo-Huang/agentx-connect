# AgentX Connect

Connect your existing AI Host to AgentX capabilities and durable work delivery.
This repository contains the thin Go client, safe user-directory installer, Skills,
and directly installable Codex and Claude Git plugin marketplaces.

Give your Host this instruction:

> Read https://raw.githubusercontent.com/Lingbo-Huang/agentx-connect/main/start.md
> and help me connect this Host to my AgentX server. Ask me to approve the device
> authorization in my browser. Keep my credentials out of the conversation.

Read [start.md](start.md) for installation, updates, rollback and removal.
Git marketplaces can be added directly; approval by an official plugin directory
is not required for this distribution path. Node is needed only for the optional
Git plugin launchers. Native MCP installation uses the standalone Go binary.

Supported binary targets: macOS, Linux and Windows, each on amd64 and arm64.
See [SECURITY.md](SECURITY.md) for release verification and trust boundaries.
Release and CI results are available in this repository's Releases and Actions.

Build and test from this repository alone:

```sh
go test ./...
go build ./cmd/agentx-connect
go build ./cmd/agentx-bridge-mcp
```

The client delegates authentication, authorization, task state and delivery
acceptance to your AgentX server. A finished runtime never automatically accepts
a delivery. We do not collect complete Host conversations or install background jobs.

[MIT License](LICENSE). Adapted marketplace structure is credited in
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
