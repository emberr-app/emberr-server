package jobs

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/emberr-app/emberr-server/internal/store"
	"github.com/emberr-app/emberr-server/internal/testutil"
)

const testObjectBytes = 500

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
