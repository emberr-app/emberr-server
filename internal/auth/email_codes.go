package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/emberr-app/emberr-server/internal/store"
)

type EmailCodePurpose string

const (
	EmailCodeForSignup          EmailCodePurpose = "signup"
	EmailCodeForNewDevice       EmailCodePurpose = "login"
	EmailCodeForRecovery        EmailCodePurpose = "recover"
	EmailCodeForChangeEmail     EmailCodePurpose = "change"
	EmailCodeForAccountDeletion EmailCodePurpose = "delete"
)

const (
	emailCodeLifetime          = time.Hour
	emailCodeWrongAttemptLimit = 20
	emailCodeSendLimit         = 10
	emailCodeSendWindow        = time.Hour
)

var (
	ErrTooManyAttempts   = errors.New("too many attempts")
	ErrWrongEmailCode    = errors.New("wrong email code")
	ErrNoActiveEmailCode = errors.New("no active email code")
)

type MailSender interface {
	Send(ctx context.Context, toEmail string, subject string, body string) error
}

type EmailCodes struct {
	codeTable      store.EmailCodeTable
	accountTable   store.AccountTable
	rateLimits     store.RateLimitTable
	mailSender     MailSender
	emailProtector EmailProtector
	now            func() time.Time
}

func NewEmailCodes(pool *pgxpool.Pool, mailSender MailSender, emailProtector EmailProtector) *EmailCodes {
	return &EmailCodes{
		codeTable:      store.NewEmailCodeTable(pool),
		accountTable:   store.NewAccountTable(pool),
		rateLimits:     store.NewRateLimitTable(pool),
		mailSender:     mailSender,
		emailProtector: emailProtector,
		now:            time.Now,
	}
}

func (purpose EmailCodePurpose) isForExistingAccount() bool {
	return purpose != EmailCodeForSignup && purpose != EmailCodeForChangeEmail
}

func (emailCodes *EmailCodes) Send(ctx context.Context, email string, purpose EmailCodePurpose) error {
	email = NormalizeEmail(email)
	emailHash := emailCodes.emailProtector.Hash(email)
	now := emailCodes.now()

	allowed, err := emailCodes.rateLimits.CountAttempt(ctx, "code:"+emailHash, emailCodeSendLimit, emailCodeSendWindow, now)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrTooManyAttempts
	}

	accountExists, err := emailCodes.accountTable.ExistsForEmailHash(ctx, emailHash)
	if err != nil {
		return err
	}
	if accountExists != purpose.isForExistingAccount() {
		return nil
	}

	code := newEmailCode()
	codeHash := emailCodes.hashCode(emailHash, purpose, code)
	if err := emailCodes.codeTable.Save(ctx, emailHash, string(purpose), codeHash, now.Add(emailCodeLifetime)); err != nil {
		return err
	}

	subject := "Verification code: " + code
	body := "Your Emberr verification code is " + code + ".\n\nIt expires in 1 hour. If you did not ask for it, you can ignore this email.\n"
	if err := emailCodes.mailSender.Send(ctx, email, subject, body); err != nil {
		return fmt.Errorf("send email code: %w", err)
	}
	return nil
}

func (emailCodes *EmailCodes) Check(ctx context.Context, email string, purpose EmailCodePurpose, code string) error {
	emailHash := emailCodes.emailProtector.Hash(email)

	activeCode, found, err := emailCodes.codeTable.AddAttempt(ctx, emailHash, string(purpose), emailCodes.now())
	if err != nil {
		return err
	}
	if !found {
		return ErrNoActiveEmailCode
	}
	if activeCode.Attempts > emailCodeWrongAttemptLimit {
		return ErrTooManyAttempts
	}

	submittedCodeHash := emailCodes.hashCode(emailHash, purpose, strings.TrimSpace(code))
	if subtle.ConstantTimeCompare(submittedCodeHash, activeCode.CodeHash) != 1 {
		return ErrWrongEmailCode
	}

	deleted, err := emailCodes.codeTable.Delete(ctx, emailHash, string(purpose), activeCode.CodeHash)
	if err != nil {
		return err
	}
	if !deleted {
		return ErrNoActiveEmailCode
	}
	return nil
}

func (emailCodes *EmailCodes) hashCode(emailHash string, purpose EmailCodePurpose, code string) []byte {
	return emailCodes.emailProtector.keyedHash("email-code:" + string(purpose) + ":" + emailHash + ":" + code)
}

func newEmailCode() string {
	number, _ := rand.Int(rand.Reader, big.NewInt(1_000_000))
	return fmt.Sprintf("%06d", number.Int64())
}
