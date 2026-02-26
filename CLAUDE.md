# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**mcpl** is a daemon-based MCP (Model Context Protocol) server launcher written in Go. It shares MCP server subprocesses across multiple editor sessions (Claude Code, Claude Desktop, Cursor) via a single daemon that manages subprocess lifecycle over a Unix socket. Sessions connect through lightweight stdio shims.

Design document: `docs/plans/2026-02-26-mcpl-design.md`

## Build & Development Commands

```bash
# Build
go build -ldflags="-s -w" -o mcpl .

# Test
go test ./... -race

# Run single test
go test ./internal/daemon/ -run TestServerLifecycle -race

# Lint (install: brew install golangci-lint)
golangci-lint run

# Run the binary
./mcpl <command>
```

## Architecture

**Daemon + Shim pattern:**

- **Shim** (`mcpl connect <name>`) — tiny (~5MB) stdio proxy that bridges Claude Code's stdin/stdout to the daemon over a Unix socket. Auto-starts the daemon via flock-based atomic startup.
- **Daemon** (~12MB) — long-running process that manages MCP server subprocesses. It's a JSON-RPC-aware reverse proxy that remaps request IDs, caches `initialize` responses, and routes notifications.
- **Server subprocesses** — actual MCP servers spawned lazily on first connection, killed after idle timeout.

### Key packages

```
cmd/              # cobra CLI commands (connect, daemon, init, add, remove, status, stop, restart, logs, doctor, version)
internal/
  daemon/         # daemon loop, socket listener, server lifecycle state machine, idle timeouts
  protocol/       # JSON-RPC envelope parsing, ID remapping, MCP init caching, notification routing
  config/         # config load/save, env var resolution, client config detection & rewriting
  shim/           # connect logic, stdio bridging, flock-based daemon auto-start
main.go           # func main() { cmd.Execute() }
```

### Server lifecycle state machine

```
STOPPED -> STARTING -> INITIALIZING -> READY -> DRAINING -> STOPPED
```

### JSON-RPC ID remapping flow

Shim sends `id:1` → daemon rewrites to globally unique `id:7` → server responds `id:7` → daemon rewrites back to `id:1` → routes to correct shim. This prevents ID collisions when multiple shims share a server.

## Tech Stack

- Go 1.22+ (stdlib-heavy: `log/slog`, `encoding/json`, `net`, `os/exec`, `syscall`)
- Dependencies: cobra (CLI), testify (test), google/uuid
- IPC: Unix domain sockets with newline-delimited JSON
- Distribution: goreleaser + Homebrew tap

## Config Paths

| Platform | Config dir | Socket | Logs |
|----------|-----------|--------|------|
| macOS | `~/Library/Application Support/mcpl/` | `$TMPDIR/mcpl-$UID/mcpl.sock` | `~/Library/Logs/mcpl/` |
| Linux | `~/.config/mcpl/` | `$XDG_RUNTIME_DIR/mcpl/mcpl.sock` | `~/.config/mcpl/logs/` |

Override with `MCPL_CONFIG_DIR`.

## Key Design Decisions

- **Direct exec only** — no shell invocation, `command + args` array prevents injection
- **Connection = session** — each Unix socket connection becomes a session with a generated UUID, used for ID remapping and notification routing
- **Config hot-reload** — daemon re-reads config on each new `connect` handshake (adds new servers; changes to existing servers require restart)
- **Process group isolation** — server subprocesses spawned with `Setpgid: true`, killed via `kill(-pgid, SIGTERM)`
- **Daemon ignores SIGINT** — runs in own session (`Setsid: true`), immune to Claude Code's process group signals
- **File permissions** — `umask(0077)` on daemon startup; config `0600`, directories `0700`, socket `0600`
- **Env var references** — `$VAR` syntax in config env values, resolved at spawn time (secrets stay out of config)
- **Atomic file writes** — temp file + `rename()`, symlink check via `os.Lstat()`

## Conventions

- Structured logging via `log/slog` (JSON format)
- All file I/O through atomic write helpers
- Tests run with `-race` flag
- CI: GitHub Actions
