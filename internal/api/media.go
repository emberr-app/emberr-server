package api

import (
	"fmt"
	"net/http"

	"github.com/emberr-app/emberr-server/internal/sync"
)

const (
	mediaPath             = "/v1/media/{mediaId}"
	mediaUploadURLsPath   = mediaPath + "/upload-urls"
	mediaDownloadURLsPath = mediaPath + "/download-urls"
	maxMediaPieceCount    = 10000
)

type mediaUploadURLsRequest struct {
	PieceSizes []int64 `json:"pieceSizes"`
}

type mediaUploadURLsResponse struct {
	Committed bool                       `json:"committed"`
	Pieces    []sync.MediaPieceUploadURL `json:"pieces"`
	ExpiresAt int64                      `json:"expiresAt,omitempty"`
}

type commitMediaResponse struct {
	SizeBytes int64 `json:"sizeBytes"`
}

type mediaDownloadURLsResponse struct {
	Pieces    []sync.MediaPieceDownloadURL `json:"pieces"`
	ExpiresAt int64                        `json:"expiresAt"`
}

func handleCreateMediaUploadURLs(media *sync.Media) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mediaID := r.PathValue("mediaId")
		if err := checkSyncID("mediaId", mediaID); err != nil {
			writeError(w, r, err)
			return
		}
		var request mediaUploadURLsRequest
		if err := readJSON(w, r, &request); err != nil {
			writeError(w, r, err)
			return
		}
		if err := checkPieceSizes(request.PieceSizes); err != nil {
			writeError(w, r, err)
			return
		}

		uploadURLs, err := media.CreateUploadURLs(r.Context(), signedInAccountID(r.Context()), mediaID, request.PieceSizes)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, mediaUploadURLsResponse{Committed: uploadURLs.Committed, Pieces: uploadURLs.Pieces, ExpiresAt: uploadURLs.ExpiresAt})
	}
}

func handleCommitMedia(media *sync.Media) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mediaID := r.PathValue("mediaId")
		if err := checkSyncID("mediaId", mediaID); err != nil {
			writeError(w, r, err)
			return
		}

		sizeBytes, err := media.Commit(r.Context(), signedInAccountID(r.Context()), mediaID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, commitMediaResponse{SizeBytes: sizeBytes})
	}
}

func handleCreateMediaDownloadURLs(media *sync.Media) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mediaID := r.PathValue("mediaId")
		if err := checkSyncID("mediaId", mediaID); err != nil {
			writeError(w, r, err)
			return
		}

		downloadURLs, err := media.CreateDownloadURLs(r.Context(), signedInAccountID(r.Context()), mediaID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, mediaDownloadURLsResponse{Pieces: downloadURLs.Pieces, ExpiresAt: downloadURLs.ExpiresAt})
	}
}

func checkPieceSizes(pieceSizes []int64) error {
	if len(pieceSizes) < 1 || len(pieceSizes) > maxMediaPieceCount {
		return newBadRequestError(fmt.Sprintf("pieceSizes must hold 1 to %d sizes", maxMediaPieceCount))
	}
	var totalBytes int64
	for _, pieceSize := range pieceSizes {
		if pieceSize < 1 {
			return newBadRequestError("every piece size must be 1 or more")
		}
		if pieceSize > sync.MaxMediaBytes-totalBytes {
			return errMediaTooLarge
		}
		totalBytes += pieceSize
	}
	return nil
}
