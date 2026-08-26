# Craaft MCP Server

Model Context Protocol server for [Craaft](https://craaft.io) — lets AI assistants read and write your Kanban boards.

Deployed as an HTTP service. Each request authenticates via the caller's `Authorization: Bearer cra_*` header — the same personal access tokens used by the Craaft REST API.

## Deploy

```bash
cd mcp
gcloud run deploy craaft-mcp \
  --source . \
  --region us-central1 \
  --allow-unauthenticated \
  --set-env-vars CRAAFT_BASE_URL=https://craaft.io
```

The `PORT` env var is set automatically by Cloud Run.

## Configure (Claude Desktop)

1. Sign in to Craaft → **Settings → API keys** → create a token
2. Add to `~/.config/claude/claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "craaft": {
      "url": "https://craaft-mcp-xxxxx.a.run.app/mcp",
      "headers": {
        "Authorization": "Bearer cra_your_token_here"
      }
    }
  }
}
```

## Tools

| Tool | Description |
|------|-------------|
| `list_projects` | List all accessible Kanban projects |
| `get_board` | Get a board's columns and cards |
| `search_cards` | Cross-project card search |
| `create_card` | Create a card with title, column, and optional metadata |
| `update_card` | Update a card's title, description, column, priority, assignee, etc. |
| `move_card` | Move a card to another column or board |
| `delete_card` | Permanently delete a card |
| `add_comment` | Add a comment to a card (supports @mentions) |
| `get_comments` | List comments on a card |
| `get_upcoming` | List all due-dated cards across projects |
| `get_focus` | Get focus overview: due cards, attention cards, hygiene counters |
| `get_me` | Get the authenticated user's profile |

## Endpoints

| Path | Purpose |
|------|---------|
| `POST /mcp` | MCP Streamable HTTP endpoint |
| `GET /healthz` | Health check (returns `{"status":"ok"}`) |

## Env vars

| Var | Default | Description |
|-----|---------|-------------|
| `PORT` | `8080` | Listen port (Cloud Run sets this automatically) |
| `CRAAFT_BASE_URL` | `https://craaft.io` | Upstream Craaft API URL |
