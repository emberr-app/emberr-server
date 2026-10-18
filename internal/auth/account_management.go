package auth

import (
	"context"
	"errors"
	"log/slog"

	"github.com/emberr-app/emberr-server/internal/store"
)

var ErrEmailAlreadyUsed = errors.New("email already belongs to another account")

type AccountDetails struct {
	Email            string            `json:"email"`
	StorageUsedBytes int64             `json:"storageUsedBytes"`
	Keys             store.AccountKeys `json:"keys"`
}

type PasswordChange struct {
	LoginKey           []byte
	Keys               store.PasswordKeys
	LogOutOtherDevices bool
	CurrentToken       string
}

func (accounts *Accounts) Details(ctx context.Context, accountID int64) (AccountDetails, error) {
	account, email, err := accounts.findAccountWithEmail(ctx, accountID)
	if err != nil {
		return AccountDetails{}, err
	}
	return AccountDetails{Email: email, StorageUsedBytes: account.StorageUsedBytes, Keys: account.Keys}, nil
}

func (accounts *Accounts) ChangePassword(ctx context.Context, accountID int64, change PasswordChange) error {
	return accounts.accountTable.ChangePassword(ctx, store.PasswordChange{
		AccountID:            accountID,
		LoginKeyHash:         hashLoginKey(change.LoginKey),
		Keys:                 change.Keys,
		SignOutOtherSessions: change.LogOutOtherDevices,
		CurrentTokenHash:     hashSessionToken(change.CurrentToken),
	})
}

func (accounts *Accounts) SendChangeEmailCode(ctx context.Context, newEmail string) error {
	return accounts.emailCodes.Send(ctx, newEmail, EmailCodeForChangeEmail)
}

func (accounts *Accounts) SendDeletionCode(ctx context.Context, accountID int64) error {
	_, email, err := accounts.findAccountWithEmail(ctx, accountID)
	if err != nil {
		return err
	}
	return accounts.emailCodes.Send(ctx, email, EmailCodeForAccountDeletion)
}

func (accounts *Accounts) ChangeEmail(ctx context.Context, accountID int64, newEmail string, code string) error {
	if err := accounts.emailCodes.Check(ctx, newEmail, EmailCodeForChangeEmail, code); err != nil {
		return err
	}
	_, oldEmail, err := accounts.findAccountWithEmail(ctx, accountID)
	if err != nil {
		return err
	}

	encryptedEmail := accounts.emailProtector.Encrypt(newEmail)
	err = accounts.accountTable.UpdateEmail(ctx, accountID, accounts.emailProtector.Hash(newEmail), encryptedEmail.Ciphertext, encryptedEmail.Nonce)
	if errors.Is(err, store.ErrEmailHashTaken) {
		return ErrEmailAlreadyUsed
	}
	if err != nil {
		return err
	}

	body := "The email address of your Emberr account was changed to " + NormalizeEmail(newEmail) + ".\n\nIf you did not make this change, contact Emberr support.\n"
	if err := accounts.mailSender.Send(ctx, oldEmail, "Email address updated", body); err != nil {
		slog.WarnContext(ctx, "send email changed notice failed", "error", err)
	}
	return nil
}

func (accounts *Accounts) Delete(ctx context.Context, accountID int64, loginKey []byte, code string) error {
	account, email, err := accounts.findAccountWithEmail(ctx, accountID)
	if err != nil {
		return err
	}
	matches, err := loginKeyMatchesHash(loginKey, account.LoginKeyHash)
	if err != nil {
		return err
	}
	if !matches {
		return ErrWrongLoginKey
	}
	if err := accounts.emailCodes.Check(ctx, email, EmailCodeForAccountDeletion, code); err != nil {
		return err
	}
	return accounts.accountTable.Delete(ctx, accountID)
}

func (accounts *Accounts) findAccountWithEmail(ctx context.Context, accountID int64) (store.Account, string, error) {
	account, found, err := accounts.accountTable.Find(ctx, accountID)
	if err != nil {
		return store.Account{}, "", err
	}
	if !found {
		return store.Account{}, "", ErrInvalidSession
	}
	email, err := accounts.emailProtector.Decrypt(EncryptedEmail{Ciphertext: account.EncryptedEmail, Nonce: account.EncryptedEmailNonce})
	if err != nil {
		return store.Account{}, "", err
	}
	return account, email, nil
}
