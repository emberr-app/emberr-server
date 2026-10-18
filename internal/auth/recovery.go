package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"

	"golang.org/x/crypto/nacl/box"

	"github.com/emberr-app/emberr-server/internal/store"
)

const publicKeyLength = 32

func (accounts *Accounts) Recover(ctx context.Context, email string, code string, deviceName string) (string, store.AccountKeys, error) {
	if err := accounts.emailCodes.Check(ctx, email, EmailCodeForRecovery, code); err != nil {
		return "", store.AccountKeys{}, err
	}
	record, found, err := accounts.accountTable.FindLoginRecord(ctx, accounts.emailProtector.Hash(email))
	if err != nil {
		return "", store.AccountKeys{}, err
	}
	if !found {
		return "", store.AccountKeys{}, ErrNoActiveEmailCode
	}

	token, tokenHash := newSessionToken()
	sealedToken, err := sealSessionToken(token, record.Keys.PublicKey)
	if err != nil {
		return "", store.AccountKeys{}, err
	}
	if err := accounts.sessionTable.Create(ctx, tokenHash, record.AccountID, deviceName); err != nil {
		return "", store.AccountKeys{}, err
	}
	return sealedToken, record.Keys, nil
}

func sealSessionToken(token string, encodedPublicKey string) (string, error) {
	publicKey, err := base64.StdEncoding.DecodeString(encodedPublicKey)
	if err != nil || len(publicKey) != publicKeyLength {
		return "", errors.New("seal session token: stored public key is not 32 bytes of base64")
	}
	tokenBytes, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		return "", fmt.Errorf("seal session token: %w", err)
	}
	sealedToken, err := box.SealAnonymous(nil, tokenBytes, (*[publicKeyLength]byte)(publicKey), rand.Reader)
	if err != nil {
		return "", fmt.Errorf("seal session token: %w", err)
	}
	return base64.StdEncoding.EncodeToString(sealedToken), nil
}
