package api

import (
	"net/http"

	"github.com/emberr-app/emberr-server/internal/auth"
	"github.com/emberr-app/emberr-server/internal/store"
)

const (
	paramsPath       = "/v1/auth/params"
	loginPath        = "/v1/auth/login"
	verifyDevicePath = "/v1/auth/verify-device"
)

type loginRequest struct {
	Email      string `json:"email"`
	LoginKey   string `json:"loginKey"`
	DeviceName string `json:"deviceName"`
}

type loginResponse struct {
	EmailCodeNeeded bool `json:"emailCodeNeeded"`
}

type verifyDeviceRequest struct {
	Email      string `json:"email"`
	LoginKey   string `json:"loginKey"`
	Code       string `json:"code"`
	DeviceName string `json:"deviceName"`
}

type verifyDeviceResponse struct {
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

		if err := accounts.LogIn(r.Context(), email, loginKey); err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, loginResponse{EmailCodeNeeded: true})
	}
}

func handleVerifyDevice(accounts *auth.Accounts) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request verifyDeviceRequest
		if err := readJSON(w, r, &request); err != nil {
			writeError(w, r, err)
			return
		}
		email, loginKey, err := checkLoginFields(request.Email, request.LoginKey, request.DeviceName)
		if err != nil {
			writeError(w, r, err)
			return
		}

		token, keys, err := accounts.VerifyDevice(r.Context(), email, loginKey, request.Code, request.DeviceName)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, verifyDeviceResponse{Token: token, Keys: keys})
	}
}
