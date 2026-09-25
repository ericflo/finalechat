-- Facebook Messenger connector: one linked Messenger conversation per
-- account, the relay's delivery cursors, and the bookkeeping that lets a
-- swipe-reply or a button tap find its thread and question again.

CREATE TABLE messenger_links (
    user_id          uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    -- psid is the page-scoped id Messenger assigns the person.
    psid             text NOT NULL UNIQUE,
    page_id          text NOT NULL,
    -- pinned_thread_id holds replies on one thread (/go N); NULL follows
    -- whichever thread spoke last (last_thread_id).
    pinned_thread_id uuid REFERENCES threads(id) ON DELETE SET NULL,
    last_thread_id   uuid REFERENCES threads(id) ON DELETE SET NULL,
    -- Delivery cursors: (created_at, id) of the last relayed message and
    -- question.
    message_at       timestamptz NOT NULL,
    message_id       uuid,
    question_at      timestamptz NOT NULL,
    question_id      uuid,
    -- Messenger only lets the page write within 24 hours of the person's
    -- last message; window_closed_at records a refusal so the relay holds
    -- until the next inbound message.
    last_inbound_at  timestamptz NOT NULL DEFAULT now(),
    window_closed_at timestamptz,
    -- important_only relays questions and important messages only (/quiet).
    important_only   boolean NOT NULL DEFAULT false,
    -- state carries the /new conversation and the /more position.
    state            jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE messenger_link_codes (
    code_hash  bytea PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL
);
CREATE INDEX messenger_link_codes_user_idx ON messenger_link_codes (user_id);

-- Short numbers (#1, #2, ...) for threads, stable for the life of a link.
CREATE TABLE messenger_handles (
    user_id   uuid NOT NULL REFERENCES messenger_links(user_id) ON DELETE CASCADE,
    thread_id uuid NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    handle    integer NOT NULL,
    PRIMARY KEY (user_id, thread_id),
    UNIQUE (user_id, handle)
);

-- Every message the page sent, so a swipe-reply routes to its thread.
CREATE TABLE messenger_sent (
    mid         text PRIMARY KEY,
    user_id     uuid NOT NULL REFERENCES messenger_links(user_id) ON DELETE CASCADE,
    thread_id   uuid REFERENCES threads(id) ON DELETE CASCADE,
    question_id uuid REFERENCES questions(id) ON DELETE CASCADE,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX messenger_sent_created_idx ON messenger_sent (created_at);

-- Inbound messages already handled, so a webhook Meta redelivers is not
-- acted on twice.
CREATE TABLE messenger_inbound (
    mid        text PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX messenger_inbound_created_idx ON messenger_inbound (created_at);

-- The relay reads an account's new messages and questions across threads.
CREATE INDEX messages_user_created_idx ON messages (user_id, created_at, id);
CREATE INDEX questions_user_created_idx ON questions (user_id, created_at, id);
