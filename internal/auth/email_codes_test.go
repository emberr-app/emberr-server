package auth

import (
	"crypto/rand"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/emberr-app/emberr-server/internal/store"
	"github.com/emberr-app/emberr-server/internal/testutil"
)

func TestRightEmailCodePassesOnlyOnce(t *testing.T) {
	emailCodes, mailSender := newTestEmailCodes(t)
	email := newTestEmail()
	code := sendTestCode(t, emailCodes, mailSender, email)

	if err := emailCodes.Check(t.Context(), email, EmailCodeForSignup, code); err != nil {
		t.Fatalf("first check with the right code: %v", err)
	}
	if err := emailCodes.Check(t.Context(), email, EmailCodeForSignup, code); !errors.Is(err, ErrNoActiveEmailCode) {
		t.Fatalf("second check with the same code = %v, want %v", err, ErrNoActiveEmailCode)
	}
}

func TestExpiredEmailCodeFails(t *testing.T) {
	emailCodes, mailSender := newTestEmailCodes(t)
	email := newTestEmail()
	code := sendTestCode(t, emailCodes, mailSender, email)

	emailCodes.now = func() time.Time { return time.Now().Add(emailCodeLifetime + time.Second) }
	if err := emailCodes.Check(t.Context(), email, EmailCodeForSignup, code); !errors.Is(err, ErrNoActiveEmailCode) {
		t.Fatalf("check after expiry = %v, want %v", err, ErrNoActiveEmailCode)
	}
}

func TestTooManyWrongTriesLockTheCodeEvenForTheRightCode(t *testing.T) {
	emailCodes, mailSender := newTestEmailCodes(t)
	email := newTestEmail()
	code := sendTestCode(t, emailCodes, mailSender, email)

	for attempt := 1; attempt <= emailCodeWrongAttemptLimit; attempt++ {
		if err := emailCodes.Check(t.Context(), email, EmailCodeForSignup, differentCode(code)); !errors.Is(err, ErrWrongEmailCode) {
			t.Fatalf("wrong try %d = %v, want %v", attempt, err, ErrWrongEmailCode)
		}
	}
	if err := emailCodes.Check(t.Context(), email, EmailCodeForSignup, code); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("right code after %d wrong tries = %v, want %v", emailCodeWrongAttemptLimit, err, ErrTooManyAttempts)
	}
}

func TestNewEmailCodeReplacesTheOldOne(t *testing.T) {
	emailCodes, mailSender := newTestEmailCodes(t)
	email := newTestEmail()
	oldCode := sendTestCode(t, emailCodes, mailSender, email)
	newCode := sendTestCode(t, emailCodes, mailSender, email)
	if oldCode == newCode {
		t.Skip("both random codes happened to be the same number")
	}

	if err := emailCodes.Check(t.Context(), email, EmailCodeForSignup, oldCode); !errors.Is(err, ErrWrongEmailCode) {
		t.Fatalf("check with the old code = %v, want %v", err, ErrWrongEmailCode)
	}
	if err := emailCodes.Check(t.Context(), email, EmailCodeForSignup, newCode); err != nil {
		t.Fatalf("check with the new code: %v", err)
	}
}

func newTestEmailCodes(t *testing.T) (*EmailCodes, *testutil.FakeMailSender) {
	t.Helper()

	pool := testutil.OpenTestDatabase(t)
	if err := store.ApplyMigrations(pool, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	mailSender := &testutil.FakeMailSender{}
	return NewEmailCodes(pool, mailSender, NewEmailProtector(testEncryptionKey, testHashKey)), mailSender
}

func newTestEmail() string {
	return strings.ToLower(rand.Text()) + "@example.com"
}

func sendTestCode(t *testing.T, emailCodes *EmailCodes, mailSender *testutil.FakeMailSender, email string) string {
	t.Helper()

	if err := emailCodes.Send(t.Context(), email, EmailCodeForSignup); err != nil {
		t.Fatalf("send code: %v", err)
	}
	if len(mailSender.SentMails) == 0 {
		t.Fatal("no email was sent")
	}
	lastMail := mailSender.SentMails[len(mailSender.SentMails)-1]
	return strings.TrimPrefix(lastMail.Subject, "Verification code: ")
}

func differentCode(code string) string {
	number, _ := strconv.Atoi(code)
	return fmt.Sprintf("%06d", (number+1)%1_000_000)
}
