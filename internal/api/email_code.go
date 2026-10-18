package api

import (
	"net/http"
	"slices"

	"github.com/emberr-app/emberr-server/internal/auth"
)

const emailCodePath = "/v1/auth/email-code"

var signedOutEmailCodePurposes = []auth.EmailCodePurpose{auth.EmailCodeForSignup, auth.EmailCodeForRecovery}

type emailCodeRequest struct {
	Email   string `json:"email"`
	Purpose string `json:"purpose"`
}

func handleEmailCode(emailCodes *auth.EmailCodes) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request emailCodeRequest
		if err := readJSON(w, r, &request); err != nil {
			writeError(w, r, err)
			return
		}
		email, err := checkEmail(request.Email)
		if err != nil {
			writeError(w, r, err)
			return
		}
		purpose := auth.EmailCodePurpose(request.Purpose)
		if !slices.Contains(signedOutEmailCodePurposes, purpose) {
			writeError(w, r, newBadRequestError("purpose must be signup or recover"))
			return
		}

		if err := emailCodes.Send(r.Context(), email, purpose); err != nil {
			writeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}
