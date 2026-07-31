# Global Hub and MCP Implementation Plan

1. Add a global-root resolver with `COLLAB_HOME` override and first-run default client initialization.
2. Add a project registration service that validates a local directory and atomically creates project plus Binding.
3. Update the executable, launcher, and web server to use the global root and expose project selection/registration.
4. Add the official MCP Go SDK and a stdio `collab mcp` command.
5. Implement read tools directly on verified catalog/query services.
6. Implement write tools only through agent-intake and handoff services using captured candidate bytes.
7. Add the AI operating guide and expose it as an MCP resource.
8. Add unit, web, MCP, and stdio end-to-end tests.
9. Run formatting, tests, vet, build, diff checks, and a focused security review.
