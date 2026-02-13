# Readeck MCP Server (Go) — Specification

> Repo doc to implement an MCP server that exposes a self-hosted Readeck instance as MCP tools/resources for search, filtering, archiving, and citations.

## Goal

Implement an **MCP server in Go** that lets an MCP client (ChatGPT/Codex/agent runtime) do:

- Search for articles (bookmarks)
- Filter by title, full text, labels
- Exclude archived articles by default (and optionally include/only)
- Archive/unarchive
- Generate citations (for whole articles and optionally specific highlights/quotes)

The server should **only** fetch/normalize Readeck data and expose it as MCP **tools** and **resources**.
LLM summarization / active recall / spaced repetition is handled by the client (ChatGPT), not inside this server.

MCP transport: **stdio** by default (recommended by spec). MCP uses JSON-RPC messages over stdio.
References: MCP transports overview. [oai_citation:0‡Model Context Protocol](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports)

## Non-goals

- Writing into the Obsidian vault directly (pair this with a filesystem/Obsidian MCP server)
- Running any LLMs inside this server
- Building a full Readeck client SDK (keep it minimal for required endpoints)

---

## Readeck API & Auth assumptions

- Base API endpoint: `${READECK_BASE_URL}/api`
- Auth: **Bearer token** via `Authorization: Bearer <token>` (create token via Readeck UI / OAuth flow depending on Readeck version)
- Note: Readeck has been deprecating legacy auth endpoints like `/api/auth`; apps should use OAuth to obtain API tokens in newer versions. [oai_citation:1‡Readeck](https://readeck.org/en/blog/202511-readeck-21/)

**Important implementation requirement:** Because exact endpoints/params can vary by Readeck version, keep HTTP routes and query param mapping isolated in `internal/readeck/`, and verify against the API docs served by your own instance.

## Configuration

### Environment variables

- `READECK_BASE_URL` (required)
  Example: `https://readeck.example.com`
- `READECK_API_TOKEN` (required)
  Bearer token (never log it)
- `READECK_TIMEOUT_SECONDS` (optional, default `20`)
- `READECK_USER_AGENT` (optional, default `readeck-mcp/0.1`)
- `READECK_VERIFY_TLS` (optional, default `true`)
- `READECK_MAX_PAGE_SIZE` (optional, default `100`)

### HTTP defaults

- `Authorization: Bearer ${READECK_API_TOKEN}`
- `Accept: application/json`
- Per-request timeout + context cancellation
- Retry only safe requests (GET) on `429`/`5xx` with exponential backoff

## Internal Data Model (normalized)

Use Go structs with JSON tags. Be tolerant to extra fields from Readeck.

### Bookmark / Article (normalized)

- `id` (string)
- `url` (string)
- `title` (string)
- `site_name` (string, optional)
- `author` (string, optional)
- `published_at` (RFC3339 string, optional)
- `created_at` (RFC3339 string, required if available)
- `updated_at` (RFC3339 string, required if available)
- `is_archived` (bool)
- `is_favorite` (bool, optional)
- `labels` ([]Label)
- `content_text` (string, optional; fetched on demand)
- `content_html` (string, optional; fetched on demand)
- `highlights` ([]Highlight, optional; fetched on demand)

### Label

- `id` (string, optional)
- `name` (string)
- `color` (string, optional)

### Highlight

- `id` (string)
- `bookmark_id` (string)
- `text` (string)
- `note` (string, optional)
- `color` (string, optional)
- `created_at` (RFC3339 string, optional)
- `location` (optional; anchor/offset if Readeck provides it)

### Citation output

- `style` (`apa` | `mla` | `chicago` | `bibtex` | `csl-json` | `markdown`)
- `text` (string) — rendered citation (or markdown)
- `csl_json` (object, optional)
- `bibtex` (string, optional)
- `metadata` (object): `title`, `author`, `site_name`, `published_at`, `url`, `accessed_at`

## MCP Features

Expose:

- **Tools** for actions & queries
- **Resources** for large content (avoid huge tool outputs)
- Optional **Prompt templates** for common workflows

MCP supports tools/resources/prompts as first-class server features.  [oai_citation:2‡Model Context Protocol](https://modelcontextprotocol.io/specification/2025-06-18/basic)

### Tools

#### `readeck.search`

Search and filter bookmarks.

##### Input schema

- `query` (string, optional) — full-text search (title + body where supported)
- `title` (string, optional) — title-only filter (use API param if supported; else post-filter)
- `text` (string, optional) — body-only filter (use API param if supported; else merge into `query`)
- `labels` ([]string, optional) — label names
- `archived` (`include` | `only` | `exclude`, default `exclude`)
- `favorites` (bool, optional)
- `sort` (`relevance` | `updated_desc` | `created_desc` | `published_desc`, optional)
- `limit` (int, default 20, max `READECK_MAX_PAGE_SIZE`)
- `cursor` (string, optional) — pagination token

##### Output schema

- `items` ([]BookmarkSummary)
- `next_cursor` (string, optional)

##### BookmarkSummary

- `id`, `title`, `url`
- `is_archived`
- `labels` ([]string)
- `created_at`, `updated_at`, `published_at` (if known)
- `snippet` (string, optional; short excerpt)

##### Default behavior

- Exclude archived items unless `archived=include|only`.

#### `readeck.get`

Fetch a bookmark with optional content/highlights.

##### Input

- `id` (string, required)
- `include` (object, optional):
  - `content` (bool, default false)
  - `highlights` (bool, default true)
  - `labels` (bool, default true)

##### Output

- `bookmark` (Bookmark)

#### `readeck.archive`

Archive/unarchive a bookmark.

##### Input

- `id` (string, required)
- `archived` (bool, default true)

##### Output

- `id`
- `is_archived`
- `updated_at` (if known)

##### Idempotency

- Calling archive on already archived should succeed (no error).

---

#### `readeck.labels.list`

List all labels.

##### Input

- `limit` (int, default 200, max 500)
- `cursor` (string, optional)

##### Output

- `labels` ([]Label)
- `next_cursor` (string, optional)

---

#### `readeck.labels.set`

Replace labels on a bookmark (idempotent).

##### Input

- `id` (string, required)
- `labels` ([]string, required) — label names

##### Output

- `id`
- `labels` ([]string)

---

#### `readeck.highlights.list`

List highlights for a bookmark (bookmark-scoped).

##### Input

- `bookmark_id` (string, required)
- `limit` (int, default 200, max 500)
- `cursor` (string, optional)

##### Output

- `highlights` ([]Highlight)
- `next_cursor` (string, optional)

---

#### `readeck.cite`

Generate a citation for a bookmark, optionally including a specific highlight or user-provided quote.

##### Input

- `bookmark_id` (string, required)
- `highlight_id` (string, optional)
- `quote` (string, optional) — if provided, embed into markdown output
- `style` (`apa`|`mla`|`chicago`|`bibtex`|`csl-json`|`markdown`, default `markdown`)
- `accessed_at` (RFC3339 string, optional; default now)

##### Output

- `citation` (Citation)

##### Rules

- If `author` missing: use `site_name` or omit author.
- If `published_at` missing: use “n.d.” (or omit date depending on style) and always include `accessed_at`.
- Markdown output should include:
  - one reference line
  - the URL
  - optional blockquote if `highlight_id`/`quote` present

### Resources

Resources allow the client to fetch large content without returning it in tool responses.

#### Resource URIs

- `readeck://bookmark/{id}`
  JSON metadata (Bookmark without full content by default)
- `readeck://bookmark/{id}/content.md`
  Markdown (front-matter + cleaned text)
- `readeck://bookmark/{id}/content.txt`
  Plain text
- `readeck://bookmark/{id}/highlights.json`
  Highlights list JSON
- `readeck://bookmark/{id}/highlights.md`
  Highlights rendered as Markdown quotes/bullets

#### Markdown rendering guidelines (`content.md`)

- YAML front-matter:
  - `title`, `url`, `author`, `site_name`, `published_at`, `created_at`, `updated_at`
  - `readeck_id`
  - `labels`
  - `archived`
- Body:
  - cleaned article text (best-effort)
- Footer section:
  - “## Highlights” (optional; only when requested or when generating combined view)

---

### Prompt templates (optional)

These are reusable prompt “stubs” for the MCP client. They do not execute LLM calls.

#### `readeck.prompt.summarize`

Inputs:

- `bookmark_id`
- `focus` (`key_ideas` | `arguments` | `action_items` | `teach_back`)

Template instructs client to open:

- `readeck://bookmark/{id}/content.md`
- `readeck://bookmark/{id}/highlights.md`

#### `readeck.prompt.flashcards`

Inputs:

- `bookmark_id`
- `num_cards` (default 10)
- `card_type` (`qa` | `cloze`)
- `use_highlights` (bool, default true)

Template instructs client to open:

- `readeck://bookmark/{id}/content.md`
- `readeck://bookmark/{id}/highlights.md`

## Search Semantics & Ranking

Because Readeck API capabilities may vary:

1. Prefer server-side filters if the API supports them.
2. Otherwise, do post-filtering on the fetched page(s):
   - `title`: substring match (case-insensitive)
   - `labels`: must contain all requested labels (AND semantics)
   - `archived`: enforce default exclude

Sorting:

- If API returns relevance for `query`, keep it when `sort=relevance`
- Otherwise default to `updated_at desc` (or `created_at desc` if `updated_at` missing)

Pagination:

- Use upstream cursor/page tokens if provided
- Server should return `next_cursor` opaque to client

---

## Citation Generation Details

The citation tool must be fully offline (no external services).

### CSL-JSON minimal shape

- `type`: `webpage` or `article`
- `title`
- `URL`
- `author`: array of `{ family, given }` if parseable, else `{ literal }`
- `issued`: date-parts if `published_at` exists
- `accessed`: date-parts always

### BibTeX minimal shape

Use `@online{<key>, ...}` with:

- `title`, `author` (if available), `year` (if available), `url`, `urldate`

Key generation:

- base on `site_name` + `year` + short hash of URL

## Errors & Observability

### Tool error mapping

Return MCP errors with:

- `code`: `invalid_input` | `unauthorized` | `not_found` | `rate_limited` | `upstream_error`
- `message`: human readable
- `details`: `{ http_status, endpoint, request_id }` (never include token)

### Logging (stderr)

- request id per tool call
- upstream latency
- status codes
- retry counts
- response sizes (optional)

## Security

- Never log `READECK_API_TOKEN`
- Redact `Authorization` header in any debug output
- Optional: validate `READECK_BASE_URL` scheme is https (unless explicitly allowed for local dev)
- If adding HTTP transport later, bind to localhost or require a separate MCP-server API key

## Suggested Go Project Layout

- `cmd/readeck-mcp/main.go`
  MCP bootstrap, stdio transport, capability wiring
- `internal/mcp/`
  Tool handlers, resource handlers, prompt templates
- `internal/readeck/`
  HTTP client, DTO mapping, pagination, retries
- `internal/render/`
  Markdown rendering, HTML->text cleaning, snippets
- `internal/citation/`
  CSL-JSON + BibTeX + Markdown formatting
- `internal/config/`
  env parsing + validation

## Acceptance Criteria (Definition of Done)

1. `readeck.search` returns only non-archived by default; `archived=include|only` works.
2. Filters by `labels` and `title` behave correctly (server-side if possible, else post-filter).
3. `readeck.archive` is idempotent and supports unarchive.
4. Resource `readeck://bookmark/{id}/content.md` returns readable markdown (front-matter + text).
5. `readeck.cite` returns valid output for `markdown`, `csl-json`, and `bibtex` with graceful fallbacks.

## References

- MCP specification overview and server features (tools/resources/prompts). [oai_citation:3‡Model Context Protocol](https://modelcontextprotocol.io/specification/2025-06-18/basic)
- MCP transports (stdio + Streamable HTTP; JSON-RPC).  [oai_citation:4‡Model Context Protocol](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports)
- Readeck auth changes: legacy `/api/auth` deprecated; OAuth/token direction.  [oai_citation:5‡Readeck](https://readeck.org/en/blog/202511-readeck-21/)
- Bearer auth scheme usage in Readeck API context.  [oai_citation:6‡GitHub](https://github.com/FreshRSS/FreshRSS/issues/6552)
