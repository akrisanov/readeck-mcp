# readeck-mcp

An MCP (Model Context Protocol) server for a self-hosted [Readeck](https://readeck.org/en/) instance.

This server exposes Readeck as MCP **tools** and **resources** so AI clients (ChatGPT/Codex/agent runtimes) can:

- Search articles/bookmarks
- Filter by title, full text, labels
- Exclude archived items by default (optionally include/only archived)
- Archive and unarchive articles
- Fetch full content and highlights as structured resources
- Generate citations (Markdown / CSL-JSON / BibTeX)

The goal is to make Readeck a reliable reading and highlighting backend for an AI-assisted learning
workflow (summaries, active recall, spaced repetition), while keeping the MCP server focused on
data access and normalization.

## Status

Early stage — only the project skeleton and specification are in place.

- Spec: [`docs/SPEC.md`](docs/SPEC.md)

## Requirements

- Go 1.22+ (recommended)

## Configuration

Set environment variables:

- `READECK_BASE_URL` — base URL of your Readeck instance, e.g. `https://readeck.example.com`
- `READECK_API_TOKEN` — Readeck API token (Bearer)
- `READECK_TIMEOUT_SECONDS` — optional (default: `20`)
- `READECK_USER_AGENT` — optional (default: `readeck-mcp/0.1`)
- `READECK_VERIFY_TLS` — optional (default: `true`)

## Quick start

```shell
go run ./cmd/readeck-mcp
```

## Project layout

- `cmd/readeck-mcp/` — entrypoint
- `internal/readeck/` — Readeck HTTP client and  DTO mapping
- `internal/mcp/` — MCP tools and resources handlers
- `internal/render/` — HTML/text cleaning and Markdown rendering
- `internal/citation/` — citation formatting (Markdown / CSL-JSON / BibTeX)
- `internal/config/` — configuration parsing/validation
- `docs/` — specifications and documentation

--

(C) 2026, Andrey Krisanov
