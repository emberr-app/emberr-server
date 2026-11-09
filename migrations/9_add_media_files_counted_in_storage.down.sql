ALTER TABLE media_files DROP COLUMN counted_in_storage;

UPDATE accounts SET storage_used_bytes =
    (SELECT coalesce(sum(items.size_bytes), 0)::bigint
     FROM items JOIN spaces ON spaces.space_ref = items.space_ref
     WHERE spaces.owner_account_id = accounts.account_id AND items.deleted = FALSE)
    +
    (SELECT coalesce(sum(media_files.size_bytes), 0)::bigint
     FROM media_files
     WHERE media_files.account_id = accounts.account_id
       AND media_files.committed_at IS NOT NULL);
