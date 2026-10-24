package api

import (
	"encoding/base64"
	"net/http"
	"slices"

	"github.com/emberr-app/emberr-server/internal/store"
	"github.com/emberr-app/emberr-server/internal/sync"
)

const (
	spaceItemPath         = spacesPath + "/{spaceRef}/items/{itemId}"
	spaceItemType         = "SPACE"
	maxInlinePayloadBytes = 1 << 20
)

var inlineItemTypes = []string{spaceItemType, "FOLDER", "SPACE_LISTS", "ACCOUNT_LISTS"}

type commitItemRequest struct {
	BaseRevision       int64  `json:"baseRevision"`
	ItemType           string `json:"itemType"`
	PayloadVersion     int32  `json:"payloadVersion"`
	ParentID           string `json:"parentId"`
	EncryptedKey       string `json:"encryptedKey"`
	EncryptedKeyNonce  string `json:"encryptedKeyNonce"`
	InlinePayload      string `json:"inlinePayload"`
	InlinePayloadNonce string `json:"inlinePayloadNonce"`
}

type deleteItemRequest struct {
	BaseRevision int64 `json:"baseRevision"`
}

type revisionResponse struct {
	Revision int64 `json:"revision"`
}

func handleCommitItem(spaces *sync.Spaces) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		spaceRef, itemID, err := spaceItemFromPath(r)
		if err != nil {
			writeError(w, r, err)
			return
		}
		var request commitItemRequest
		if err := readJSON(w, r, &request); err != nil {
			writeError(w, r, err)
			return
		}
		item, err := itemFromCommitRequest(itemID, request)
		if err != nil {
			writeError(w, r, err)
			return
		}

		revision, err := spaces.CommitItem(r.Context(), signedInAccountID(r.Context()), spaceRef, request.BaseRevision, item)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, revisionResponse{Revision: revision})
	}
}

func handleDeleteItem(spaces *sync.Spaces) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		spaceRef, itemID, err := spaceItemFromPath(r)
		if err != nil {
			writeError(w, r, err)
			return
		}
		var request deleteItemRequest
		if err := readJSON(w, r, &request); err != nil {
			writeError(w, r, err)
			return
		}
		if err := checkBaseRevision(request.BaseRevision); err != nil {
			writeError(w, r, err)
			return
		}

		revision, err := spaces.DeleteItem(r.Context(), signedInAccountID(r.Context()), spaceRef, itemID, request.BaseRevision)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, revisionResponse{Revision: revision})
	}
}

func spaceItemFromPath(r *http.Request) (int64, string, error) {
	spaceRef, err := spaceRefFromPath(r)
	if err != nil {
		return 0, "", err
	}
	itemID := r.PathValue("itemId")
	if err := checkSyncID("itemId", itemID); err != nil {
		return 0, "", err
	}
	return spaceRef, itemID, nil
}

func checkBaseRevision(baseRevision int64) error {
	if baseRevision < 0 {
		return newBadRequestError("baseRevision must be 0 or more")
	}
	return nil
}

func itemFromCommitRequest(itemID string, request commitItemRequest) (store.Item, error) {
	if err := checkBaseRevision(request.BaseRevision); err != nil {
		return store.Item{}, err
	}
	if !slices.Contains(inlineItemTypes, request.ItemType) {
		return store.Item{}, newBadRequestError("itemType must be SPACE, FOLDER, SPACE_LISTS or ACCOUNT_LISTS")
	}
	if request.PayloadVersion < 1 {
		return store.Item{}, newBadRequestError("payloadVersion must be 1 or more")
	}
	if request.ParentID != "" {
		if err := checkSyncID("parentId", request.ParentID); err != nil {
			return store.Item{}, err
		}
	}
	if err := checkItemKey(request); err != nil {
		return store.Item{}, err
	}
	if _, err := decodeBase64Field("inlinePayloadNonce", request.InlinePayloadNonce, keyNonceLength); err != nil {
		return store.Item{}, err
	}
	payloadSize, err := measureInlinePayload(request.InlinePayload)
	if err != nil {
		return store.Item{}, err
	}

	return store.Item{
		ItemID:             itemID,
		ItemType:           request.ItemType,
		PayloadVersion:     request.PayloadVersion,
		ParentID:           textOrNil(request.ParentID),
		EncryptedKey:       textOrNil(request.EncryptedKey),
		EncryptedKeyNonce:  textOrNil(request.EncryptedKeyNonce),
		InlinePayload:      &request.InlinePayload,
		InlinePayloadNonce: &request.InlinePayloadNonce,
		SizeBytes:          int64(payloadSize),
	}, nil
}

func checkItemKey(request commitItemRequest) error {
	if request.ItemType == spaceItemType {
		if request.EncryptedKey != "" || request.EncryptedKeyNonce != "" {
			return newBadRequestError("a SPACE item has no encryptedKey or encryptedKeyNonce, the space key is registered with the space")
		}
		return nil
	}
	return checkBase64Fields([]base64Field{
		{"encryptedKey", request.EncryptedKey, lockedKeyLength},
		{"encryptedKeyNonce", request.EncryptedKeyNonce, keyNonceLength},
	})
}

func measureInlinePayload(encodedPayload string) (int, error) {
	payload, err := base64.StdEncoding.DecodeString(encodedPayload)
	if err != nil || len(payload) == 0 || base64.StdEncoding.EncodeToString(payload) != encodedPayload {
		return 0, newBadRequestError("inlinePayload must be standard base64 and not empty")
	}
	if len(payload) > maxInlinePayloadBytes {
		return 0, errPayloadTooLarge
	}
	return len(payload), nil
}

func textOrNil(text string) *string {
	if text == "" {
		return nil
	}
	return &text
}
