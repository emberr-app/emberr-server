package api

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"slices"

	"github.com/emberr-app/emberr-server/internal/store"
	"github.com/emberr-app/emberr-server/internal/sync"
)

const (
	spaceItemPath         = spacesPath + "/{spaceRef}/items/{itemId}"
	uploadURLsPath        = spaceItemPath + "/upload-urls"
	spaceItemType         = "SPACE"
	maxInlinePayloadBytes = 1 << 20
)

var (
	inlineItemTypes = []string{spaceItemType, "FOLDER", "SPACE_LISTS", "ACCOUNT_LISTS"}
	blobItemTypes   = []string{"NOTE", "DAILY", "CHAT"}
)

type commitItemRequest struct {
	BaseRevision       int64  `json:"baseRevision"`
	ItemType           string `json:"itemType"`
	PayloadVersion     int32  `json:"payloadVersion"`
	ParentID           string `json:"parentId"`
	EncryptedKey       string `json:"encryptedKey"`
	EncryptedKeyNonce  string `json:"encryptedKeyNonce"`
	InlinePayload      string `json:"inlinePayload"`
	InlinePayloadNonce string `json:"inlinePayloadNonce"`
	ObjectKey          string `json:"objectKey"`
	BlobNonce          string `json:"blobNonce"`
	SizeBytes          int64  `json:"sizeBytes"`
}

type uploadURLRequest struct {
	SizeBytes int64 `json:"sizeBytes"`
}

type uploadURLResponse struct {
	ObjectKey string `json:"objectKey"`
	UploadURL string `json:"uploadUrl"`
	ExpiresAt int64  `json:"expiresAt"`
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

func handleCreateUploadURL(spaces *sync.Spaces, maxItemBytes int64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		spaceRef, itemID, err := spaceItemFromPath(r)
		if err != nil {
			writeError(w, r, err)
			return
		}
		var request uploadURLRequest
		if err := readJSON(w, r, &request); err != nil {
			writeError(w, r, err)
			return
		}
		if err := checkBlobSize(request.SizeBytes, maxItemBytes); err != nil {
			writeError(w, r, err)
			return
		}

		upload, err := spaces.CreateUploadURL(r.Context(), signedInAccountID(r.Context()), spaceRef, itemID, request.SizeBytes)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, uploadURLResponse{ObjectKey: upload.ObjectKey, UploadURL: upload.URL, ExpiresAt: upload.ExpiresAt})
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
	isInline := slices.Contains(inlineItemTypes, request.ItemType)
	if !isInline && !slices.Contains(blobItemTypes, request.ItemType) {
		return store.Item{}, newBadRequestError("itemType must be SPACE, FOLDER, SPACE_LISTS, ACCOUNT_LISTS, NOTE, DAILY or CHAT")
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

	item := store.Item{
		ItemID:            itemID,
		ItemType:          request.ItemType,
		PayloadVersion:    request.PayloadVersion,
		ParentID:          textOrNil(request.ParentID),
		EncryptedKey:      textOrNil(request.EncryptedKey),
		EncryptedKeyNonce: textOrNil(request.EncryptedKeyNonce),
	}
	if isInline {
		return withInlinePayload(item, request)
	}
	return withBlob(item, request)
}

func withInlinePayload(item store.Item, request commitItemRequest) (store.Item, error) {
	if request.ObjectKey != "" || request.BlobNonce != "" || request.SizeBytes != 0 {
		return store.Item{}, newBadRequestError(request.ItemType + " items are stored inline, send inlinePayload instead of objectKey, blobNonce and sizeBytes")
	}
	if _, err := decodeBase64Field("inlinePayloadNonce", request.InlinePayloadNonce, keyNonceLength); err != nil {
		return store.Item{}, err
	}
	payloadSize, err := measureInlinePayload(request.InlinePayload)
	if err != nil {
		return store.Item{}, err
	}

	item.InlinePayload = &request.InlinePayload
	item.InlinePayloadNonce = &request.InlinePayloadNonce
	item.SizeBytes = int64(payloadSize)
	return item, nil
}

func withBlob(item store.Item, request commitItemRequest) (store.Item, error) {
	if request.InlinePayload != "" || request.InlinePayloadNonce != "" {
		return store.Item{}, newBadRequestError(request.ItemType + " items are stored as blobs, send objectKey, blobNonce and sizeBytes instead of inlinePayload")
	}
	if request.ObjectKey == "" {
		return store.Item{}, newBadRequestError("objectKey is missing, ask for an upload URL first")
	}
	if _, err := decodeBase64Field("blobNonce", request.BlobNonce, keyNonceLength); err != nil {
		return store.Item{}, err
	}
	if request.SizeBytes < 1 {
		return store.Item{}, newBadRequestError("sizeBytes must be 1 or more")
	}

	item.BlobObjectKey = &request.ObjectKey
	item.BlobNonce = &request.BlobNonce
	item.SizeBytes = request.SizeBytes
	return item, nil
}

func checkBlobSize(sizeBytes int64, maxItemBytes int64) error {
	if sizeBytes < 1 {
		return newBadRequestError("sizeBytes must be 1 or more")
	}
	if sizeBytes > maxItemBytes {
		return &apiError{Code: "REQUEST_TOO_LARGE", Message: fmt.Sprintf("sizeBytes must be at most %d", maxItemBytes), statusCode: http.StatusRequestEntityTooLarge}
	}
	return nil
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
