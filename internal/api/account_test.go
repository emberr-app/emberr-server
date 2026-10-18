package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"golang.org/x/crypto/nacl/box"

	"github.com/emberr-app/emberr-server/internal/auth"
	"github.com/emberr-app/emberr-server/internal/store"
)

func TestSessionListMarksTheCurrentSessionAndShowsNoTokens(t *testing.T) {
	server := newTestServer(t)
	account := server.signUp(t)
	otherToken := server.newSession(t, account)

	response := server.sendSignedIn(http.MethodGet, sessionsPath, account.token, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("list sessions: status = %d %s, want 200", response.Code, response.Body)
	}
	for _, token := range []string{account.token, otherToken} {
		if strings.Contains(response.Body.String(), token) {
			t.Fatalf("the session list %s contains the token %s", response.Body, token)
		}
	}
	var body sessionsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("read session list: %v", err)
	}
	if len(body.Sessions) != 2 {
		t.Fatalf("listed %d sessions, want 2: %s", len(body.Sessions), response.Body)
	}
	var currentIDs []string
	for _, session := range body.Sessions {
		if session.IsCurrent {
			currentIDs = append(currentIDs, session.ID)
		}
	}
	if wantIDs := []string{sessionIDForToken(account.token)}; !slices.Equal(currentIDs, wantIDs) {
		t.Fatalf("sessions marked current = %v, want only the signup session %v", currentIDs, wantIDs)
	}
}

func TestSignedOutSessionTokenStopsWorking(t *testing.T) {
	server := newTestServer(t)
	account := server.signUp(t)
	otherToken := server.newSession(t, account)

	response := server.sendSignedIn(http.MethodDelete, sessionsPath+"/"+sessionIDForToken(otherToken), account.token, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("sign out session: status = %d %s, want 200", response.Code, response.Body)
	}

	requireErrorCode(t, server.checkSession(otherToken), http.StatusUnauthorized, "INVALID_TOKEN")
	if check := server.checkSession(account.token); check.Code != http.StatusOK {
		t.Fatalf("the session that signed the other out was refused: %d %s", check.Code, check.Body)
	}
}

func TestAnotherAccountsSessionCannotBeSignedOut(t *testing.T) {
	server := newTestServer(t)
	owner := server.signUp(t)
	stranger := server.signUp(t)

	response := server.sendSignedIn(http.MethodDelete, sessionsPath+"/"+sessionIDForToken(owner.token), stranger.token, nil)

	requireErrorCode(t, response, http.StatusNotFound, "NOT_FOUND")
	if check := server.checkSession(owner.token); check.Code != http.StatusOK {
		t.Fatalf("the owner's session was signed out by another account: %d %s", check.Code, check.Body)
	}
}

func TestChangePasswordReplacesTheLoginKeyAndSignsOutOtherSessions(t *testing.T) {
	server := newTestServer(t)
	account := server.signUp(t)
	otherToken := server.newSession(t, account)
	change := newTestPasswordChange(nil)

	response := server.sendSignedIn(http.MethodPost, changePasswordPath, account.token, change)
	if response.Code != http.StatusOK {
		t.Fatalf("change password: status = %d %s, want 200", response.Code, response.Body)
	}

	oldKeyResponse := server.send(http.MethodPost, loginPath, loginRequest{Email: account.email, LoginKey: account.loginKey, DeviceName: testDeviceName})
	requireErrorCode(t, oldKeyResponse, http.StatusUnauthorized, "INVALID_PASSWORD")
	server.logIn(t, testAccount{email: account.email, loginKey: change.LoginKey})

	requireErrorCode(t, server.checkSession(otherToken), http.StatusUnauthorized, "INVALID_TOKEN")
	if check := server.checkSession(account.token); check.Code != http.StatusOK {
		t.Fatalf("the session that changed the password was signed out: %d %s", check.Code, check.Body)
	}
}

func TestChangePasswordKeepsOtherSessionsWhenAsked(t *testing.T) {
	server := newTestServer(t)
	account := server.signUp(t)
	otherToken := server.newSession(t, account)
	logOutOtherDevices := false

	response := server.sendSignedIn(http.MethodPost, changePasswordPath, account.token, newTestPasswordChange(&logOutOtherDevices))
	if response.Code != http.StatusOK {
		t.Fatalf("change password: status = %d %s, want 200", response.Code, response.Body)
	}

	if check := server.checkSession(otherToken); check.Code != http.StatusOK {
		t.Fatalf("the other session was signed out although logOutOtherDevices was false: %d %s", check.Code, check.Body)
	}
}

func TestChangePasswordRefusesSettingsOverTheCaps(t *testing.T) {
	server := newTestServer(t)
	account := server.signUp(t)
	tooCostlySettings := map[string]func(keys *store.PasswordKeys){
		"2 GiB of memory":             func(keys *store.PasswordKeys) { keys.MemLimit = 2 << 30 },
		"64 MiB of memory, 65 passes": func(keys *store.PasswordKeys) { keys.OpsLimit = 65 },
	}

	for name, makeTooCostly := range tooCostlySettings {
		change := newTestPasswordChange(nil)
		makeTooCostly(&change.Keys)
		response := server.sendSignedIn(http.MethodPost, changePasswordPath, account.token, change)
		if response.Code != http.StatusBadRequest {
			t.Errorf("change password with %s: status = %d, want %d", name, response.Code, http.StatusBadRequest)
		}
	}
}

func TestChangeEmailWithAWrongCodeFails(t *testing.T) {
	server := newTestServer(t)
	account := server.signUp(t)
	newEmail := randomTestEmail()
	server.requestAccountCode(t, account.token, auth.EmailCodeForChangeEmail, newEmail)

	response := server.sendSignedIn(http.MethodPost, changeEmailPath, account.token, changeEmailRequest{
		Email: newEmail, Code: wrongCode(server.latestCode(t, newEmail)),
	})

	requireErrorCode(t, response, http.StatusUnauthorized, "INCORRECT_CODE")
}

func TestAfterChangeEmailLoginWorksOnlyWithTheNewEmail(t *testing.T) {
	server := newTestServer(t)
	account := server.signUp(t)
	newEmail := randomTestEmail()
	t.Cleanup(func() {
		server.pool.Exec(context.Background(), `DELETE FROM accounts WHERE email_hash = $1`, server.emailProtector.Hash(newEmail))
	})
	server.requestAccountCode(t, account.token, auth.EmailCodeForChangeEmail, newEmail)

	response := server.sendSignedIn(http.MethodPost, changeEmailPath, account.token, changeEmailRequest{
		Email: newEmail, Code: server.latestCode(t, newEmail),
	})
	if response.Code != http.StatusOK {
		t.Fatalf("change email: status = %d %s, want 200", response.Code, response.Body)
	}

	oldEmailResponse := server.send(http.MethodPost, loginPath, loginRequest{Email: account.email, LoginKey: account.loginKey, DeviceName: testDeviceName})
	requireErrorCode(t, oldEmailResponse, http.StatusUnauthorized, "INVALID_PASSWORD")
	server.logIn(t, testAccount{email: newEmail, loginKey: account.loginKey})
}

func TestRecoverWithAWrongCodeFails(t *testing.T) {
	server := newTestServer(t)
	account := server.signUp(t)
	server.requestRecoveryCode(t, account.email)

	response := server.send(http.MethodPost, recoverPath, recoverRequest{
		Email: account.email, Code: wrongCode(server.latestCode(t, account.email)), DeviceName: "Recovered laptop",
	})

	requireErrorCode(t, response, http.StatusUnauthorized, "INCORRECT_CODE")
}

func TestRecoverGivesATokenOnlyTheAccountsPrivateKeyOpens(t *testing.T) {
	server := newTestServer(t)
	publicKey, privateKey, err := box.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("make a key pair: %v", err)
	}
	signup := newTestSignupRequest(randomTestEmail(), "")
	signup.Keys.PublicKey = base64.StdEncoding.EncodeToString(publicKey[:])
	account := server.signUpWith(t, signup)
	server.requestRecoveryCode(t, account.email)

	response := server.send(http.MethodPost, recoverPath, recoverRequest{
		Email: account.email, Code: server.latestCode(t, account.email), DeviceName: "Recovered laptop",
	})
	if response.Code != http.StatusOK {
		t.Fatalf("recover: status = %d %s, want 200", response.Code, response.Body)
	}
	var body recoverResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("read recover answer: %v", err)
	}
	wantKeys := recoveryKeys{
		MasterKeyEncryptedWithRecoveryKey:      signup.Keys.MasterKeyEncryptedWithRecoveryKey,
		MasterKeyEncryptedWithRecoveryKeyNonce: signup.Keys.MasterKeyEncryptedWithRecoveryKeyNonce,
		PublicKey:                              signup.Keys.PublicKey,
		EncryptedPrivateKey:                    signup.Keys.EncryptedPrivateKey,
		EncryptedPrivateKeyNonce:               signup.Keys.EncryptedPrivateKeyNonce,
	}
	if body.Keys != wantKeys {
		t.Errorf("returned keys = %+v, want %+v", body.Keys, wantKeys)
	}

	sealedToken, err := base64.StdEncoding.DecodeString(body.EncryptedToken)
	if err != nil {
		t.Fatalf("encryptedToken is not base64: %v", err)
	}
	otherPublicKey, otherPrivateKey, _ := box.GenerateKey(rand.Reader)
	if _, opened := box.OpenAnonymous(nil, sealedToken, otherPublicKey, otherPrivateKey); opened {
		t.Fatal("the sealed token opened with a key pair that is not the account's")
	}
	tokenBytes, opened := box.OpenAnonymous(nil, sealedToken, publicKey, privateKey)
	if !opened {
		t.Fatal("the sealed token did not open with the account's private key")
	}
	token := base64.URLEncoding.EncodeToString(tokenBytes)
	if strings.Contains(response.Body.String(), token) {
		t.Fatalf("the recover answer contains the token in plain form: %s", response.Body)
	}

	recoveredCheck := server.checkSession(token)
	signupCheck := server.checkSession(account.token)
	if recoveredCheck.Code != http.StatusOK || recoveredCheck.Body.String() != signupCheck.Body.String() {
		t.Fatalf("session check with the recovered token = %d %s, want 200 and the same account as the signup token (%s)",
			recoveredCheck.Code, recoveredCheck.Body, signupCheck.Body)
	}
}

func TestDeleteAccountNeedsTheRightLoginKeyAndCode(t *testing.T) {
	server := newTestServer(t)
	account := server.signUp(t)
	server.requestAccountCode(t, account.token, auth.EmailCodeForAccountDeletion, "")
	code := server.latestCode(t, account.email)

	wrongKeyResponse := server.sendSignedIn(http.MethodDelete, accountPath, account.token, deleteAccountRequest{LoginKey: randomBase64(auth.LoginKeyLength), Code: code})
	requireErrorCode(t, wrongKeyResponse, http.StatusUnauthorized, "INVALID_PASSWORD")
	wrongCodeResponse := server.sendSignedIn(http.MethodDelete, accountPath, account.token, deleteAccountRequest{LoginKey: account.loginKey, Code: wrongCode(code)})
	requireErrorCode(t, wrongCodeResponse, http.StatusUnauthorized, "INCORRECT_CODE")

	if !server.accountExists(t, account.email) {
		t.Fatal("the account was deleted without the right login key and code")
	}
}

func TestDeleteAccountRemovesItsRowsAndSignsOutEveryDevice(t *testing.T) {
	server := newTestServer(t)
	account := server.signUp(t)
	otherToken := server.newSession(t, account)
	accountID := server.accountID(t, account.email)
	server.requestAccountCode(t, account.token, auth.EmailCodeForAccountDeletion, "")

	response := server.sendSignedIn(http.MethodDelete, accountPath, account.token, deleteAccountRequest{
		LoginKey: account.loginKey, Code: server.latestCode(t, account.email),
	})
	if response.Code != http.StatusOK {
		t.Fatalf("delete account: status = %d %s, want 200", response.Code, response.Body)
	}

	for _, table := range []string{"accounts", "account_keys", "sessions"} {
		var remainingRows int
		if err := server.pool.QueryRow(t.Context(), `SELECT count(*) FROM `+table+` WHERE account_id = $1`, accountID).Scan(&remainingRows); err != nil {
			t.Fatalf("count %s rows: %v", table, err)
		}
		if remainingRows != 0 {
			t.Errorf("%s still has %d rows for the deleted account", table, remainingRows)
		}
	}
	for _, token := range []string{account.token, otherToken} {
		requireErrorCode(t, server.checkSession(token), http.StatusUnauthorized, "INVALID_TOKEN")
	}
}

func (server testServer) newSession(t *testing.T, account testAccount) string {
	t.Helper()

	server.logIn(t, account)
	response := server.send(http.MethodPost, verifyDevicePath, verifyDeviceRequest{
		Email: account.email, LoginKey: account.loginKey, Code: server.latestCode(t, account.email), DeviceName: "Test laptop",
	})
	if response.Code != http.StatusOK {
		t.Fatalf("verify device: status = %d %s, want 200", response.Code, response.Body)
	}
	var body verifyDeviceResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("read verify device answer: %v", err)
	}
	return body.Token
}

func (server testServer) requestAccountCode(t *testing.T, token string, purpose auth.EmailCodePurpose, email string) {
	t.Helper()

	response := server.sendSignedIn(http.MethodPost, accountEmailCodePath, token, accountEmailCodeRequest{Purpose: string(purpose), Email: email})
	if response.Code != http.StatusOK {
		t.Fatalf("request %s code: status = %d %s, want 200", purpose, response.Code, response.Body)
	}
}

func (server testServer) requestRecoveryCode(t *testing.T, email string) {
	t.Helper()

	response := server.send(http.MethodPost, emailCodePath, emailCodeRequest{Email: email, Purpose: string(auth.EmailCodeForRecovery)})
	if response.Code != http.StatusOK {
		t.Fatalf("request recovery code: status = %d %s, want 200", response.Code, response.Body)
	}
}

func (server testServer) accountID(t *testing.T, email string) int64 {
	t.Helper()

	var accountID int64
	err := server.pool.QueryRow(t.Context(), `SELECT account_id FROM accounts WHERE email_hash = $1`, server.emailProtector.Hash(email)).Scan(&accountID)
	if err != nil {
		t.Fatalf("load account id: %v", err)
	}
	return accountID
}

func newTestPasswordChange(logOutOtherDevices *bool) changePasswordRequest {
	return changePasswordRequest{
		LoginKey: randomBase64(auth.LoginKeyLength),
		Keys: store.PasswordKeys{
			PasswordSalt:            randomBase64(auth.PasswordSaltLength),
			MemLimit:                auth.DefaultMemLimit,
			OpsLimit:                auth.DefaultOpsLimit,
			EncryptedMasterKey:      randomBase64(lockedKeyLength),
			EncryptedMasterKeyNonce: randomBase64(keyNonceLength),
		},
		LogOutOtherDevices: logOutOtherDevices,
	}
}

func sessionIDForToken(token string) string {
	tokenHash := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(tokenHash[:])
}
