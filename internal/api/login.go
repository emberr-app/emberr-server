package api

import (
	"net/http"

	"github.com/emberr-app/emberr-server/internal/auth"
	"github.com/emberr-app/emberr-server/internal/store"
)

const (
	paramsPath = "/v1/auth/params"
	loginPath  = "/v1/auth/login"
)

type loginRequest struct {
	Email      string `json:"email"`
	LoginKey   string `json:"loginKey"`
	DeviceName string `json:"deviceName"`
}

type loginResponse struct {
	Token string            `json:"token"`
	Keys  store.AccountKeys `json:"keys"`
}

func handleParams(accounts *auth.Accounts) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		email, err := checkEmail(r.URL.Query().Get("email"))
		if err != nil {
			writeError(w, r, err)
			return
		}
		params, err := accounts.PasswordParams(r.Context(), email)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, params)
	}
}

func handleLogin(accounts *auth.Accounts) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request loginRequest
		if err := readJSON(w, r, &request); err != nil {
			writeError(w, r, err)
			return
		}
		email, loginKey, err := checkLoginFields(request.Email, request.LoginKey, request.DeviceName)
		if err != nil {
			writeError(w, r, err)
			return
		}

		token, keys, err := accounts.LogIn(r.Context(), email, loginKey, request.DeviceName)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, loginResponse{Token: token, Keys: keys})
	}
}
