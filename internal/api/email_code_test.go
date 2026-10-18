package api

import (
	"context"
	"crypto/rand"
	"fmt"
	mathrand "math/rand/v2"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/emberr-app/emberr-server/internal/auth"
	"github.com/emberr-app/emberr-server/internal/store"
	"github.com/emberr-app/emberr-server/internal/testutil"
)

func TestEmailCodeAnswerIsTheSameForExistingAndUnknownEmails(t *testing.T) {
	pool := testutil.OpenTestDatabase(t)
	if err := store.ApplyMigrations(pool, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	emailProtector := auth.NewEmailProtector([auth.EmailEncryptionKeyLength]byte{1}, [auth.EmailHashKeyLength]byte{2})
	mailSender := &testutil.FakeMailSender{}
	router := NewRouter(Services{
		Database:   pool,
		EmailCodes: auth.NewEmailCodes(pool, mailSender, emailProtector),
		RateLimits: store.NewRateLimitTable(pool),
	})

	existingEmail := "existing-" + strings.ToLower(rand.Text()) + "@example.com"
	unknownEmail := "unknown-" + strings.ToLower(rand.Text()) + "@example.com"
	encryptedEmail := emailProtector.Encrypt(existingEmail)
	var accountID int64
	err := pool.QueryRow(t.Context(),
		`INSERT INTO accounts (email_hash, encrypted_email, encrypted_email_nonce) VALUES ($1, $2, $3) RETURNING account_id`,
		emailProtector.Hash(existingEmail), encryptedEmail.Ciphertext, encryptedEmail.Nonce,
	).Scan(&accountID)
	if err != nil {
		t.Fatalf("insert account: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM accounts WHERE account_id = $1`, accountID)
	})

	clientAddress := fmt.Sprintf("[2001:db8::%x:%x]:4000", mathrand.Uint32(), mathrand.Uint32())
	for _, purpose := range []string{"signup", "recover"} {
		existingEmailResponse := requestEmailCode(router, clientAddress, existingEmail, purpose)
		unknownEmailResponse := requestEmailCode(router, clientAddress, unknownEmail, purpose)

		if existingEmailResponse.Code != http.StatusOK {
			t.Errorf("%s for an existing email: status = %d, want %d", purpose, existingEmailResponse.Code, http.StatusOK)
		}
		if existingEmailResponse.Code != unknownEmailResponse.Code || existingEmailResponse.Body.String() != unknownEmailResponse.Body.String() {
			t.Errorf("%s answers differ: existing email got %d %q, unknown email got %d %q", purpose,
				existingEmailResponse.Code, existingEmailResponse.Body, unknownEmailResponse.Code, unknownEmailResponse.Body)
		}
	}

	var recipients []string
	for _, sentMail := range mailSender.SentMails {
		recipients = append(recipients, sentMail.ToEmail)
	}
	if wantRecipients := []string{unknownEmail, existingEmail}; !slices.Equal(recipients, wantRecipients) {
		t.Fatalf("codes went to %v, want the signup code to the unknown email and the recovery code to the existing one: %v", recipients, wantRecipients)
	}
}

func requestEmailCode(router http.Handler, clientAddress string, email string, purpose string) *httptest.ResponseRecorder {
	body := fmt.Sprintf(`{"email":%q,"purpose":%q}`, email, purpose)
	request := httptest.NewRequest(http.MethodPost, emailCodePath, strings.NewReader(body))
	request.RemoteAddr = clientAddress
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}
