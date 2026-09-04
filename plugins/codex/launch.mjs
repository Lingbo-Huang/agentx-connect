// Only starts the separately installed, authorized Codex connector.
import { spawn } from 'node:child_process';
import { homedir } from 'node:os';
import path from 'node:path';
const root = process.env.AGENTX_CONNECT_ROOT || path.join(homedir(), '.agentx-connect', 'codex');
const file = path.join(root, 'bin', `agentx-bridge-mcp${process.platform === 'win32' ? '.exe' : ''}`);
const child = spawn(file, ['--host', 'codex', '--profile', 'compact'], { stdio: 'inherit', shell: false });
child.on('error', () => { console.error('AgentX connector unavailable. Follow start.md to install and authorize this Host.'); process.exitCode = 1; });
child.on('exit', (code) => { process.exitCode = code ?? 1; });
for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => child.kill(signal));
