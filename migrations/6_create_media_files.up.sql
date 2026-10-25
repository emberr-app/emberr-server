CREATE TABLE IF NOT EXISTS media_files
(
    media_id     TEXT     PRIMARY KEY,
    account_id   BIGINT   NOT NULL,
    upload_id    TEXT     NOT NULL,
    piece_sizes  BIGINT[] NOT NULL,
    size_bytes   BIGINT   NOT NULL,
    created_at   BIGINT   NOT NULL DEFAULT now_utc_micro_seconds(),
    expires_at   BIGINT   NOT NULL,
    committed_at BIGINT,
    CONSTRAINT fk_media_files_account_id
        FOREIGN KEY (account_id)
            REFERENCES accounts (account_id)
            ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS media_files_account_id_index ON media_files (account_id);

CREATE INDEX IF NOT EXISTS media_files_unfinished_expires_at_index ON media_files (expires_at) WHERE committed_at IS NULL;
