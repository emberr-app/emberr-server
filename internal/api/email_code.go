package api

import (
	"net"
	"net/http"
	"slices"
	"time"

	"github.com/emberr-app/emberr-server/internal/auth"
	"github.com/emberr-app/emberr-server/internal/store"
)

const (
	emailCodePath          = "/v1/auth/email-code"
	emailCodeRequestsPerIP = 10
	emailCodeIPLimitWindow = time.Minute
)

var signedOutEmailCodePurposes = []auth.EmailCodePurpose{auth.EmailCodeForSignup, auth.EmailCodeForRecovery}

type emailCodeRequest struct {
	Email   string `json:"email"`
	Purpose string `json:"purpose"`
}

func handleEmailCode(emailCodes *auth.EmailCodes, rateLimits store.RateLimitTable) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limitKey := "ip:" + clientIP(r) + ":" + emailCodePath
		allowed, err := rateLimits.CountAttempt(r.Context(), limitKey, emailCodeRequestsPerIP, emailCodeIPLimitWindow, time.Now())
		if err != nil {
			writeError(w, r, err)
			return
		}
		if !allowed {
			writeError(w, r, errTooManyRequests)
			return
		}

		var request emailCodeRequest
		if err := readJSON(w, r, &request); err != nil {
			writeError(w, r, err)
			return
		}
		email := auth.NormalizeEmail(request.Email)
		if email == "" {
			writeError(w, r, newBadRequestError("email is missing"))
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

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
