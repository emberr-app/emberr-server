package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	mathrand "math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/emberr-app/emberr-server/internal/auth"
	"github.com/emberr-app/emberr-server/internal/store"
	"github.com/emberr-app/emberr-server/internal/sync"
	"github.com/emberr-app/emberr-server/internal/testutil"
)

const (
	testDeviceName                = "Test phone"
	verificationCodeSubjectPrefix = "Verification code: "
	testMaxItemBytes              = 1 << 20
)

type testServer struct {
	router         http.Handler
	pool           *pgxpool.Pool
	emailProtector auth.EmailProtector
	mailSender     *testutil.FakeMailSender
	sessions       *auth.Sessions
	blobStore      *testutil.FakeBlobStore
}

type testAccount struct {
	email    string
	loginKey string
	keys     store.AccountKeys
	token    string
}

func TestParamsGiveTheSameFakeSaltPerUnknownEmailShapedLikeARealOne(t *testing.T) {
	server := newTestServer(t)
	account := server.signUp(t)
	unknownEmail := randomTestEmail()

	realParams := server.params(t, account.email)
	fakeParams := server.params(t, unknownEmail)
	fakeParamsAgain := server.params(t, unknownEmail)
	otherFakeParams := server.params(t, randomTestEmail())

	if realParams["salt"] != account.keys.PasswordSalt {
		t.Errorf("real salt = %v, want the salt sent at signup %v", realParams["salt"], account.keys.PasswordSalt)
	}
	if fakeParams["salt"] != fakeParamsAgain["salt"] {
		t.Errorf("the same unknown email got two salts: %v and %v", fakeParams["salt"], fakeParamsAgain["salt"])
	}
	if fakeParams["salt"] == otherFakeParams["salt"] {
		t.Errorf("two unknown emails got the same salt %v", fakeParams["salt"])
	}
	if len(fakeParams) != len(realParams) {
		t.Errorf("fake params %v and real params %v have different fields", fakeParams, realParams)
	}
	for name, realValue := range realParams {
		fakeValue, present := fakeParams[name]
		switch {
		case !present:
			t.Errorf("fake params have no %q field", name)
		case name == "salt" && len(fakeValue.(string)) != len(realValue.(string)):
			t.Errorf("fake salt %q and real salt %q differ in length", fakeValue, realValue)
		case name != "salt" && fakeValue != realValue:
			t.Errorf("fake %s = %v, real %s = %v", name, fakeValue, name, realValue)
		}
	}
}

func TestSignupWithAWrongCodeCreatesNoAccount(t *testing.T) {
	server := newTestServer(t)
	email := randomTestEmail()
	server.requestSignupCode(t, email)

	request := newTestSignupRequest(email, wrongCode(server.latestCode(t, email)))
	response := server.send(http.MethodPost, signupPath, request)

	requireErrorCode(t, response, http.StatusUnauthorized, "INCORRECT_CODE")
	if server.accountExists(t, email) {
		t.Fatal("an account was created with a wrong code")
	}
}

func TestSignupRefusesWeakArgon2Settings(t *testing.T) {
	server := newTestServer(t)
	weakSettings := map[string]func(keys *store.AccountKeys){
		"32 MiB of memory": func(keys *store.AccountKeys) { keys.MemLimit = 32 << 20 },
		"2 passes":         func(keys *store.AccountKeys) { keys.OpsLimit = 2 },
	}

	for name, weaken := range weakSettings {
		request := newTestSignupRequest(randomTestEmail(), "000000")
		weaken(&request.Keys)
		response := server.send(http.MethodPost, signupPath, request)
		if response.Code != http.StatusBadRequest {
			t.Errorf("signup with %s: status = %d, want %d", name, response.Code, http.StatusBadRequest)
		}
	}
}

func TestSignupCreatesTheAccountItsKeysAndASession(t *testing.T) {
	server := newTestServer(t)
	account := server.signUp(t)

	var storedSalt, loginKeyHash, deviceName string
	tokenHash := sha256.Sum256([]byte(account.token))
	err := server.pool.QueryRow(t.Context(),
		`SELECT account_keys.password_salt, account_keys.login_key_hash, sessions.device_name
		FROM accounts
		JOIN account_keys ON account_keys.account_id = accounts.account_id
		JOIN sessions ON sessions.account_id = accounts.account_id
		WHERE accounts.email_hash = $1 AND sessions.token_hash = $2`,
		server.emailProtector.Hash(account.email), tokenHash[:],
	).Scan(&storedSalt, &loginKeyHash, &deviceName)
	if err != nil {
		t.Fatalf("load the new account, keys and session: %v", err)
	}

	if storedSalt != account.keys.PasswordSalt {
		t.Errorf("stored salt = %q, want %q", storedSalt, account.keys.PasswordSalt)
	}
	if !strings.HasPrefix(loginKeyHash, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Errorf("login key hash = %q, want an argon2id hash with m=19456,t=2,p=1", loginKeyHash)
	}
	if deviceName != testDeviceName {
		t.Errorf("device name = %q, want %q", deviceName, testDeviceName)
	}
}

func TestWrongLoginKeyAndUnknownEmailGetTheSameAnswer(t *testing.T) {
	server := newTestServer(t)
	account := server.signUp(t)

	wrongKeyResponse := server.send(http.MethodPost, loginPath, loginRequest{Email: account.email, LoginKey: randomBase64(auth.LoginKeyLength), DeviceName: testDeviceName})
	unknownEmailResponse := server.send(http.MethodPost, loginPath, loginRequest{Email: randomTestEmail(), LoginKey: randomBase64(auth.LoginKeyLength), DeviceName: testDeviceName})

	requireErrorCode(t, wrongKeyResponse, http.StatusUnauthorized, "INVALID_PASSWORD")
	if wrongKeyResponse.Code != unknownEmailResponse.Code || wrongKeyResponse.Body.String() != unknownEmailResponse.Body.String() {
		t.Fatalf("answers differ: wrong key got %d %s, unknown email got %d %s",
			wrongKeyResponse.Code, wrongKeyResponse.Body, unknownEmailResponse.Code, unknownEmailResponse.Body)
	}
}

func TestRightLoginKeyGivesAWorkingTokenAndTheKeysAndSendsOneLoginNotice(t *testing.T) {
	server := newTestServer(t)
	account := server.signUp(t)

	login := server.logIn(t, account)

	if login.Keys != account.keys {
		t.Errorf("returned keys = %+v, want the keys sent at signup %+v", login.Keys, account.keys)
	}
	accountResponse := server.sendSignedIn(http.MethodGet, accountPath, login.Token, nil)
	if accountResponse.Code != http.StatusOK || !strings.Contains(accountResponse.Body.String(), account.email) {
		t.Fatalf("account details with the login token = %d %s, want 200 with the account's email", accountResponse.Code, accountResponse.Body)
	}

	notices := server.mailSender.WaitForMails(t, account.email, auth.NewLoginNoticeSubject)
	if len(notices) != 1 {
		t.Fatalf("login sent %d notices, want 1", len(notices))
	}
	if !strings.Contains(notices[0].Body, testDeviceName) {
		t.Errorf("the notice %q does not name the device %q", notices[0].Body, testDeviceName)
	}
	if strings.Contains(notices[0].Body, "2001:db8") {
		t.Errorf("the notice %q contains the client's IP address", notices[0].Body)
	}
}

func TestUnknownSessionTokenIsRefused(t *testing.T) {
	server := newTestServer(t)

	requireErrorCode(t, server.checkSession(randomBase64(32)), http.StatusUnauthorized, "INVALID_TOKEN")
}

func TestSessionUnusedForMoreThan90DaysIsRefusedAndDeleted(t *testing.T) {
	server := newTestServer(t)
	account := server.signUp(t)
	tokenHash := sha256.Sum256([]byte(account.token))
	ninetyOneDaysAgo := time.Now().Add(-91 * 24 * time.Hour)
	if _, err := server.pool.Exec(t.Context(), `UPDATE sessions SET last_used_at = $2 WHERE token_hash = $1`, tokenHash[:], ninetyOneDaysAgo.UnixMicro()); err != nil {
		t.Fatalf("age the session: %v", err)
	}

	requireErrorCode(t, server.checkSession(account.token), http.StatusUnauthorized, "INVALID_TOKEN")

	var remainingSessions int
	if err := server.pool.QueryRow(t.Context(), `SELECT count(*) FROM sessions WHERE token_hash = $1`, tokenHash[:]).Scan(&remainingSessions); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if remainingSessions != 0 {
		t.Fatal("the expired session was not deleted")
	}
}

func TestRepeatedWrongLoginKeysAreRefused(t *testing.T) {
	server := newTestServer(t)
	account := server.signUp(t)
	wrongTriesAllowedPerHour := 10

	for attempt := 1; attempt <= wrongTriesAllowedPerHour; attempt++ {
		response := server.send(http.MethodPost, loginPath, loginRequest{Email: account.email, LoginKey: randomBase64(auth.LoginKeyLength), DeviceName: testDeviceName})
		requireErrorCode(t, response, http.StatusUnauthorized, "INVALID_PASSWORD")
	}
	response := server.send(http.MethodPost, loginPath, loginRequest{Email: account.email, LoginKey: account.loginKey, DeviceName: testDeviceName})
	requireErrorCode(t, response, http.StatusTooManyRequests, "TOO_MANY_REQUESTS")
}

func newTestServer(t *testing.T) testServer {
	t.Helper()

	pool := testutil.OpenTestDatabase(t)
	if err := store.ApplyMigrations(pool, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	emailProtector := auth.NewEmailProtector([auth.EmailEncryptionKeyLength]byte{1}, [auth.EmailHashKeyLength]byte{2})
	mailSender := &testutil.FakeMailSender{}
	emailCodes := auth.NewEmailCodes(pool, mailSender, emailProtector)
	sessions := auth.NewSessions(pool)
	blobStore := &testutil.FakeBlobStore{}
	router := NewRouter(Services{
		Database:     pool,
		EmailCodes:   emailCodes,
		Accounts:     auth.NewAccounts(pool, emailCodes, emailProtector, [auth.FakeSaltKeyLength]byte{3}),
		Sessions:     sessions,
		RateLimits:   store.NewRateLimitTable(pool),
		Spaces:       sync.NewSpaces(pool, blobStore),
		MaxItemBytes: testMaxItemBytes,
	})
	return testServer{
		router:         router,
		pool:           pool,
		emailProtector: emailProtector,
		mailSender:     mailSender,
		sessions:       sessions,
		blobStore:      blobStore,
	}
}

func (server testServer) send(method string, path string, body any) *httptest.ResponseRecorder {
	return server.sendSignedIn(method, path, "", body)
}

func (server testServer) sendSignedIn(method string, path string, token string, body any) *httptest.ResponseRecorder {
	var requestBody io.Reader = http.NoBody
	if body != nil {
		encodedBody, _ := json.Marshal(body)
		requestBody = bytes.NewReader(encodedBody)
	}
	request := httptest.NewRequest(method, path, requestBody)
	request.RemoteAddr = fmt.Sprintf("[2001:db8::%x:%x]:4000", mathrand.Uint32(), mathrand.Uint32())
	if token != "" {
		request.Header.Set(sessionTokenHeader, token)
	}
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request)
	return response
}

func (server testServer) signUp(t *testing.T) testAccount {
	t.Helper()

	return server.signUpWith(t, newTestSignupRequest(randomTestEmail(), ""))
}

func (server testServer) signUpWith(t *testing.T, request signupRequest) testAccount {
	t.Helper()

	t.Cleanup(func() {
		server.pool.Exec(context.Background(), `DELETE FROM accounts WHERE email_hash = $1`, server.emailProtector.Hash(request.Email))
	})
	server.requestSignupCode(t, request.Email)
	request.Code = server.latestCode(t, request.Email)

	response := server.send(http.MethodPost, signupPath, request)
	if response.Code != http.StatusOK {
		t.Fatalf("sign up: status = %d %s, want 200", response.Code, response.Body)
	}
	var body signupResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("read signup answer: %v", err)
	}
	return testAccount{email: request.Email, loginKey: request.LoginKey, keys: request.Keys, token: body.Token}
}

func (server testServer) requestSignupCode(t *testing.T, email string) {
	t.Helper()

	response := server.send(http.MethodPost, emailCodePath, emailCodeRequest{Email: email, Purpose: string(auth.EmailCodeForSignup)})
	if response.Code != http.StatusOK {
		t.Fatalf("request signup code: status = %d %s, want 200", response.Code, response.Body)
	}
}

func (server testServer) logIn(t *testing.T, account testAccount) loginResponse {
	t.Helper()

	response := server.send(http.MethodPost, loginPath, loginRequest{Email: account.email, LoginKey: account.loginKey, DeviceName: testDeviceName})
	if response.Code != http.StatusOK {
		t.Fatalf("log in: status = %d %s, want 200", response.Code, response.Body)
	}
	var body loginResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("read login answer: %v", err)
	}
	return body
}

func (server testServer) latestCode(t *testing.T, email string) string {
	t.Helper()

	sentMails := server.mailSender.SentMails()
	for index := len(sentMails) - 1; index >= 0; index-- {
		if sentMail := sentMails[index]; sentMail.ToEmail == email && strings.HasPrefix(sentMail.Subject, verificationCodeSubjectPrefix) {
			return strings.TrimPrefix(sentMail.Subject, verificationCodeSubjectPrefix)
		}
	}
	t.Fatalf("no email code was sent to %s", email)
	return ""
}

func (server testServer) params(t *testing.T, email string) map[string]any {
	t.Helper()

	response := server.send(http.MethodGet, paramsPath+"?email="+email, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("params: status = %d %s, want 200", response.Code, response.Body)
	}
	var params map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &params); err != nil {
		t.Fatalf("read params: %v", err)
	}
	return params
}

func (server testServer) checkSession(token string) *httptest.ResponseRecorder {
	showSignedInAccount := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, signedInAccountID(r.Context()))
	})
	request := httptest.NewRequest(http.MethodGet, "/v1/session-check-test", nil)
	request.Header.Set(sessionTokenHeader, token)
	response := httptest.NewRecorder()
	requireSession(server.sessions, showSignedInAccount).ServeHTTP(response, request)
	return response
}

func (server testServer) accountExists(t *testing.T, email string) bool {
	t.Helper()

	var exists bool
	err := server.pool.QueryRow(t.Context(), `SELECT EXISTS (SELECT 1 FROM accounts WHERE email_hash = $1)`, server.emailProtector.Hash(email)).Scan(&exists)
	if err != nil {
		t.Fatalf("check account exists: %v", err)
	}
	return exists
}

func newTestSignupRequest(email string, code string) signupRequest {
	return signupRequest{
		Email:      email,
		Code:       code,
		DeviceName: testDeviceName,
		LoginKey:   randomBase64(auth.LoginKeyLength),
		Keys: store.AccountKeys{
			PasswordSalt:                           randomBase64(auth.PasswordSaltLength),
			MemLimit:                               auth.DefaultMemLimit,
			OpsLimit:                               auth.DefaultOpsLimit,
			EncryptedMasterKey:                     randomBase64(lockedKeyLength),
			EncryptedMasterKeyNonce:                randomBase64(keyNonceLength),
			MasterKeyEncryptedWithRecoveryKey:      randomBase64(lockedKeyLength),
			MasterKeyEncryptedWithRecoveryKeyNonce: randomBase64(keyNonceLength),
			RecoveryKeyEncryptedWithMasterKey:      randomBase64(lockedKeyLength),
			RecoveryKeyEncryptedWithMasterKeyNonce: randomBase64(keyNonceLength),
			PublicKey:                              randomBase64(publicKeyLength),
			EncryptedPrivateKey:                    randomBase64(lockedKeyLength),
			EncryptedPrivateKeyNonce:               randomBase64(keyNonceLength),
		},
	}
}

func requireErrorCode(t *testing.T, response *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()

	var body apiError
	json.Unmarshal(response.Body.Bytes(), &body)
	if response.Code != wantStatus || body.Code != wantCode {
		t.Fatalf("answer = %d %s, want %d with code %s", response.Code, strings.TrimSpace(response.Body.String()), wantStatus, wantCode)
	}
}

func randomTestEmail() string {
	return strings.ToLower(rand.Text()) + "@example.com"
}

func randomBase64(length int) string {
	randomBytes := make([]byte, length)
	rand.Read(randomBytes)
	return base64.StdEncoding.EncodeToString(randomBytes)
}

func wrongCode(code string) string {
	number, _ := strconv.Atoi(code)
	return fmt.Sprintf("%06d", (number+1)%1_000_000)
}
