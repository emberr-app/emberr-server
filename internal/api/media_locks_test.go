package api

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/emberr-app/emberr-server/internal/testutil"
)

func TestMediaLockTakesTheNextRevisionAndChangesPageThroughItemsAndLocksInOrder(t *testing.T) {
	server := newTestServer(t)
	account := server.signUp(t)
	space := server.registerSpace(t, account.token, rand.Text(), randomBase64(lockedKeyLength))
	mediaID := server.addCommittedMedia(t, account.token)

	noteRevision := server.commitTestNote(t, account.token, space.SpaceRef, "note")
	lockRevision := server.addMediaLock(t, account.token, space.SpaceRef, mediaID, "note", newTestMediaLock())
	folderRevision := server.commitItem(t, account.token, space.SpaceRef, "folder", newTestFolder(rand.Text()))

	if !slices.Equal([]int64{noteRevision, lockRevision, folderRevision}, []int64{1, 2, 3}) {
		t.Fatalf("revisions = note %d, lock %d, folder %d; want 1, 2, 3", noteRevision, lockRevision, folderRevision)
	}
	firstPage := server.changes(t, account.token, space.SpaceRef, 0, 1)
	if ids := itemIDs(firstPage.Items); !slices.Equal(ids, []string{"note"}) || len(firstPage.MediaLocks) != 0 || !firstPage.HasMore || firstPage.Latest != 1 {
		t.Fatalf("first page = %+v, want only the note, hasMore and latest 1", firstPage)
	}
	secondPage := server.changes(t, account.token, space.SpaceRef, firstPage.Latest, 1)
	if len(secondPage.Items) != 0 || len(secondPage.MediaLocks) != 1 || secondPage.MediaLocks[0].MediaID != mediaID || !secondPage.HasMore || secondPage.Latest != 2 {
		t.Fatalf("second page = %+v, want only the lock, hasMore and latest 2", secondPage)
	}
	thirdPage := server.changes(t, account.token, space.SpaceRef, secondPage.Latest, 1)
	if ids := itemIDs(thirdPage.Items); !slices.Equal(ids, []string{"folder"}) || len(thirdPage.MediaLocks) != 0 || thirdPage.HasMore || thirdPage.Latest != 3 {
		t.Fatalf("third page = %+v, want only the folder, no more and latest 3", thirdPage)
	}
}

func TestAddingALiveLockAgainKeepsItsRevisionAndRemovingItMakesATombstone(t *testing.T) {
	server := newTestServer(t)
	account := server.signUp(t)
	space := server.registerSpace(t, account.token, rand.Text(), randomBase64(lockedKeyLength))
	mediaID := server.addCommittedMedia(t, account.token)
	server.commitTestNote(t, account.token, space.SpaceRef, "note")
	firstLock := newTestMediaLock()
	lockRevision := server.addMediaLock(t, account.token, space.SpaceRef, mediaID, "note", firstLock)

	if revision := server.addMediaLock(t, account.token, space.SpaceRef, mediaID, "note", newTestMediaLock()); revision != lockRevision {
		t.Fatalf("adding the lock again answered revision %d, want %d", revision, lockRevision)
	}
	livePage := server.changes(t, account.token, space.SpaceRef, 0, maxChangesLimit)
	if livePage.Latest != lockRevision || len(livePage.MediaLocks) != 1 || *livePage.MediaLocks[0].EncryptedKey != firstLock.EncryptedKey {
		t.Fatalf("changes after adding twice = %+v, want one lock with the first key at revision %d", livePage, lockRevision)
	}

	tombstoneRevision := server.removeMediaLock(t, account.token, space.SpaceRef, mediaID, "note")
	if revision := server.removeMediaLock(t, account.token, space.SpaceRef, mediaID, "note"); revision != tombstoneRevision {
		t.Fatalf("removing the lock again answered revision %d, want %d", revision, tombstoneRevision)
	}
	page := server.changes(t, account.token, space.SpaceRef, lockRevision, maxChangesLimit)
	if len(page.MediaLocks) != 1 || tombstoneRevision != lockRevision+1 {
		t.Fatalf("changes after the remove = %+v, want one tombstone at revision %d", page, lockRevision+1)
	}
	tombstone := page.MediaLocks[0]
	if !tombstone.Deleted || tombstone.Revision != tombstoneRevision || tombstone.EncryptedKey != nil || tombstone.EncryptedKeyNonce != nil {
		t.Fatalf("tombstone = %+v, want deleted at revision %d with no key", tombstone, tombstoneRevision)
	}
}

func TestFileBecomesUnusedOnlyWhenItsLastLockIsRemovedAndANewLockClearsIt(t *testing.T) {
	server := newTestServer(t)
	account := server.signUp(t)
	space := server.registerSpace(t, account.token, rand.Text(), randomBase64(lockedKeyLength))
	mediaID := server.addCommittedMedia(t, account.token)
	if server.unusedSince(t, mediaID) == nil {
		t.Fatal("a newly committed file has no unused-since time")
	}
	for _, noteID := range []string{"first-note", "second-note"} {
		server.commitTestNote(t, account.token, space.SpaceRef, noteID)
		server.addMediaLock(t, account.token, space.SpaceRef, mediaID, noteID, newTestMediaLock())
	}

	server.removeMediaLock(t, account.token, space.SpaceRef, mediaID, "first-note")
	if unusedSince := server.unusedSince(t, mediaID); unusedSince != nil {
		t.Fatalf("unused since %d while the second note still uses the file", *unusedSince)
	}
	server.removeMediaLock(t, account.token, space.SpaceRef, mediaID, "second-note")
	if server.unusedSince(t, mediaID) == nil {
		t.Fatal("the file has no unused-since time after its last lock was removed")
	}
	server.addMediaLock(t, account.token, space.SpaceRef, mediaID, "first-note", newTestMediaLock())
	if unusedSince := server.unusedSince(t, mediaID); unusedSince != nil {
		t.Fatalf("unused since %d after a new lock", *unusedSince)
	}
}

func TestDeletingANoteTombstonesItsLocksAndMarksOnlyFilesWithoutALiveLockUnused(t *testing.T) {
	server := newTestServer(t)
	account := server.signUp(t)
	space := server.registerSpace(t, account.token, rand.Text(), randomBase64(lockedKeyLength))
	onlyThisNoteMediaID, sharedMediaID := server.addCommittedMedia(t, account.token), server.addCommittedMedia(t, account.token)
	noteRevision := server.commitTestNote(t, account.token, space.SpaceRef, "note")
	server.commitTestNote(t, account.token, space.SpaceRef, "other-note")
	server.addMediaLock(t, account.token, space.SpaceRef, onlyThisNoteMediaID, "note", newTestMediaLock())
	server.addMediaLock(t, account.token, space.SpaceRef, sharedMediaID, "note", newTestMediaLock())
	latestBeforeDelete := server.addMediaLock(t, account.token, space.SpaceRef, sharedMediaID, "other-note", newTestMediaLock())

	response := server.sendSignedIn(http.MethodDelete, itemPath(space.SpaceRef, "note"), account.token, deleteItemRequest{BaseRevision: noteRevision})
	if response.Code != http.StatusOK {
		t.Fatalf("delete note: status = %d %s, want 200", response.Code, response.Body)
	}

	page := server.changes(t, account.token, space.SpaceRef, latestBeforeDelete, maxChangesLimit)
	if len(page.Items) != 1 || !page.Items[0].Deleted || page.Items[0].Revision != latestBeforeDelete+1 || len(page.MediaLocks) != 2 || page.Latest != latestBeforeDelete+3 {
		t.Fatalf("changes after the delete = %+v, want the note tombstone and two lock tombstones up to revision %d", page, latestBeforeDelete+3)
	}
	seenRevisions := map[int64]bool{page.Items[0].Revision: true}
	for _, lock := range page.MediaLocks {
		if lock.NoteID != "note" || !lock.Deleted || lock.EncryptedKey != nil || seenRevisions[lock.Revision] {
			t.Fatalf("lock = %+v, want a tombstone of the note at its own revision", lock)
		}
		seenRevisions[lock.Revision] = true
	}
	if server.unusedSince(t, onlyThisNoteMediaID) == nil {
		t.Error("the file only the deleted note used is not marked unused")
	}
	if unusedSince := server.unusedSince(t, sharedMediaID); unusedSince != nil {
		t.Errorf("the file the other note still uses is unused since %d", *unusedSince)
	}
}

func TestMediaLockOnAMissingOrDeletedNoteAnUnfinishedFileOrAnotherAccountsSpaceAnswersNotFound(t *testing.T) {
	server := newTestServer(t)
	account := server.signUp(t)
	stranger := server.signUp(t)
	space := server.registerSpace(t, account.token, rand.Text(), randomBase64(lockedKeyLength))
	mediaID := server.addCommittedMedia(t, account.token)
	unfinishedMediaID := "media_" + rand.Text()
	server.requestMediaUploadURLs(t, account.token, unfinishedMediaID, testPieceSizes)
	server.commitTestNote(t, account.token, space.SpaceRef, "note")
	deletedNoteRevision := server.commitTestNote(t, account.token, space.SpaceRef, "deleted-note")
	response := server.sendSignedIn(http.MethodDelete, itemPath(space.SpaceRef, "deleted-note"), account.token, deleteItemRequest{BaseRevision: deletedNoteRevision})
	if response.Code != http.StatusOK {
		t.Fatalf("delete note: status = %d %s, want 200", response.Code, response.Body)
	}

	refusedLocks := []struct {
		token   string
		mediaID string
		noteID  string
	}{
		{account.token, mediaID, "missing-note"},
		{account.token, mediaID, "deleted-note"},
		{account.token, unfinishedMediaID, "note"},
		{stranger.token, mediaID, "note"},
	}
	for _, refused := range refusedLocks {
		response := server.sendSignedIn(http.MethodPut, mediaLockPathFor(space.SpaceRef, refused.mediaID, refused.noteID), refused.token, newTestMediaLock())
		requireErrorCode(t, response, http.StatusNotFound, "NOT_FOUND")
	}

	if page := server.changes(t, account.token, space.SpaceRef, 0, maxChangesLimit); len(page.MediaLocks) != 0 {
		t.Fatalf("changes after the refused locks = %+v, want no locks", page)
	}
}

func TestMediaDownloadURLsGiveTheOwnerEveryPieceAndAnotherAccountNotFound(t *testing.T) {
	server := newTestServer(t)
	owner := server.signUp(t)
	stranger := server.signUp(t)
	mediaID := "media_" + rand.Text()
	uploadedPieces := server.uploadMediaPieces(t, owner.token, mediaID, testPieceSizes)
	server.commitMedia(t, owner.token, mediaID)

	response := server.sendSignedIn(http.MethodPost, mediaFilePath(mediaID)+"/download-urls", owner.token, nil)
	strangerResponse := server.sendSignedIn(http.MethodPost, mediaFilePath(mediaID)+"/download-urls", stranger.token, nil)

	if response.Code != http.StatusOK {
		t.Fatalf("download-urls: status = %d %s, want 200", response.Code, response.Body)
	}
	var body mediaDownloadURLsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("read download-urls answer: %v", err)
	}
	if len(body.Pieces) != len(testPieceSizes) || body.ExpiresAt == 0 {
		t.Fatalf("download-urls = %+v, want %d pieces and an expiry", body, len(testPieceSizes))
	}
	for index, piece := range body.Pieces {
		wantURL := testutil.FakeDownloadURL(testutil.FakeObjectKeyOf(uploadedPieces[index].UploadURL))
		if piece.Index != index || piece.SizeBytes != testPieceSizes[index] || piece.DownloadURL != wantURL {
			t.Errorf("piece %d = %+v, want size %d and URL %s", index, piece, testPieceSizes[index], wantURL)
		}
	}
	requireErrorCode(t, strangerResponse, http.StatusNotFound, "NOT_FOUND")
}

func TestEraseTurnsEveryLiveLockIntoATombstoneAndKeepsRevisionsUnique(t *testing.T) {
	server := newTestServer(t)
	account := server.signUp(t)
	space := server.registerSpace(t, account.token, rand.Text(), randomBase64(lockedKeyLength))
	mediaID := server.addCommittedMedia(t, account.token)
	for _, noteID := range []string{"first-note", "second-note"} {
		server.commitTestNote(t, account.token, space.SpaceRef, noteID)
		server.addMediaLock(t, account.token, space.SpaceRef, mediaID, noteID, newTestMediaLock())
	}

	server.eraseAccountData(t, account)

	page := server.changes(t, account.token, space.SpaceRef, 0, maxChangesLimit)
	if len(page.Items) != 2 || len(page.MediaLocks) != 2 || page.Latest != 8 {
		t.Fatalf("changes after the erase = %+v, want two notes and two locks up to revision 8", page)
	}
	seenRevisions := map[int64]bool{}
	for _, item := range page.Items {
		if !item.Deleted || item.Revision <= 4 || seenRevisions[item.Revision] {
			t.Fatalf("item = %+v, want a tombstone at its own revision above 4", item)
		}
		seenRevisions[item.Revision] = true
	}
	for _, lock := range page.MediaLocks {
		if !lock.Deleted || lock.EncryptedKey != nil || lock.EncryptedKeyNonce != nil || lock.Revision <= 4 || seenRevisions[lock.Revision] {
			t.Fatalf("lock = %+v, want a tombstone with no key at its own revision above 4", lock)
		}
		seenRevisions[lock.Revision] = true
	}
}

func (server testServer) addCommittedMedia(t *testing.T, token string) string {
	t.Helper()

	mediaID := "media_" + rand.Text()
	server.uploadMediaPieces(t, token, mediaID, testPieceSizes)
	server.commitMedia(t, token, mediaID)
	return mediaID
}

func (server testServer) commitTestNote(t *testing.T, token string, spaceRef int64, noteID string) int64 {
	t.Helper()

	upload := server.uploadBlob(t, token, spaceRef, noteID, testBlobBytes)
	return server.commitItem(t, token, spaceRef, noteID, newTestNote(upload.ObjectKey, testBlobBytes))
}

func (server testServer) addMediaLock(t *testing.T, token string, spaceRef int64, mediaID string, noteID string, request addMediaLockRequest) int64 {
	t.Helper()

	return server.requireRevision(t, "add media lock", server.sendSignedIn(http.MethodPut, mediaLockPathFor(spaceRef, mediaID, noteID), token, request))
}

func (server testServer) removeMediaLock(t *testing.T, token string, spaceRef int64, mediaID string, noteID string) int64 {
	t.Helper()

	return server.requireRevision(t, "remove media lock", server.sendSignedIn(http.MethodDelete, mediaLockPathFor(spaceRef, mediaID, noteID), token, nil))
}

func (server testServer) requireRevision(t *testing.T, action string, response *httptest.ResponseRecorder) int64 {
	t.Helper()

	if response.Code != http.StatusOK {
		t.Fatalf("%s: status = %d %s, want 200", action, response.Code, response.Body)
	}
	var body revisionResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("read %s answer: %v", action, err)
	}
	return body.Revision
}

func (server testServer) unusedSince(t *testing.T, mediaID string) *int64 {
	t.Helper()

	var unusedSince *int64
	if err := server.pool.QueryRow(t.Context(), `SELECT unused_since FROM media_files WHERE media_id = $1`, mediaID).Scan(&unusedSince); err != nil {
		t.Fatalf("load unused since: %v", err)
	}
	return unusedSince
}

func newTestMediaLock() addMediaLockRequest {
	return addMediaLockRequest{EncryptedKey: randomBase64(lockedKeyLength), EncryptedKeyNonce: randomBase64(keyNonceLength)}
}

func mediaLockPathFor(spaceRef int64, mediaID string, noteID string) string {
	return fmt.Sprintf("%s/%d/media-locks/%s/%s", spacesPath, spaceRef, mediaID, noteID)
}
