-- /pause keeps a Messenger link but stops the relay; /resume starts it
-- again from that moment.

ALTER TABLE messenger_links ADD COLUMN paused_at timestamptz;
