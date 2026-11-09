CREATE TABLE IF NOT EXISTS subscriptions
(
    account_id               BIGINT PRIMARY KEY,
    plan                     TEXT   NOT NULL,
    storage_limit_bytes      BIGINT NOT NULL,
    expires_at               BIGINT NOT NULL,
    provider                 TEXT,
    provider_subscription_id TEXT,
    created_at               BIGINT NOT NULL DEFAULT now_utc_micro_seconds(),
    cloud_copy_removed_at    BIGINT,
    CONSTRAINT fk_subscriptions_account_id
        FOREIGN KEY (account_id)
            REFERENCES accounts (account_id)
            ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS subscriptions_not_removed_expires_at_index ON subscriptions (expires_at) WHERE cloud_copy_removed_at IS NULL;

CREATE TABLE IF NOT EXISTS subscription_emails
(
    account_id BIGINT NOT NULL,
    kind       TEXT   NOT NULL,
    sent_at    BIGINT NOT NULL,
    PRIMARY KEY (account_id, kind),
    CONSTRAINT fk_subscription_emails_account_id
        FOREIGN KEY (account_id)
            REFERENCES accounts (account_id)
            ON DELETE CASCADE
);

INSERT INTO subscriptions (account_id, plan, storage_limit_bytes, expires_at)
SELECT account_id, 'trial', 104857600, now_utc_micro_seconds() + 14 * 24 * 60 * 60 * CAST(1000000 AS BIGINT)
FROM accounts;
