DROP INDEX media_files_unused_since_index;

ALTER TABLE media_files DROP COLUMN unused_since;

DROP TABLE media_locks;
