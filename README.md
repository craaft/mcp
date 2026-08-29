# Craaft MCP Server

Model Context Protocol server for [Craaft](https://craaft.io) - lets AI assistants read and write your Kanban boards.

Deployed as an HTTP service. Each request authenticates via the caller's `Authorization: Bearer cra_*` header - the same personal access tokens used by the Craaft REST API.

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
| `get_card` | Get a single card by ID. Prefer over `get_board` when the card ID is known |
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
| `bulk_create_cards` | Create up to 100 cards in one transaction |
| `bulk_update_cards` | Apply up to 100 partial card updates in one transaction |
| `bulk_move_cards` | Sweep up to 100 cards into a column, optionally on another board |
| `get_checklist` | List a card's checklist items |
| `add_checklist_item` | Append an item to a card's checklist |
| `update_checklist_item` | Tick, untick or retitle a checklist item |
| `delete_checklist_item` | Delete a checklist item |
| `get_card_activity` | A card's activity log: moves, priority and assignee changes |
| `get_hygiene` | The cards behind a `get_focus` hygiene counter (`ghosts`, `stuck`, `mine_no_date`) |
| `list_attachments` | List the files attached to a card |
| `download_attachment` | Fetch an attachment's bytes (text inline, binary base64) |
| `upload_attachment` | Attach a file to a card from inline content |
| `delete_attachment` | Delete an attachment and its stored bytes |
| `get_project` | One project with columns, counts and your role |
| `create_project` | Create a board (starts with To Do / In Progress / Done) |
| `update_project` | Rename, favourite, change visibility or colour scheme |
| `delete_project` | Delete a board and everything in it |
| `export_project` | Export a board as JSON or CSV |
| `add_column` | Add a column to a board |
| `update_column` | Rename, recolour, reorder, or set a WIP limit |
| `delete_column` | Delete an empty column |
| `archive_column` | Archive every card in a done column |
| `list_members` | Workspace members, roles and board access |
| `update_member_role` | Change a member's workspace role |
| `remove_member` | Remove someone from the workspace |
| `list_invitations` | Pending invitations |
| `invite_member` | Invite by email, optionally with board grants |
| `revoke_invitation` | Revoke a pending invitation |
| `list_board_members` | Who can see a board, and whether access is explicit or implied |
| `grant_board_access` | Give a member access to a specific board |
| `revoke_board_access` | Remove an explicit board grant |
| `update_comment` | Edit a comment (author only) |
| `delete_comment` | Delete a comment (author, or workspace owner/admin) |
| `set_card_follow` | Follow or unfollow a card |
| `list_tags` | Distinct tags on a board's cards, sorted, including archived |
| `list_milestones` | A board's milestones, earliest due first |
| `add_milestone` | Add a milestone (board admins) |
| `update_milestone` | Rename, re-date, or mark achieved |
| `delete_milestone` | Delete a milestone |
| `enable_sharing` | Turn on read-only public sharing, returns token + URL |
| `disable_sharing` | Revoke the public link |
| `get_avatar` | A user's avatar bytes, or the cue to render a placeholder |

The three `bulk_*` tools are all-or-nothing: one bad item rolls the whole
batch back and the error names the offending index, so a failed call never
leaves a partially applied board. They send no notification emails (activity
events and realtime updates still fire), and each call spends one rate-limit
token against a 60-burst budget - which is why they should be preferred over
looping the single-card tools.

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

### Attachments

This server runs remotely, so it cannot read the caller's filesystem:
`upload_attachment` takes the contents inline (`content_text` for text,
`content_base64` for binary), not a path.

`download_attachment` returns text-ish content types as readable text and
everything else base64-encoded. Attachments can be up to 25 MiB, which no
conversation wants inline, so it refuses anything over `max_bytes`
(default 256 KiB) and reports the real size instead. Text is truncated
rather than refused, since truncated text is still readable; truncated
base64 is not, so binary over the limit is skipped entirely.

Attachments are a paid feature. On a Free workspace the upload returns 402
and the error names the current plan. Check `canUploadAttachments` on the
project first - it reflects the **board's** workspace plan, not the caller's.

### Known platform quirk: archive then delete

`archive_column` only archives cards in a column flagged `isDone`, and
returns `200` with `archived: 0` on any other column rather than erroring -
check the count.

`delete_column` is blocked by **live** cards only. Archived cards no longer
block it - they are reassigned to the board's first column and the delete
proceeds, so they keep existing and stay archived. (This used to fail: the
`cards_project_column_fk` constraint still saw the archived rows, so the
delete returned 409 forever. Fixed on the platform side.)

### Not exposed, and why

`GET /projects/{id}/events` is the board's **SSE realtime stream**, not an
activity log - a long-lived `text/event-stream` that doesn't fit a
request/response tool. For per-card history use `get_card_activity`
(`/cards/{id}/events`), which is a real activity log.

Also absent: `POST /projects/{id}/cards/rebalance` (internal position
maintenance), the board background-image endpoints, and the unauthenticated
`/public/projects/{token}` read - a token-holding agent should just use
`get_board`.
