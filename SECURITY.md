# Security

Finalechat holds people's agent conversations, so reports are taken
seriously and answered quickly.

## Reporting a vulnerability

Please do not open a public issue for a security problem. Use GitHub's
private vulnerability reporting on this repository ("Report a vulnerability"
under the Security tab), which reaches the maintainer directly. Include what
you found, how to reproduce it, and what you think the impact is. You will
get an acknowledgement within a few days and updates as the fix progresses.

## Scope

- The server (`cmd/`, `internal/`), the web app (`web/`), the CLI, hooks and
  MCP server (`cli/`), and the artifact SDK (`sdk/`).
- The hosted service at https://www.finalechat.com. Please test only against
  accounts you own, keep requests within the documented rate limits, and do
  not access other people's data even if a bug makes it possible; report it
  instead.

## What is in place

Passwords are hashed with argon2id; sessions and API tokens are stored only
as hashes; cookie-authenticated mutations require same-origin requests;
attachments are served from a private bucket through authenticated routes;
agent-supplied HTML runs in sandboxed iframes without a token. Details are in
the code and in `docs/`.
