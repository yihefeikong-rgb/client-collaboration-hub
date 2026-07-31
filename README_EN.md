# client-collaboration-hub

A local, auditable collaboration hub for independent AI clients. Codex, CC-HAHA and other clients stay equal and independent; the hub persists tasks, evidence, events and handoff packages, while a human keeps the final review.

This is not a chatbot UI, not an "AI assistant" product, and not an agent auto-control center. The hub never takes over a client's native UI, session, login, skills or MCP servers.

## Boundaries

- Clients keep their own native interface, session, context, skills and MCP.
- Tasks, messages, evidence, events and handoffs are persisted locally, auditable and portable.
- Agents may create and advance ordinary collaboration facts; **approval and request-changes are human-only actions**.
- Messages are not completion evidence; only registered Evidence and events count.

State machine:

```text
DRAFT → ASSIGNED → WORKING → REVIEW
                            ↘ REVISION_REQUIRED → WORKING
REVIEW → DONE / BLOCKED
```

## Quick start

Requires Go 1.25+.

```powershell
go build -o collab.exe ./cmd/collab
.\collab.exe project register-local --id my-project --name "My Project" --path "D:\path\to\project"
```

Start the human review console:

```powershell
.\scripts\start-web-console.ps1
```

Then open <http://127.0.0.1:8567>.

The MCP server is a local stdio server (`collab.exe mcp`) - no network port, no API key.

**Codex** (`%USERPROFILE%\.codex\config.toml`):

```toml
[mcp_servers.client-collaboration-hub]
command = 'D:\path\to\collab.exe'
args = ["mcp"]
default_tools_approval_mode = "writes"
startup_timeout_sec = 10
tool_timeout_sec = 60
```

**CC-HAHA / Claude Code**:

```powershell
claude mcp add --scope user client-collaboration-hub -- "D:\path\to\collab.exe" mcp
```

After connecting, read `collab://manual/agent-operating-guide` first. See [docs/mcp/AI-OPERATING-GUIDE.md](docs/mcp/AI-OPERATING-GUIDE.md) for details.

## Data location

Default global data root:

```text
%LOCALAPPDATA%\client-collaboration-hub\
```

Override with the `COLLAB_HOME` environment variable. Do not hand-edit Journal files.

## Key commands

```text
collab init
collab project register-local / bind / binding-status
collab task create / assign / accept / resume / submit / block
collab evidence add
collab review request-changes / approve
collab handoff export / next
collab response validate
collab status --task <id>
collab ui
collab mcp
collab version
```

## Tests

```powershell
go vet ./...
go test ./...
go build ./cmd/collab
.\scripts\e2e-cli.ps1        # Windows binary E2E
bash scripts/e2e-cli.sh      # Linux binary E2E
```

## Security and audit

- Journal is an append-only event ledger; corrupt states are flagged, never silently repaired.
- Handoff packages are portable: no local absolute paths, PIDs, PTYs, sessions, credentials.
- Candidate responses are validated and rendered as manual steps; the validator never executes shell commands.
- Approve/request-changes tools are intentionally absent from MCP; they exist only in the web console and human CLI.

## License

MIT, see [LICENSE](LICENSE).
