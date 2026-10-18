package api

import (
	"fmt"
	"net/http"

	"github.com/emberr-app/emberr-server/internal/auth"
	"github.com/emberr-app/emberr-server/internal/store"
)

const (
	signupPath               = "/v1/auth/signup"
	lockedKeyLength          = 32 + 16
	keyNonceLength           = 24
	publicKeyLength          = 32
	maxMemLimit              = 1 << 30
	maxMemLimitTimesOpsLimit = 4 << 30
)

type base64Field struct {
	name   string
	value  string
	length int
}

type signupRequest struct {
	Email      string            `json:"email"`
	Code       string            `json:"code"`
	DeviceName string            `json:"deviceName"`
	LoginKey   string            `json:"loginKey"`
	Keys       store.AccountKeys `json:"keys"`
}

type signupResponse struct {
	Token string `json:"token"`
}

func handleSignup(accounts *auth.Accounts) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request signupRequest
		if err := readJSON(w, r, &request); err != nil {
			writeError(w, r, err)
			return
		}
		email, loginKey, err := checkLoginFields(request.Email, request.LoginKey, request.DeviceName)
		if err != nil {
			writeError(w, r, err)
			return
		}
		if err := checkAccountKeys(request.Keys); err != nil {
			writeError(w, r, err)
			return
		}

		token, err := accounts.SignUp(r.Context(), auth.Signup{
			Email:      email,
			Code:       request.Code,
			DeviceName: request.DeviceName,
			LoginKey:   loginKey,
			Keys:       request.Keys,
		})
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, signupResponse{Token: token})
	}
}

func checkPasswordSettings(memLimit int64, opsLimit int32) error {
	if memLimit < auth.DefaultMemLimit || opsLimit < auth.DefaultOpsLimit {
		return newBadRequestError(fmt.Sprintf("keys.memLimit must be at least %d and keys.opsLimit at least %d", auth.DefaultMemLimit, auth.DefaultOpsLimit))
	}
	if memLimit > maxMemLimit || memLimit*int64(opsLimit) > maxMemLimitTimesOpsLimit {
		return newBadRequestError(fmt.Sprintf("keys.memLimit must be at most %d and keys.memLimit times keys.opsLimit at most %d", maxMemLimit, int64(maxMemLimitTimesOpsLimit)))
	}
	return nil
}

func checkBase64Fields(fields []base64Field) error {
	for _, field := range fields {
		if _, err := decodeBase64Field(field.name, field.value, field.length); err != nil {
			return err
		}
	}
	return nil
}

func checkAccountKeys(keys store.AccountKeys) error {
	if err := checkPasswordSettings(keys.MemLimit, keys.OpsLimit); err != nil {
		return err
	}
	return checkBase64Fields([]base64Field{
		{"keys.salt", keys.PasswordSalt, auth.PasswordSaltLength},
		{"keys.encryptedMasterKey", keys.EncryptedMasterKey, lockedKeyLength},
		{"keys.encryptedMasterKeyNonce", keys.EncryptedMasterKeyNonce, keyNonceLength},
		{"keys.masterKeyEncryptedWithRecoveryKey", keys.MasterKeyEncryptedWithRecoveryKey, lockedKeyLength},
		{"keys.masterKeyEncryptedWithRecoveryKeyNonce", keys.MasterKeyEncryptedWithRecoveryKeyNonce, keyNonceLength},
		{"keys.recoveryKeyEncryptedWithMasterKey", keys.RecoveryKeyEncryptedWithMasterKey, lockedKeyLength},
		{"keys.recoveryKeyEncryptedWithMasterKeyNonce", keys.RecoveryKeyEncryptedWithMasterKeyNonce, keyNonceLength},
		{"keys.publicKey", keys.PublicKey, publicKeyLength},
		{"keys.encryptedPrivateKey", keys.EncryptedPrivateKey, lockedKeyLength},
		{"keys.encryptedPrivateKeyNonce", keys.EncryptedPrivateKeyNonce, keyNonceLength},
	})
}
