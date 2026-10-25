package api

import (
	"net/http"

	"github.com/emberr-app/emberr-server/internal/store"
	"github.com/emberr-app/emberr-server/internal/sync"
)

const mediaLockPath = spacesPath + "/{spaceRef}/media-locks/{mediaId}/{noteId}"

type addMediaLockRequest struct {
	EncryptedKey      string `json:"encryptedKey"`
	EncryptedKeyNonce string `json:"encryptedKeyNonce"`
}

func handleAddMediaLock(spaces *sync.Spaces) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		spaceRef, mediaID, noteID, err := mediaLockFromPath(r)
		if err != nil {
			writeError(w, r, err)
			return
		}
		var request addMediaLockRequest
		if err := readJSON(w, r, &request); err != nil {
			writeError(w, r, err)
			return
		}
		err = checkBase64Fields([]base64Field{
			{"encryptedKey", request.EncryptedKey, lockedKeyLength},
			{"encryptedKeyNonce", request.EncryptedKeyNonce, keyNonceLength},
		})
		if err != nil {
			writeError(w, r, err)
			return
		}

		revision, err := spaces.AddMediaLock(r.Context(), signedInAccountID(r.Context()), spaceRef, store.MediaLock{
			MediaID:           mediaID,
			NoteID:            noteID,
			EncryptedKey:      &request.EncryptedKey,
			EncryptedKeyNonce: &request.EncryptedKeyNonce,
		})
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, revisionResponse{Revision: revision})
	}
}

func handleRemoveMediaLock(spaces *sync.Spaces) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		spaceRef, mediaID, noteID, err := mediaLockFromPath(r)
		if err != nil {
			writeError(w, r, err)
			return
		}

		revision, err := spaces.RemoveMediaLock(r.Context(), signedInAccountID(r.Context()), spaceRef, mediaID, noteID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, revisionResponse{Revision: revision})
	}
}

func mediaLockFromPath(r *http.Request) (int64, string, string, error) {
	spaceRef, err := spaceRefFromPath(r)
	if err != nil {
		return 0, "", "", err
	}
	mediaID := r.PathValue("mediaId")
	if err := checkSyncID("mediaId", mediaID); err != nil {
		return 0, "", "", err
	}
	noteID := r.PathValue("noteId")
	if err := checkSyncID("noteId", noteID); err != nil {
		return 0, "", "", err
	}
	return spaceRef, mediaID, noteID, nil
}
