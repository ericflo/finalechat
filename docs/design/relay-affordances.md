# Relay threads: affordances worth adding

Scope: relay threads only, where eagent runs Codex or Claude Code for you. The rest of Finalechat stays as it is.

The evidence comes from your 17 relay sessions (805 messages) and builds on what shipped this week: the agent picker, the "Sent to Codex" bubbles, the badge and controls sheet, the To toggle, and hold-to-interrupt.

Ranked by value for effort. S means small: a day or less across eagent and Finalechat.

## 1. Delivery receipts on your messages (S)

A small line under each of your messages in a relay thread:
- `→ Codex`: typed into it.
- `✓ Codex has it`: its transcript shows the message arrived.
- `held for the end of its turn`
- `kept for eagent`: not forwarded, and why ("a question about progress").
- `✗ not delivered · Retry`

**Why.** You asked these more than once:
- "You sent that to codex right?"
- "Is my message from yesterday still queued?"
- "How is Claude receiving this?"
- "Are we sure we're not reading something wrong in the screen session?"

**Build.** eagent already records `relay.send` (with the user message's seq) and `relay.delivered`. It sets `meta.relay_delivery` on the mirrored user message. Finalechat renders it, with Retry re-sending the text.

## 2. Codex's own words, one tap away (S)

Under the narrator's turn summary, a collapsed "**Codex's report**" holds its final message verbatim: duration, tool count, then the full text.

**Why.** The paraphrase lost constraints (www vs next, twice); "send me the prompt you sent"; "is this you or codex?".

**Build.** eagent posts the `relay.turn` text as a `meta.kind: relay_report` message, collapsed by default and threaded to the summary it belongs to. It is the same pattern as the "Sent to Codex" bubble.

## 3. Quick replies above the composer (S)

A row of chips in relay threads. Each sends its text verbatim to the agent, or interrupts first where noted:

`Keep going` · `Commit & push` · `Deploy` · `Screenshots` · `Stop` (interrupts)

**Why.** These are your most repeated messages to agents:

| Message | Times |
|---|---|
| stop / wait | 33 |
| deploy / ship | 32 |
| commit / push | 20 |
| screenshots / show me | 19 |
| keep going | 19 |

**Build.** Finalechat only, for the fixed set. Editable per thread later if you want different chips.

## 4. Claude's suggested reply as a chip (M)

When Claude Code shows its own suggested next message (the grey text in its input box), offer it as a chip: `Send: "Run the full test suite"`. One tap sends it.

**Why.** You answered "Send" 23 times to accept these, and they were mistaken for questions 42 times.

**Build.** eagent reads the suggestion from the pane while Claude is idle (styled capture), publishes it in `meta.relay.suggestion`, and clears it when anything is sent. Finalechat shows the chip.

## 5. Screen peek (S)

"Screen" in the controls sheet posts a snapshot of the agent's terminal as a monospace card, with secrets blanked.

**Why.** For the moments you want to see what it sees: a dialog, a stuck prompt, "is it really doing that?".

**Build.** A `/screen` command in eagent (the pane capture exists as `relay_screen`) that posts the card. Finalechat adds a sheet item and a monospace rendering for `meta.kind: relay_screen`.

## 6. Reply to a specific report (S)

Long-press one of Codex's reports, choose **Reply**, and your message reaches Codex with the quoted lines, e.g. `> Shipped lava; sky and depth next`. It knows what "that" means.

**Why.**
- "Wait so it did lava but what about sky?"
- "What does all this mean? > On hold per your calls: PG14…"

You already quote by hand with `>`.

**Build.** Finalechat adds the quote and `meta.reply_to`. eagent forwards the text as is: it already includes the quote.

## 7. "Tell me when it's done" (S)

A bell on the "Codex · working" badge buzzes you once when the current turn ends, even if updates are off.

**Why.** "Let me know when there's a new release apk to install", and long builds you would otherwise poll.

**Build.** A `/notify-done` command. eagent marks the next turn's report important.

## 8. Recent relays on New session (S)

Under the agent picker, the last few combinations as one-tap chips: `Codex · SoundboxingWork`, `Claude · snowdown`, `Codex · epsilon`. Each fills the agent and folder.

**Why.** Your openings are the same handful of agent and folder pairs.

**Build.** Finalechat remembers the agent and folder per connector (local), or eagent lists recent relays in `details`.

## Small extras

- The controls sheet gets **Copy attach command** (`eagent relays attach codex`), for when you are at the desk.
- When an image you send goes to the agent, its bubble says `→ Codex can see this`.
- Messenger gets quick-reply buttons with the same chips as 3.

## Suggested first batch

**1, 2, 3, 5, 7.** All are small. Together they cover trust (receipts, its own words, the screen), speed (chips) and calm (notify when done) without changing anything outside relay threads.

## Status

Built in Finalechat (relay threads only, phone first, light and dark):

- **1. Receipts**: done, from thread meta `relay_receipts` (message id to `sent`, `delivered`, `held`, `eagent` or `failed`) rather than a field on each message. They update live and offer Retry when a message failed. The "why" for `kept for eagent` is not shown yet.
- **2. Codex's report**: done for `meta.kind: relay_report`. It is folded to a line with its duration, tool count and "interrupted", and expands to the full text. It is not yet threaded to the narrator's summary.
- **3. Quick replies**: done, and Stop interrupts the agent. Messenger also gets Keep going, Commit & push and Stop as buttons under the agent's messages.
- **5. Screen peek**: done. "Screen" in the controls sends `/screen`, and `relay_screen` messages render as an unwrapped terminal card with Copy.
- **6. Reply to a report**: done as a Reply button on the report, not a long press. It quotes the report's first line into the composer. No `meta.reply_to` is sent.
- **7. Tell me when it's done**: done as a bell beside the badge and an item in the controls, both sending `/notify-done`. They show as armed while `meta.relay.notify_done` is true.
- **8. Recent relays**: done, kept on the device: the last five agent and folder pairs started from this device, per connector.
- **Copy attach command**: done, from `meta.relay.attach`.

Not built: 4 (Claude's suggested reply) and "→ Codex can see this" on images.
