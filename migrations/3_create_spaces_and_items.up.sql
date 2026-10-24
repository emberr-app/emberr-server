CREATE TABLE IF NOT EXISTS spaces
(
    space_ref           BIGINT PRIMARY KEY GENERATED ALWAYS AS IDENTITY,
    owner_account_id    BIGINT NOT NULL,
    client_space_id     TEXT   NOT NULL,
    kind                TEXT   NOT NULL,
    latest_revision     BIGINT NOT NULL DEFAULT 0,
    encrypted_key       TEXT   NOT NULL,
    encrypted_key_nonce TEXT   NOT NULL,
    pruned_revision     BIGINT NOT NULL DEFAULT 0,
    created_at          BIGINT NOT NULL DEFAULT now_utc_micro_seconds(),
    CONSTRAINT spaces_owner_client_space_id_unique UNIQUE (owner_account_id, client_space_id),
    CONSTRAINT spaces_kind_check CHECK (kind IN ('space', 'account')),
    CONSTRAINT fk_spaces_owner_account_id
        FOREIGN KEY (owner_account_id)
            REFERENCES accounts (account_id)
            ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS items
(
    space_ref            BIGINT  NOT NULL,
    item_id              TEXT    NOT NULL,
    item_type            TEXT    NOT NULL,
    payload_version      INTEGER NOT NULL,
    parent_id            TEXT,
    encrypted_key        TEXT,
    encrypted_key_nonce  TEXT,
    inline_payload       TEXT,
    inline_payload_nonce TEXT,
    revision             BIGINT  NOT NULL,
    size_bytes           BIGINT  NOT NULL,
    deleted              BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at           BIGINT  NOT NULL DEFAULT now_utc_micro_seconds(),
    PRIMARY KEY (space_ref, item_id),
    CONSTRAINT fk_items_space_ref
        FOREIGN KEY (space_ref)
            REFERENCES spaces (space_ref)
            ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS items_space_ref_revision_index ON items (space_ref, revision);
