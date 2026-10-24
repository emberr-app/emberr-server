DROP TABLE object_deletions;

DROP TABLE pending_uploads;

ALTER TABLE items
    DROP COLUMN blob_nonce,
    DROP COLUMN blob_object_key;
