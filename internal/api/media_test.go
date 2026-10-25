package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/emberr-app/emberr-server/internal/sync"
	"github.com/emberr-app/emberr-server/internal/testutil"
)

var testPieceSizes = []int64{100, 200, 300}

func TestSecondUploadURLsRequestAnswersOnlyThePiecesNotYetStored(t *testing.T) {
	server := newTestServer(t)
	account := server.signUp(t)
	mediaID := "media_" + rand.Text()
	firstAnswer := server.requestMediaUploadURLs(t, account.token, mediaID, testPieceSizes)
	if firstAnswer.Committed || len(firstAnswer.Pieces) != 3 {
		t.Fatalf("first upload-urls = %+v, want 3 pieces", firstAnswer)
	}
	server.blobStore.PutObject(testutil.FakeObjectKeyOf(firstAnswer.Pieces[0].UploadURL), testPieceSizes[0])

	secondAnswer := server.requestMediaUploadURLs(t, account.token, mediaID, testPieceSizes)

	if secondAnswer.Committed || len(secondAnswer.Pieces) != 2 || secondAnswer.Pieces[0].Index != 1 || secondAnswer.Pieces[1].Index != 2 {
		t.Fatalf("second upload-urls = %+v, want pieces 1 and 2 only", secondAnswer)
	}
	for index, piece := range secondAnswer.Pieces {
		if piece.UploadURL != firstAnswer.Pieces[index+1].UploadURL {
			t.Errorf("piece %d URL changed to %s, want the same upload kept", piece.Index, piece.UploadURL)
		}
	}
}

func TestMediaCommitWithAMissingOrWrongSizePieceIsRefusedAndCountsNothing(t *testing.T) {
	server := newTestServer(t)
	account := server.signUp(t)
	mediaID := "media_" + rand.Text()
	answer := server.requestMediaUploadURLs(t, account.token, mediaID, testPieceSizes[:2])
	server.blobStore.PutObject(testutil.FakeObjectKeyOf(answer.Pieces[0].UploadURL), testPieceSizes[0])

	missingPieceResponse := server.sendSignedIn(http.MethodPut, mediaFilePath(mediaID), account.token, nil)
	server.blobStore.PutObject(testutil.FakeObjectKeyOf(answer.Pieces[1].UploadURL), testPieceSizes[1]-1)
	wrongSizeResponse := server.sendSignedIn(http.MethodPut, mediaFilePath(mediaID), account.token, nil)

	requireErrorCode(t, missingPieceResponse, http.StatusBadRequest, "BAD_REQUEST")
	requireErrorCode(t, wrongSizeResponse, http.StatusBadRequest, "BAD_REQUEST")
	if used := server.storageUsed(t, account.token); used != 0 {
		t.Fatalf("storage used after the refused commits = %d, want 0", used)
	}
}

func TestMediaCommitCountsTheFileSizeOnce(t *testing.T) {
	server := newTestServer(t)
	account := server.signUp(t)
	mediaID := "media_" + rand.Text()
	server.uploadMediaPieces(t, account.token, mediaID, testPieceSizes)
	wantSize := testPieceSizes[0] + testPieceSizes[1] + testPieceSizes[2]

	for range 2 {
		if sizeBytes := server.commitMedia(t, account.token, mediaID); sizeBytes != wantSize {
			t.Fatalf("commit answered sizeBytes %d, want %d", sizeBytes, wantSize)
		}
	}
	answerAfterCommit := server.requestMediaUploadURLs(t, account.token, mediaID, testPieceSizes)

	if !answerAfterCommit.Committed || answerAfterCommit.Pieces == nil || len(answerAfterCommit.Pieces) != 0 {
		t.Fatalf("upload-urls after the commit = %+v, want committed with no pieces", answerAfterCommit)
	}
	if used := server.storageUsed(t, account.token); used != wantSize {
		t.Fatalf("storage used = %d, want %d counted once", used, wantSize)
	}
}

func TestAnotherAccountsMediaIDAnswersNotFound(t *testing.T) {
	server := newTestServer(t)
	owner := server.signUp(t)
	stranger := server.signUp(t)
	mediaID := "media_" + rand.Text()
	server.uploadMediaPieces(t, owner.token, mediaID, testPieceSizes)

	uploadURLsResponse := server.sendSignedIn(http.MethodPost, mediaFilePath(mediaID)+"/upload-urls", stranger.token, mediaUploadURLsRequest{PieceSizes: testPieceSizes})
	commitResponse := server.sendSignedIn(http.MethodPut, mediaFilePath(mediaID), stranger.token, nil)

	requireErrorCode(t, uploadURLsResponse, http.StatusNotFound, "NOT_FOUND")
	requireErrorCode(t, commitResponse, http.StatusNotFound, "NOT_FOUND")
	server.commitMedia(t, owner.token, mediaID)
}

func TestEraseQueuesEveryMediaPieceNowAndRemovesTheMediaRows(t *testing.T) {
	server := newTestServer(t)
	account := server.signUp(t)
	committedMediaID, unfinishedMediaID := "media_"+rand.Text(), "media_"+rand.Text()
	committedPieces := server.uploadMediaPieces(t, account.token, committedMediaID, testPieceSizes)
	server.commitMedia(t, account.token, committedMediaID)
	unfinishedPieces := server.requestMediaUploadURLs(t, account.token, unfinishedMediaID, testPieceSizes[:1]).Pieces

	server.eraseAccountData(t, account)

	for _, piece := range append(committedPieces, unfinishedPieces...) {
		objectKey := testutil.FakeObjectKeyOf(piece.UploadURL)
		deleteAfter, queued := server.deletionTimeOf(t, objectKey)
		if !queued || deleteAfter > time.Now().Add(time.Minute).UnixMicro() {
			t.Errorf("piece %s: queued = %v, delete after %d; want queued and ready now", objectKey, queued, deleteAfter)
		}
	}
	var remainingMediaRows int
	err := server.pool.QueryRow(t.Context(), `SELECT count(*) FROM media_files WHERE media_id = ANY($1)`, []string{committedMediaID, unfinishedMediaID}).Scan(&remainingMediaRows)
	if err != nil {
		t.Fatalf("count media rows: %v", err)
	}
	if remainingMediaRows != 0 {
		t.Errorf("%d media rows remain after the erase, want 0", remainingMediaRows)
	}
	if used := server.storageUsed(t, account.token); used != 0 {
		t.Fatalf("storage used after the erase = %d, want 0", used)
	}
}

func (server testServer) requestMediaUploadURLs(t *testing.T, token string, mediaID string, pieceSizes []int64) mediaUploadURLsResponse {
	t.Helper()

	t.Cleanup(func() {
		server.pool.Exec(context.Background(), `DELETE FROM object_deletions WHERE starts_with(object_key, $1)`, "media/"+mediaID+"/")
	})
	response := server.sendSignedIn(http.MethodPost, mediaFilePath(mediaID)+"/upload-urls", token, mediaUploadURLsRequest{PieceSizes: pieceSizes})
	if response.Code != http.StatusOK {
		t.Fatalf("request media upload URLs: status = %d %s, want 200", response.Code, response.Body)
	}
	var body mediaUploadURLsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("read media upload URLs answer: %v", err)
	}
	return body
}

func (server testServer) uploadMediaPieces(t *testing.T, token string, mediaID string, pieceSizes []int64) []sync.MediaPieceUploadURL {
	t.Helper()

	pieces := server.requestMediaUploadURLs(t, token, mediaID, pieceSizes).Pieces
	for _, piece := range pieces {
		server.blobStore.PutObject(testutil.FakeObjectKeyOf(piece.UploadURL), pieceSizes[piece.Index])
	}
	return pieces
}

func (server testServer) commitMedia(t *testing.T, token string, mediaID string) int64 {
	t.Helper()

	response := server.sendSignedIn(http.MethodPut, mediaFilePath(mediaID), token, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("commit media: status = %d %s, want 200", response.Code, response.Body)
	}
	var body commitMediaResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("read commit media answer: %v", err)
	}
	return body.SizeBytes
}

func mediaFilePath(mediaID string) string {
	return "/v1/media/" + mediaID
}
