package api

import (
	"net/http"

	"github.com/emberr-app/emberr-server/internal/auth"
	"github.com/emberr-app/emberr-server/internal/store"
)

const changePasswordPath = "/v1/auth/change-password"

type changePasswordRequest struct {
	LoginKey           string             `json:"loginKey"`
	Keys               store.PasswordKeys `json:"keys"`
	LogOutOtherDevices *bool              `json:"logOutOtherDevices"`
	Code               string             `json:"code"`
}

func handleChangePassword(accounts *auth.Accounts) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request changePasswordRequest
		if err := readJSON(w, r, &request); err != nil {
			writeError(w, r, err)
			return
		}
		loginKey, err := decodeBase64Field("loginKey", request.LoginKey, auth.LoginKeyLength)
		if err != nil {
			writeError(w, r, err)
			return
		}
		if err := checkPasswordKeys(request.Keys); err != nil {
			writeError(w, r, err)
			return
		}
		logOutOtherDevices := true
		if request.LogOutOtherDevices != nil {
			logOutOtherDevices = *request.LogOutOtherDevices
		}

		err = accounts.ChangePassword(r.Context(), signedInAccountID(r.Context()), auth.PasswordChange{
			LoginKey:           loginKey,
			Keys:               request.Keys,
			LogOutOtherDevices: logOutOtherDevices,
			CurrentToken:       signedInToken(r),
			Code:               request.Code,
		})
		if err != nil {
			writeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

func checkPasswordKeys(keys store.PasswordKeys) error {
	if err := checkPasswordSettings(keys.MemLimit, keys.OpsLimit); err != nil {
		return err
	}
	return checkBase64Fields([]base64Field{
		{"keys.salt", keys.PasswordSalt, auth.PasswordSaltLength},
		{"keys.encryptedMasterKey", keys.EncryptedMasterKey, lockedKeyLength},
		{"keys.encryptedMasterKeyNonce", keys.EncryptedMasterKeyNonce, keyNonceLength},
	})
}
