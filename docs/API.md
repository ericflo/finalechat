# Finalechat API reference

This is the complete reference for the Finalechat HTTP API. If you are an
agent that only needs to post messages and ask questions, start with the
shorter guide at <https://www.finalechat.com/AGENTS.md>. A machine-readable
OpenAPI 3.1 document is at <https://www.finalechat.com/api/openapi.json>.

## Base URL

```
https://www.finalechat.com/api/v1
https://api.finalechat.com/api/v1
```

Both hosts serve the same API. Examples below use the first.

## Authentication

**API tokens** are for agents and scripts. The user creates them in the app
under Settings → Agents. A token starts with `fc_` and is shown once. Send it
as a bearer token:

```
Authorization: Bearer fc_0123456789abcdefghijklmnopqrstuvwxyzABCD
```

A token acts as the user who created it: it sees the user's threads, posts
as the `agent` sender by default, and can answer questions (useful for
testing). Tokens cannot create or revoke tokens or change the account.

**Sessions** are for the app. `POST /auth/login` sets an `fc_session`
cookie (HttpOnly, SameSite=Lax, 90 days sliding). Cookie-authenticated
requests that change state must be same-origin: the server checks
`Sec-Fetch-Site` when present and otherwise requires `Origin` to match the
request host; failures return `403 csrf`.

Unauthenticated requests to any `/api/v1/*` route other than the auth
endpoints and `GET /push/vapid` receive `401 unauthorized`.

## Conventions

- Requests and responses are JSON (`Content-Type: application/json`).
  Responses are `no-store` and compressed when the client accepts gzip.
- Every id is a UUID. Ids are time-ordered, so sorting by id is sorting by
  creation.
- Every timestamp is UTC in RFC 3339 form, e.g. `2026-09-07T05:38:55.043799Z`.
- Objects are returned inside a named key (`{"thread": {...}}`,
  `{"messages": [...]}`) so responses can grow without breaking clients.
- Unknown fields in requests are ignored. Unknown routes under `/api/v1/`
  return `404 not_found`.
- `meta` fields are free-form JSON objects (at most 16 KiB) that the API
  stores and returns verbatim. `PATCH` and idempotent create merge `meta`
  keys rather than replacing the object.
- Attachment bytes are stored in a private Backblaze B2 bucket and streamed
  through the API; PostgreSQL holds only their metadata. Downloads need the
  same credentials as every other route.

### Errors

```json
{"error": {"code": "validation_failed", "message": "prompt is required."}}
```

| Status | Code | When |
| --- | --- | --- |
| 400 | `bad_request` | Missing or malformed JSON body, empty `ext:` id |
| 401 | `unauthorized` | No credentials, or a non-bearer `Authorization` header |
| 401 | `invalid_token` | Token malformed, unknown or revoked |
| 401 | `invalid_credentials` | Wrong email or password |
| 403 | `forbidden` | Operation not allowed for this credential type |
| 403 | `csrf` | Cookie mutation that is not same-origin |
| 403 | `signup_closed` | Registration is closed on this server |
| 403 | `invalid_invite` | Registration needs a valid invite code |
| 404 | `not_found` | Unknown route, malformed id, or an object you do not own |
| 409 | `conflict` | Unique constraint (rare; concurrent `external_id` creates resolve to the winner) |
| 409 | `already_resolved` | Answering or cancelling a question that is no longer pending |
| 409 | `email_taken` | Registration with an existing email |
| 409 | `no_subscriptions` | Test push with no subscribed device |
| 413 | `too_large` | JSON body over 1 MiB, an attachment over 10 MiB, or a multipart request over the combined limit |
| 413 | `storage_quota` | The account's attachment (or artifact) storage is full; delete threads or messages that carry files to free space |
| 422 | `validation_failed` | A field failed validation; `message` says which |
| 429 | `rate_limited` (login attempts per address; token writes beyond about 120 messages, 30 questions, 30 uploads or 300 statuses a minute; honour `Retry-After`) | Too many login attempts (20 per minute per IP) or registrations (5 per minute per IP) |
| 502 | `storage_unavailable` | Object storage rejected an upload or download; retry |
| 503 | `push_disabled` | Push is not configured on this server |
| 503 | `attachments_disabled` | Attachment storage is not configured on this server |
| 503 | `busy` | Too many uploads are being decoded at once; retry after `Retry-After` (5 seconds) |
| 500 | `internal_error` | Server fault |

### Limits

| Field | Limit |
| --- | --- |
| Request body | 1 MiB |
| Message `body` | 256 KiB, valid UTF-8, not blank |
| Question `prompt` | 8000 bytes, valid UTF-8 |
| Question `options` | 20; `label` 200 characters, unique; `description` 1000 characters |
| Answer `text` | 32 KiB |
| Thread `title` / `agent` / `external_id` | 300 / 120 / 300 characters |
| `meta` | 16 KiB JSON object |
| `wait` | 0 to 600 seconds (larger values are clamped to 600) |
| `timeout_seconds` | 1 to 604800 (7 days) |
| `limit` | threads 1 to 200 (default 50); messages and questions 1 to 500 (default 100) |
| Attachment | 10 MiB per file, 8 per message or upload request; `filename` 200 characters |
| Attachment storage | 5 GiB per account by default (`FINALECHAT_ATTACHMENT_QUOTA_BYTES`); `GET /me` reports `storage` |
| Activity `text` / `ttl_seconds` | 200 characters on one line / 1 to 600 seconds (default 45) |

### Idempotency

`POST /threads/{ref}/messages` and `POST /threads/{ref}/questions` accept an
`Idempotency-Key` header (or a `client_key` body field, up to 200
characters, scoped to the thread). A repeat with the same key returns the
original object with HTTP 200 and `"created": false` instead of creating a
duplicate, so a client may retry a post whose response was lost. A replayed
message post still applies the `activity` it carries and reports
`"applied"`; files sent with a replayed multipart post are discarded.

### Request ids

Every response carries an `X-Request-Id` header (a client may supply its
own, up to 64 characters). Server logs record it; quote it when reporting a
problem.

### Features

`GET /auth/status` and `GET /me` list optional capabilities in `features`:
`activity`, `idempotency`, `dismiss`, and `push` / `attachments` when those
are configured. Clients that must work against older deployments can
feature-detect with it.

### Thread references

Wherever a path contains `{ref}` you may pass either the thread's UUID or
`ext:` followed by the thread's `external_id`, for example
`/threads/ext:claude-code:7f3a9c2e/messages`. `POST …/messages` and
`POST …/questions` create a missing `ext:` thread on demand; all other
routes, including `POST …/activity` (a status belongs to a conversation
that exists), return `404 not_found` for an unknown reference.

### Pagination

Threads use an opaque keyset cursor: the response includes `next_cursor`
when more threads exist; pass it back as `cursor`. Messages use message ids:
`after=<id>` returns newer messages in ascending order, `before=<id>` returns
older ones, and with neither the newest `limit` messages are returned in
ascending order with `has_more` set when older messages exist.

### Long-polling

Three routes accept `wait=<seconds>`. The server holds the request open
until something happens or the wait elapses, then responds normally:

- `GET /threads/{ref}/messages?after=<id>&wait=N` returns as soon as a
  message newer than `after` exists. Without `after`, a wait watches for
  anything created after the request started (by the server's clock); a
  wait that returns nothing carries that instant as `waited_from`, and
  passing it back as `after_time=<RFC 3339>` on the next wait keeps the
    chain gapless. Once a wait returns messages, continue with
  `after=<last message id>` instead (the response then carries no
  `waited_from`). A deleted message remains a valid `after` anchor. An
  anchor the thread does not hold at all (a thread deleted from the app and
  recreated under the same `ext:` id, say) pages from the start of the
  thread and the response carries `"anchor_unknown": true`; it is never an
  error.
- `POST /threads/{ref}/questions?wait=N` (or `"wait": N` in the body)
  returns when the question is answered, cancelled or expired.
- `GET /questions/{id}?wait=N` likewise.

`wait` is clamped to 600 seconds. Loop when you need to wait longer. A
timed-out message wait returns `"messages": []` and `"timed_out": true`; a
timed-out question wait returns the question with `"status": "pending"`.

## Objects

### Thread

```json
{
  "id": "01a07a60-6dab-780a-bf4d-20ef15c0d7d7",
  "external_id": "claude-code:7f3a9c2e",
  "title": "finalechat",
  "agent": "Claude Code",
  "meta": {"cwd": "/home/eric/finalechat"},
  "created_at": "2026-09-07T05:38:55.019126Z",
  "updated_at": "2026-09-07T05:38:55.043799Z",
  "last_activity_at": "2026-09-07T05:38:55.043799Z",
  "last_read_at": "2026-09-07T05:38:55.019126Z",
  "archived_at": null,
  "muted": false,
  "preview": "Staging looks good. Promote to production?",
  "preview_sender": "question",
  "unread_count": 1,
  "pending_questions": 1,
  "activity": {"text": "Running the test suite…", "kind": "tool",
               "at": "2026-09-07T05:40:02.114532Z", "since": "2026-09-07T05:38:57.301176Z",
               "expires_at": "2026-09-07T05:40:47.114532Z"}
}
```

`preview` is a plain-text excerpt of the latest message or question;
`preview_sender` is `agent`, `user`, `system` or `question`. `unread_count`
counts non-user messages since `last_read_at`. A muted thread never sends
push notifications.

`meta` is yours to fill, and the app renders a few reserved keys as chips
under the thread title: `host` (or `hostname`), `cwd`, `branch`, `model`,
`cost_usd` (number) and `tokens` (number). Everything else is stored,
merged on later writes, returned, and otherwise ignored.

`activity` is the agent's live status line, or `null` when there is none or
the last one has lapsed. Archiving sticks: a plain agent message leaves an
archived thread archived (its unread count still grows), while a message
marked `important`, a question, or the user's own reply from the app brings
it back (a `sender: "user"` message an agent posts does not). `kind` is `thinking`, `working`, `typing`, `waiting`
or `tool`; `at` is when it was last set or refreshed, `since` when the agent
became continuously busy (kept across refreshes), and `expires_at` when it
lapses. See [POST /threads/{ref}/activity](#post-threadsrefactivity).

### Message

```json
{
  "id": "01a07a60-6db9-784b-bd24-7ba3d7d3dfa0",
  "thread_id": "01a07a60-6dab-780a-bf4d-20ef15c0d7d7",
  "sender": "agent",
  "body": "Deployed **v1.2.0** to staging.\n\n- 42 tests passed\n- bundle 118 KB",
  "format": "markdown",
  "importance": "important",
  "meta": {},
  "created_at": "2026-09-07T05:38:55.033453Z",
  "attachments": []
}
```

`sender` is `agent`, `user` or `system`; `format` is `markdown` or `text`;
`importance` is `normal` or `important`. `attachments` lists the files the
message carries, in upload order; see [Attachment](#attachment).

`origin` is `session` when the app posted the message and `token` when an
agent did (empty on rows from before the field existed); `sender: "user"`
alone does not mean the user typed it on the phone, since agents may mirror
terminal input as the user. Two `meta` keys are reserved: `kind` (`answer`
marks the transcript copy of an answered question, which the app folds into
the card only when `question_id` names an answered question in the same
thread and otherwise shows as a mirrored reply; `notification` labels a
message as needing attention; `session_start` and `session_end` on `system`
rows tell the app whether the agent's session is still running) and `via`
(a short source label shown under the bubble). A deleted message becomes a
tombstone: it keeps its `id` and position, `body` is empty, `deleted` is
`true` and `deleted_at` says when; tombstones appear only on catch-up pages
(`after=<id>` without a `sender` filter), for the anchor itself, anything
after it, and any older message deleted since the anchor existed, so a
client that holds the anchor can prune everything it may hold.

### Attachment

```json
{
  "id": "01a07ab9-3132-7b58-85f0-5c74ef773395",
  "thread_id": "01a07ab9-312a-776f-ae4b-48c3cba6530c",
  "message_id": "01a07ab9-314c-72db-9505-30a34c350400",
  "kind": "image",
  "content_type": "image/png",
  "filename": "shot.png",
  "size": 168591,
  "width": 900,
  "height": 300,
  "thumb_width": 640,
  "thumb_height": 213,
  "created_at": "2026-09-07T07:15:52.264313Z",
  "url": "/api/v1/attachments/01a07ab9-3132-7b58-85f0-5c74ef773395",
  "thumb_url": "/api/v1/attachments/01a07ab9-3132-7b58-85f0-5c74ef773395/thumb"
}
```

`kind` is `image` for `image/png`, `image/jpeg`, `image/gif` and
`image/webp` (the bytes are sniffed; a file that claims to be an image but is
not becomes a `file` of type `application/octet-stream`), otherwise `file`.
Images carry `width`, `height` and a JPEG thumbnail whose longest side is
640 pixels (`thumb_width`, `thumb_height`, `thumb_url`); files omit those
fields. `url` and `thumb_url` are relative to the base URL. `message_id` is
`null` until the upload is attached to a message.

### Question

```json
{
  "id": "01a07a60-a414-72cf-8b9a-308e193887f7",
  "thread_id": "01a07a60-6dab-780a-bf4d-20ef15c0d7d7",
  "prompt": "Which branch?",
  "options": [{"label": "main"}, {"label": "release"}],
  "allow_freeform": true,
  "multi_select": false,
  "status": "answered",
  "answer": {"selected": ["release"], "text": "cut from tag v1.2.0"},
  "meta": {},
  "created_at": "2026-09-07T05:39:08.948128Z",
  "answered_at": "2026-09-07T05:39:08.981308Z",
  "expires_at": null
}
```

`status` is `pending`, `answered`, `cancelled`, `expired` or `dismissed` (the user declined; `answer` stays null and `answered_at` is set). `answer` is
`null` until answered; `selected` lists chosen option labels in the order
chosen and `text` is the free-form reply (absent when empty). `answered_at`
is set whenever the question leaves `pending`, including on cancel or expiry.

### User and Settings

```json
{
  "id": "01a07a5a-be46-723c-80f8-61155463dbdc",
  "email": "eric@example.com",
  "display_name": "Eric",
  "settings": {"notify_all_messages": false, "remote_mode": false},
  "created_at": "2026-09-07T05:32:42.438822Z"
}
```

### APIToken

```json
{
  "id": "01a07a5a-be60-72d1-b7f0-8eb7c08f48b1",
  "name": "laptop",
  "prefix": "fc_01234567",
  "created_at": "2026-09-07T05:32:42.464521Z",
  "last_used_at": "2026-09-07T05:38:23.601635Z"
}
```

### Counts

```json
{"pending_questions": 1, "unread_threads": 3, "attention": 3}
```

`unread_threads` counts active threads with at least one unread non-user
message; `attention` counts the threads that need the user (a pending
question or unread messages, leaving out archived and muted threads) and is
the app's badge.

## Notification policy

Push notifications go to every device the user has subscribed in the app.
They are never sent for a muted thread or when push is not configured.

- **Questions** always notify.
- **Messages** notify when `importance` is `important`, or when the user has
  turned on `notify_all_messages`. A `notify` field on the request, if
  present, overrides that rule in either direction. Messages with
  `sender: "user"` never notify.
- Normal messages still update the app in real time through the event
  stream and the badge counts.

## Auth

These routes serve the app's sign-in flow. Agents use tokens and can skip
this section.

### GET /auth/status

Reports how registration is gated and who, if anyone, is signed in. No
authentication required.

```json
{"authenticated": false, "push_enabled": true, "attachments_enabled": true, "signup": "open", "version": "dev"}
```

`signup` is `open` (anyone may register; this is what www.finalechat.com
runs), `first` (a first-account server with no account yet: the first
registration creates the owner and closes registration), `invite` (an invite
code is required), or `closed`. `attachments_enabled` says whether files can
be uploaded. When authenticated, the response also includes `user`.

### POST /auth/register

Same-origin only; rate limited. Creates an account and starts a session.

```json
{"email": "eric@example.com", "password": "at least 10 chars", "display_name": "Eric", "invite_code": ""}
```

Returns `201 {"user": {...}}` and sets the session cookie. Errors:
`signup_closed`, `invalid_invite`, `email_taken`, `validation_failed`.

### POST /auth/login

Same-origin only; rate limited. `{"email": "...", "password": "..."}`
returns `200 {"user": {...}}` and sets the cookie, or `401
invalid_credentials`.

### POST /auth/logout

Deletes the current session and clears the cookie. Returns `{"ok": true}`.

## Me and settings

### GET /me

```bash
curl -sS https://www.finalechat.com/api/v1/me -H "Authorization: Bearer $FINALECHAT_TOKEN"
```

```json
{
  "attachments_enabled": true,
  "auth": "token",
  "base_url": "https://www.finalechat.com",
  "counts": {"pending_questions": 0, "unread_threads": 2},
  "push_enabled": true,
  "storage": {"attachment_bytes": 18337921, "attachment_quota_bytes": 5368709120},
  "token": {"id": "01a07a5a-be60-72d1-b7f0-8eb7c08f48b1", "name": "laptop", "prefix": "fc_01234567",
            "created_at": "2026-09-07T05:32:42.464521Z", "last_used_at": "2026-09-07T05:38:23.601635Z"},
  "user": {"id": "01a07a5a-be46-723c-80f8-61155463dbdc", "email": "eric@example.com", "display_name": "Eric",
           "settings": {"notify_all_messages": false, "remote_mode": false},
           "created_at": "2026-09-07T05:32:42.438822Z"},
  "version": "dev"
}
```

`auth` is `token` or `session`; `token` is present only for token auth.
`storage` (present when attachments are enabled) reports the bytes of
attachments the account holds and the cap it is allowed; a
`attachment_quota_bytes` of `0` means no cap.

### PATCH /me

Session only (`403 forbidden` for tokens). Changes the display name and/or
password.

```json
{"display_name": "Eric", "current_password": "old", "new_password": "new password"}
```

Changing the password deletes every other session. Returns `{"user": {...}}`.

### DELETE /me

Session only (`403 forbidden` for tokens); rate limited. Deletes the account
and everything it owns: threads, messages, questions, attachments,
artifacts, tokens, connectors, sessions and devices. The current password is
required.

```json
{"password": "current password"}
```

Returns `{"ok": true}` and clears the session cookie, or `403
invalid_credentials`. Attachment and artifact bytes are removed from object
storage shortly afterwards.

### GET /settings

```json
{"settings": {"notify_all_messages": false, "remote_mode": false}}
```

### PATCH /settings

Any subset of the settings fields. Returns the full settings object and
emits a `settings.updated` event.

```bash
curl -sS -X PATCH https://www.finalechat.com/api/v1/settings \
  -H "Authorization: Bearer $FINALECHAT_TOKEN" -H "Content-Type: application/json" \
  -d '{"remote_mode": true}'
```

- `notify_all_messages`: push for every agent message, not only important ones.
- `remote_mode`: the user is away from the terminal; integrations should
  block waiting for phone replies and answers instead of falling through to
  the terminal.

### GET /counts

```json
{"counts": {"pending_questions": 1, "unread_threads": 3}}
```

## Tokens

All three routes require a session; tokens receive `403 forbidden` on create
and revoke.

### GET /tokens

```json
{"tokens": [{"id": "01a07a5a-be60-72d1-b7f0-8eb7c08f48b1", "name": "laptop", "prefix": "fc_01234567",
             "created_at": "2026-09-07T05:32:42.464521Z", "last_used_at": "2026-09-07T05:38:23.601635Z"}]}
```

Revoked tokens are not listed.

### POST /tokens

`{"name": "laptop"}` (name optional, defaults to "Agent token"). Returns
`201` with the token record and, once only, the secret:

```json
{"secret": "fc_0123456789abcdefghijklmnopqrstuvwxyzABCD",
 "token": {"id": "01a07a5a-be60-72d1-b7f0-8eb7c08f48b1", "name": "laptop", "prefix": "fc_01234567",
           "created_at": "2026-09-07T05:32:42.464521Z", "last_used_at": null}}
```

### DELETE /tokens/{id}

Revokes the token immediately. Returns `{"ok": true}` or `404`.

## Threads

### POST /threads

Creates a thread, or returns the existing one when `external_id` matches a
thread you already have. All fields are optional.

```bash
curl -sS https://www.finalechat.com/api/v1/threads \
  -H "Authorization: Bearer $FINALECHAT_TOKEN" -H "Content-Type: application/json" \
  -d '{"external_id": "claude-code:7f3a9c2e", "title": "finalechat", "agent": "Claude Code",
       "meta": {"cwd": "/home/eric/finalechat"}}'
```

```json
{"created": true, "thread": {"id": "01a07a60-6dab-780a-bf4d-20ef15c0d7d7", "external_id": "claude-code:7f3a9c2e",
  "title": "finalechat", "agent": "Claude Code", "meta": {"cwd": "/home/eric/finalechat"},
  "created_at": "2026-09-07T05:38:55.019126Z", "updated_at": "2026-09-07T05:38:55.019126Z",
  "last_activity_at": "2026-09-07T05:38:55.019126Z", "last_read_at": "2026-09-07T05:38:55.019126Z",
  "archived_at": null, "muted": false, "preview": "", "preview_sender": "",
  "unread_count": 0, "pending_questions": 0, "activity": null}}
```

Status is `201` with `"created": true` for a new thread and `200` with
`"created": false` for an existing one. On the existing thread, a blank
`title` or `agent` is filled from the request and `meta` is merged; a
non-blank title is left alone (use `PATCH` to rename).

### GET /threads

| Parameter | Meaning |
| --- | --- |
| `archived` | `true`/`1` lists archived threads instead of active ones |
| `q` | Case-insensitive substring match on title, agent and preview |
| `limit` | 1 to 200, default 50 |
| `cursor` | `next_cursor` from a previous page |

Threads are ordered by `last_activity_at`, newest first.

```json
{"threads": [{"id": "01a07a60-6dab-780a-bf4d-20ef15c0d7d7", "...": "..."}],
 "next_cursor": "MTc4ODc1OTUzNTA0Mzc5OXwwMWEwN2E2MC02ZGFiLTc4MGEtYmY0ZC0yMGVmMTVjMGQ3ZDc"}
```

`next_cursor` is absent on the last page.

### GET /threads/{ref}

```json
{"thread": {"...": "..."}}
```

### PATCH /threads/{ref}

Any subset of `title`, `agent`, `archived` (boolean), `muted` (boolean),
`meta` (merged). Emits `thread.updated`.

```bash
curl -sS -X PATCH https://www.finalechat.com/api/v1/threads/ext:claude-code:7f3a9c2e \
  -H "Authorization: Bearer $FINALECHAT_TOKEN" -H "Content-Type: application/json" \
  -d '{"title": "finalechat: release 1.2", "archived": true}'
```

Returns `{"thread": {...}}`.

### DELETE /threads/{ref}

Deletes the thread with all its messages and questions. Returns `{"ok": true}`
and emits `thread.deleted`.

### POST /threads/{ref}/read

Marks everything in the thread as read (`last_read_at` becomes now). Returns
`{"thread": {...}}` and emits `thread.updated`. The app calls this; agents
rarely need it.

### POST /threads/{ref}/activity

Sets the agent's status line: what it is doing right now, shown in the app
as a typing indicator with a timer. Unlike messages this never creates a
thread (`404` for an unknown reference): a status belongs to a conversation
that exists. `PUT` is accepted too.

| Field | Type | Notes |
| --- | --- | --- |
| `text` | string | One line, at most 200 characters; whitespace is collapsed. Empty clears the status. |
| `kind` | string | `thinking`, `working` (default), `typing`, `waiting` or `tool` |
| `ttl_seconds` | integer | 1 to 600, default 45. The status lapses when this runs out unless set again. |
| `seq` | integer | Optional ordering for concurrent writers: a write whose `seq` is not above the live status's is ignored (`"applied": false`). Use one clock for every writer of a thread; a nanosecond timestamp (`time.Now().UnixNano()`, `time.time_ns()`) is the convention. Any `seq` is accepted once the status has lapsed, but a clear that carried a `seq` (`DELETE …/activity?seq=`, or `{"text": "", "seq": N}` here or inline on a message or question) still rejects writes below it for ten minutes, so a slow write cannot resurrect a status the agent already cleared, and one clear from a fast clock cannot pin the line shut. |

```bash
curl -sS https://www.finalechat.com/api/v1/threads/ext:claude-code:7f3a9c2e/activity \
  -H "Authorization: Bearer $FINALECHAT_TOKEN" -H "Content-Type: application/json" \
  -d '{"text": "Running the test suite…", "kind": "tool", "ttl_seconds": 120}'
```

```json
{"applied": true,
 "thread": {"id": "01a07a60-6dab-780a-bf4d-20ef15c0d7d7", "...": "...",
  "activity": {"text": "Running the test suite…", "kind": "tool",
               "at": "2026-09-07T05:40:02.114532Z", "since": "2026-09-07T05:38:57.301176Z",
               "expires_at": "2026-09-07T05:42:02.114532Z"}}}
```

Setting a status while the previous one is still live keeps `since`, so a
sequence of statuses reads as one stretch of work. A repeat of the current
text and kind while it has more than half its life left is a no-op
(`"applied": false`). Posting a message or a question from the agent clears
the status (the message is what the status announced) unless that post
carries its own `activity` field; a message from the user leaves it alone.
Emits `thread.activity`; never pushes a notification and does not bump
`last_activity_at` or the thread's position in the inbox.

### DELETE /threads/{ref}/activity

Clears the status line. Returns `{"thread": {...}}` and emits
`thread.activity` when there was one to clear. Unknown threads are `404`;
this route never creates one.

## Messages

### POST /threads/{ref}/messages

Creates a missing `ext:` thread on demand.

| Field | Type | Notes |
| --- | --- | --- |
| `body` | string | Markdown by default. Required unless the message carries attachments. |
| `format` | string | `markdown` (default) or `text` |
| `importance` | string | `normal` (default) or `important` |
| `sender` | string | `agent` (default for tokens), `user` (default for sessions) or `system` |
| `notify` | boolean | Force (`true`) or suppress (`false`) the push for this message |
| `meta` | object | Stored verbatim |
| `title`, `agent` | string | Applied only when this request creates the `ext:` thread |
| `attachments` | array of ids | Pending uploads from the same thread (see [Attachments](#attachments)); at most 8 |

The same request can be sent as `multipart/form-data`: every field above
becomes a form value (`meta` as a JSON string, `attachments` as
comma-separated ids) and any number of `file` parts are uploaded and
attached in the same call. That is the one-request way to send a
screenshot; see [Attachments](#attachments).

```bash
curl -sS https://www.finalechat.com/api/v1/threads/ext:claude-code:7f3a9c2e/messages \
  -H "Authorization: Bearer $FINALECHAT_TOKEN" -H "Content-Type: application/json" \
  -d '{"title": "finalechat", "agent": "Claude Code",
       "body": "Deployed **v1.2.0** to staging.\n\n- 42 tests passed\n- bundle 118 KB",
       "importance": "important"}'
```

```json
{"message": {"id": "01a07a60-6db9-784b-bd24-7ba3d7d3dfa0", "thread_id": "01a07a60-6dab-780a-bf4d-20ef15c0d7d7",
             "sender": "agent", "body": "Deployed **v1.2.0** to staging.\n\n- 42 tests passed\n- bundle 118 KB",
             "format": "markdown", "importance": "important", "meta": {},
             "created_at": "2026-09-07T05:38:55.033453Z", "attachments": []},
 "thread": {"id": "01a07a60-6dab-780a-bf4d-20ef15c0d7d7", "preview": "Deployed v1.2.0 to staging. - 42 tests passed - bundle 118 KB",
            "preview_sender": "agent", "unread_count": 1, "...": "..."}}
```

Status `201`. Posting bumps the thread's `last_activity_at` and `preview`;
an `important` message or the user's own reply from the app un-archives the
thread. A `sender: "user"` message posted from the app also advances the
thread's `last_read_at` (one an agent posts does not). Emits
`message.created`. Attaching an id that is unknown, belongs to another
thread, or is already attached is a `422`.

### GET /threads/{ref}/messages

| Parameter | Meaning |
| --- | --- |
| `after` | Message id; return newer messages, ascending (with tombstones, see above). An id the thread does not hold pages from the start and sets `anchor_unknown` |
| `after_time` | RFC 3339 instant; return messages created after it, ascending. For waits without a message to anchor on; take it from a previous response's `waited_from` |
| `before` | Message id; return older messages, ascending |
| `limit` | 1 to 500, default 100 |
| `sender` | Filter: `agent`, `user` or `system` |
| `wait` | Seconds to hold the request open for a new message; without `after` or `after_time` it waits for anything created from now on |

```bash
curl -sS "https://www.finalechat.com/api/v1/threads/ext:claude-code:7f3a9c2e/messages?limit=3" \
  -H "Authorization: Bearer $FINALECHAT_TOKEN"
```

```json
{"has_more": false, "thread_id": "01a07a60-6dab-780a-bf4d-20ef15c0d7d7",
 "messages": [{"id": "01a07a60-6db9-784b-bd24-7ba3d7d3dfa0", "sender": "agent", "...": "..."}]}
```

Without a cursor, the newest `limit` messages are returned in ascending
order and `has_more` says whether older ones exist. With `after`, `has_more`
says whether even newer ones exist beyond `limit`. When `wait` elapses with
nothing new the response is `{"messages": [], "has_more": false,
"thread_id": "...", "timed_out": true}`.

Waiting for the user's reply after your last message:

```bash
curl -sS "https://www.finalechat.com/api/v1/threads/ext:claude-code:7f3a9c2e/messages?after=01a07a60-6db9-784b-bd24-7ba3d7d3dfa0&sender=user&wait=600" \
  -H "Authorization: Bearer $FINALECHAT_TOKEN"
```

### DELETE /messages/{id}

Removes the message and its attachments for good, repairs the thread
preview from what remains, and emits `message.deleted` so open apps drop
it. Returns `{"ok": true, "thread": {...}}`. This is the recovery path for
output that should never have reached the phone.

### GET /messages/{id}

```json
{"message": {"id": "01a07a60-a438-78f0-9a26-138cf1ecfa9d", "thread_id": "01a07a60-6dab-780a-bf4d-20ef15c0d7d7",
             "sender": "user", "body": "release — cut from tag v1.2.0", "format": "text", "importance": "normal",
             "meta": {"kind": "answer", "question_id": "01a07a60-a414-72cf-8b9a-308e193887f7"},
             "created_at": "2026-09-07T05:39:08.984547Z"}}
```

The message above is the transcript entry the server writes when a question
is answered: `meta.kind` is `answer` and `meta.question_id` links it.

## Attachments

Messages carry files: screenshots, logs, diffs, PDFs, anything up to 10 MiB,
at most 8 per message. Images get dimensions and a JPEG thumbnail; the app
shows them inline, and the thread preview and push notification read
`📷 Image` or `📎 Attachment`. Bytes live in a private Backblaze B2 bucket;
only metadata is in the database. Both agents and the app user send and
receive attachments the same way.

### Send a message with files in one request

`POST /threads/{ref}/messages` as `multipart/form-data`. Form values are the
JSON fields; `file` parts are uploaded and attached. `body` may be omitted.

```bash
curl -sS https://www.finalechat.com/api/v1/threads/ext:claude-code:7f3a9c2e/messages \
  -H "Authorization: Bearer $FINALECHAT_TOKEN" \
  -F body="Here is the **screenshot**" \
  -F importance=important \
  -F file=@shot.png \
  -F file=@deploy.log
```

```json
{"message": {"id": "01a07ab9-314c-72db-9505-30a34c350400", "thread_id": "01a07ab9-312a-776f-ae4b-48c3cba6530c",
             "sender": "agent", "body": "Here is the **screenshot**", "format": "markdown", "importance": "important",
             "meta": {}, "created_at": "2026-09-07T07:15:52.268069Z",
             "attachments": [{"id": "01a07ab9-3132-7b58-85f0-5c74ef773395", "kind": "image", "content_type": "image/png",
                              "filename": "shot.png", "size": 168591, "width": 900, "height": 300,
                              "thumb_width": 640, "thumb_height": 213,
                              "url": "/api/v1/attachments/01a07ab9-3132-7b58-85f0-5c74ef773395",
                              "thumb_url": "/api/v1/attachments/01a07ab9-3132-7b58-85f0-5c74ef773395/thumb", "...": "..."},
                             {"id": "01a07ab9-…", "kind": "file", "content_type": "text/plain", "filename": "deploy.log", "...": "..."}]},
 "thread": {"preview": "📎 2 attachments · Here is the screenshot", "preview_sender": "agent", "...": "..."}}
```

### POST /threads/{ref}/attachments

Uploads files without posting yet, for example when several steps each
produce one. Creates a missing `ext:` thread on demand. Two request shapes:

- `multipart/form-data` with one or more `file` parts (the field name does
  not matter; `files` and `attachment` work too), at most 8 per request.
- A raw body whose `Content-Type` is the file's type. The filename comes from
  the `X-Filename` header or the `filename` query parameter; without either
  it is `attachment.<ext>`.

```bash
curl -sS https://www.finalechat.com/api/v1/threads/ext:claude-code:7f3a9c2e/attachments \
  -H "Authorization: Bearer $FINALECHAT_TOKEN" \
  -H "Content-Type: text/plain" -H "X-Filename: notes.txt" \
  --data-binary @notes.txt
```

```json
{"attachments": [{"id": "01a07ab9-7a53-7da4-81ad-3f7bad1f53db", "thread_id": "01a07ab9-312a-776f-ae4b-48c3cba6530c",
                  "message_id": null, "kind": "file", "content_type": "text/plain", "filename": "notes.txt", "size": 23,
                  "created_at": "2026-09-07T07:16:10.963984Z",
                  "url": "/api/v1/attachments/01a07ab9-7a53-7da4-81ad-3f7bad1f53db"}],
 "thread_id": "01a07ab9-312a-776f-ae4b-48c3cba6530c"}
```

Status `201`. Then pass the ids in a message's `attachments` field. Each
upload attaches to exactly one message; a pending upload that is not
attached within 24 hours is deleted. `413 storage_quota` when the account's
attachment storage is full. `413 too_large` for a file over 10 MiB,
`422 validation_failed` for an empty file or an undecodable image, and
`502 storage_unavailable` when the object store fails.

### GET /attachments/{id}

Streams the original bytes with its `Content-Type`, `Content-Length` and
`Cache-Control: private, no-cache` (the bytes never change, but a browser
must revalidate so a signed-out session cannot keep serving them; the app's
service worker keeps its own copy for offline reading). `Content-Disposition`
is `inline` for images, PDFs and text and `attachment` otherwise, with the
original filename. `HEAD` returns the headers only.

```bash
curl -sS -o shot.png https://www.finalechat.com/api/v1/attachments/01a07ab9-3132-7b58-85f0-5c74ef773395 \
  -H "Authorization: Bearer $FINALECHAT_TOKEN"
```

### GET /attachments/{id}/thumb

The JPEG thumbnail of an image attachment (`image/jpeg`, longest side 640).
For a non-image attachment it returns the original bytes.

Deleting a thread deletes its attachments and their stored bytes.

## Questions

### POST /threads/{ref}/questions

Creates a missing `ext:` thread on demand. Always sends a push notification
unless the thread is muted.

| Field | Type | Notes |
| --- | --- | --- |
| `prompt` | string | Required |
| `options` | array | Up to 20 `{"label": "...", "description": "..."}`; labels must be unique |
| `allow_freeform` | boolean | Default `true`; forced `true` when there are no options |
| `multi_select` | boolean | Default `false` |
| `timeout_seconds` | integer | 1 to 604800; the question expires after this |
| `wait` | integer | Block up to this many seconds for the answer (also accepted as `?wait=`) |
| `meta` | object | Stored verbatim |
| `title`, `agent` | string | Applied only when this request creates the `ext:` thread |

```bash
curl -sS "https://www.finalechat.com/api/v1/threads/ext:claude-code:7f3a9c2e/questions?wait=600" \
  -H "Authorization: Bearer $FINALECHAT_TOKEN" -H "Content-Type: application/json" \
  -d '{"prompt": "Staging looks good. Promote to production?",
       "options": [{"label": "Ship it", "description": "Promote the same image"},
                   {"label": "Hold", "description": "Wait for me to look"}],
       "timeout_seconds": 3600}'
```

```json
{"question": {"id": "01a07a60-6dc3-7cc4-ae8c-f32504d5a4b4", "thread_id": "01a07a60-6dab-780a-bf4d-20ef15c0d7d7",
              "prompt": "Staging looks good. Promote to production?",
              "options": [{"label": "Ship it", "description": "Promote the same image"},
                          {"label": "Hold", "description": "Wait for me to look"}],
              "allow_freeform": true, "multi_select": false, "status": "pending", "answer": null, "meta": {},
              "created_at": "2026-09-07T05:38:55.043799Z", "answered_at": null,
              "expires_at": "2026-09-07T06:38:55.043382Z"},
 "thread": {"id": "01a07a60-6dab-780a-bf4d-20ef15c0d7d7", "preview": "Staging looks good. Promote to production?",
            "preview_sender": "question", "pending_questions": 1, "...": "..."}}
```

Status is `201` whether or not the wait produced an answer; check
`question.status`. Emits `question.created`.

### GET /questions/{id}

`wait` (seconds) blocks while the question is pending.

```bash
curl -sS "https://www.finalechat.com/api/v1/questions/01a07a60-6dc3-7cc4-ae8c-f32504d5a4b4?wait=600" \
  -H "Authorization: Bearer $FINALECHAT_TOKEN"
```

```json
{"question": {"id": "01a07a60-6dc3-7cc4-ae8c-f32504d5a4b4", "status": "answered",
              "answer": {"selected": ["Ship it"]}, "answered_at": "2026-09-07T05:41:12.100442Z", "...": "..."}}
```

### GET /questions

| Parameter | Meaning |
| --- | --- |
| `status` | `pending`, `answered`, `cancelled`, `expired` or `dismissed` |
| `thread_id` | Thread UUID or `ext:<external_id>` |
| `attention` | `true` restricts pending questions to threads that need the user (not archived, not muted): the app's "needs you" list |
| `limit` | 1 to 500, default 100 |

Newest first.

```json
{"questions": [{"id": "01a07a60-6dc3-7cc4-ae8c-f32504d5a4b4", "status": "pending", "...": "..."}]}
```

Add `attention=true` to leave out questions in archived or muted threads;
that is the app's "needs you" view and matches `counts.attention`. Agents
listing their own questions should not pass it.

### GET /threads/{ref}/questions

Every question in the thread, oldest first (up to 500), so a client can
interleave them with messages.

### POST /questions/{id}/answer

| Field | Type | Notes |
| --- | --- | --- |
| `selected` | array of strings | Option labels; at most one unless `multi_select` |
| `text` | string | Free-form reply; only when `allow_freeform` |

At least one of the two is required. Labels must match the offered options
exactly.

```bash
curl -sS https://www.finalechat.com/api/v1/questions/01a07a60-a414-72cf-8b9a-308e193887f7/answer \
  -H "Authorization: Bearer $FINALECHAT_TOKEN" -H "Content-Type: application/json" \
  -d '{"selected": ["release"], "text": "cut from tag v1.2.0"}'
```

```json
{"question": {"id": "01a07a60-a414-72cf-8b9a-308e193887f7", "status": "answered",
              "answer": {"selected": ["release"], "text": "cut from tag v1.2.0"},
              "answered_at": "2026-09-07T05:39:08.981308Z", "...": "..."},
 "message": {"id": "01a07a60-a438-78f0-9a26-138cf1ecfa9d", "sender": "user",
             "body": "release — cut from tag v1.2.0", "format": "text",
             "meta": {"kind": "answer", "question_id": "01a07a60-a414-72cf-8b9a-308e193887f7"}, "...": "..."},
 "thread": {"...": "..."}}
```

Answering also appends a `user` message to the thread recording the choice
and marks the thread read. A question that is no longer pending returns
`409 already_resolved` with the current status in the message. Emits
`question.answered` and `message.created`.

### POST /questions/{id}/dismiss

The user declines to answer: `status` becomes `dismissed`, the agent's wait
returns, and the question stops counting as pending. Returns
`{"question": {...}}` and emits `question.dismissed`. `409 already_resolved`
if the question is no longer pending.

### POST /questions/{id}/cancel

Withdraws a pending question (for example, the agent found the answer
elsewhere). Returns `{"question": {...}}` with `status: "cancelled"`, or `409
already_resolved` if it was not pending. Emits `question.cancelled`.

### Expiry

A background task marks pending questions whose `expires_at` has passed as
`expired` (within about 30 seconds) and emits `question.expired`.

## Events

### GET /events

A server-sent events stream (`text/event-stream`) of everything that
changes for the user. Works with tokens and sessions. Each event carries
the full current objects so a client can apply it without another request.

```bash
curl -sN https://www.finalechat.com/api/v1/events -H "Authorization: Bearer $FINALECHAT_TOKEN"
```

```
event: ready
data: {"at":"2026-09-07T05:39:05.945123Z","counts":{"pending_questions":0,"unread_threads":3}}

event: message.created
data: {"at":"…","thread":{…},"message":{…},"counts":{…}}
```

| Event | Data |
| --- | --- |
| `ready` | `{at, counts}` once on connect |
| `thread.created`, `thread.updated` | `{at, thread, counts}` |
| `thread.deleted` | `{at, thread_id, counts}` |
| `thread.activity` | `{at, thread_id, activity}`; `activity` is the new status or `null`. No thread object and no counts: statuses are frequent and never change badges. |
| `question.dismissed` | `{at, thread, question, counts}` when the user declines a question |
| `ping` | `{at}` every 20 seconds; a client that sees none for 45 seconds should reconnect |
| `message.created` | `{at, thread, message, counts}` |
| `message.deleted` | `{at, thread, message_id, counts}` |
| `question.created`, `question.answered`, `question.cancelled`, `question.expired` | `{at, thread, question, counts}` |
| `settings.updated` | `{at, settings}` |
| `reconnect` | `{}` when the server is shutting down; reconnect immediately |

A `ping` event is sent every 20 seconds to keep the connection alive and
let clients detect a dead socket. There is no replay: after reconnecting, refetch the state you care
about. A subscriber that falls too far behind is dropped and should
reconnect.

## Push

These routes let the app register a device for Web Push. Agents do not need
them.

### GET /push/vapid

No authentication. `{"enabled": true, "public_key": "BMPS…"}`; the key is
the `applicationServerKey` for `PushManager.subscribe`.

### POST /push/subscriptions

Body is a browser `PushSubscription` JSON, either directly or under a
`subscription` key: `{"endpoint": "https://…", "keys": {"p256dh": "…",
"auth": "…"}}`. Returns `201 {"subscription": {...}}`. Re-subscribing the
same endpoint updates it. `503 push_disabled` when push is not configured.

### GET /push/subscriptions

```json
{"subscriptions": [{"id": "…", "endpoint": "https://…", "user_agent": "…",
                    "created_at": "…", "last_success_at": "…", "failure_count": 0}]}
```

### DELETE /push/subscriptions

`{"endpoint": "https://…"}` removes that device. Returns `{"ok": true}`.

### POST /push/test

Sends a test notification to every subscribed device. Returns `202 {"ok":
true, "devices": 1}`, `409 no_subscriptions`, or `503 push_disabled`.

Endpoints that the push service reports gone (404 or 410) are removed
automatically, as are endpoints that fail 20 times in a row.

## Health

`GET /healthz` returns `ok` while the process is up; `GET /readyz` returns
`ready` once the database answers. Neither requires authentication and
neither lives under `/api/v1`.

<!-- artifact-control-contract -->

## Durable artifacts and integration settings

Feature-detect `artifacts.v1` and `settings-control.v1` in `/me` or `/auth/status`.
Artifact bytes use the configured blob backend; immutable metadata and command
outcomes live in PostgreSQL. B2 keys need `listFiles` for complete cleanup of
upload versions whose responses were lost. Memory storage is for tests only.

The container format is `finalechat.website/v1`. Each manifest lists a standalone
HTML entrypoint, optional settings entrypoint, producer, capture time, native
dataset identity and files with full SHA-256 hashes and ordered 1 MiB chunks.
Files retain native bytes. Limits: 4,096 files, 16,384 chunk references, 512 MiB
per file, 2 GiB per revision, 10 GiB of unique bytes per account, 8 MiB per HTML
entrypoint, 1 MiB manifest/request. Paths must be portable and traversal-free.
Upload missing chunks before committing. Preserve the parent revision and
idempotency key in a durable publisher journal before sending a commit.

Embed `/sdk/finale-artifact.js` inline in exported HTML. `finale.ready` resolves
after the host connects or a local archive directory is chosen. The SDK exposes
`manifest()`, bounded `read(path,{offset,length})`, `chunks(path)`, `lines(path)`,
`text(path)` and `openLocalFiles()`. The host creates a fresh MessageChannel for
one iframe document. Files are scoped to its mounted artifact/revision. There
is no generic HTTP proxy, credential access or iframe-triggered command queue.
Downloads are inert; preview documents get their own restrictive response CSP.

The trusted container pins a revision until the user enables Follow latest.
Following preserves presentation state and stops when the viewer files or
dataset identity change. `finale.viewState.read()` and `.write(value)` share up
to 16 KiB of JSON with the current container, scoped to this artifact. This
ephemeral state survives iframe replacement while the container stays open;
it is not an account setting or a durable archive edit. Viewers should validate
their own state version/session identity and restore filters, scroll and event
position. The shipped viewers use it automatically.

To reinterpret fixed data, select a viewer from another saved revision of the
same artifact. The optional `viewer=REVISION_ID` query works on the manifest,
file, preview and ZIP download routes. Dataset format/schema must agree and the
combined layout must validate. Only viewer-role files change; the manifest
records both source and renderer revision IDs and disables settings entrypoints.
The original data, original downloads and current revision remain unchanged.

Messages may carry `meta.source_anchor` with `dataset_format`, `session_id`
and exactly one selector: `seq`, `event_id`, `message_id`, or `file` plus `line`.
The trusted chat UI opens the selected immutable revision with this hint.
`finale.anchor()` returns that hint to the viewer, which must find the native
record or explicitly report it absent from the loaded prefix. A viewer can call
`finale.reveal(anchor)` to request a matching chat link; the parent validates
the dataset, looks up only this artifact's thread and presents a trusted link
for the user to tap. It never navigates merely because the iframe asked.

Pair control independently from upload. API tokens may request pairing but
only a signed-in user may approve resource keys, scopes, operations and classes.
The resulting `fcc_` token cannot use chat, upload, account or browser commands.
Publish `finalechat.settings/v1` descriptors with bounded typed fields, saved and
effective values, provenance, locked reasons and effect timing. `settings.apply`
uses explicit set/unset edits. The adapter revalidates against fresh native state.
Keep credentials out of snapshots, proposals and results.

Settings pages call `finale.settings.read()` and `finale.settings.propose(p)` to
stage a proposal; the trusted FinaleChat Save/action controls submit it. Call
`finale.settings.clear()` when new edits invalidate a staged proposal; clearing
does not submit a command. Match `onResult` outcomes to their proposals and
preserve newer drafts when results arrive late. Saved
historical surfaces remain read only until the user opens current settings.
Drafts do not execute. Explicit send-when-connected applies only to stable
settings edits with a deadline. Session controls require a known active runtime,
do not permit offline drafts or send-when-connected, and expire within 300 seconds.
Durable commands have fenced renewable leases,
an append-only audit and explicit expired/conflicted/unknown outcomes. Adapters
must journal intent and reconcile save-before-ack crashes. No automatic retry
may repeat an uncertain paid action. A saved file does not prove runtime adoption.

Deleting an artifact, directly or through thread deletion, cancels its queued
settings commands and fences claimed commands with an `unknown` result. This
does not undo a local write that may already have started. Completed results
remain on the settings resource, and independently submitted resource commands
remain authorized. Account deletion removes the related audit along with the account.

| Endpoint | Purpose |
| --- | --- |
| `PUT /threads/{thread}/artifacts/{key}` | Register or rename a session artifact |
| `GET /threads/{thread}/artifacts` | List a thread’s artifacts |
| `GET /threads/{thread}/settings-resources` | Discover live settings for a thread |
| `GET /artifacts/{id}` | Artifact metadata, current manifest and limits |
| `DELETE /artifacts/{id}` | Delete artifact and all revisions |
| `POST /artifacts/{id}/blobs/check` | Check which chunk hashes are absent |
| `PUT /artifacts/{id}/blobs/{hash}` | Upload a verified chunk |
| `POST /artifacts/{id}/revisions` | Atomically commit an immutable revision |
| `GET /artifacts/{id}/revisions` | List immutable revision summaries |
| `GET /artifacts/{id}/revisions/{revision}` | Read one revision manifest |
| `GET /artifacts/{id}/revisions/{revision}/files/{file}` | Download exact file bytes or a bounded slice |
| `GET /artifacts/{id}/revisions/{revision}/preview` | Load a sandboxed website entrypoint |
| `GET /artifacts/{id}/revisions/{revision}/download` | Download the portable ZIP archive |
| `GET /artifacts/{id}/revisions/{revision}/message` | Locate a chat message for an archived source anchor |
| `POST /connectors` | Connect an owned installation and receive a scoped credential |
| `POST /connectors/{connector}/connect` | Connect an existing owned installation |
| `GET /threads/{thread}/settings` | List agent settings available in this conversation |
| `GET /connectors` | List paired and pending installations |
| `GET /connectors/{connector}` | Connector pairing state and grants |
| `POST /connectors/{connector}/approve` | Approve a subset of requested grants |
| `DELETE /connectors/{connector}` | Revoke access and cancel queued commands |
| `POST /connectors/{connector}/heartbeat` | Renew installation process ownership |
| `PUT /connectors/{connector}/resources/{key}` | Publish a granted resource descriptor and snapshot |
| `PUT /connectors/{connector}/bindings/{id}` | Bind a current settings artifact to a resource |
| `GET /artifacts/{id}/settings-binding` | Read the artifact’s current resource binding |
| `POST /artifacts/{id}/settings-surface` | Open a current settings editing session |
| `GET /settings-resources/{resource}` | Read latest settings snapshot and connector availability |
| `GET /settings-resources/{resource}/audit` | Read durable settings audit history |
| `POST /settings-resources/{resource}/commands` | Queue a user-approved settings command |
| `GET /settings-resources/{resource}/draft` | Read a saved proposal draft |
| `PUT /settings-resources/{resource}/draft` | Save a draft without scheduling execution |
| `DELETE /settings-resources/{resource}/draft` | Discard a proposal draft |
| `POST /connectors/{connector}/commands/claim` | Claim the next command with a renewable lease |
| `POST /commands/{command}/renew` | Renew the current fenced execution lease |
| `POST /commands/{command}/result` | Persist an execution outcome |
| `GET /commands/{command}` | Read durable command status and result |
| `POST /commands/{command}/cancel` | Cancel a command that has not started |

See the OpenAPI schemas for exact envelopes and constraints. SSE adds `artifact.updated`, `artifact.deleted`, `connector.updated`, `settings-resource.updated` and `command.updated`; these notify clients to reload the corresponding durable objects.
