# Connect this Host to AgentX

Select exactly one Host: `codex`, `claude`, `cursor`, `workbuddy`, `trae`, `lobi`
or `openclaw`. Obtain the user's AgentX HTTPS server origin. Do not guess their
organization, borrow another Host's credentials, or print credential files.

## Native MCP installation

Download and inspect the installer. Run from the intended project for project-scoped
Hosts such as Trae. Replace `codex` and the example origin before executing:

```sh
curl -fL --proto '=https' --proto-redir '=https' https://raw.githubusercontent.com/Lingbo-Huang/agentx-connect/v0.2.2/install.sh -o /tmp/agentx-install.sh
sh /tmp/agentx-install.sh install --host codex --version v0.2.2 --server https://YOUR-AGENTX-SERVER
```

Windows PowerShell (download first; use your organization's normal script execution policy):

```powershell
Invoke-WebRequest https://raw.githubusercontent.com/Lingbo-Huang/agentx-connect/v0.2.2/install.ps1 -OutFile "$env:TEMP\agentx-install.ps1"
& "$env:TEMP\agentx-install.ps1" install --host codex --version v0.2.2 --server https://YOUR-AGENTX-SERVER
```

No source checkout, Go, Node, administrator permission or PATH modification is needed.
`--no-connect` installs files only; `--no-open-browser` prints the approval link.
The user must approve Device Authorization personally. After approval, reload
the Host and enable/trust the AgentX MCP if the Host asks. WorkBuddy requires
its custom-connector Trust step.

## Direct Git plugin marketplaces

For Codex or Claude, choose this instead of registering native MCP twice. Use the
same installer command with `--plugin` to install the binary and authorize the
Host without registering a second MCP entry. Install Node for the plugin launcher.

Codex:

```sh
codex plugin marketplace add Lingbo-Huang/agentx-connect
codex plugin add agentx-codex@agentx
```

Claude Code:

```text
/plugin marketplace add Lingbo-Huang/agentx-connect
/plugin install agentx-claude@agentx
```

The Git plugins bundle their own Skill and launch only their own Host credential.
They do not download updates or start a browser during MCP startup. For a custom
`--root`, provide that same absolute path as `AGENTX_CONNECT_ROOT` to the plugin.

## Verify the result

Call `search_capabilities` from the actual Host. A successful installer or Doctor
alone does not prove the Host loaded its tools. For work requiring another owner,
create one explicit Handoff, save its reference, then close/reopen the Host and
recover it with LIST_HANDOFFS/GET_STATUS. Fetch the delivery and let the user
explicitly request revision or accept it. Reuse the original idempotency key
after ambiguous failures; do not create a replacement task blindly.

Native diagnostics: run the downloaded installer with `doctor --host codex`.
For plugins, call the actual loaded tools; native Doctor checks native registration.

## Upgrade, rollback and remove

Use a reviewed release tag and its installer. Re-run with
`upgrade --host codex --version vX.Y.Z --no-connect` (and `--plugin` for plugin mode).
The downloaded installer must come from that same tag; released installers reject
mixing their embedded Skill with another release's binary before changing files.
Download/checksum failures preserve the installed version. Interrupted file writes
are rolled back on the next run. Close the Host before upgrading on Windows.
Run `rollback --host codex` to restore the previous verified binary and Skill.
User-modified Skills are preserved; resolve the conflict explicitly before retrying.
WorkBuddy Desktop uses `~/.workbuddy/skills`. Upgrading a v0.2.0 installation with
v0.2.1 migrates its unchanged managed Skill from the old CodeBuddy CLI directory;
rollback restores the previous location. An occupied destination is preserved.

For native MCP run `uninstall --host codex`. For Git plugins remove the plugin in
the Host first, then run `uninstall --host codex --plugin`. Only exact managed
files are deleted. Local removal does not revoke server authorization: revoke
the Host in AgentX settings and verify a subsequent call is rejected.

## Remote and closed Hosts

Lobi Code has a local Codewiz runtime; `lobi` authorization does not configure a
managed Lobi Chat workspace. Remote Lobi/Seal must be authorized in that remote
workspace with its supported connector flow. A local credential is not a remote
installation. Standard remote HTTP MCP uses your administrator's OAuth server;
Device Authorization is a separate local-client flow.

If a closed Host cannot execute commands or load MCP, share the selected task
explicitly into AgentX's web surface. Do not pretend a pasted Skill installed a
native connector. Skills guide tool selection; they cannot guarantee automatic
invocation or authorize uploading the entire conversation.
