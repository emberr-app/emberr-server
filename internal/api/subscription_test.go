package api

import (
	"crypto/rand"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/emberr-app/emberr-server/internal/auth"
	"github.com/emberr-app/emberr-server/internal/store"
	"github.com/emberr-app/emberr-server/internal/sync"
)

const (
	testTrialStorageBytes = 100 << 20
	testTrialLength       = 14 * 24 * time.Hour
	testStorageLimitBytes = 1000
)

func TestSignupStartsATrialThatTheAccountDetailsShow(t *testing.T) {
	server := newTestServer(t)
	account := server.signUp(t)

	subscription := server.accountDetails(t, account.token).Subscription

	wantExpiresAt := time.Now().Add(testTrialLength)
	if subscription.Plan != store.TrialPlan || subscription.StorageLimitBytes != testTrialStorageBytes || subscription.CloudCopyRemovedAt != nil {
		t.Fatalf("subscription = %+v, want a %d byte trial not removed", subscription, testTrialStorageBytes)
	}
	if difference := time.UnixMicro(subscription.ExpiresAt).Sub(wantExpiresAt).Abs(); difference > time.Minute {
		t.Fatalf("trial ends %s, want about %s", time.UnixMicro(subscription.ExpiresAt), wantExpiresAt)
	}
}

func TestUploadsOverTheLimitAndItsOverflowAreRefusedWhileACommittedFileStillAnswers(t *testing.T) {
	server := newTestServer(t)
	account := server.signUp(t)
	space := server.registerSpace(t, account.token, rand.Text(), randomBase64(lockedKeyLength))
	committedMediaID := server.addCommittedMedia(t, account.token)
	server.setStorageLimit(t, account, testStorageLimitBytes)
	server.setStorageUsed(t, account, testStorageLimitBytes+sync.StorageOverflowAboveLimit-100)

	server.requestUploadURL(t, account.token, space.SpaceRef, "note-at-overflow", 100)
	server.requestMediaUploadURLs(t, account.token, "media_"+rand.Text(), []int64{100})
	itemOverTheOverflow := server.sendSignedIn(http.MethodPost, itemPath(space.SpaceRef, "note-over-overflow")+"/upload-urls", account.token, uploadURLRequest{SizeBytes: 101})
	mediaOverTheOverflow := server.sendSignedIn(http.MethodPost, mediaFilePath("media_"+rand.Text())+"/upload-urls", account.token, mediaUploadURLsRequest{PieceSizes: []int64{101}})
	committedMedia := server.requestMediaUploadURLs(t, account.token, committedMediaID, testPieceSizes)

	requireErrorCode(t, itemOverTheOverflow, http.StatusUpgradeRequired, "STORAGE_LIMIT_EXCEEDED")
	requireErrorCode(t, mediaOverTheOverflow, http.StatusUpgradeRequired, "STORAGE_LIMIT_EXCEEDED")
	if !committedMedia.Committed || len(committedMedia.Pieces) != 0 {
		t.Fatalf("upload-urls for the committed file over the limit = %+v, want committed with no pieces", committedMedia)
	}
}

func TestAfterTheTrialEndsUploadsAreRefusedWhileReadsAndDeletesStillWork(t *testing.T) {
	server := newTestServer(t)
	account := server.signUp(t)
	clientSpaceID := rand.Text()
	space := server.registerSpace(t, account.token, clientSpaceID, randomBase64(lockedKeyLength))
	folderRevision := server.commitItem(t, account.token, space.SpaceRef, "folder", newTestFolder(rand.Text()))
	server.commitTestNote(t, account.token, space.SpaceRef, "note")
	mediaID := server.addCommittedMedia(t, account.token)
	server.addMediaLock(t, account.token, space.SpaceRef, mediaID, "note", newTestMediaLock())
	server.endTrial(t, account)

	changedFolder := newTestFolder(rand.Text())
	changedFolder.BaseRevision = folderRevision
	refusedResponses := map[string]int{
		"item commit":       server.sendSignedIn(http.MethodPut, itemPath(space.SpaceRef, "folder"), account.token, changedFolder).Code,
		"item upload-urls":  server.sendSignedIn(http.MethodPost, itemPath(space.SpaceRef, "new-note")+"/upload-urls", account.token, uploadURLRequest{SizeBytes: 1}).Code,
		"media upload-urls": server.sendSignedIn(http.MethodPost, mediaFilePath("media_"+rand.Text())+"/upload-urls", account.token, mediaUploadURLsRequest{PieceSizes: []int64{1}}).Code,
		"lock add":          server.sendSignedIn(http.MethodPut, mediaLockPathFor(space.SpaceRef, mediaID, "note"), account.token, newTestMediaLock()).Code,
		"new space":         server.sendSignedIn(http.MethodPost, spacesPath, account.token, registerSpaceRequest{ClientSpaceID: rand.Text(), Kind: spaceKindSpace, EncryptedKey: randomBase64(lockedKeyLength), EncryptedKeyNonce: randomBase64(keyNonceLength)}).Code,
	}
	for action, statusCode := range refusedResponses {
		if statusCode != http.StatusPaymentRequired {
			t.Errorf("%s after the trial ended answered %d, want 402", action, statusCode)
		}
	}

	server.changes(t, account.token, space.SpaceRef, 0, maxChangesLimit)
	if downloadResponse := server.sendSignedIn(http.MethodPost, mediaFilePath(mediaID)+"/download-urls", account.token, nil); downloadResponse.Code != http.StatusOK {
		t.Errorf("media download-urls after the trial ended: status = %d %s, want 200", downloadResponse.Code, downloadResponse.Body)
	}
	server.removeMediaLock(t, account.token, space.SpaceRef, mediaID, "note")
	server.requireRevision(t, "delete folder", server.sendSignedIn(http.MethodDelete, itemPath(space.SpaceRef, "folder"), account.token, deleteItemRequest{BaseRevision: folderRevision}))
	if existingSpace := server.registerSpace(t, account.token, clientSpaceID, randomBase64(lockedKeyLength)); existingSpace.SpaceRef != space.SpaceRef {
		t.Errorf("registering the existing space answered spaceRef %d, want %d", existingSpace.SpaceRef, space.SpaceRef)
	}
}

func (server testServer) accountDetails(t *testing.T, token string) auth.AccountDetails {
	t.Helper()

	response := server.sendSignedIn(http.MethodGet, accountPath, token, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("account details: status = %d %s, want 200", response.Code, response.Body)
	}
	var details auth.AccountDetails
	if err := json.Unmarshal(response.Body.Bytes(), &details); err != nil {
		t.Fatalf("read account details: %v", err)
	}
	return details
}

func (server testServer) setStorageLimit(t *testing.T, account testAccount, storageLimitBytes int64) {
	t.Helper()

	_, err := server.pool.Exec(t.Context(), `UPDATE subscriptions SET storage_limit_bytes = $2 WHERE account_id = $1`, server.accountID(t, account.email), storageLimitBytes)
	if err != nil {
		t.Fatalf("set storage limit: %v", err)
	}
}

func (server testServer) setStorageUsed(t *testing.T, account testAccount, storageUsedBytes int64) {
	t.Helper()

	_, err := server.pool.Exec(t.Context(), `UPDATE accounts SET storage_used_bytes = $2 WHERE account_id = $1`, server.accountID(t, account.email), storageUsedBytes)
	if err != nil {
		t.Fatalf("set storage used: %v", err)
	}
}

func (server testServer) endTrial(t *testing.T, account testAccount) {
	t.Helper()

	_, err := server.pool.Exec(t.Context(), `UPDATE subscriptions SET expires_at = $2 WHERE account_id = $1`, server.accountID(t, account.email), time.Now().Add(-time.Hour).UnixMicro())
	if err != nil {
		t.Fatalf("end trial: %v", err)
	}
}
