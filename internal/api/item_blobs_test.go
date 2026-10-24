package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/emberr-app/emberr-server/internal/auth"
	"github.com/emberr-app/emberr-server/internal/testutil"
)

const (
	testBlobBytes             = 500
	replacedBlobDeletionDelay = 7 * 24 * time.Hour
)

func TestCommitNamingAKeyNotHandedOutForThatItemIsRefusedAndChangesNothing(t *testing.T) {
	server := newTestServer(t)
	account := server.signUp(t)
	space := server.registerSpace(t, account.token, rand.Text(), randomBase64(lockedKeyLength))
	otherItemUpload := server.uploadBlob(t, account.token, space.SpaceRef, "other-note", testBlobBytes)
	madeUpObjectKey := fmt.Sprintf("items/%d/note/%s", space.SpaceRef, rand.Text())
	server.blobStore.PutObject(madeUpObjectKey, testBlobBytes)

	for _, objectKey := range []string{otherItemUpload.ObjectKey, madeUpObjectKey} {
		response := server.sendSignedIn(http.MethodPut, itemPath(space.SpaceRef, "note"), account.token, newTestNote(objectKey, testBlobBytes))
		requireErrorCode(t, response, http.StatusBadRequest, "BAD_REQUEST")
	}

	page := server.changes(t, account.token, space.SpaceRef, 0, maxChangesLimit)
	if page.Latest != 0 || len(page.Items) != 0 {
		t.Fatalf("after the refused commits, changes = %+v, want nothing", page)
	}
	if used := server.storageUsed(t, account.token); used != 0 {
		t.Fatalf("storage used after the refused commits = %d, want 0", used)
	}
	server.commitItem(t, account.token, space.SpaceRef, "other-note", newTestNote(otherItemUpload.ObjectKey, testBlobBytes))
}

func TestCommitWhoseStoredSizeDiffersFromSizeBytesIsRefused(t *testing.T) {
	server := newTestServer(t)
	account := server.signUp(t)
	space := server.registerSpace(t, account.token, rand.Text(), randomBase64(lockedKeyLength))
	upload := server.requestUploadURL(t, account.token, space.SpaceRef, "note", testBlobBytes)
	server.blobStore.PutObject(upload.ObjectKey, testBlobBytes-1)

	response := server.sendSignedIn(http.MethodPut, itemPath(space.SpaceRef, "note"), account.token, newTestNote(upload.ObjectKey, testBlobBytes))

	requireErrorCode(t, response, http.StatusBadRequest, "BAD_REQUEST")
	if page := server.changes(t, account.token, space.SpaceRef, 0, maxChangesLimit); len(page.Items) != 0 {
		t.Fatalf("after the refused commit, changes = %+v, want nothing", page)
	}
}

func TestReplacingABlobQueuesTheOldKeyAndDeletingTheItemQueuesTheCurrentKey(t *testing.T) {
	server := newTestServer(t)
	account := server.signUp(t)
	space := server.registerSpace(t, account.token, rand.Text(), randomBase64(lockedKeyLength))
	firstUpload := server.uploadBlob(t, account.token, space.SpaceRef, "note", testBlobBytes)
	firstRevision := server.commitItem(t, account.token, space.SpaceRef, "note", newTestNote(firstUpload.ObjectKey, testBlobBytes))
	secondUpload := server.uploadBlob(t, account.token, space.SpaceRef, "note", testBlobBytes)
	replacement := newTestNote(secondUpload.ObjectKey, testBlobBytes)
	replacement.BaseRevision = firstRevision

	secondRevision := server.commitItem(t, account.token, space.SpaceRef, "note", replacement)

	server.requireQueuedForDeletionAround(t, firstUpload.ObjectKey, time.Now().Add(replacedBlobDeletionDelay))
	if _, queued := server.deletionTimeOf(t, secondUpload.ObjectKey); queued {
		t.Fatal("the current blob was queued for deletion while the item still uses it")
	}
	page := server.changes(t, account.token, space.SpaceRef, firstRevision, maxChangesLimit)
	if len(page.Items) != 1 || page.Items[0].DownloadURL == nil || *page.Items[0].DownloadURL != testutil.FakeDownloadURL(secondUpload.ObjectKey) {
		t.Fatalf("changes after the replacement = %+v, want the item with the second blob's download URL", page)
	}

	response := server.sendSignedIn(http.MethodDelete, itemPath(space.SpaceRef, "note"), account.token, deleteItemRequest{BaseRevision: secondRevision})
	if response.Code != http.StatusOK {
		t.Fatalf("delete item: status = %d %s, want 200", response.Code, response.Body)
	}
	server.requireQueuedForDeletionAround(t, secondUpload.ObjectKey, time.Now().Add(replacedBlobDeletionDelay))
}

func TestDeletingTheAccountQueuesEveryBlobKeyAndPendingKeyAtOnce(t *testing.T) {
	server := newTestServer(t)
	account := server.signUp(t)
	space := server.registerSpace(t, account.token, rand.Text(), randomBase64(lockedKeyLength))
	committedUpload := server.uploadBlob(t, account.token, space.SpaceRef, "note", testBlobBytes)
	server.commitItem(t, account.token, space.SpaceRef, "note", newTestNote(committedUpload.ObjectKey, testBlobBytes))
	pendingUpload := server.requestUploadURL(t, account.token, space.SpaceRef, "other-note", testBlobBytes)
	server.requestAccountCode(t, account.token, auth.EmailCodeForAccountDeletion, "")

	response := server.sendSignedIn(http.MethodDelete, accountPath, account.token, deleteAccountRequest{
		LoginKey: account.loginKey, Code: server.latestCode(t, account.email),
	})
	if response.Code != http.StatusOK {
		t.Fatalf("delete account: status = %d %s, want 200", response.Code, response.Body)
	}

	for _, objectKey := range []string{committedUpload.ObjectKey, pendingUpload.ObjectKey} {
		deleteAfter, queued := server.deletionTimeOf(t, objectKey)
		if !queued || deleteAfter > time.Now().Add(time.Minute).UnixMicro() {
			t.Errorf("object %s: queued = %v, delete after %d; want queued and ready now", objectKey, queued, deleteAfter)
		}
	}
	var remainingPendingUploads int
	if err := server.pool.QueryRow(t.Context(), `SELECT count(*) FROM pending_uploads WHERE object_key = $1`, pendingUpload.ObjectKey).Scan(&remainingPendingUploads); err != nil {
		t.Fatalf("count pending uploads: %v", err)
	}
	if remainingPendingUploads != 0 {
		t.Error("the deleted account's pending upload is still there")
	}
}

func (server testServer) requestUploadURL(t *testing.T, token string, spaceRef int64, itemID string, sizeBytes int64) uploadURLResponse {
	t.Helper()

	response := server.sendSignedIn(http.MethodPost, itemPath(spaceRef, itemID)+"/upload-urls", token, uploadURLRequest{SizeBytes: sizeBytes})
	if response.Code != http.StatusOK {
		t.Fatalf("request upload URL: status = %d %s, want 200", response.Code, response.Body)
	}
	var body uploadURLResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("read upload URL answer: %v", err)
	}
	t.Cleanup(func() {
		server.pool.Exec(context.Background(), `DELETE FROM pending_uploads WHERE object_key = $1`, body.ObjectKey)
		server.pool.Exec(context.Background(), `DELETE FROM object_deletions WHERE object_key = $1`, body.ObjectKey)
	})
	return body
}

func (server testServer) uploadBlob(t *testing.T, token string, spaceRef int64, itemID string, sizeBytes int64) uploadURLResponse {
	t.Helper()

	upload := server.requestUploadURL(t, token, spaceRef, itemID, sizeBytes)
	server.blobStore.PutObject(upload.ObjectKey, sizeBytes)
	return upload
}

func (server testServer) deletionTimeOf(t *testing.T, objectKey string) (int64, bool) {
	t.Helper()

	var deleteAfter int64
	err := server.pool.QueryRow(t.Context(), `SELECT delete_after FROM object_deletions WHERE object_key = $1`, objectKey).Scan(&deleteAfter)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false
	}
	if err != nil {
		t.Fatalf("load object deletion: %v", err)
	}
	return deleteAfter, true
}

func (server testServer) requireQueuedForDeletionAround(t *testing.T, objectKey string, wantDeleteAfter time.Time) {
	t.Helper()

	deleteAfter, queued := server.deletionTimeOf(t, objectKey)
	if !queued {
		t.Fatalf("object %s is not queued for deletion", objectKey)
	}
	if difference := time.UnixMicro(deleteAfter).Sub(wantDeleteAfter).Abs(); difference > time.Minute {
		t.Fatalf("object %s may be deleted from %s, want about %s", objectKey, time.UnixMicro(deleteAfter), wantDeleteAfter)
	}
}

func newTestNote(objectKey string, sizeBytes int64) commitItemRequest {
	return commitItemRequest{
		ItemType:          "NOTE",
		PayloadVersion:    1,
		EncryptedKey:      randomBase64(lockedKeyLength),
		EncryptedKeyNonce: randomBase64(keyNonceLength),
		ObjectKey:         objectKey,
		BlobNonce:         randomBase64(keyNonceLength),
		SizeBytes:         sizeBytes,
	}
}
