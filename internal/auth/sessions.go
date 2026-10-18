package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/emberr-app/emberr-server/internal/store"
)

const (
	sessionTokenLength            = 32
	sessionIdleLimit              = 90 * 24 * time.Hour
	sessionLastUsedUpdateInterval = time.Minute
)

var ErrInvalidSession = errors.New("invalid or expired session")

type Sessions struct {
	sessionTable store.SessionTable
}

func NewSessions(pool *pgxpool.Pool) *Sessions {
	return &Sessions{sessionTable: store.NewSessionTable(pool)}
}

func newSessionToken() (string, []byte) {
	tokenBytes := make([]byte, sessionTokenLength)
	rand.Read(tokenBytes)
	token := base64.URLEncoding.EncodeToString(tokenBytes)
	return token, hashSessionToken(token)
}

func hashSessionToken(token string) []byte {
	tokenHash := sha256.Sum256([]byte(token))
	return tokenHash[:]
}

func (sessions *Sessions) AccountForToken(ctx context.Context, token string) (int64, error) {
	if token == "" {
		return 0, ErrInvalidSession
	}
	tokenHash := hashSessionToken(token)
	session, found, err := sessions.sessionTable.Find(ctx, tokenHash)
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, ErrInvalidSession
	}

	now := time.Now()
	unusedFor := now.Sub(session.LastUsedAt)
	if unusedFor > sessionIdleLimit {
		if err := sessions.sessionTable.Delete(ctx, tokenHash); err != nil {
			return 0, err
		}
		return 0, ErrInvalidSession
	}
	if unusedFor >= sessionLastUsedUpdateInterval {
		if err := sessions.sessionTable.MarkUsed(ctx, tokenHash, now); err != nil {
			return 0, err
		}
	}
	return session.AccountID, nil
}
