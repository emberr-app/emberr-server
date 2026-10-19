ALTER TABLE sessions
    ADD COLUMN IF NOT EXISTS password_reset_until BIGINT;
