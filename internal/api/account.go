package api

import (
	"net/http"

	"github.com/emberr-app/emberr-server/internal/auth"
)

const accountPath = "/v1/account"

type deleteAccountRequest struct {
	LoginKey string `json:"loginKey"`
	Code     string `json:"code"`
}

func handleAccountDetails(accounts *auth.Accounts) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		details, err := accounts.Details(r.Context(), signedInAccountID(r.Context()))
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, details)
	}
}

func handleDeleteAccount(accounts *auth.Accounts) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request deleteAccountRequest
		if err := readJSON(w, r, &request); err != nil {
			writeError(w, r, err)
			return
		}
		loginKey, err := decodeBase64Field("loginKey", request.LoginKey, auth.LoginKeyLength)
		if err != nil {
			writeError(w, r, err)
			return
		}

		if err := accounts.Delete(r.Context(), signedInAccountID(r.Context()), loginKey, request.Code); err != nil {
			writeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}
