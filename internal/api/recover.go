package api

import (
	"net/http"

	"github.com/emberr-app/emberr-server/internal/auth"
)

const recoverPath = "/v1/auth/recover"

type recoverRequest struct {
	Email      string `json:"email"`
	Code       string `json:"code"`
	DeviceName string `json:"deviceName"`
}

type recoverResponse struct {
	EncryptedToken string       `json:"encryptedToken"`
	Keys           recoveryKeys `json:"keys"`
}

type recoveryKeys struct {
	MasterKeyEncryptedWithRecoveryKey      string `json:"masterKeyEncryptedWithRecoveryKey"`
	MasterKeyEncryptedWithRecoveryKeyNonce string `json:"masterKeyEncryptedWithRecoveryKeyNonce"`
	PublicKey                              string `json:"publicKey"`
	EncryptedPrivateKey                    string `json:"encryptedPrivateKey"`
	EncryptedPrivateKeyNonce               string `json:"encryptedPrivateKeyNonce"`
}

func handleRecover(accounts *auth.Accounts) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request recoverRequest
		if err := readJSON(w, r, &request); err != nil {
			writeError(w, r, err)
			return
		}
		email, err := checkEmail(request.Email)
		if err != nil {
			writeError(w, r, err)
			return
		}
		if err := checkDeviceName(request.DeviceName); err != nil {
			writeError(w, r, err)
			return
		}

		encryptedToken, keys, err := accounts.Recover(r.Context(), email, request.Code, request.DeviceName)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, recoverResponse{
			EncryptedToken: encryptedToken,
			Keys: recoveryKeys{
				MasterKeyEncryptedWithRecoveryKey:      keys.MasterKeyEncryptedWithRecoveryKey,
				MasterKeyEncryptedWithRecoveryKeyNonce: keys.MasterKeyEncryptedWithRecoveryKeyNonce,
				PublicKey:                              keys.PublicKey,
				EncryptedPrivateKey:                    keys.EncryptedPrivateKey,
				EncryptedPrivateKeyNonce:               keys.EncryptedPrivateKeyNonce,
			},
		})
	}
}
