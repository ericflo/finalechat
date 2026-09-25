# Finalechat in Facebook Messenger

Some places only let one chat app through. The free messaging passes on
flights are the usual case: Messenger, iMessage and WhatsApp work, the rest
of the internet does not, and photos usually do not either. The Messenger
connector lets you run your agents from there. Agent messages and
questions arrive as Messenger messages. What you write back becomes your
reply in the right thread, an answer to a question, or a new eagent session.

To your agents it is the same as the app. A reply from Messenger is your
own message (`origin: "session"`), and answers go through the normal answer
path. Each one carries a client capsule (`app: "finalechat-messenger/…"`,
`device: "phone"`) so the agent knows you are reading plain text on a phone.

## Using it

Once linked, open the chat with the Page in Messenger.

- **Messages.** Agent messages arrive as `[#3 title] …`. Markdown is
  reduced to what Messenger shows: bold, italics, `code` and code blocks. A
  long message arrives as up to three bubbles; `/more` sends the rest.
  Files are named on a `📎 name (size)` line and then sent: images show
  inline, other files as download cards.
- **Questions.** They arrive with a numbered list, one button per option,
  and **Skip**. Tap a button, or type the number (`1, 3` for several), or
  type a free answer when the question takes one. Skip is the app's
  dismiss: the agent is told to use its judgement.
- **Replies.** Plain text goes to the thread that last spoke to you. When
  that is not where your previous reply went, the Page says `→ [#2 title]`
  so you notice. If that thread has a question open, what you type answers
  it. That is also what eagent does with a message sent while it waits.
  - **Swipe-reply** to a message to reply to its thread, whoever spoke
    last. This is the reliable way when several sessions are talking.
  - **`/go 3`** sends every reply to #3 until you send `/go` on its own.

| Command | Does |
| --- | --- |
| `/ls` | Recent threads with their numbers, live status and open questions; tap one to pin it |
| `/go 3`, `/go` | Pin replies to #3; follow whoever spoke last |
| `/where` | The current thread, what it is doing, and its open question |
| `/read 3` | The last few messages of #3 (or of the current thread) |
| `/new` | Start an eagent session: pick the project and folder, then send the first message. `/new fix the flaky test` starts one right away in the project root |
| `/skip` | Decline the current thread's open question |
| `/more` | The rest of a long message |
| `/quiet`, `/loud` | Only questions and important messages, or everything |
| `/pause`, `/resume` | Stop the relay while you are back at your desk, and start it again when you leave; the link stays. Resuming starts from that moment and re-sends the questions still waiting on you |
| `/remote on`, `/remote off` | Claude Code's remote mode: its hooks wait for your replies instead of the terminal |
| `/unlink` | Disconnect this chat |
| `/help` | This list |

Other slash commands, such as eagent's `/status` or `/tasks`, are passed on
to the agent. `/quit` asks first, because it would stop the session; send
`/quit!` to go ahead.

### Messenger's rules

- **24-hour window.** A Page may only write to you within 24 hours of your
  last message to it. After that, delivery pauses. The next time you write,
  the Page catches up: a few messages are delivered one by one, and a longer
  backlog comes as a summary per thread plus every question still open.
- **Files.** Photos and files you send from Messenger (up to 10 MB) are
  attached to your reply in the thread, where the agent can fetch them.
  The thumbs-up button arrives as 👍. Free in-flight passes usually block
  images in both directions; the text still gets through.
- **New sessions need a running eagent.** `/new` needs `eagent connector
  run` (or `eagent serve`) running in the project on your machine. A reply
  to a session that has finished restarts it the same way.

## Setting it up

You need a Facebook Page, a Meta developer app in Development mode, and
four server settings. Only people with a role on a Development-mode app can
message it, so no App Review is involved.

1. **Create the app.** At developers.facebook.com, go to **My Apps →
   Create App**. Choose **Business messaging → Engage with customers on
   Messenger from Meta** and skip the business portfolio. Leave the app in
   Development mode.
2. **Create a Page.** Any name will do. Nobody needs to find it.
3. **Get a Page token.** Open the app, then **Use cases → Customize** on the
   Messenger card, then **Messenger API Settings → Generate access tokens**.
   Connect the Page, making sure to tick it in the dialog, and click
   **Generate**. The Access Token Debugger should show **Type: Page** and
   **Expires: Never**. Note the Page ID it reports. For new-style Pages this
   differs from the number in the Page's `profile.php?id=` URL, and the
   debugger's is the one to use.
4. **Configure the server.**

   | Variable | Value |
   | --- | --- |
   | `FINALECHAT_MESSENGER_PAGE_ID` | The Page ID from the token debugger |
   | `FINALECHAT_MESSENGER_PAGE_TOKEN` | The Page token |
   | `FINALECHAT_MESSENGER_APP_SECRET` | App settings → Basic → App secret; it checks the webhook signatures |
   | `FINALECHAT_MESSENGER_VERIFY_TOKEN` | Any random string; Meta echoes it when you register the webhook |

   All four must be set together. `FINALECHAT_MESSENGER_GRAPH_URL` overrides
   the Graph API base, for tests only.
5. **Register the webhook.** Do this once the server runs with those
   settings. In **Messenger API Settings → Configure webhooks**:
   - Set the callback URL to `https://<your host>/api/messenger/webhook`
     and enter the verify token.
   - Click **Verify and Save**.
   - Subscribe to **messages** and **messaging_postbacks**.
   - Under **Generate access tokens**, click **Add Subscription** on the
     Page and tick the same two fields. Without this nothing arrives.
6. **Link your account.** In Finalechat, go to **Settings → Messenger →
   Get a link code**. Send `link CODE` to the Page from Messenger within 15
   minutes.

Before you depend on it, check that each of these works:

- `/ls`
- a reply
- a question answered with a button
- a swipe-reply
- `/new`

## How it works

- **Webhook** (`POST /api/messenger/webhook`). Each call's
  `X-Hub-Signature-256` is checked with the app secret. Each event is
  handled once; a redelivery is recognized by its message id. The
  connector then posts the reply, answers or dismisses the question, or
  queues a `session.start` command, exactly as the app does.
- **Relay.** Only one server replica relays at a time; it holds a
  PostgreSQL advisory lock. It keeps a cursor per linked chat over the
  account's messages and questions and sends what is new. Agent and system
  messages are sent, except `session_start`. Your own words are never sent
  back: not your Messenger replies, not your replies from the app, and not
  the terminal input an agent mirrors. Events only make the relay look
  sooner; the database decides what is new. Rows younger than two seconds
  wait for the next pass, so a slow transaction cannot be skipped.
- **Typing.** While the thread you are talking to shows a status line, the
  Page shows Messenger's typing dots.
- **Security.** Linking gives the chat the same authority as a signed-in
  app: it can answer, dismiss and start sessions. Link codes are single-use,
  last 15 minutes, and can only be created from the app. `/unlink` or
  Settings → Messenger → Unlink ends it.
