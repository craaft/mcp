# AGENTS.md

Instructions for AI coding agents (Claude, Codex, Cursor, Copilot, Gemini and others) working in this repository: the craaft MCP server.

## Copy: read VOICE.md first

craaft's voice guide lives in the platform repo: `../platform/VOICE.md` when the repos sit side by side, or https://github.com/craaft/platform/blob/main/VOICE.md. **Read it before writing or editing any text a person or an agent will read, and follow it.** Here that means tool names and descriptions, argument descriptions (the `jsonschema` tags), error messages, the README, commit messages and PR descriptions.

The rules agents break most often:

- Always lowercase "craaft", even at the start of a sentence.
- British spelling: colour, organise, licence (noun).
- No em dashes or en dashes, anywhere. Use a full stop, a comma, a colon or two sentences.
- Numerals for anything countable: "3 projects", "5 MB".
- No hype: no "AI-powered", "supercharge", "seamless", "10x". Say what the tool does.
- Only describe what the platform actually does.

Check your text against VOICE.md before you hand it back.

## Working rules

- The platform's API (`../platform/docs/openapi.yaml` and its handlers) is the source of truth for what a tool can promise.
- `go vet ./...` and `go test ./...` must pass. The README's tool table must list every registered tool (a test checks it).
- Don't commit, push or open pull requests unless the person you're working for asks you to.
- Never add AI attribution (co-author lines, "generated with" footers) to commits, PRs, code comments or files.
