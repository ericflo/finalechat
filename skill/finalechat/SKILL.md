---
name: finalechat
description: Message the user's phone via Finalechat when a task will take a while, when you need a decision and the user may be away from the terminal, or when you found something worth reporting. Posts markdown messages, asks multiple-choice questions and waits for the answer, and waits for the user's reply in a thread.
allowed-tools: Bash(finalechat:*), Bash(curl:*)
---

# Finalechat

Finalechat is the app where agents message the user. The user reads on their
phone, gets a notification for anything important, answers your questions
with a tap, and can reply in the thread. Use it for the messages you would
otherwise only print in the terminal.

## Check that it works

```bash
finalechat whoami
```

Prints the account email when the CLI is installed and a token is
configured (from `FINALECHAT_TOKEN` or `~/.config/finalechat/config.json`).
If the command is missing, install it:

```bash
curl -fsSL https://www.finalechat.com/install.sh | sh
```

If there is no token, ask the user for one (created in the app under
Settings → Agents) and run `finalechat login fc_...`. Never guess a token.

## One thread per session

Reuse one thread for the whole session so the user sees one conversation.
The CLI picks the thread automatically: `-t/--thread`, then
`FINALECHAT_THREAD`, then the Claude Code session thread for this directory,
then a per-directory default. Name it on the first post with `--title` (the
project or task) and `--agent` (who you are); later posts ignore both.

## The three moves

**Say** what you would tell the user. Markdown renders in the app.

```bash
finalechat say "Migrated 3 tables, 42 tests green. Starting the backfill (about 20 min)." --title finalechat --agent "Claude Code"
finalechat say "Backfill failed on step 3: unique violation on users.email. Need a decision." --important
```

Plain messages update the app silently. Add `--important` for anything that
deserves a buzz on the phone: a result, a finished long task, a blocker.
Use `--text` to post raw output without markdown rendering.

**Ask** when you need a decision instead of stopping. Questions always
notify. Give 2 to 5 short options with a description after `::`; the user
can also type a free-form answer unless you pass `--no-freeform`.

```bash
finalechat ask "Backfill hit duplicates. How should I proceed?" \
  -o "Dedupe::Keep the newest row per email" \
  -o "Abort::Roll back the migration" \
  -o "Skip::Leave duplicates and continue" \
  --timeout 3600
```

The command blocks until answered. It prints the selected labels on the
first line and any free text after it. Add `--multi` for questions where
several options may apply, and `--wait SECS` to change how long to block
(default 1800).

**Wait** for the user's next reply after you post something they may want
to react to:

```bash
finalechat wait --timeout 900
```

Prints the reply body. Then act on it and keep going.

## Say what you are doing

Between messages the user sees silence. Before a step that takes more than
a few seconds, set a status line; the app shows it as a typing indicator
with a timer, and it lapses on its own (default 45 seconds, `--ttl` up to
600) or as soon as you post the next message:

```bash
finalechat status "Running the test suite…" --ttl 120
finalechat status "Reading the migration files" --kind tool
finalechat status --clear
```

It never notifies and is not part of the transcript, so send one for every
step change. The Claude Code hooks write the same line ("Running: go test",
"Thinking…") from one shared clock, so a status you set yourself stays until
your next tool call ends; for a step that outlives one tool call, set it
again from inside the step or give it a long `--ttl`.

## Screenshots

Attach files to a message with `-f` (repeatable, up to 8, 10 MiB each);
the message text is optional when a file is present:

```bash
finalechat say "Staging after the deploy. Note the empty sidebar." -f /tmp/staging.png
```

When the user's reply carries a file, `wait` and `read` print a line like
`📎 IMG_0421.png (image/png, 165 KB) https://www.finalechat.com/api/v1/attachments/<id>`
under the message. Download it, then read the image file:

```bash
finalechat fetch <id> -o /tmp/IMG_0421.png
```

## Exit codes

- `0`: answered, or a reply arrived.
- `3`: the wait ran out. For `ask`, the question is still open on the phone;
  the id is printed to stderr. Resume with `finalechat wait-answer <id>` if
  you can keep waiting, otherwise say so and proceed with a sensible default
  (tell the user which one you took).
- `4`: the question was cancelled or expired. Do not retry the same
  question; continue with your best judgement and say so.

## Without the CLI

The API is plain HTTPS with a bearer token. Post to a thread named by an
id you choose (`ext:` prefix); it is created on first use.

```bash
curl -sS https://www.finalechat.com/api/v1/threads/ext:$SESSION_ID/messages \
  -H "Authorization: Bearer $FINALECHAT_TOKEN" -H "Content-Type: application/json" \
  -d '{"title": "finalechat", "agent": "Claude Code", "body": "Tests are green.", "importance": "important"}'

curl -sS "https://www.finalechat.com/api/v1/threads/ext:$SESSION_ID/questions?wait=600" \
  -H "Authorization: Bearer $FINALECHAT_TOKEN" -H "Content-Type: application/json" \
  -d '{"prompt": "Promote to production?", "options": [{"label": "Ship it"}, {"label": "Hold"}]}'
```

The question response has `question.status` and `question.answer`
(`selected` labels and `text`). If it is still `pending`, poll
`GET /api/v1/questions/<id>?wait=600`. Full reference:
<https://www.finalechat.com/AGENTS.md>.

## Good habits

- Post the final summary of every task, and progress on anything longer
  than a few minutes.
- Keep messages self-contained: the user reads them on a phone without the
  terminal.
- Mark `--important` sparingly so notifications stay meaningful.
- Never post secrets, tokens or credentials. If one slips out, remove the
  message for good with `finalechat delete <message id>` (ids are printed
  by `say`, `read` and `wait`).
