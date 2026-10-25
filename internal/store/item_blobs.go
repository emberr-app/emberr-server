package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type PendingUpload struct {
	ObjectKey string
	AccountID int64
	SpaceRef  int64
	ItemID    string
	SizeBytes int64
	ExpiresAt int64
}

func (table SpaceTable) AddPendingUpload(ctx context.Context, upload PendingUpload) error {
	_, err := table.pool.Exec(ctx,
		`INSERT INTO pending_uploads (object_key, account_id, space_ref, item_id, size_bytes, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		upload.ObjectKey, upload.AccountID, upload.SpaceRef, upload.ItemID, upload.SizeBytes, upload.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("insert pending upload: %w", err)
	}
	return nil
}

func (table SpaceTable) FindPendingUpload(ctx context.Context, objectKey string) (PendingUpload, bool, error) {
	upload := PendingUpload{ObjectKey: objectKey}
	err := table.pool.QueryRow(ctx,
		`SELECT account_id, space_ref, item_id, size_bytes, expires_at FROM pending_uploads WHERE object_key = $1`,
		objectKey,
	).Scan(&upload.AccountID, &upload.SpaceRef, &upload.ItemID, &upload.SizeBytes, &upload.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return PendingUpload{}, false, nil
	}
	if err != nil {
		return PendingUpload{}, false, fmt.Errorf("load pending upload: %w", err)
	}
	return upload, true, nil
}

func (space *LockedSpace) TakePendingUpload(ctx context.Context, objectKey string, itemID string, sizeBytes int64) (bool, error) {
	var takenObjectKey string
	err := space.tx.QueryRow(ctx,
		`DELETE FROM pending_uploads
		WHERE object_key = $1 AND account_id = $2 AND space_ref = $3 AND item_id = $4 AND size_bytes = $5
			AND expires_at > now_utc_micro_seconds()
		RETURNING object_key`,
		objectKey, space.ownerAccountID, space.spaceRef, itemID, sizeBytes,
	).Scan(&takenObjectKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("take pending upload: %w", err)
	}
	return true, nil
}

func (space *LockedSpace) QueueObjectDeletion(ctx context.Context, objectKey string, delay time.Duration) error {
	_, err := space.tx.Exec(ctx,
		`INSERT INTO object_deletions (object_key, delete_after) VALUES ($1, now_utc_micro_seconds() + $2)
		ON CONFLICT (object_key) DO NOTHING`,
		objectKey, delay.Microseconds(),
	)
	if err != nil {
		return fmt.Errorf("queue object deletion: %w", err)
	}
	return nil
}

func (table SpaceTable) QueueExpiredPendingUploadsForDeletion(ctx context.Context, maxCount int) (int64, error) {
	var queuedCount int64
	err := table.pool.QueryRow(ctx,
		`WITH expired_uploads AS (
			DELETE FROM pending_uploads
			WHERE object_key IN (
				SELECT object_key FROM pending_uploads
				WHERE expires_at <= now_utc_micro_seconds()
				LIMIT $1
				FOR UPDATE SKIP LOCKED
			)
			RETURNING object_key
		),
		queued_deletions AS (
			INSERT INTO object_deletions (object_key, delete_after)
			SELECT object_key, now_utc_micro_seconds() FROM expired_uploads
			ON CONFLICT (object_key) DO NOTHING
		)
		SELECT count(*) FROM expired_uploads`,
		maxCount,
	).Scan(&queuedCount)
	if err != nil {
		return 0, fmt.Errorf("queue expired pending uploads for deletion: %w", err)
	}
	return queuedCount, nil
}

func (table SpaceTable) ListDueObjectDeletions(ctx context.Context, maxCount int) ([]string, error) {
	rows, err := table.pool.Query(ctx,
		`SELECT object_key FROM object_deletions
		WHERE delete_after <= now_utc_micro_seconds()
		ORDER BY delete_after
		LIMIT $1`,
		maxCount,
	)
	if err != nil {
		return nil, fmt.Errorf("list due object deletions: %w", err)
	}
	objectKeys, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("read due object deletions: %w", err)
	}
	return objectKeys, nil
}

func (table SpaceTable) RemoveObjectDeletion(ctx context.Context, objectKey string) error {
	_, err := table.pool.Exec(ctx, `DELETE FROM object_deletions WHERE object_key = $1`, objectKey)
	if err != nil {
		return fmt.Errorf("remove object deletion: %w", err)
	}
	return nil
}
