package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const mediaPieceKeyColumn = `'media/' || media.media_id || '/' || media.upload_id || '/piece_' || lpad(piece_index::text, 5, '0')`

const everyMediaPiece = `generate_series(0, cardinality(media.piece_sizes) - 1) AS piece_index`

type MediaTable struct {
	pool *pgxpool.Pool
}

type MediaUpload struct {
	UploadID   string
	PieceSizes []int64
	SizeBytes  int64
}

type MediaFile struct {
	Upload        MediaUpload
	UploadExpired bool
	Committed     bool
}

type LockedMediaFile struct {
	MediaFile
	tx        pgx.Tx
	mediaID   string
	accountID int64
}

const mediaFileColumns = `upload_id, piece_sizes, size_bytes, expires_at <= now_utc_micro_seconds(), committed_at IS NOT NULL`

func mediaFileScanTargets(file *MediaFile) []any {
	return []any{&file.Upload.UploadID, &file.Upload.PieceSizes, &file.Upload.SizeBytes, &file.UploadExpired, &file.Committed}
}

func NewMediaTable(pool *pgxpool.Pool) MediaTable {
	return MediaTable{pool: pool}
}

func MediaUploadPrefix(mediaID string, uploadID string) string {
	return "media/" + mediaID + "/" + uploadID + "/"
}

func MediaPieceKey(mediaID string, uploadID string, pieceIndex int) string {
	return fmt.Sprintf("%spiece_%05d", MediaUploadPrefix(mediaID, uploadID), pieceIndex)
}

func (table MediaTable) Find(ctx context.Context, accountID int64, mediaID string) (MediaFile, bool, error) {
	var file MediaFile
	err := table.pool.QueryRow(ctx,
		`SELECT `+mediaFileColumns+` FROM media_files WHERE media_id = $1 AND account_id = $2`,
		mediaID, accountID,
	).Scan(mediaFileScanTargets(&file)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return MediaFile{}, false, nil
	}
	if err != nil {
		return MediaFile{}, false, fmt.Errorf("load media file: %w", err)
	}
	return file, true, nil
}

func (table MediaTable) LockOrCreate(ctx context.Context, accountID int64, mediaID string, newUpload MediaUpload, uploadLifetime time.Duration, change func(file *LockedMediaFile) error) (bool, error) {
	found := false
	err := pgx.BeginFunc(ctx, table.pool, func(tx pgx.Tx) error {
		file := LockedMediaFile{tx: tx, mediaID: mediaID, accountID: accountID}
		err := tx.QueryRow(ctx,
			`INSERT INTO media_files (media_id, account_id, upload_id, piece_sizes, size_bytes, expires_at)
			VALUES ($1, $2, $3, $4, $5, now_utc_micro_seconds() + $6)
			ON CONFLICT (media_id) DO UPDATE SET account_id = EXCLUDED.account_id
			WHERE media_files.account_id = EXCLUDED.account_id
			RETURNING `+mediaFileColumns,
			mediaID, accountID, newUpload.UploadID, newUpload.PieceSizes, newUpload.SizeBytes, uploadLifetime.Microseconds(),
		).Scan(mediaFileScanTargets(&file.MediaFile)...)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("lock or create media file: %w", err)
		}
		found = true
		return change(&file)
	})
	if err != nil {
		return false, fmt.Errorf("update media file: %w", err)
	}
	return found, nil
}

func (table MediaTable) UpdateLocked(ctx context.Context, accountID int64, mediaID string, change func(file *LockedMediaFile) error) (bool, error) {
	found := false
	err := pgx.BeginFunc(ctx, table.pool, func(tx pgx.Tx) error {
		file := LockedMediaFile{tx: tx, mediaID: mediaID, accountID: accountID}
		err := tx.QueryRow(ctx,
			`SELECT `+mediaFileColumns+` FROM media_files WHERE media_id = $1 AND account_id = $2 FOR UPDATE`,
			mediaID, accountID,
		).Scan(mediaFileScanTargets(&file.MediaFile)...)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("lock media file: %w", err)
		}
		found = true
		return change(&file)
	})
	if err != nil {
		return false, fmt.Errorf("update media file: %w", err)
	}
	return found, nil
}

func (file *LockedMediaFile) StartOver(ctx context.Context, newUpload MediaUpload, uploadLifetime time.Duration, oldPiecesDeletionDelay time.Duration) error {
	_, err := file.tx.Exec(ctx,
		`INSERT INTO object_deletions (object_key, delete_after)
		SELECT `+mediaPieceKeyColumn+`, now_utc_micro_seconds() + $2
		FROM media_files AS media, `+everyMediaPiece+`
		WHERE media.media_id = $1
		ON CONFLICT (object_key) DO NOTHING`,
		file.mediaID, oldPiecesDeletionDelay.Microseconds(),
	)
	if err != nil {
		return fmt.Errorf("queue old media pieces for deletion: %w", err)
	}

	_, err = file.tx.Exec(ctx,
		`UPDATE media_files SET upload_id = $2, piece_sizes = $3, size_bytes = $4, expires_at = now_utc_micro_seconds() + $5
		WHERE media_id = $1`,
		file.mediaID, newUpload.UploadID, newUpload.PieceSizes, newUpload.SizeBytes, uploadLifetime.Microseconds(),
	)
	if err != nil {
		return fmt.Errorf("start media upload over: %w", err)
	}
	file.Upload = newUpload
	file.UploadExpired = false
	return nil
}

func (file *LockedMediaFile) Commit(ctx context.Context) error {
	_, err := file.tx.Exec(ctx, `UPDATE media_files SET committed_at = now_utc_micro_seconds(), unused_since = now_utc_micro_seconds() WHERE media_id = $1`, file.mediaID)
	if err != nil {
		return fmt.Errorf("mark media file committed: %w", err)
	}

	_, err = file.tx.Exec(ctx,
		`UPDATE accounts SET storage_used_bytes = storage_used_bytes + $2 WHERE account_id = $1`,
		file.accountID, file.Upload.SizeBytes,
	)
	if err != nil {
		return fmt.Errorf("update storage used: %w", err)
	}
	file.Committed = true
	return nil
}

func (table MediaTable) QueueExpiredUnfinishedUploadsForDeletion(ctx context.Context, maxCount int, piecesDeletionDelay time.Duration) (int64, error) {
	var removedCount int64
	err := table.pool.QueryRow(ctx,
		`WITH expired_media AS (
			DELETE FROM media_files
			WHERE media_id IN (
				SELECT media_id FROM media_files
				WHERE committed_at IS NULL AND expires_at <= now_utc_micro_seconds()
				LIMIT $1
				FOR UPDATE SKIP LOCKED
			)
			RETURNING media_id, upload_id, piece_sizes
		),
		queued_deletions AS (
			INSERT INTO object_deletions (object_key, delete_after)
			SELECT `+mediaPieceKeyColumn+`, now_utc_micro_seconds() + $2
			FROM expired_media AS media, `+everyMediaPiece+`
			ON CONFLICT (object_key) DO NOTHING
		)
		SELECT count(*) FROM expired_media`,
		maxCount, piecesDeletionDelay.Microseconds(),
	).Scan(&removedCount)
	if err != nil {
		return 0, fmt.Errorf("queue expired media uploads for deletion: %w", err)
	}
	return removedCount, nil
}

func (table MediaTable) QueueUnusedFilesForDeletion(ctx context.Context, maxCount int, unusedFor time.Duration) (int64, error) {
	var removedCount int64
	err := table.pool.QueryRow(ctx,
		`WITH unused_media AS (
			DELETE FROM media_files
			WHERE media_id IN (
				SELECT media_id FROM media_files
				WHERE committed_at IS NOT NULL AND unused_since <= now_utc_micro_seconds() - $2
					AND NOT EXISTS (SELECT 1 FROM media_locks WHERE media_locks.media_id = media_files.media_id AND media_locks.deleted = FALSE)
				LIMIT $1
				FOR UPDATE SKIP LOCKED
			)
			RETURNING media_id, account_id, upload_id, piece_sizes, size_bytes
		),
		queued_deletions AS (
			INSERT INTO object_deletions (object_key, delete_after)
			SELECT `+mediaPieceKeyColumn+`, now_utc_micro_seconds()
			FROM unused_media AS media, `+everyMediaPiece+`
			ON CONFLICT (object_key) DO NOTHING
		),
		removed_bytes_per_account AS (
			SELECT account_id, sum(size_bytes)::bigint AS removed_bytes FROM unused_media GROUP BY account_id
		),
		lowered_storage AS (
			UPDATE accounts SET storage_used_bytes = accounts.storage_used_bytes - removed_bytes_per_account.removed_bytes
			FROM removed_bytes_per_account
			WHERE accounts.account_id = removed_bytes_per_account.account_id
		)
		SELECT count(*) FROM unused_media`,
		maxCount, unusedFor.Microseconds(),
	).Scan(&removedCount)
	if err != nil {
		return 0, fmt.Errorf("queue unused media files for deletion: %w", err)
	}
	return removedCount, nil
}
