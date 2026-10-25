package api

import (
	"net/http"
	"strconv"

	"github.com/emberr-app/emberr-server/internal/store"
	"github.com/emberr-app/emberr-server/internal/sync"
)

const (
	spacesPath           = "/v1/spaces"
	syncStatePath        = "/v1/sync/state"
	spaceChangesPath     = spacesPath + "/{spaceRef}/changes"
	accountClientSpaceID = "account"
	spaceKindSpace       = "space"
	spaceKindAccount     = "account"
	maxChangesLimit      = 500
)

type registerSpaceRequest struct {
	ClientSpaceID     string `json:"clientSpaceId"`
	Kind              string `json:"kind"`
	EncryptedKey      string `json:"encryptedKey"`
	EncryptedKeyNonce string `json:"encryptedKeyNonce"`
}

type registerSpaceResponse struct {
	SpaceRef          int64  `json:"spaceRef"`
	EncryptedKey      string `json:"encryptedKey"`
	EncryptedKeyNonce string `json:"encryptedKeyNonce"`
}

type syncStateResponse struct {
	Spaces []store.Space `json:"spaces"`
}

type changesResponse struct {
	Items      []store.Item      `json:"items"`
	MediaLocks []store.MediaLock `json:"mediaLocks"`
	Latest     int64             `json:"latest"`
	HasMore    bool              `json:"hasMore"`
}

type resyncRequiredResponse struct {
	ResyncRequired bool  `json:"resyncRequired"`
	Latest         int64 `json:"latest"`
}

func handleRegisterSpace(spaces *sync.Spaces) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request registerSpaceRequest
		if err := readJSON(w, r, &request); err != nil {
			writeError(w, r, err)
			return
		}
		if err := checkRegisterSpaceRequest(request); err != nil {
			writeError(w, r, err)
			return
		}

		space, err := spaces.Register(r.Context(), store.NewSpace{
			OwnerAccountID:    signedInAccountID(r.Context()),
			ClientSpaceID:     request.ClientSpaceID,
			Kind:              request.Kind,
			EncryptedKey:      request.EncryptedKey,
			EncryptedKeyNonce: request.EncryptedKeyNonce,
		})
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, registerSpaceResponse{SpaceRef: space.SpaceRef, EncryptedKey: space.EncryptedKey, EncryptedKeyNonce: space.EncryptedKeyNonce})
	}
}

func handleSyncState(spaces *sync.Spaces) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accountSpaces, err := spaces.List(r.Context(), signedInAccountID(r.Context()))
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, syncStateResponse{Spaces: accountSpaces})
	}
}

func handleSpaceChanges(spaces *sync.Spaces) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		spaceRef, err := spaceRefFromPath(r)
		if err != nil {
			writeError(w, r, err)
			return
		}
		sinceRevision, limit, err := readChangesQuery(r)
		if err != nil {
			writeError(w, r, err)
			return
		}

		page, err := spaces.Changes(r.Context(), signedInAccountID(r.Context()), spaceRef, sinceRevision, limit)
		if err != nil {
			writeError(w, r, err)
			return
		}
		if page.ResyncRequired {
			writeJSON(w, resyncRequiredResponse{ResyncRequired: true, Latest: page.Latest})
			return
		}
		writeJSON(w, changesResponse{Items: page.Items, MediaLocks: page.MediaLocks, Latest: page.Latest, HasMore: page.HasMore})
	}
}

func checkRegisterSpaceRequest(request registerSpaceRequest) error {
	if err := checkSyncID("clientSpaceId", request.ClientSpaceID); err != nil {
		return err
	}
	wantKind := spaceKindSpace
	if request.ClientSpaceID == accountClientSpaceID {
		wantKind = spaceKindAccount
	}
	if request.Kind != wantKind {
		return newBadRequestError(`kind must be "account" for the clientSpaceId "account" and "space" for every other`)
	}
	return checkBase64Fields([]base64Field{
		{"encryptedKey", request.EncryptedKey, lockedKeyLength},
		{"encryptedKeyNonce", request.EncryptedKeyNonce, keyNonceLength},
	})
}

func spaceRefFromPath(r *http.Request) (int64, error) {
	spaceRef, err := strconv.ParseInt(r.PathValue("spaceRef"), 10, 64)
	if err != nil || spaceRef < 1 {
		return 0, newBadRequestError("spaceRef must be a whole number of 1 or more")
	}
	return spaceRef, nil
}

func readChangesQuery(r *http.Request) (int64, int, error) {
	query := r.URL.Query()

	sinceRevision := int64(0)
	if sinceText := query.Get("since"); sinceText != "" {
		parsedSince, err := strconv.ParseInt(sinceText, 10, 64)
		if err != nil || parsedSince < 0 {
			return 0, 0, newBadRequestError("since must be a whole number of 0 or more")
		}
		sinceRevision = parsedSince
	}

	limit := maxChangesLimit
	if limitText := query.Get("limit"); limitText != "" {
		parsedLimit, err := strconv.Atoi(limitText)
		if err != nil || parsedLimit < 1 || parsedLimit > maxChangesLimit {
			return 0, 0, newBadRequestError("limit must be a whole number from 1 to " + strconv.Itoa(maxChangesLimit))
		}
		limit = parsedLimit
	}
	return sinceRevision, limit, nil
}
