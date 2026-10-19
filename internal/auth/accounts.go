package auth

import (
	"context"
	"encoding/base64"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/blake2b"

	"github.com/emberr-app/emberr-server/internal/store"
)

const (
	FakeSaltKeyLength  = 32
	PasswordSaltLength = 16
	DefaultMemLimit    = 64 << 20
	DefaultOpsLimit    = 3
	loginAttemptLimit  = 10
	loginAttemptWindow = time.Hour
)

const NewLoginNoticeSubject = "New login to your Emberr account"

type PasswordParams struct {
	Salt     string `json:"salt"`
	MemLimit int64  `json:"memLimit"`
	OpsLimit int32  `json:"opsLimit"`
}

type Signup struct {
	Email      string
	Code       string
	DeviceName string
	LoginKey   []byte
	Keys       store.AccountKeys
}

type Accounts struct {
	accountTable   store.AccountTable
	sessionTable   store.SessionTable
	rateLimits     store.RateLimitTable
	emailCodes     *EmailCodes
	mailSender     MailSender
	emailProtector EmailProtector
	fakeSaltKey    [FakeSaltKeyLength]byte
}

func NewAccounts(pool *pgxpool.Pool, emailCodes *EmailCodes, emailProtector EmailProtector, fakeSaltKey [FakeSaltKeyLength]byte) *Accounts {
	return &Accounts{
		accountTable:   store.NewAccountTable(pool),
		sessionTable:   store.NewSessionTable(pool),
		rateLimits:     store.NewRateLimitTable(pool),
		emailCodes:     emailCodes,
		mailSender:     emailCodes.mailSender,
		emailProtector: emailProtector,
		fakeSaltKey:    fakeSaltKey,
	}
}

func (accounts *Accounts) PasswordParams(ctx context.Context, email string) (PasswordParams, error) {
	record, found, err := accounts.accountTable.FindLoginRecord(ctx, accounts.emailProtector.Hash(email))
	if err != nil {
		return PasswordParams{}, err
	}
	if !found {
		return PasswordParams{Salt: accounts.fakeSalt(email), MemLimit: DefaultMemLimit, OpsLimit: DefaultOpsLimit}, nil
	}
	return PasswordParams{Salt: record.Keys.PasswordSalt, MemLimit: record.Keys.MemLimit, OpsLimit: record.Keys.OpsLimit}, nil
}

func (accounts *Accounts) fakeSalt(email string) string {
	hasher, _ := blake2b.New(PasswordSaltLength, accounts.fakeSaltKey[:])
	hasher.Write([]byte("fake-salt:" + NormalizeEmail(email)))
	return base64.StdEncoding.EncodeToString(hasher.Sum(nil))
}

func (accounts *Accounts) SignUp(ctx context.Context, signup Signup) (string, error) {
	if err := accounts.emailCodes.Check(ctx, signup.Email, EmailCodeForSignup, signup.Code); err != nil {
		return "", err
	}

	token, tokenHash := newSessionToken()
	encryptedEmail := accounts.emailProtector.Encrypt(signup.Email)
	err := accounts.accountTable.CreateWithSession(ctx, store.NewAccount{
		EmailHash:           accounts.emailProtector.Hash(signup.Email),
		EncryptedEmail:      encryptedEmail.Ciphertext,
		EncryptedEmailNonce: encryptedEmail.Nonce,
		LoginKeyHash:        hashLoginKey(signup.LoginKey),
		Keys:                signup.Keys,
		SessionTokenHash:    tokenHash,
		DeviceName:          signup.DeviceName,
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

func (accounts *Accounts) LogIn(ctx context.Context, email string, loginKey []byte, deviceName string) (string, store.AccountKeys, error) {
	record, err := accounts.checkLoginKey(ctx, email, loginKey)
	if err != nil {
		return "", store.AccountKeys{}, err
	}

	token, tokenHash := newSessionToken()
	if err := accounts.sessionTable.Create(ctx, tokenHash, record.AccountID, deviceName); err != nil {
		return "", store.AccountKeys{}, err
	}
	accounts.sendNewLoginNotice(ctx, email, deviceName)
	return token, record.Keys, nil
}

func (accounts *Accounts) sendNewLoginNotice(ctx context.Context, email string, deviceName string) {
	loginTime := time.Now().UTC().Format("02 Jan, 2006 15:04")
	body := "We detected a new login to your Emberr account at " + loginTime + " UTC.\n\n" +
		"Device: " + deviceName + "\n\n" +
		"If this was not you, please change your password.\n"
	contextThatOutlivesTheRequest := context.WithoutCancel(ctx)
	go func() {
		if err := accounts.mailSender.Send(contextThatOutlivesTheRequest, email, NewLoginNoticeSubject, body); err != nil {
			slog.WarnContext(contextThatOutlivesTheRequest, "send new login notice failed", "error", err)
		}
	}()
}

func (accounts *Accounts) checkLoginKey(ctx context.Context, email string, loginKey []byte) (store.LoginRecord, error) {
	emailHash := accounts.emailProtector.Hash(email)
	allowed, err := accounts.rateLimits.CountAttempt(ctx, "login:"+emailHash, loginAttemptLimit, loginAttemptWindow, time.Now())
	if err != nil {
		return store.LoginRecord{}, err
	}
	if !allowed {
		return store.LoginRecord{}, ErrTooManyAttempts
	}

	record, found, err := accounts.accountTable.FindLoginRecord(ctx, emailHash)
	if err != nil {
		return store.LoginRecord{}, err
	}
	storedHash := record.LoginKeyHash
	if !found {
		storedHash = loginKeyHashForUnknownEmails
	}
	matches, err := loginKeyMatchesHash(loginKey, storedHash)
	if err != nil {
		return store.LoginRecord{}, err
	}
	if !found || !matches {
		return store.LoginRecord{}, ErrWrongLoginKey
	}
	return record, nil
}
