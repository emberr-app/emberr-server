CREATE INDEX IF NOT EXISTS object_deletions_delete_after_index ON object_deletions (delete_after);

CREATE INDEX IF NOT EXISTS pending_uploads_expires_at_index ON pending_uploads (expires_at);
