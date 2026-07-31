// Package version is the single source of truth for the collab release version.
package version

// Version is the single source of truth for the collab release version. It is
// referenced by the CLI version command and the MCP server handshake; change it
// here rather than duplicating the value in command or server files.
const Version = "0.2.0"
