package api

import (
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/emberr-app/emberr-server/internal/auth"
	"github.com/emberr-app/emberr-server/internal/store"
)

type erasableTestData struct {
	space             registerSpaceResponse
	noteUpload        uploadURLResponse
	pendingUpload     uploadURLResponse
	deletedFolder     store.Item
	latestBeforeErase int64
}

func TestEraseWithAWrongLoginKeyChangesNothing(t *testing.T) {
	server := newTestServer(t)
	account := server.signUp(t)
	data := server.addErasableTestData(t, account.token)

	response := server.sendSignedIn(http.MethodPost, eraseAccountPath, account.token, eraseAccountDataRequest{LoginKey: randomBase64(auth.LoginKeyLength)})

	requireErrorCode(t, response, http.StatusUnauthorized, "INVALID_PASSWORD")
	page := server.changes(t, account.token, data.space.SpaceRef, 0, maxChangesLimit)
	if page.Latest != data.latestBeforeErase || len(page.Items) != 3 {
		t.Fatalf("changes after the refused erase = %+v, want 3 items up to revision %d", page, data.latestBeforeErase)
	}
	for _, item := range page.Items {
		if item.Deleted != (item.ItemID == "deleted-folder") {
			t.Fatalf("item %s deleted = %v after the refused erase", item.ItemID, item.Deleted)
		}
	}
	if used := server.storageUsed(t, account.token); used != testPayloadBytes+testBlobBytes {
		t.Fatalf("storage used after the refused erase = %d, want %d", used, testPayloadBytes+testBlobBytes)
	}
	for _, objectKey := range []string{data.noteUpload.ObjectKey, data.pendingUpload.ObjectKey} {
		if _, queued := server.deletionTimeOf(t, objectKey); queued {
			t.Errorf("object %s was queued for deletion by a refused erase", objectKey)
		}
	}
}

func TestEraseTurnsEveryLiveItemIntoATombstoneAndKeepsSpacesAndOtherAccounts(t *testing.T) {
	server := newTestServer(t)
	account := server.signUp(t)
	stranger := server.signUp(t)
	data := server.addErasableTestData(t, account.token)
	strangerSpace := server.registerSpace(t, stranger.token, rand.Text(), randomBase64(lockedKeyLength))
	strangerRevision := server.commitItem(t, stranger.token, strangerSpace.SpaceRef, "folder", newTestFolder(rand.Text()))
	spacesBeforeErase := server.syncState(t, account.token)

	response := server.eraseAccountData(t, account)

	if response.Body.Len() != 0 {
		t.Fatalf("erase answered %q, want an empty body", response.Body)
	}
	page := server.changes(t, account.token, data.space.SpaceRef, data.latestBeforeErase, maxChangesLimit)
	if len(page.Items) != 2 || page.Latest != data.latestBeforeErase+2 {
		t.Fatalf("changes after the erase = %+v, want the folder and the note as tombstones up to revision %d", page, data.latestBeforeErase+2)
	}
	seenRevisions := map[int64]bool{}
	for _, tombstone := range page.Items {
		if !tombstone.Deleted || tombstone.Revision <= data.latestBeforeErase || seenRevisions[tombstone.Revision] ||
			tombstone.SizeBytes != 0 || tombstone.ParentID != nil || tombstone.EncryptedKey != nil || tombstone.EncryptedKeyNonce != nil ||
			tombstone.InlinePayload != nil || tombstone.InlinePayloadNonce != nil || tombstone.BlobNonce != nil || tombstone.DownloadURL != nil {
			t.Fatalf("tombstone = %+v, want deleted at its own new revision with no size, parent, key or payload", tombstone)
		}
		seenRevisions[tombstone.Revision] = true
	}
	oldTombstone := server.changes(t, account.token, data.space.SpaceRef, 0, maxChangesLimit).Items[0]
	if oldTombstone.ItemID != "deleted-folder" || oldTombstone.Revision != data.deletedFolder.Revision || oldTombstone.UpdatedAt != data.deletedFolder.UpdatedAt {
		t.Fatalf("first item after the erase = %+v, want the old tombstone %+v unchanged", oldTombstone, data.deletedFolder)
	}

	if used := server.storageUsed(t, account.token); used != 0 {
		t.Fatalf("storage used after the erase = %d, want 0", used)
	}
	for _, objectKey := range []string{data.noteUpload.ObjectKey, data.pendingUpload.ObjectKey} {
		deleteAfter, queued := server.deletionTimeOf(t, objectKey)
		if !queued || deleteAfter > time.Now().Add(time.Minute).UnixMicro() {
			t.Errorf("object %s: queued = %v, delete after %d; want queued and ready now", objectKey, queued, deleteAfter)
		}
	}
	spacesAfterErase := server.syncState(t, account.token)
	if len(spacesAfterErase) != 1 || spacesAfterErase[0].SpaceRef != spacesBeforeErase[0].SpaceRef ||
		spacesAfterErase[0].EncryptedKey != spacesBeforeErase[0].EncryptedKey || spacesAfterErase[0].EncryptedKeyNonce != spacesBeforeErase[0].EncryptedKeyNonce {
		t.Fatalf("spaces after the erase = %+v, want %+v with the same keys", spacesAfterErase, spacesBeforeErase)
	}

	strangerPage := server.changes(t, stranger.token, strangerSpace.SpaceRef, 0, maxChangesLimit)
	if strangerPage.Latest != strangerRevision || len(strangerPage.Items) != 1 || strangerPage.Items[0].Deleted {
		t.Fatalf("another account's space changed after the erase: %+v", strangerPage)
	}
	if used := server.storageUsed(t, stranger.token); used != testPayloadBytes {
		t.Fatalf("another account's storage used after the erase = %d, want %d", used, testPayloadBytes)
	}
}

func TestAfterAnEraseAnItemCommitsAgainOnItsTombstoneRevision(t *testing.T) {
	server := newTestServer(t)
	account := server.signUp(t)
	data := server.addErasableTestData(t, account.token)
	server.eraseAccountData(t, account)
	tombstones := server.changes(t, account.token, data.space.SpaceRef, data.latestBeforeErase, maxChangesLimit).Items
	tombstoneRevisions := map[string]int64{}
	for _, tombstone := range tombstones {
		tombstoneRevisions[tombstone.ItemID] = tombstone.Revision
	}

	newUpload := server.uploadBlob(t, account.token, data.space.SpaceRef, "note", testBlobBytes)
	note := newTestNote(newUpload.ObjectKey, testBlobBytes)
	note.BaseRevision = tombstoneRevisions["note"]
	noteRevision := server.commitItem(t, account.token, data.space.SpaceRef, "note", note)

	if wantRevision := data.latestBeforeErase + 3; noteRevision != wantRevision {
		t.Fatalf("committing the note again got revision %d, want %d", noteRevision, wantRevision)
	}
	page := server.changes(t, account.token, data.space.SpaceRef, data.latestBeforeErase+2, maxChangesLimit)
	if len(page.Items) != 1 || page.Items[0].Deleted || page.Items[0].ItemID != "note" {
		t.Fatalf("changes after committing again = %+v, want the live note", page)
	}
	if used := server.storageUsed(t, account.token); used != testBlobBytes {
		t.Fatalf("storage used after committing again = %d, want %d", used, testBlobBytes)
	}
}

func (server testServer) addErasableTestData(t *testing.T, token string) erasableTestData {
	t.Helper()

	space := server.registerSpace(t, token, rand.Text(), randomBase64(lockedKeyLength))
	deletedFolderRevision := server.commitItem(t, token, space.SpaceRef, "deleted-folder", newTestFolder(rand.Text()))
	response := server.sendSignedIn(http.MethodDelete, itemPath(space.SpaceRef, "deleted-folder"), token, deleteItemRequest{BaseRevision: deletedFolderRevision})
	if response.Code != http.StatusOK {
		t.Fatalf("delete folder: status = %d %s, want 200", response.Code, response.Body)
	}
	deletedFolderPage := server.changes(t, token, space.SpaceRef, deletedFolderRevision, maxChangesLimit)
	if len(deletedFolderPage.Items) != 1 || !deletedFolderPage.Items[0].Deleted {
		t.Fatalf("changes after deleting the folder = %+v, want one tombstone", deletedFolderPage)
	}
	server.commitItem(t, token, space.SpaceRef, "folder", newTestFolder(rand.Text()))
	noteUpload := server.uploadBlob(t, token, space.SpaceRef, "note", testBlobBytes)
	latestBeforeErase := server.commitItem(t, token, space.SpaceRef, "note", newTestNote(noteUpload.ObjectKey, testBlobBytes))
	pendingUpload := server.requestUploadURL(t, token, space.SpaceRef, "other-note", testBlobBytes)

	return erasableTestData{
		space:             space,
		noteUpload:        noteUpload,
		pendingUpload:     pendingUpload,
		deletedFolder:     deletedFolderPage.Items[0],
		latestBeforeErase: latestBeforeErase,
	}
}

func (server testServer) eraseAccountData(t *testing.T, account testAccount) *httptest.ResponseRecorder {
	t.Helper()

	response := server.sendSignedIn(http.MethodPost, eraseAccountPath, account.token, eraseAccountDataRequest{LoginKey: account.loginKey})
	if response.Code != http.StatusOK {
		t.Fatalf("erase account data: status = %d %s, want 200", response.Code, response.Body)
	}
	return response
}

func (server testServer) syncState(t *testing.T, token string) []store.Space {
	t.Helper()

	response := server.sendSignedIn(http.MethodGet, syncStatePath, token, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("sync state: status = %d %s, want 200", response.Code, response.Body)
	}
	var state syncStateResponse
	if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
		t.Fatalf("read sync state: %v", err)
	}
	return state.Spaces
}
