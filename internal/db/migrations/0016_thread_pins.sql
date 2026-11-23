-- A title or summary the account holder writes in the app is theirs: it is
-- pinned, and an agent's later value for that field is ignored until the
-- user clears it. Agents keep both current otherwise.

ALTER TABLE threads
    ADD COLUMN title_pinned BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN description_pinned BOOLEAN NOT NULL DEFAULT false;
