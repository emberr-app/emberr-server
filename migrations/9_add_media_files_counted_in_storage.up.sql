ALTER TABLE media_files ADD COLUMN counted_in_storage BOOLEAN NOT NULL DEFAULT TRUE;

UPDATE media_files SET counted_in_storage = FALSE
WHERE committed_at IS NOT NULL
  AND unused_since IS NOT NULL
  AND EXISTS (SELECT 1 FROM media_locks WHERE media_locks.media_id = media_files.media_id);

UPDATE accounts SET storage_used_bytes =
    (SELECT coalesce(sum(items.size_bytes), 0)::bigint
     FROM items JOIN spaces ON spaces.space_ref = items.space_ref
     WHERE spaces.owner_account_id = accounts.account_id AND items.deleted = FALSE)
    +
    (SELECT coalesce(sum(media_files.size_bytes), 0)::bigint
     FROM media_files
     WHERE media_files.account_id = accounts.account_id
       AND media_files.committed_at IS NOT NULL
       AND media_files.counted_in_storage = TRUE);
