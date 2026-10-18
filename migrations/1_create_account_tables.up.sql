CREATE OR REPLACE FUNCTION now_utc_micro_seconds() RETURNS BIGINT AS
$$
SELECT CAST(extract(EPOCH FROM now() AT TIME ZONE 'utc') * 1000000 AS BIGINT);
$$ LANGUAGE sql;

CREATE TABLE IF NOT EXISTS accounts
(
    account_id            BIGINT PRIMARY KEY GENERATED ALWAYS AS IDENTITY,
    email_hash            TEXT   NOT NULL UNIQUE,
    encrypted_email       BYTEA  NOT NULL,
    encrypted_email_nonce BYTEA  NOT NULL,
    storage_used_bytes    BIGINT NOT NULL DEFAULT 0,
    created_at            BIGINT NOT NULL DEFAULT now_utc_micro_seconds()
);

CREATE TABLE IF NOT EXISTS account_keys
(
    account_id                                   BIGINT  PRIMARY KEY,
    password_salt                                TEXT    NOT NULL,
    mem_limit                                    BIGINT  NOT NULL,
    ops_limit                                    INTEGER NOT NULL,
    login_key_hash                               TEXT    NOT NULL,
    encrypted_master_key                         TEXT    NOT NULL,
    encrypted_master_key_nonce                   TEXT    NOT NULL,
    master_key_encrypted_with_recovery_key       TEXT    NOT NULL,
    master_key_encrypted_with_recovery_key_nonce TEXT    NOT NULL,
    recovery_key_encrypted_with_master_key       TEXT    NOT NULL,
    recovery_key_encrypted_with_master_key_nonce TEXT    NOT NULL,
    public_key                                   TEXT    NOT NULL,
    encrypted_private_key                        TEXT    NOT NULL,
    encrypted_private_key_nonce                  TEXT    NOT NULL,
    created_at                                   BIGINT  NOT NULL DEFAULT now_utc_micro_seconds(),
    CONSTRAINT fk_account_keys_account_id
        FOREIGN KEY (account_id)
            REFERENCES accounts (account_id)
            ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS sessions
(
    token_hash   BYTEA  PRIMARY KEY,
    account_id   BIGINT NOT NULL,
    device_name  TEXT   NOT NULL,
    created_at   BIGINT NOT NULL DEFAULT now_utc_micro_seconds(),
    last_used_at BIGINT NOT NULL DEFAULT now_utc_micro_seconds(),
    CONSTRAINT sessions_token_hash_length CHECK (octet_length(token_hash) = 32),
    CONSTRAINT fk_sessions_account_id
        FOREIGN KEY (account_id)
            REFERENCES accounts (account_id)
            ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS sessions_account_id_index ON sessions (account_id);

CREATE TABLE IF NOT EXISTS email_codes
(
    email_hash     TEXT    NOT NULL,
    purpose        TEXT    NOT NULL,
    code_hash      BYTEA   NOT NULL,
    wrong_attempts INTEGER NOT NULL DEFAULT 0,
    created_at     BIGINT  NOT NULL DEFAULT now_utc_micro_seconds(),
    expires_at     BIGINT  NOT NULL,
    PRIMARY KEY (email_hash, purpose)
);

CREATE TABLE IF NOT EXISTS rate_limits
(
    limit_key      TEXT    PRIMARY KEY,
    attempt_count  INTEGER NOT NULL,
    window_ends_at BIGINT  NOT NULL
);
