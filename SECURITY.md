# Security policy

## Reporting a vulnerability

Report security vulnerabilities through GitHub's private vulnerability
reporting, not through a public issue. Go to the
[Security tab](https://github.com/jophira/weft/security) and select "Report a
vulnerability". This opens a private advisory visible only to you and the
maintainers, and keeps the report out of the public issue tracker until a fix
is ready.

Public issues are the wrong channel for a vulnerability report. weft is
pre-1.0 (currently at v0.1.0), so a fix will usually ship as a patch release
once triaged.

## Supported versions

weft is pre-1.0. Only the latest release is supported with security fixes.

## Scope

weft reads and writes files under your home directory and harness config
directories (`~/.claude`, `~/.codex`, `~/.cursor`, and similar), manages git
sources, and can run `weft mcp serve` as a local MCP server over stdio. Issues
in scope include anything that lets weft write outside its managed paths,
mishandle a credential, or expose data to a party that should not see it.
