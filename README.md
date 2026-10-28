# Craaft MCP Server

Model Context Protocol server for [Craaft](https://craaft.io) - lets AI assistants read and write your Kanban boards.

Deployed as an HTTP service. Each request authenticates via the caller's `Authorization: Bearer` header - either a `cra_*` personal access token (same ones used by the Craaft REST API) or a `cro_*` OAuth access token minted by signing in through the connector flow below.

Two-factor sign-in on a craaft account doesn't affect either kind of token. Account security settings (password, two-factor, API keys) are browser-only on the platform, so no tool changes them.

## Deploy

```bash
cd mcp
gcloud run deploy craaft-mcp \
  --source . \
  --region us-central1 \
  --allow-unauthenticated \
  --set-env-vars CRAAFT_BASE_URL=https://craaft.io,MCP_PUBLIC_URL=https://ai.craaft.io/mcp
```

The `PORT` env var is set automatically by Cloud Run.

## Connect from Claude (OAuth)

With `MCP_PUBLIC_URL` set, the server is an OAuth 2.1 protected resource: no
token needs to be copied by hand.

craaft is in the [Claude directory](https://claude.ai/directory/craaft):
connect it from there and skip to step 3. Or add it as a custom connector:

1. In Claude Desktop or claude.ai, go to **Settings > Connectors > Add custom
   connector**.
2. Paste `https://ai.craaft.io/mcp`.
3. Sign in to craaft, choose **Read and write** or **Read only**, and approve
   the connection.

With read-only access, tools that only read (`list_projects`, `get_board`,
`search_cards` and the like) work, and every tool that changes something
fails with `this token is read-only; this request needs a token with read and
write access`. The same goes for a `cra_` key created as read only.

You can also connect it to **Chosen boards** only. Tools then see just those
boards: other boards are not found, and `list_projects` and `search_cards`
leave them out. `create_project`, `import_board`, `list_members`,
`list_invitations` and the other member tools fail with `this token is limited
to some boards; this request needs a token for all boards`. To change an app's
access or boards, connect it again and choose again.

To revoke access later, go to Craaft **Settings > Connected apps**.

## Configure (Claude Desktop, `cra_` token)

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

## MCP registry

craaft is listed in the official [MCP registry](https://registry.modelcontextprotocol.io)
as `io.craaft/mcp`, described by `server.json`. craaft.io proves it owns the
`io.craaft` namespace by serving a public key at
`https://craaft.io/.well-known/mcp-registry-auth` (platform repo,
`internal/server/mcpregistry.go`). The matching private key is not in this repo.

To publish a change, raise `version` in `server.json` (a published version
can't be changed or republished), then:

```bash
PRIVATE_KEY="$(openssl pkey -in ~/.config/craaft/mcp-registry-key.pem -noout -text | grep -A3 'priv:' | tail -n +2 | tr -d ' :\n')"
mcp-publisher login http --domain craaft.io --private-key "$PRIVATE_KEY"
mcp-publisher publish
```

Install `mcp-publisher` from the registry's
[releases](https://github.com/modelcontextprotocol/registry/releases).

## Tools

| Tool | Description |
|------|-------------|
| `list_projects` | List all accessible Kanban projects |
| `get_board` | Get a board's brief, columns and cards (descriptions omitted) |
| `get_card` | Get a single card by ID, including description |
| `get_card_detail` | Card plus comments, events, checklist and attachments in one call |
| `get_card_prompt` | Card as a Markdown brief for an AI tool, the same text as Copy as prompt |
| `search_cards` | Cross-project card search (description is a ≤180-char snippet) |
| `create_card` | Create a card at the bottom of a column, with optional metadata (assigned to you unless you name someone) |
| `update_card` | Update a card's title, description, column, position, priority, assignee, tags, etc. |
| `move_card` | Move a card to another column or board |
| `delete_card` | Permanently delete a card |
| `add_comment` | Add a comment to a card (supports @mentions) |
| `get_comments` | List comments on a card |
| `get_focus` | Get focus overview: due cards, attention cards, hygiene counters |
| `list_notifications` | Your notification inbox, newest first, plus the unseen count. Pass `kind: mention` to list where you were mentioned, with project, card and comment ids |
| `mark_notifications_read` | Mark notifications read by id, by card, or all of them |
| `get_me` | Get the authenticated user's profile |
| `bulk_create_cards` | Create up to 100 cards in one transaction |
| `bulk_update_cards` | Apply up to 100 partial card updates in one transaction |
| `bulk_move_cards` | Sweep up to 100 cards into a column, optionally on another board |
| `rebalance_cards` | Reorder a whole column in one transaction (positions 1, 2, 3, ...) |
| `get_checklist` | List a card's checklist items |
| `add_checklist_item` | Append an item to a card's checklist |
| `update_checklist_item` | Tick, untick or retitle a checklist item |
| `delete_checklist_item` | Delete a checklist item |
| `get_card_activity` | A card's activity log: column and board moves, priority and assignee changes |
| `get_board_activity` | A board's activity feed, newest first: cards added, moved, archived, deleted, comments, column changes |
| `get_hygiene` | The cards behind a `get_focus` hygiene counter (`ghosts`, `stuck`, `mine_no_date`) |
| `list_attachments` | List the files attached to a card |
| `list_board_files` | Search every file on a board's cards, with the card each is on |
| `download_attachment` | Fetch an attachment's bytes (text inline, binary base64) |
| `upload_attachment` | Attach a file to a card from inline content |
| `delete_attachment` | Delete an attachment and its stored bytes |
| `get_project` | One project with columns, counts, brief and your role |
| `create_project` | Create a board, optionally from a template (starts with To Do / In Progress / Done otherwise) |
| `update_project` | Rename, edit the brief, favourite, change visibility, colour scheme, background, text colour or top-bar buttons |
| `delete_project` | Delete a board and everything in it |
| `export_project` | Export a board as JSON or CSV |
| `preview_import` | Dry run of an import: what board a Trello, Jira, Asana, CSV or craaft export would create |
| `import_board` | Create a new board from a Trello, Jira, Asana, CSV or craaft export |
| `list_board_templates` | The fixed catalogue of board templates (kanban, sprint, bug tracker, ...) |
| `add_column` | Add a column to a board |
| `update_column` | Rename, recolour, reorder, or set a WIP limit |
| `delete_column` | Delete an empty column |
| `archive_column` | Archive every card in a done column, returns the archived ids |
| `archive_card` | Archive one card without deleting it |
| `restore_card` | Restore an archived card to its original column and position |
| `list_archived_cards` | A board's archived cards, newest first |
| `list_webhooks` | A board's outbound webhook subscriptions plus the event catalogue |
| `create_webhook` | Register an outbound webhook (craaft signed / Slack / Discord) |
| `update_webhook` | Pause/resume a webhook, or change its URL, format or event filter |
| `delete_webhook` | Delete a webhook subscription |
| `get_inbound_email` | A board's email-to-card intake address, if configured, and the month's AI card usage |
| `enable_inbound_email` | Turn on email-to-card and mint the intake address |
| `update_inbound_email` | Change active state, target column, or rotate the token |
| `disable_inbound_email` | Turn off email-to-card for a board |
| `list_members` | Workspace members, roles and board access |
| `update_member_role` | Change a member's workspace role |
| `remove_member` | Remove someone from the workspace |
| `list_invitations` | Pending invitations |
| `invite_member` | Invite by email, optionally with board grants |
| `revoke_invitation` | Revoke a pending invitation |
| `list_board_members` | Who can see a board, and whether access is explicit or implied |
| `grant_board_access` | Give a member access to a specific board, or change their board role |
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
| `GET /.well-known/oauth-protected-resource`, `GET /.well-known/oauth-protected-resource/mcp` | RFC 9728 protected-resource metadata (only served when `MCP_PUBLIC_URL` is set) |

## Env vars

| Var | Default | Description |
|-----|---------|-------------|
| `PORT` | `8080` | Listen port (Cloud Run sets this automatically) |
| `CRAAFT_BASE_URL` | `https://craaft.io` | Upstream Craaft API URL |
| `MCP_PUBLIC_URL` | unset (OAuth metadata off) | This server's own public `/mcp` URL, e.g. `https://ai.craaft.io/mcp`. When set, serves RFC 9728 protected-resource metadata and 401-challenges missing/invalid tokens to start the OAuth flow. Accepts both `cra_` personal access tokens and `cro_` OAuth access tokens either way. Must equal the platform's `MCP_RESOURCE_URL` (both sides lowercase the scheme and host and trim a trailing slash before comparing). |

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

### Import

`preview_import` and `import_board` take the same arguments: `filename`,
the file inline (`content_text` or `content_base64`), and optionally
`source` (`trello`, `jira`, `asana`, `csv` or `craaft`; detected when left
out), `name` for the new board, and a CSV `mapping` from craaft fields to
headers. Preview first: it writes nothing and, for a CSV, returns the
headers, the suggested mapping and sample rows. A mapping replaces the
suggestion, so send every field you want. `import_board` always creates a
new private board and counts toward the workspace's board limit (402 when
full). Limits: 5 MB, 2,000 cards, 200 columns; attachments aren't imported.

### Known platform quirk: archive then delete

`archive_column` only archives cards in a column flagged `isDone`, and
returns `200` with `archived: 0` (and an empty id list) on any other column
rather than erroring - check the count. Its returned ids let you undo the
sweep with `restore_card` on each one.

`delete_column` is blocked by **live** cards only. Archived cards no longer
block it - they are reassigned to the board's first column and the delete
proceeds, so they keep existing and stay archived. (This used to fail: the
`cards_project_column_fk` constraint still saw the archived rows, so the
delete returned 409 forever. Fixed on the platform side.)

### Archive and restore

`archive_card` takes a card off its board without deleting it; it stays
searchable and shows up in `list_archived_cards` until `restore_card` puts it
back in the exact column and position it was archived from. Archiving an
already-archived card, or restoring one that isn't archived, is an error.
`list_archived_cards` uses the same lean board projection as `get_board`
(description omitted), plus `archivedAt`, capped at 200 rows (newest first) -
older archived cards stay reachable through `search_cards`.

### Board templates

`list_board_templates` is the same fixed catalogue for every caller, no plan
gate. Pass a template's `key` as `create_project`'s `template` argument to
start a board with that column layout. `create_project`'s response never
includes `columns` at all, template or not - that field is only populated by
`get_project` / `get_board`. Only the default `kanban` template seeds the
historical `todo` / `doing` / `done` column keys anyway, every other template
generates its own, so call `get_project` on the new board before creating
cards on it.

### Webhooks and email-to-card (board admin, Pro)

Both surfaces are gated the same way: board not visible → not found; visible
but you're not a board admin → forbidden; Free workspace → a 402 naming
`"integrations"` as the limit. `delete_webhook` and `disable_inbound_email`
are **not** plan-gated, so a workspace that downgraded after setting either
up can still turn them off.

A webhook subscription's `secret` is returned in full on every read (list,
create, update) - it is never redacted after creation. It signs `craaft`
format deliveries via the `X-Craaft-Signature` header; `slack` and `discord`
deliveries are unsigned chat messages, authenticated only by the receiving
service's secret URL. `update_webhook`'s `subscription_id` argument is the
subscription `id`, not the workspace-level `endpoint_id` the response also
carries.

Email-to-card mints one intake address per board
(`<token>@<inbound-domain>`). Mail becomes a card only when the sender is a
workspace member and SPF/DKIM pass; everything else is dropped silently, no
error surfaces to the sender. `update_inbound_email`'s `rotate` flag mints a
new token and invalidates the old address immediately - there's no grace
period.

### Not exposed, and why

`GET /projects/{id}/events` is the board's **SSE realtime stream**, not an
activity log - a long-lived `text/event-stream` that doesn't fit a
request/response tool. For a board's history use `get_board_activity`
(`/projects/{id}/activity`), and for one card's history `get_card_activity`
(`/cards/{id}/events`).

Also absent: `POST /projects/{id}/cards/rebalance` (internal position
maintenance), the board background-image endpoints, and the unauthenticated
`/public/projects/{token}` read - a token-holding agent should just use
`get_board`.
