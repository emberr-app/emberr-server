package jobs

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/emberr-app/emberr-server/internal/store"
	"github.com/emberr-app/emberr-server/internal/sync"
	"github.com/emberr-app/emberr-server/internal/testutil"
)

const (
	testObjectBytes   = 500
	testMediaUploadID = "upload"
)

func TestDueObjectIsDeletedWhileANotYetDueObjectStays(t *testing.T) {
	cleanup, pool, blobStore := newTestCleanup(t)
	dueKey, notYetDueKey := "items/1/note/due", "items/1/note/not-yet-due"
	blobStore.PutObject(dueKey, testObjectBytes)
	blobStore.PutObject(notYetDueKey, testObjectBytes)
	queueObjectDeletion(t, pool, dueKey, time.Now().Add(-time.Minute))
	queueObjectDeletion(t, pool, notYetDueKey, time.Now().Add(time.Hour))

	if err := cleanup.DeleteDueObjects(t.Context()); err != nil {
		t.Fatal(err)
	}

	if blobStore.HasObject(dueKey) {
		t.Error("the due object is still in the blob store")
	}
	if _, queued := deletionTimeOf(t, pool, dueKey); queued {
		t.Error("the due object's row is still queued")
	}
	if !blobStore.HasObject(notYetDueKey) {
		t.Error("the not yet due object was deleted")
	}
	if _, queued := deletionTimeOf(t, pool, notYetDueKey); !queued {
		t.Error("the not yet due object's row was removed")
	}
}

func TestFailedObjectDeleteKeepsItsRowForTheNextRun(t *testing.T) {
	cleanup, pool, blobStore := newTestCleanup(t)
	objectKey := "items/1/note/fails"
	blobStore.PutObject(objectKey, testObjectBytes)
	blobStore.FailDeletesOf(objectKey)
	queueObjectDeletion(t, pool, objectKey, time.Now().Add(-time.Minute))

	if err := cleanup.DeleteDueObjects(t.Context()); err != nil {
		t.Fatal(err)
	}

	if _, queued := deletionTimeOf(t, pool, objectKey); !queued {
		t.Error("the row was removed although the object could not be deleted")
	}
	if !blobStore.HasObject(objectKey) {
		t.Error("the object is gone although its delete failed")
	}
}

func TestExpiredPendingUploadIsQueuedForDeletionNowWhileAnUnexpiredOneStays(t *testing.T) {
	cleanup, pool, _ := newTestCleanup(t)
	expiredKey, unexpiredKey := "items/1/note/expired", "items/1/note/unexpired"
	addPendingUpload(t, pool, expiredKey, time.Now().Add(-time.Minute))
	addPendingUpload(t, pool, unexpiredKey, time.Now().Add(time.Hour))

	if err := cleanup.QueueExpiredPendingUploads(t.Context()); err != nil {
		t.Fatal(err)
	}

	if isPendingUpload(t, pool, expiredKey) {
		t.Error("the expired pending upload is still pending")
	}
	if deleteAfter, queued := deletionTimeOf(t, pool, expiredKey); !queued || deleteAfter > time.Now().UnixMicro() {
		t.Errorf("expired upload: queued = %v, delete after %d; want queued and ready now", queued, deleteAfter)
	}
	if !isPendingUpload(t, pool, unexpiredKey) {
		t.Error("the unexpired pending upload was removed")
	}
	if _, queued := deletionTimeOf(t, pool, unexpiredKey); queued {
		t.Error("the unexpired pending upload was queued for deletion")
	}
}

func TestExpiredUnfinishedMediaUploadIsQueuedWhileUnexpiredAndCommittedFilesStay(t *testing.T) {
	cleanup, pool, _ := newTestCleanup(t)
	accountID := addTestAccount(t, pool)
	addMediaFile(t, pool, accountID, "media_expired", time.Now().Add(-time.Minute), false)
	addMediaFile(t, pool, accountID, "media_unexpired", time.Now().Add(time.Hour), false)
	addMediaFile(t, pool, accountID, "media_committed", time.Now().Add(-time.Minute), true)

	if err := cleanup.QueueExpiredMediaUploads(t.Context()); err != nil {
		t.Fatal(err)
	}

	if hasMediaFile(t, pool, "media_expired") {
		t.Error("the expired unfinished upload's row is still there")
	}
	wantDeleteAfter := time.Now().Add(sync.AbandonedMediaPieceDeletionDelay)
	for pieceIndex := range 2 {
		pieceKey := store.MediaPieceKey("media_expired", testMediaUploadID, pieceIndex)
		deleteAfter, queued := deletionTimeOf(t, pool, pieceKey)
		if !queued || time.UnixMicro(deleteAfter).Sub(wantDeleteAfter).Abs() > time.Minute {
			t.Errorf("piece %s: queued = %v, delete after %s; want queued for about %s", pieceKey, queued, time.UnixMicro(deleteAfter), wantDeleteAfter)
		}
	}
	for _, mediaID := range []string{"media_unexpired", "media_committed"} {
		if !hasMediaFile(t, pool, mediaID) {
			t.Errorf("%s was removed", mediaID)
		}
		if _, queued := deletionTimeOf(t, pool, store.MediaPieceKey(mediaID, testMediaUploadID, 0)); queued {
			t.Errorf("a piece of %s was queued for deletion", mediaID)
		}
	}
}

func TestMediaFileUnusedForOverEightDaysIsRemovedWhileARecentlyUnusedOneStays(t *testing.T) {
	cleanup, pool, _ := newTestCleanup(t)
	accountID := addTestAccount(t, pool)
	addMediaFile(t, pool, accountID, "media_long_unused", time.Now(), true)
	addMediaFile(t, pool, accountID, "media_recently_unused", time.Now(), true)
	markMediaFileUnusedSince(t, pool, "media_long_unused", time.Now().Add(-sync.UnusedMediaDeletionDelay-time.Hour))
	markMediaFileUnusedSince(t, pool, "media_recently_unused", time.Now().Add(-sync.UnusedMediaDeletionDelay+time.Hour))
	storageBeforeRun := 4 * testObjectBytes
	if _, err := pool.Exec(t.Context(), `UPDATE accounts SET storage_used_bytes = $2 WHERE account_id = $1`, accountID, storageBeforeRun); err != nil {
		t.Fatalf("set storage used: %v", err)
	}

	if err := cleanup.QueueUnusedMediaFiles(t.Context()); err != nil {
		t.Fatal(err)
	}

	if hasMediaFile(t, pool, "media_long_unused") {
		t.Error("the file unused for over 8 days is still there")
	}
	for pieceIndex := range 2 {
		pieceKey := store.MediaPieceKey("media_long_unused", testMediaUploadID, pieceIndex)
		if deleteAfter, queued := deletionTimeOf(t, pool, pieceKey); !queued || deleteAfter > time.Now().UnixMicro() {
			t.Errorf("piece %s: queued = %v, delete after %d; want queued and ready now", pieceKey, queued, deleteAfter)
		}
	}
	if !hasMediaFile(t, pool, "media_recently_unused") {
		t.Error("the recently unused file was removed")
	}
	if _, queued := deletionTimeOf(t, pool, store.MediaPieceKey("media_recently_unused", testMediaUploadID, 0)); queued {
		t.Error("a piece of the recently unused file was queued for deletion")
	}
	var storageUsed int64
	if err := pool.QueryRow(t.Context(), `SELECT storage_used_bytes FROM accounts WHERE account_id = $1`, accountID).Scan(&storageUsed); err != nil {
		t.Fatalf("load storage used: %v", err)
	}
	if wantStorage := int64(storageBeforeRun - 2*testObjectBytes); storageUsed != wantStorage {
		t.Errorf("storage used = %d, want %d", storageUsed, wantStorage)
	}
}

func newTestCleanup(t *testing.T) (*Cleanup, *pgxpool.Pool, *testutil.FakeBlobStore) {
	t.Helper()

	pool := testutil.OpenThrowawayDatabase(t)
	if err := store.ApplyMigrations(pool, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	blobStore := &testutil.FakeBlobStore{}
	return NewCleanup(pool, blobStore), pool, blobStore
}

func queueObjectDeletion(t *testing.T, pool *pgxpool.Pool, objectKey string, deleteAfter time.Time) {
	t.Helper()

	_, err := pool.Exec(t.Context(), `INSERT INTO object_deletions (object_key, delete_after) VALUES ($1, $2)`, objectKey, deleteAfter.UnixMicro())
	if err != nil {
		t.Fatalf("queue object deletion: %v", err)
	}
}

func addPendingUpload(t *testing.T, pool *pgxpool.Pool, objectKey string, expiresAt time.Time) {
	t.Helper()

	err := store.NewSpaceTable(pool).AddPendingUpload(t.Context(), store.PendingUpload{
		ObjectKey: objectKey,
		AccountID: 1,
		SpaceRef:  1,
		ItemID:    "note",
		SizeBytes: testObjectBytes,
		ExpiresAt: expiresAt.UnixMicro(),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func deletionTimeOf(t *testing.T, pool *pgxpool.Pool, objectKey string) (int64, bool) {
	t.Helper()

	var deleteAfter int64
	err := pool.QueryRow(t.Context(), `SELECT delete_after FROM object_deletions WHERE object_key = $1`, objectKey).Scan(&deleteAfter)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false
	}
	if err != nil {
		t.Fatalf("load object deletion: %v", err)
	}
	return deleteAfter, true
}

func isPendingUpload(t *testing.T, pool *pgxpool.Pool, objectKey string) bool {
	t.Helper()

	var pendingCount int
	err := pool.QueryRow(t.Context(), `SELECT count(*) FROM pending_uploads WHERE object_key = $1`, objectKey).Scan(&pendingCount)
	if err != nil {
		t.Fatalf("count pending uploads: %v", err)
	}
	return pendingCount > 0
}

func addTestAccount(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()

	var accountID int64
	err := pool.QueryRow(t.Context(),
		`INSERT INTO accounts (email_hash, encrypted_email, encrypted_email_nonce) VALUES ('email-hash', 'email', 'nonce') RETURNING account_id`,
	).Scan(&accountID)
	if err != nil {
		t.Fatalf("insert account: %v", err)
	}
	return accountID
}

func addMediaFile(t *testing.T, pool *pgxpool.Pool, accountID int64, mediaID string, expiresAt time.Time, committed bool) {
	t.Helper()

	var committedAt *int64
	if committed {
		committedAtMicroseconds := time.Now().UnixMicro()
		committedAt = &committedAtMicroseconds
	}
	_, err := pool.Exec(t.Context(),
		`INSERT INTO media_files (media_id, account_id, upload_id, piece_sizes, size_bytes, expires_at, committed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		mediaID, accountID, testMediaUploadID, []int64{testObjectBytes, testObjectBytes}, 2*testObjectBytes, expiresAt.UnixMicro(), committedAt,
	)
	if err != nil {
		t.Fatalf("insert media file: %v", err)
	}
}

func hasMediaFile(t *testing.T, pool *pgxpool.Pool, mediaID string) bool {
	t.Helper()

	var mediaCount int
	err := pool.QueryRow(t.Context(), `SELECT count(*) FROM media_files WHERE media_id = $1`, mediaID).Scan(&mediaCount)
	if err != nil {
		t.Fatalf("count media files: %v", err)
	}
	return mediaCount > 0
}

func markMediaFileUnusedSince(t *testing.T, pool *pgxpool.Pool, mediaID string, unusedSince time.Time) {
	t.Helper()

	if _, err := pool.Exec(t.Context(), `UPDATE media_files SET unused_since = $2 WHERE media_id = $1`, mediaID, unusedSince.UnixMicro()); err != nil {
		t.Fatalf("mark media file unused: %v", err)
	}
}
