package sync

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/emberr-app/emberr-server/internal/blob"
	"github.com/emberr-app/emberr-server/internal/store"
)

const (
	MaxMediaBytes                    = 10 << 30
	unfinishedMediaUploadLifetime    = 14 * 24 * time.Hour
	AbandonedMediaPieceDeletionDelay = 2 * blob.SignedURLLifetime
	UnusedMediaDeletionDelay         = 24*time.Hour + 7*24*time.Hour
)

var (
	ErrMediaNotFound         = errors.New("media file not found")
	ErrMediaTooLarge         = errors.New("media file is larger than the largest allowed size")
	ErrMediaUploadIncomplete = errors.New("media pieces are missing or have another size, or the upload started over")
)

type MediaPieceUploadURL struct {
	Index     int    `json:"index"`
	UploadURL string `json:"uploadUrl"`
}

type MediaUploadURLs struct {
	Committed bool
	Pieces    []MediaPieceUploadURL
	ExpiresAt int64
}

type MediaPieceDownloadURL struct {
	Index       int    `json:"index"`
	DownloadURL string `json:"downloadUrl"`
	SizeBytes   int64  `json:"sizeBytes"`
}

type MediaDownloadURLs struct {
	Pieces    []MediaPieceDownloadURL
	ExpiresAt int64
}

type Media struct {
	mediaTable store.MediaTable
	blobStore  blob.Store
}

func NewMedia(pool *pgxpool.Pool, blobStore blob.Store) *Media {
	return &Media{mediaTable: store.NewMediaTable(pool), blobStore: blobStore}
}

func (media *Media) CreateUploadURLs(ctx context.Context, accountID int64, mediaID string, pieceSizes []int64) (MediaUploadURLs, error) {
	newUpload := store.MediaUpload{UploadID: rand.Text(), PieceSizes: pieceSizes}
	for _, pieceSize := range pieceSizes {
		newUpload.SizeBytes += pieceSize
	}

	var upload store.MediaUpload
	committed := false
	found, err := media.mediaTable.LockOrCreate(ctx, accountID, mediaID, newUpload, unfinishedMediaUploadLifetime, func(file *store.LockedMediaFile) error {
		if file.Committed {
			committed = true
			return nil
		}
		if file.UploadExpired || !slices.Equal(file.Upload.PieceSizes, pieceSizes) {
			if err := file.StartOver(ctx, newUpload, unfinishedMediaUploadLifetime, AbandonedMediaPieceDeletionDelay); err != nil {
				return err
			}
		}
		upload = file.Upload
		return nil
	})
	if err != nil {
		return MediaUploadURLs{}, fmt.Errorf("create media upload urls: %w", err)
	}
	if !found {
		return MediaUploadURLs{}, ErrMediaNotFound
	}
	if committed {
		return MediaUploadURLs{Committed: true, Pieces: []MediaPieceUploadURL{}}, nil
	}

	storedSizes, err := media.blobStore.ObjectSizesUnder(ctx, store.MediaUploadPrefix(mediaID, upload.UploadID))
	if err != nil {
		return MediaUploadURLs{}, err
	}
	signedAt := time.Now()
	pieces := []MediaPieceUploadURL{}
	for pieceIndex, pieceSize := range upload.PieceSizes {
		pieceKey := store.MediaPieceKey(mediaID, upload.UploadID, pieceIndex)
		if storedSize, stored := storedSizes[pieceKey]; stored && storedSize == pieceSize {
			continue
		}
		uploadURL, err := media.blobStore.SignedUploadURL(ctx, pieceKey, pieceSize)
		if err != nil {
			return MediaUploadURLs{}, err
		}
		pieces = append(pieces, MediaPieceUploadURL{Index: pieceIndex, UploadURL: uploadURL})
	}
	return MediaUploadURLs{Pieces: pieces, ExpiresAt: signedAt.Add(blob.SignedURLLifetime).UnixMicro()}, nil
}

func (media *Media) Commit(ctx context.Context, accountID int64, mediaID string) (int64, error) {
	file, found, err := media.mediaTable.Find(ctx, accountID, mediaID)
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, ErrMediaNotFound
	}
	if file.Committed {
		return file.Upload.SizeBytes, nil
	}
	if file.Upload.SizeBytes > MaxMediaBytes {
		return 0, ErrMediaTooLarge
	}
	if err := media.checkEveryPieceUploaded(ctx, mediaID, file.Upload); err != nil {
		return 0, err
	}

	var committedSize int64
	found, err = media.mediaTable.UpdateLocked(ctx, accountID, mediaID, func(lockedFile *store.LockedMediaFile) error {
		committedSize = lockedFile.Upload.SizeBytes
		if lockedFile.Committed {
			return nil
		}
		if lockedFile.UploadExpired || lockedFile.Upload.UploadID != file.Upload.UploadID {
			return ErrMediaUploadIncomplete
		}
		return lockedFile.Commit(ctx)
	})
	if err != nil {
		return 0, fmt.Errorf("commit media file: %w", err)
	}
	if !found {
		return 0, ErrMediaUploadIncomplete
	}
	return committedSize, nil
}

func (media *Media) CreateDownloadURLs(ctx context.Context, accountID int64, mediaID string) (MediaDownloadURLs, error) {
	file, found, err := media.mediaTable.Find(ctx, accountID, mediaID)
	if err != nil {
		return MediaDownloadURLs{}, err
	}
	if !found || !file.Committed {
		return MediaDownloadURLs{}, ErrCommittedMediaNotFound
	}

	signedAt := time.Now()
	pieces := make([]MediaPieceDownloadURL, 0, len(file.Upload.PieceSizes))
	for pieceIndex, pieceSize := range file.Upload.PieceSizes {
		downloadURL, err := media.blobStore.SignedDownloadURL(ctx, store.MediaPieceKey(mediaID, file.Upload.UploadID, pieceIndex))
		if err != nil {
			return MediaDownloadURLs{}, err
		}
		pieces = append(pieces, MediaPieceDownloadURL{Index: pieceIndex, DownloadURL: downloadURL, SizeBytes: pieceSize})
	}
	return MediaDownloadURLs{Pieces: pieces, ExpiresAt: signedAt.Add(blob.SignedURLLifetime).UnixMicro()}, nil
}

func (media *Media) checkEveryPieceUploaded(ctx context.Context, mediaID string, upload store.MediaUpload) error {
	storedSizes, err := media.blobStore.ObjectSizesUnder(ctx, store.MediaUploadPrefix(mediaID, upload.UploadID))
	if err != nil {
		return err
	}
	for pieceIndex, pieceSize := range upload.PieceSizes {
		storedSize, stored := storedSizes[store.MediaPieceKey(mediaID, upload.UploadID, pieceIndex)]
		if !stored || storedSize != pieceSize {
			return ErrMediaUploadIncomplete
		}
	}
	return nil
}
