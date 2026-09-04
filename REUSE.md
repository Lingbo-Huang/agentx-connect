# Reference implementations and adaptations

Source snapshots were refreshed and concrete implementations inspected on 2026-09-04.
These repositories are research inputs, not build or runtime dependencies.

| Project and revision | Inspected implementation | AgentX adaptation |
| --- | --- | --- |
| [Codex EigenFlux](https://github.com/phronesis-io/codex-eigenflux/tree/56c3fd3924a84de52f93f454cd32b45dd77bf309) | `.agents/plugins/marketplace.json`, `.codex-plugin/plugin.json`, `.mcp.json`, `src/mcp-server.mjs` | Git marketplace with local plugin sources; passive stdio launcher. Adapted manifest structure retains the MIT notice. No rollout scanning or automatic Skill updates at startup. |
| [Claude EigenFlux](https://github.com/phronesis-io/eigenflux-claude-plugin/tree/dfd926895b8534b5a9d13012bd10303c9d1f5b46) | Marketplace manifests; `src/channel.ts:syncSkills`, leader election and feed polling | Direct Git marketplace distribution. AgentX uses ordinary MCP without experimental channel flags or background profile collection. |
| [EigenFlux](https://github.com/phronesis-io/eigenflux/tree/bd299373b86b60a9379b098117f10bdaba210762) | `static/install.sh:detect_invoking_host,resolve_eigenflux_home`; `cli/internal/skills/sync.go:Sync,applyStaged`; `archive.go:extractTarGz` | Explicit Host, user directory, staged writes, locking, preservation and recovery. Independently implemented; no Hub source copied. |
| [OpenClaw EigenFlux](https://github.com/phronesis-io/openclaw-eigenflux/tree/7a2a10b8d3d06e1c09bef1f52e7ae30cf274bbe1) | `src/index.ts:syncPluginSkills,registerPlugin,registerFollowupTool` | Studied registration and service lifetime. AgentX does not import the plugin or silently activate remote workspaces. |
| [Multica](https://github.com/multica-ai/multica/tree/cc09c3c12a1d3e2fff872b7fd52a26b653b98639) | `scripts/install.sh:detect_os,install_cli_binary`; `scripts/install.ps1:Convert-ToCliArch,Get-WindowsCliArch` | Native release downloads and CPU selection. Independently implemented because Multica's license has additional commercial conditions. No sudo, shell profile changes or command-line bearer credentials. |
| [OpenWork](https://github.com/different-ai/openwork/tree/b4484eae6e050fdfbb8a4cb40c6dd3e6c5130d89) | `start.md`; `packages/openwork-bootstrap/bin/openwork.mjs:runInstall,runInstallApp,downloadArtifact`; `packages/install-config/src/index.ts:installConfigSchema` | Agent-readable start instructions, explicit install manifest and per-user installation. No enterprise source, provisional workspace or cloud identity implementation copied. |
| [EigenFlux whitepaper](https://github.com/phronesis-io/eigenflux-whitepaper/tree/3ed649f7e6d19afe8e299d8ae412e77eafb432f1) | Updated research snapshot | Conceptual context only; not evidence that an installation or Host task works. |

## Where the mechanisms meet

| Boundary | Authority and lifetime | Failure and recovery |
| --- | --- | --- |
| Git marketplace → MCP | The user enables a plugin; the Host starts and stops its stdio process. | The launcher fails if the binary or credentials are absent. It never downloads code or starts authorization while loading tools. |
| Installer → release | A selected repository and immutable version identify downloaded files; SHA256 checks consistency. GitHub attestations provide independently verifiable build provenance. | Download, checksum and probe failures preserve the installed version. Platform signing remains a separate requirement. |
| Installer → local files | A manifest owns only the selected binary and Skill. OS locks serialize writers and release on process death. | Journaled file changes recover on the next run; changed or unowned files are preserved. Upgrade and rollback include WorkBuddy's corrected Skill location. |
| Installer → authorization | Installation commits before the user approves a distinct Host identity. No credentials enter the install journal or plugin package. | Failed authorization leaves verified files available for retry. Reuse requires the same Host and server; local removal does not revoke server authority. |
| Local client → remote Host | Each runtime requires its own supported authorization mechanism. | Copying local credentials is not remote installation. A Device API does not establish standard HTTP MCP OAuth interoperability. |

## Host-specific evidence

Codex and Claude Git marketplace installation was exercised against this public
repository using isolated native CLI profiles. Three-OS CI also tests the
published binary bootstrap. These checks do not replace authenticated task and
delivery acceptance in each Host's actual UI.

WorkBuddy Desktop injects its `.workbuddy` configuration directory into its
bundled CodeBuddy CLI; consequently its global Skills belong in `.workbuddy/skills`.
The separate [WorkBuddy marketplace connector contract](https://open.workbuddy.cn/docs/connector)
also specifies auth-process lifetime, status and unAuth behavior. That marketplace
flow must be validated separately from direct native MCP registration.
