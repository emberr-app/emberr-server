ALTER TABLE items
    ADD COLUMN blob_object_key TEXT,
    ADD COLUMN blob_nonce      TEXT;

CREATE TABLE IF NOT EXISTS pending_uploads
(
    object_key TEXT PRIMARY KEY,
    account_id BIGINT NOT NULL,
    space_ref  BIGINT NOT NULL,
    item_id    TEXT   NOT NULL,
    size_bytes BIGINT NOT NULL,
    created_at BIGINT NOT NULL DEFAULT now_utc_micro_seconds(),
    expires_at BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS object_deletions
(
    object_key   TEXT PRIMARY KEY,
    delete_after BIGINT NOT NULL
);
