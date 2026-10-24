package api

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/emberr-app/emberr-server/internal/auth"
	"github.com/emberr-app/emberr-server/internal/store"
)

const testPayloadBytes = 300

func TestCommitWithAStaleBaseRevisionGetsTheCurrentItemAndChangesNothing(t *testing.T) {
	server := newTestServer(t)
	account := server.signUp(t)
	space := server.registerSpace(t, account.token, rand.Text(), randomBase64(lockedKeyLength))
	firstFolder := newTestFolder(rand.Text())
	firstRevision := server.commitItem(t, account.token, space.SpaceRef, "folder", firstFolder)

	staleFolder := newTestFolder(rand.Text())
	staleFolder.BaseRevision = firstRevision - 1
	response := server.sendSignedIn(http.MethodPut, itemPath(space.SpaceRef, "folder"), account.token, staleFolder)

	requireErrorCode(t, response, http.StatusConflict, "CONFLICT")
	var conflict conflictResponse
	if err := json.Unmarshal(response.Body.Bytes(), &conflict); err != nil {
		t.Fatalf("read conflict answer: %v", err)
	}
	if conflict.Item == nil || conflict.Item.Revision != firstRevision || payloadOf(*conflict.Item) != firstFolder.InlinePayload {
		t.Fatalf("conflict answer %s, want the first commit's item at revision %d", response.Body, firstRevision)
	}
	page := server.changes(t, account.token, space.SpaceRef, 0, maxChangesLimit)
	if page.Latest != firstRevision || len(page.Items) != 1 || payloadOf(page.Items[0]) != firstFolder.InlinePayload {
		t.Fatalf("after the refused commit, changes = %+v, want only the first commit at revision %d", page, firstRevision)
	}
}

func TestRevisionsGoUpByOneAndChangesPageThroughThem(t *testing.T) {
	server := newTestServer(t)
	account := server.signUp(t)
	space := server.registerSpace(t, account.token, rand.Text(), randomBase64(lockedKeyLength))

	var revisions []int64
	for _, itemID := range []string{"first", "second", "third"} {
		revisions = append(revisions, server.commitItem(t, account.token, space.SpaceRef, itemID, newTestFolder(rand.Text())))
	}
	if !slices.Equal(revisions, []int64{1, 2, 3}) {
		t.Fatalf("revisions = %v, want [1 2 3]", revisions)
	}
	rewrittenFirst := newTestFolder(rand.Text())
	rewrittenFirst.BaseRevision = 1
	if revision := server.commitItem(t, account.token, space.SpaceRef, "first", rewrittenFirst); revision != 4 {
		t.Fatalf("rewriting the first item got revision %d, want 4", revision)
	}

	firstPage := server.changes(t, account.token, space.SpaceRef, 0, 2)
	if ids := itemIDs(firstPage.Items); !slices.Equal(ids, []string{"second", "third"}) || !firstPage.HasMore || firstPage.Latest != 3 {
		t.Fatalf("first page = items %v, hasMore %v, latest %d; want [second third], true, 3", ids, firstPage.HasMore, firstPage.Latest)
	}
	secondPage := server.changes(t, account.token, space.SpaceRef, firstPage.Latest, 2)
	if ids := itemIDs(secondPage.Items); !slices.Equal(ids, []string{"first"}) || secondPage.HasMore || secondPage.Latest != 4 {
		t.Fatalf("second page = items %v, hasMore %v, latest %d; want [first], false, 4", ids, secondPage.HasMore, secondPage.Latest)
	}
}

func TestAnotherAccountGetsNotFoundForASpaceItDoesNotOwn(t *testing.T) {
	server := newTestServer(t)
	owner := server.signUp(t)
	stranger := server.signUp(t)
	space := server.registerSpace(t, owner.token, rand.Text(), randomBase64(lockedKeyLength))
	revision := server.commitItem(t, owner.token, space.SpaceRef, "folder", newTestFolder(rand.Text()))
	strangerCommit := newTestFolder(rand.Text())
	strangerCommit.BaseRevision = revision

	readResponse := server.sendSignedIn(http.MethodGet, changesPath(space.SpaceRef, 0, maxChangesLimit), stranger.token, nil)
	requireErrorCode(t, readResponse, http.StatusNotFound, "NOT_FOUND")
	commitResponse := server.sendSignedIn(http.MethodPut, itemPath(space.SpaceRef, "folder"), stranger.token, strangerCommit)
	requireErrorCode(t, commitResponse, http.StatusNotFound, "NOT_FOUND")
	deleteResponse := server.sendSignedIn(http.MethodDelete, itemPath(space.SpaceRef, "folder"), stranger.token, deleteItemRequest{BaseRevision: revision})
	requireErrorCode(t, deleteResponse, http.StatusNotFound, "NOT_FOUND")

	page := server.changes(t, owner.token, space.SpaceRef, 0, maxChangesLimit)
	if page.Latest != revision || len(page.Items) != 1 || page.Items[0].Deleted {
		t.Fatalf("the owner's space changed after another account's requests: %+v", page)
	}
}

func TestRegisteringTheSameClientSpaceIDTwiceKeepsTheFirstKey(t *testing.T) {
	server := newTestServer(t)
	account := server.signUp(t)
	clientSpaceID := rand.Text()
	firstKey := randomBase64(lockedKeyLength)

	first := server.registerSpace(t, account.token, clientSpaceID, firstKey)
	second := server.registerSpace(t, account.token, clientSpaceID, randomBase64(lockedKeyLength))

	if second != first || second.EncryptedKey != firstKey {
		t.Fatalf("second registration = %+v, want the first one %+v with its key", second, first)
	}
	response := server.sendSignedIn(http.MethodGet, syncStatePath, account.token, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("sync state: status = %d %s, want 200", response.Code, response.Body)
	}
	var state syncStateResponse
	if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
		t.Fatalf("read sync state: %v", err)
	}
	if len(state.Spaces) != 1 || state.Spaces[0].SpaceRef != first.SpaceRef || state.Spaces[0].EncryptedKey != firstKey {
		t.Fatalf("sync state = %s, want one space %d with the first key", response.Body, first.SpaceRef)
	}
}

func TestChangesAsksForAResyncOnlyWhenSinceIsBeforeThePrunedRevision(t *testing.T) {
	server := newTestServer(t)
	account := server.signUp(t)
	space := server.registerSpace(t, account.token, rand.Text(), randomBase64(lockedKeyLength))
	for _, itemID := range []string{"first", "second", "third"} {
		server.commitItem(t, account.token, space.SpaceRef, itemID, newTestFolder(rand.Text()))
	}
	if _, err := server.pool.Exec(t.Context(), `UPDATE spaces SET pruned_revision = 2 WHERE space_ref = $1`, space.SpaceRef); err != nil {
		t.Fatalf("set pruned revision: %v", err)
	}

	response := server.sendSignedIn(http.MethodGet, changesPath(space.SpaceRef, 1, maxChangesLimit), account.token, nil)
	var resync resyncRequiredResponse
	json.Unmarshal(response.Body.Bytes(), &resync)
	if response.Code != http.StatusOK || !resync.ResyncRequired || resync.Latest != 3 {
		t.Fatalf("changes since 1 = %d %s, want 200 with resyncRequired and latest 3", response.Code, response.Body)
	}
	for _, sinceRevision := range []int64{0, 2} {
		page := server.changes(t, account.token, space.SpaceRef, sinceRevision, maxChangesLimit)
		if wantCount := 3 - int(sinceRevision); len(page.Items) != wantCount || page.Latest != 3 {
			t.Fatalf("changes since %d = %+v, want %d items and latest 3", sinceRevision, page, wantCount)
		}
	}
}

func TestDeletedItemComesBackAsATombstoneAndFreesItsStorage(t *testing.T) {
	server := newTestServer(t)
	account := server.signUp(t)
	space := server.registerSpace(t, account.token, rand.Text(), randomBase64(lockedKeyLength))
	revision := server.commitItem(t, account.token, space.SpaceRef, "folder", newTestFolder(rand.Text()))
	if used := server.storageUsed(t, account.token); used != testPayloadBytes {
		t.Fatalf("storage used after the commit = %d, want %d", used, testPayloadBytes)
	}

	response := server.sendSignedIn(http.MethodDelete, itemPath(space.SpaceRef, "folder"), account.token, deleteItemRequest{BaseRevision: revision})
	if response.Code != http.StatusOK {
		t.Fatalf("delete item: status = %d %s, want 200", response.Code, response.Body)
	}

	page := server.changes(t, account.token, space.SpaceRef, revision, maxChangesLimit)
	if len(page.Items) != 1 {
		t.Fatalf("changes after the delete = %+v, want one tombstone", page)
	}
	tombstone := page.Items[0]
	if !tombstone.Deleted || tombstone.Revision != revision+1 || tombstone.SizeBytes != 0 || tombstone.ParentID != nil ||
		tombstone.EncryptedKey != nil || tombstone.EncryptedKeyNonce != nil || tombstone.InlinePayload != nil || tombstone.InlinePayloadNonce != nil {
		t.Fatalf("tombstone = %+v, want deleted at revision %d with no size, parent, key or payload", tombstone, revision+1)
	}
	if used := server.storageUsed(t, account.token); used != 0 {
		t.Fatalf("storage used after the delete = %d, want 0", used)
	}
}

func (server testServer) registerSpace(t *testing.T, token string, clientSpaceID string, encryptedKey string) registerSpaceResponse {
	t.Helper()

	response := server.sendSignedIn(http.MethodPost, spacesPath, token, registerSpaceRequest{
		ClientSpaceID:     clientSpaceID,
		Kind:              spaceKindSpace,
		EncryptedKey:      encryptedKey,
		EncryptedKeyNonce: randomBase64(keyNonceLength),
	})
	if response.Code != http.StatusOK {
		t.Fatalf("register space: status = %d %s, want 200", response.Code, response.Body)
	}
	var body registerSpaceResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("read register space answer: %v", err)
	}
	return body
}

func (server testServer) commitItem(t *testing.T, token string, spaceRef int64, itemID string, request commitItemRequest) int64 {
	t.Helper()

	response := server.sendSignedIn(http.MethodPut, itemPath(spaceRef, itemID), token, request)
	if response.Code != http.StatusOK {
		t.Fatalf("commit %s: status = %d %s, want 200", itemID, response.Code, response.Body)
	}
	var body revisionResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("read commit answer: %v", err)
	}
	return body.Revision
}

func (server testServer) changes(t *testing.T, token string, spaceRef int64, sinceRevision int64, limit int) changesResponse {
	t.Helper()

	response := server.sendSignedIn(http.MethodGet, changesPath(spaceRef, sinceRevision, limit), token, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("changes: status = %d %s, want 200", response.Code, response.Body)
	}
	var body changesResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("read changes answer: %v", err)
	}
	return body
}

func (server testServer) storageUsed(t *testing.T, token string) int64 {
	t.Helper()

	response := server.sendSignedIn(http.MethodGet, accountPath, token, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("account details: status = %d %s, want 200", response.Code, response.Body)
	}
	var details auth.AccountDetails
	if err := json.Unmarshal(response.Body.Bytes(), &details); err != nil {
		t.Fatalf("read account details: %v", err)
	}
	return details.StorageUsedBytes
}

func newTestFolder(parentID string) commitItemRequest {
	return commitItemRequest{
		ItemType:           "FOLDER",
		PayloadVersion:     1,
		ParentID:           parentID,
		EncryptedKey:       randomBase64(lockedKeyLength),
		EncryptedKeyNonce:  randomBase64(keyNonceLength),
		InlinePayload:      randomBase64(testPayloadBytes),
		InlinePayloadNonce: randomBase64(keyNonceLength),
	}
}

func itemPath(spaceRef int64, itemID string) string {
	return fmt.Sprintf("%s/%d/items/%s", spacesPath, spaceRef, itemID)
}

func changesPath(spaceRef int64, sinceRevision int64, limit int) string {
	return fmt.Sprintf("%s/%d/changes?since=%d&limit=%d", spacesPath, spaceRef, sinceRevision, limit)
}

func payloadOf(item store.Item) string {
	if item.InlinePayload == nil {
		return ""
	}
	return *item.InlinePayload
}

func itemIDs(items []store.Item) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ItemID)
	}
	return ids
}
