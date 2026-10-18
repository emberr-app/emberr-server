package api

import (
	"context"
	"net/http"

	"github.com/emberr-app/emberr-server/internal/auth"
)

const (
	accountEmailCodePath = "/v1/account/email-code"
	changeEmailPath      = "/v1/auth/change-email"
)

type accountEmailCodeRequest struct {
	Purpose string `json:"purpose"`
	Email   string `json:"email"`
}

type changeEmailRequest struct {
	Email string `json:"email"`
	Code  string `json:"code"`
}

func handleAccountEmailCode(accounts *auth.Accounts) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request accountEmailCodeRequest
		if err := readJSON(w, r, &request); err != nil {
			writeError(w, r, err)
			return
		}

		var err error
		switch auth.EmailCodePurpose(request.Purpose) {
		case auth.EmailCodeForChangeEmail:
			err = sendChangeEmailCode(r.Context(), accounts, request.Email)
		case auth.EmailCodeForAccountDeletion:
			err = accounts.SendDeletionCode(r.Context(), signedInAccountID(r.Context()))
		default:
			err = newBadRequestError("purpose must be change or delete")
		}
		if err != nil {
			writeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

func sendChangeEmailCode(ctx context.Context, accounts *auth.Accounts, rawNewEmail string) error {
	newEmail, err := checkEmail(rawNewEmail)
	if err != nil {
		return err
	}
	return accounts.SendChangeEmailCode(ctx, newEmail)
}

func handleChangeEmail(accounts *auth.Accounts) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request changeEmailRequest
		if err := readJSON(w, r, &request); err != nil {
			writeError(w, r, err)
			return
		}
		newEmail, err := checkEmail(request.Email)
		if err != nil {
			writeError(w, r, err)
			return
		}

		if err := accounts.ChangeEmail(r.Context(), signedInAccountID(r.Context()), newEmail, request.Code); err != nil {
			writeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}
