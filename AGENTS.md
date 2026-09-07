# Finalechat for agents

Finalechat is the app where agents message the user. Everything you would
print for the user in a terminal, you can also post here; the user reads it
on their phone, gets a notification when it matters, answers your questions
with a tap, and can reply in a thread that you can wait on. One thread per
agent session, plain HTTPS, bearer token auth. This page is everything you
need. The full reference is at <https://www.finalechat.com/api/> and the
OpenAPI document at <https://www.finalechat.com/api/openapi.json>.

## Authentication

The user creates a token in the app under **Settings → Agents**. Tokens start
with `fc_` and are usually handed to you as the `FINALECHAT_TOKEN` environment
variable. Send it as a bearer token on every request:

```bash
export FINALECHAT_TOKEN=fc_...            # set by the user, or already in your env
curl -sS https://www.finalechat.com/api/v1/me \
  -H "Authorization: Bearer $FINALECHAT_TOKEN"
```

If `FINALECHAT_TOKEN` is not set and there is no `~/.config/finalechat/config.json`,
ask the user for a token; do not guess one.

## The fastest path

Three calls cover almost every situation. The `ext:` prefix names a thread by
an id you choose (your session id is ideal); the thread is created on first
use, so you never have to look anything up.

**1. Post a message.** Set `title` and `agent` on the first post so the
thread is named; they are ignored once the thread exists.

```bash
curl -sS https://www.finalechat.com/api/v1/threads/ext:my-session-42/messages \
  -H "Authorization: Bearer $FINALECHAT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "title": "finalechat",
    "agent": "Claude Code",
    "body": "Deployed **v1.2.0** to staging.\n\n- 42 tests passed\n- bundle 118 KB",
    "importance": "important"
  }'
```

**2. Ask a question and block for the answer.** `wait` holds the request open
up to 600 seconds. The response carries the question in its current state:
`status` is `answered` when the user responded, `pending` while they have
not (keep waiting with `GET /questions/{id}?wait=600`), or `dismissed`,
`cancelled` or `expired` when there will be no answer.

```bash
curl -sS "https://www.finalechat.com/api/v1/threads/ext:my-session-42/questions?wait=600" \
  -H "Authorization: Bearer $FINALECHAT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "prompt": "Staging looks good. Promote to production?",
    "options": [
      {"label": "Ship it", "description": "Promote the same image"},
      {"label": "Hold",    "description": "Wait for me to look"}
    ],
    "allow_freeform": true,
    "timeout_seconds": 3600
  }'
```

```json
{"question": {"id": "01a07a60-…", "status": "answered",
              "answer": {"selected": ["Ship it"], "text": ""}, "…": "…"},
 "thread": {"…": "…"}}
```

**3. Wait for the user's next reply.** Pass the id of the last message you
have seen as `after` (or omit it to wait for anything new from now on); the
call returns as soon as a newer message exists, or after `wait` seconds with
an empty list and `"timed_out": true`.

```bash
curl -sS "https://www.finalechat.com/api/v1/threads/ext:my-session-42/messages?after=$LAST_ID&sender=user&wait=600" \
  -H "Authorization: Bearer $FINALECHAT_TOKEN"
```

Both `wait` calls return early on their own when the user acts. A 600 second
cap keeps proxies happy; loop if you need longer.

## Threads

- A thread is one conversation between one agent session and the user. Use
  one thread per session and reuse it for the whole session.
- Every thread has an optional `external_id` that you choose. `POST /threads`
  with an `external_id` is idempotent: it returns the existing thread with
  HTTP 200 and `"created": false`, or creates it with HTTP 201. Blank
  `title`/`agent` on an existing thread are filled from your request and
  `meta` is merged.
- Anywhere a path takes a thread you may use its UUID or `ext:<external_id>`.
  `POST …/messages` and `POST …/questions` create a missing `ext:` thread on
  the fly; every other route returns 404 for an unknown one.
- `PATCH /threads/{ref}` sets `title`, `agent`, `archived`, `muted` or merges
  `meta`. A new message or question un-archives a thread automatically.

## What to post, and when

- Post every message you would show the user in the terminal: progress,
  results, the final summary. Markdown is rendered (`format` defaults to
  `markdown`; use `"text"` for raw output).
- Normal messages update the app quietly. Use `"importance": "important"` for
  things worth a buzz on the phone: a finding, a finished long task, a
  failure that blocks you. `"notify": true` forces a push for one message and
  `"notify": false` suppresses one; both override the importance rule.
- Need a decision? Ask a question instead of stopping. Questions always
  notify. Offer 2 to 5 short options with a one-line `description` each, and
  leave `allow_freeform` on unless free text makes no sense. Set
  `multi_select` when several options may apply. Give a `timeout_seconds` if
  the answer is worthless after a while; the question then expires on its own.
- Keep titles short (the project or task name); the app shows them next to
  the agent name.
- The user can reply in the app at any time. Replies arrive as messages with
  `"sender": "user"` and `"origin": "session"`; an answered question also adds
  a user message that records the choice. (A message with `"origin":
  "token"` was posted by an agent, for example a terminal prompt mirrored
  into the thread; treat only `session` messages as the user speaking from
  the app.)
- Only an answer writes a message. A question the user declines becomes
  `dismissed`, one you withdraw becomes `cancelled`, and one that runs out
  becomes `expired`, silently: while you have a question open, watch it with
  `GET /questions/{id}?wait=600` (or the `wait` on the ask itself) rather than
  polling messages alone, and treat every status other than `pending` as
  final. On `dismissed`, do not ask again; proceed with your best judgement
  and say so.
- Retries are safe: send an `Idempotency-Key` header (or a `client_key`
  field) on a message or question post, and a retry after a timeout returns
  the original with HTTP 200 and `"created": false` instead of a duplicate.
  A key is scoped to its thread and may be any string up to 200 characters
  (a UUID is ideal).
- Archiving is the user's call: a plain message leaves an archived thread
  archived (it still collects unread), while an `important` message or a
  question brings it back to the inbox.
- Do not post secrets, tokens or credentials. If one slips out,
  `DELETE /messages/{id}` (or `finalechat delete <message id>`) removes the
  message and its files for good; the app drops it at once.

## Screenshots and files

Messages can carry up to 8 attachments of up to 10 MiB each: screenshots,
logs, diffs, PDFs, anything. Images (`png`, `jpeg`, `gif`, `webp`) get a
thumbnail in the app and are shown inline. One multipart request uploads and
posts at the same time; `body` is optional when files are present:

```bash
curl -sS https://www.finalechat.com/api/v1/threads/ext:my-session-42/messages \
  -H "Authorization: Bearer $FINALECHAT_TOKEN" \
  -F body="Staging after the deploy. Note the empty sidebar." \
  -F importance=important \
  -F file=@staging.png \
  -F file=@deploy.log
```

The user can also send you files. They arrive as messages whose
`attachments` list has the metadata and a relative `url`; fetch it with the
same bearer token (images also have a `thumb_url`):

```bash
curl -sS "https://www.finalechat.com/api/v1/threads/ext:my-session-42/messages?after=$LAST_ID&sender=user&wait=600" \
  -H "Authorization: Bearer $FINALECHAT_TOKEN"
# ... "attachments": [{"id": "01a07ab9-…", "kind": "image", "content_type": "image/png",
#                      "filename": "IMG_0421.png", "size": 168591, "width": 900, "height": 300,
#                      "url": "/api/v1/attachments/01a07ab9-…", "thumb_url": "/api/v1/attachments/01a07ab9-…/thumb"}]

curl -sS -o /tmp/IMG_0421.png https://www.finalechat.com/api/v1/attachments/01a07ab9-… \
  -H "Authorization: Bearer $FINALECHAT_TOKEN"
```

Then look at the file with whatever your harness uses to read images. To
upload first and attach later (for example, several files from different
steps), `POST /threads/{ref}/attachments` with `-F file=@…` parts or a raw
body whose `Content-Type` is the file type (name it with `X-Filename`), then
pass the returned ids in the message's `attachments` field. Uploads not
attached within 24 hours are discarded.

With the CLI: `finalechat say "Staging after the deploy" -f staging.png`
(repeat `-f` for more files), `finalechat fetch <id or url> -o /tmp/x.png`
to download, and `read`/`wait` print a `📎 filename (type, size) url` line
under each message. With MCP, `finalechat_send` takes `attachments` (file
paths), and `finalechat_wait_for_reply`, `finalechat_read` and
`finalechat_view_attachment` return images as image content, so the model
sees a screenshot directly; `finalechat_fetch_attachment` saves a file.

## Say what you are doing

Between messages the user only sees silence. A status line fixes that: one
short sentence about what you are doing right now, shown in the app as a
typing indicator ("running the test suite…") with how long you have been at
it. It is not part of the transcript, it never notifies, and it can never go
stale: it lapses after `ttl_seconds` (default 45, at most 600) unless you
refresh it, and it is dropped the moment you post a message or a question.
Send one whenever you start something that takes more than a few seconds,
and again whenever the step changes; refresh it if a step runs long.

```bash
curl -sS https://www.finalechat.com/api/v1/threads/ext:my-session-42/activity \
  -H "Authorization: Bearer $FINALECHAT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"text": "Checking out the files you asked for…", "kind": "working", "ttl_seconds": 60}'
```

`kind` is `thinking`, `working` (default), `typing` (you are composing a
reply), `waiting` (blocked on the user or on something external) or `tool`
(a command or tool is running). The response is `{"thread": {...}, "applied":
true}`; the thread's `activity` field is `{text, kind, at, since,
expires_at}`, and `since` survives refreshes so the app can say "for 3
minutes". Clear it early with `DELETE /threads/{ref}/activity` or by posting
`{"text": ""}`. Unlike messages, a status never creates a thread: post a message first (a
stray status must not litter the inbox), or the call is a `404`. The app
also shows a few reserved `meta` keys on the thread as chips: `host`, `cwd`,
`branch`, `model`, `cost_usd` and `tokens`; set them with
`PATCH /threads/{ref}` (`meta` merges) whenever you have them.

Two details for busy agents. If you keep working after you speak, put the
next status on the message itself so the line never blinks:
`{"body": "Tests pass. Starting the backfill.", "activity": {"text": "Running
the backfill…", "ttl_seconds": 120}}` (questions accept the same field; a
message or question without it clears the status, because it is the outcome
the status announced). If several processes write statuses at once, pass a
monotonic `seq` (the convention is a nanosecond timestamp, and every writer
of a thread must use the same clock); a write whose `seq` is not above the
live status's is ignored and comes back with `"applied": false`, so a slow
"Thinking…" can never overwrite a newer "Running tests". Pass the same
`seq` when you clear (`DELETE …/activity?seq=`) so a straggler cannot bring
the status back.

With the CLI: `finalechat status "Running the migration…"` (add `--ttl 120`
for long steps, `--clear` to drop it). With MCP: `finalechat_status`.

## Command-line helper

The `finalechat` CLI wraps the API with sensible defaults (Python 3.9+,
standard library only). It signs in automatically when `FINALECHAT_TOKEN` is
set.

```bash
curl -fsSL https://www.finalechat.com/install.sh | sh     # installs ~/.local/bin/finalechat
finalechat login fc_...                                  # or rely on FINALECHAT_TOKEN
finalechat whoami

finalechat say "Tests are green; starting the migration." --title finalechat --agent "Claude Code"
finalechat say "Migration failed on step 3, need a decision." --important
finalechat status "Running the migration…" --ttl 120   # live status line, lapses on its own
finalechat ask "Promote to production?" -o "Ship it::Promote the same image" -o "Hold::Wait for me" --timeout 3600
finalechat wait                     # prints the user's next reply
finalechat read --limit 20          # recent messages in the current thread
finalechat threads                  # every active thread
```

The thread comes from `-t/--thread` (a UUID or `ext:<id>`), then
`FINALECHAT_THREAD`, then the Claude Code session thread for the current
directory, then a per-directory default. `ask` prints the selected labels on
the first line and any free text after it; it exits 0 when answered, 3 when
the wait ran out (resume with `finalechat wait-answer <question-id>`), and 4
when the question was cancelled or expired. `wait` exits 3 on timeout. Add
`--json` to any command for the raw API object.

## Claude Code hooks and MCP

`finalechat install claude-code` mirrors an entire Claude Code session to the
phone with hooks: `SessionStart` creates the thread `claude-code:<session_id>`
named after the project directory, `UserPromptSubmit` mirrors the user's
prompts, `Stop` posts Claude's final message of each turn, `Notification`
posts an important message when Claude needs permission or is idle,
`PreToolUse` on `AskUserQuestion` mirrors the question to the phone, every
other `PreToolUse`/`PostToolUse` keeps a live status line on the thread
("Running: go test ./…", then "Thinking…") from a detached background
process so the session never waits on it, and `SessionEnd` posts a system
note. It also registers the MCP server
(`claude mcp add --scope user finalechat -- finalechat mcp`), which exposes
`finalechat_send`, `finalechat_ask`, `finalechat_wait_for_reply`,
`finalechat_read` and `finalechat_status` as tools.

**Remote mode** is a switch in the app's Settings (also `finalechat remote
on|off`). While it is on, the `Stop` hook waits for the user's phone reply
and feeds it back to Claude as the next instruction, and a phone answer to
`AskUserQuestion` is used instead of the terminal prompt. Read it yourself
from `GET /me` → `user.settings.remote_mode` if you want to adapt your own
behaviour, for example by waiting on questions longer.

## Errors and limits

Errors are JSON with a stable `code` and a human `message`:

```json
{"error": {"code": "validation_failed", "message": "\"Maybe\" is not one of the offered options."}}
```

| Status | Codes |
| --- | --- |
| 400 | `bad_request` |
| 401 | `unauthorized`, `invalid_token`, `invalid_credentials` |
| 403 | `forbidden`, `csrf`, `signup_closed`, `invalid_invite` |
| 404 | `not_found` |
| 409 | `conflict`, `already_resolved`, `email_taken`, `no_subscriptions` |
| 413 | `too_large` |
| 422 | `validation_failed` |
| 429 | `rate_limited` (a token writing faster than about 120 messages, 30 questions, 30 uploads or 300 statuses a minute; wait for the `Retry-After` header) |
| 502 | `storage_unavailable` |
| 503 | `push_disabled`, `attachments_disabled`, `busy` |
| 500 | `internal_error` |

Limits: JSON bodies 1 MiB; message `body` 256 KiB; question `prompt` 8000
bytes; up to 20 options with labels of 200 and descriptions of 1000
characters; `meta` 16 KiB; `title` 300, `agent` 120, `external_id` 300
characters; `wait` is clamped to 600 seconds; `timeout_seconds` 1 to 604800;
attachments 10 MiB each, 8 per message; activity `text` 200 characters on
one line with `ttl_seconds` 1 to 600; a question with no `timeout_seconds`
expires after 7 days. All timestamps are UTC RFC 3339; all ids are UUIDs.
Every response carries an `X-Request-Id` header; quote it when reporting a
problem. `GET /me` lists optional capabilities in `features` (`activity`,
`idempotency`, `dismiss`, `push`, `attachments`) if you need to
feature-detect a deployment.

## Cheat sheet

Base URL `https://www.finalechat.com/api/v1` (also `https://api.finalechat.com/api/v1`).
`{ref}` is a thread UUID or `ext:<external_id>`.

| Method and path | Purpose |
| --- | --- |
| `GET /me` | Who am I, badge counts, settings (`remote_mode`) |
| `GET /threads?archived=&q=&limit=&cursor=` | List threads, newest activity first |
| `POST /threads` | Create or fetch by `external_id` (idempotent) |
| `GET /threads/{ref}` | One thread |
| `PATCH /threads/{ref}` | `title`, `agent`, `archived`, `muted`, `meta` |
| `DELETE /threads/{ref}` | Delete thread and contents |
| `POST /threads/{ref}/read` | Mark read |
| `POST /threads/{ref}/activity` | Set the status line (`text`, `kind`, `ttl_seconds`, `seq`); empty `text` clears; never creates a thread |
| `DELETE /threads/{ref}/activity` | Clear the status line |
| `POST /threads/{ref}/messages` | Post a message (creates `ext:` thread); JSON with `attachments` ids, or multipart with `file` parts; optional `activity`, `client_key` |
| `GET /threads/{ref}/messages?after=&before=&limit=&sender=&wait=` | Read or wait for messages |
| `GET /messages/{id}` | One message |
| `DELETE /messages/{id}` | Remove a message and its files for good (posted a secret by mistake) |
| `POST /threads/{ref}/attachments` | Upload files (multipart `file` parts or a raw body) to attach later |
| `GET /attachments/{id}`, `GET /attachments/{id}/thumb` | Download a file, or an image's JPEG thumbnail |
| `POST /threads/{ref}/questions?wait=` | Ask; optionally block for the answer |
| `GET /threads/{ref}/questions` | Questions in a thread, oldest first |
| `GET /questions?status=&thread_id=&limit=` | Questions across threads, newest first |
| `GET /questions/{id}?wait=` | One question; optionally block until resolved |
| `POST /questions/{id}/answer` | Answer (`selected`, `text`) |
| `POST /questions/{id}/cancel` | Withdraw a pending question |
| `POST /questions/{id}/dismiss` | The user declines to answer (the app calls this) |
| `GET /events` | Server-sent events stream (`thread.activity` carries status changes) |
| `GET /counts` | Pending questions and unread threads |
| `GET /settings`, `PATCH /settings` | `notify_all_messages`, `remote_mode` |
