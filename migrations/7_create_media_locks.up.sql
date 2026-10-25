CREATE TABLE IF NOT EXISTS media_locks
(
    space_ref           BIGINT  NOT NULL,
    media_id            TEXT    NOT NULL,
    note_id             TEXT    NOT NULL,
    encrypted_key       TEXT,
    encrypted_key_nonce TEXT,
    revision            BIGINT  NOT NULL,
    deleted             BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at          BIGINT  NOT NULL DEFAULT now_utc_micro_seconds(),
    PRIMARY KEY (space_ref, media_id, note_id),
    CONSTRAINT fk_media_locks_space_ref
        FOREIGN KEY (space_ref)
            REFERENCES spaces (space_ref)
            ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS media_locks_space_ref_revision_index ON media_locks (space_ref, revision);

CREATE INDEX IF NOT EXISTS media_locks_space_ref_note_id_index ON media_locks (space_ref, note_id);

CREATE INDEX IF NOT EXISTS media_locks_live_media_id_index ON media_locks (media_id) WHERE deleted = FALSE;

ALTER TABLE media_files ADD COLUMN unused_since BIGINT;

UPDATE media_files SET unused_since = committed_at WHERE committed_at IS NOT NULL;

CREATE INDEX IF NOT EXISTS media_files_unused_since_index ON media_files (unused_since) WHERE unused_since IS NOT NULL;
