# Global Hub and MCP Design

## Goal

Make `client-collaboration-hub` a single local collaboration service for all projects. A user adds a project
once from the web console; supported AI clients use a local MCP server to create and progress tasks. Agents may
write normal collaboration facts, while final approval and change requests remain human-only web actions.

## Non-goals

- No remote HTTP MCP transport, authentication service, or cloud synchronization.
- No native client process, UI, session, skill, MCP, login, or database control.
- No MCP tool for approve, request-changes, arbitrary event writes, or arbitrary state transitions.
- No silent migration, deletion, or overwrite of existing project-local `collaboration/` stores.

## Global data root

The default data root is:

```text
%LOCALAPPDATA%\client-collaboration-hub
```

It contains a single `collaboration/` store and local settings. Real source repositories remain in their existing
locations and are referenced only through local Bindings. Tests and explicit integrations may override the root
with `COLLAB_HOME`; the production default never depends on the current working directory.

On first use, the application initializes the global store and idempotently registers the built-in `codex` and
`cc-haha` clients. Existing local stores remain untouched and may be imported later through an explicit workflow.

## Project registration

The console exposes an “Add local project” action. The user selects or enters an existing directory. The server:

1. resolves and validates the absolute local directory;
2. derives a stable safe project ID from the requested name or directory name;
3. creates the logical project in the global registry;
4. creates the current-device Binding;
5. returns the registered project without scanning or uploading repository contents.

The project selector filters the queue and task views. An “all projects” option remains available.

## MCP server

`collab mcp` runs an MCP server over stdio using the official Go SDK. stdout is reserved for MCP messages and logs
go to stderr. The server uses the same global root and the same registry, query, handoff, intake, locking, policy,
version, and Journal services as CLI and web.

Initial tools:

- `collab_list_projects`
- `collab_register_project`
- `collab_list_tasks`
- `collab_get_task`
- `collab_get_next_work`
- `collab_create_task`
- `collab_generate_handoff`
- `collab_submit_candidate`
- `collab_list_events`
- `collab_list_evidence`
- `collab_list_submissions`

Write tools accept structured inputs but must use agent-intake service methods. They cannot directly write task
files, events, state, Evidence, or receipts. The MCP layer generates canonical candidate bytes in memory and passes
the same captured bytes through receipt identity and validation. It must not create mutable temporary candidate
paths for re-reading.

The server exposes `collab://manual/agent-operating-guide` as a resource backed by the checked-in AI guide.

## Human-final policy

MCP never exposes approve or request-changes. Agent submission mode remains `agent_auto`, final review remains
`human`, and `auto_done` remains false. The web console is the only first-party surface for final review actions.

## Error behavior

Tool errors are concise, structured, and path-redacted. Version conflicts instruct the agent to refresh the task.
Rejected candidates return their persisted receipt status and reason. Unknown commit outcomes instruct the agent
to query submission history before retrying. MCP disconnects never imply that a task write failed or succeeded.

## Documentation

`docs/mcp/AI-OPERATING-GUIDE.md` explains the product boundary, tool choice, standard task workflow, Evidence
requirements, retry rules, and human-only operations. The same content is available as an MCP resource.

## Verification

- Global root resolution and first-run initialization tests.
- Multi-project registration and filtering tests.
- MCP in-process tool tests plus a stdio end-to-end smoke test.
- Tests proving approve and request-changes tools do not exist.
- Existing intake consistency, handoff, web, CLI, protocol, and store tests.
- `go test ./...`, `go vet ./...`, Windows build, and `git diff --check`.
