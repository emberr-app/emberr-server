package jobs

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/emberr-app/emberr-server/internal/blob"
	"github.com/emberr-app/emberr-server/internal/store"
	"github.com/emberr-app/emberr-server/internal/sync"
)

const cleanupBatchSize = 1000

type Cleanup struct {
	spaceTable     store.SpaceTable
	mediaTable     store.MediaTable
	emailCodeTable store.EmailCodeTable
	rateLimitTable store.RateLimitTable
	blobStore      blob.Store
}

func NewCleanup(pool *pgxpool.Pool, blobStore blob.Store) *Cleanup {
	return &Cleanup{
		spaceTable:     store.NewSpaceTable(pool),
		mediaTable:     store.NewMediaTable(pool),
		emailCodeTable: store.NewEmailCodeTable(pool),
		rateLimitTable: store.NewRateLimitTable(pool),
		blobStore:      blobStore,
	}
}

func (cleanup *Cleanup) DeleteDueObjects(ctx context.Context) error {
	deletedCount, failedCount := 0, 0
	var lastFailure error
	for {
		objectKeys, err := cleanup.spaceTable.ListDueObjectDeletions(ctx, cleanupBatchSize)
		if err != nil {
			return err
		}

		failedBefore := failedCount
		for _, objectKey := range objectKeys {
			if err := cleanup.blobStore.DeleteObject(ctx, objectKey); err != nil {
				if ctx.Err() != nil {
					return fmt.Errorf("delete due objects: %w", ctx.Err())
				}
				failedCount++
				lastFailure = err
				continue
			}
			if err := cleanup.spaceTable.RemoveObjectDeletion(ctx, objectKey); err != nil {
				return err
			}
			deletedCount++
		}

		if len(objectKeys) < cleanupBatchSize || failedCount > failedBefore {
			break
		}
	}

	if deletedCount > 0 {
		slog.Info("deleted queued objects", "deleted", deletedCount)
	}
	if failedCount > 0 {
		slog.Error("could not delete some queued objects, they stay queued", "failed", failedCount, "error", blob.WithoutRequestURL(lastFailure))
	}
	return nil
}

func (cleanup *Cleanup) QueueExpiredPendingUploads(ctx context.Context) error {
	var totalQueued int64
	for {
		queuedCount, err := cleanup.spaceTable.QueueExpiredPendingUploadsForDeletion(ctx, cleanupBatchSize)
		if err != nil {
			return err
		}
		totalQueued += queuedCount
		if queuedCount < cleanupBatchSize {
			break
		}
	}

	if totalQueued > 0 {
		slog.Info("queued expired pending uploads for deletion", "queued", totalQueued)
	}
	return nil
}

func (cleanup *Cleanup) QueueExpiredMediaUploads(ctx context.Context) error {
	var totalRemoved int64
	for {
		removedCount, err := cleanup.mediaTable.QueueExpiredUnfinishedUploadsForDeletion(ctx, cleanupBatchSize, sync.AbandonedMediaPieceDeletionDelay)
		if err != nil {
			return err
		}
		totalRemoved += removedCount
		if removedCount < cleanupBatchSize {
			break
		}
	}

	if totalRemoved > 0 {
		slog.Info("queued expired media uploads for deletion", "uploads", totalRemoved)
	}
	return nil
}

func (cleanup *Cleanup) RemoveExpiredCodesAndRateLimits(ctx context.Context) error {
	deletedCodes, err := cleanup.emailCodeTable.DeleteExpired(ctx)
	if err != nil {
		return err
	}
	deletedRateLimits, err := cleanup.rateLimitTable.DeleteEnded(ctx)
	if err != nil {
		return err
	}

	if deletedCodes > 0 || deletedRateLimits > 0 {
		slog.Info("removed expired email codes and rate limits", "emailCodes", deletedCodes, "rateLimits", deletedRateLimits)
	}
	return nil
}
