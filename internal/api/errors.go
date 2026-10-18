package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/emberr-app/emberr-server/internal/auth"
)

type apiError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	statusCode int
}

func (err *apiError) Error() string {
	return err.Code + ": " + err.Message
}

func newBadRequestError(message string) *apiError {
	return &apiError{Code: "BAD_REQUEST", Message: message, statusCode: http.StatusBadRequest}
}

var (
	errRequestTooLarge  = &apiError{Code: "REQUEST_TOO_LARGE", Message: "request body is too large", statusCode: http.StatusRequestEntityTooLarge}
	errTooManyRequests  = &apiError{Code: "TOO_MANY_REQUESTS", Message: "too many attempts, try again later", statusCode: http.StatusTooManyRequests}
	errInternal         = &apiError{Code: "INTERNAL_ERROR", Message: "something went wrong", statusCode: http.StatusInternalServerError}
	errWrongPassword    = &apiError{Code: "INVALID_PASSWORD", Message: "email or password is wrong", statusCode: http.StatusUnauthorized}
	errWrongEmailCode   = &apiError{Code: "INCORRECT_CODE", Message: "the email code is wrong", statusCode: http.StatusUnauthorized}
	errExpiredEmailCode = &apiError{Code: "EXPIRED_CODE", Message: "the email code has expired, ask for a new one", statusCode: http.StatusGone}
	errInvalidToken     = &apiError{Code: "INVALID_TOKEN", Message: "sign in again", statusCode: http.StatusUnauthorized}
	errUpgradeRequired  = &apiError{Code: "UPGRADE_REQUIRED", Message: "this app version is too old, update the app", statusCode: http.StatusUpgradeRequired}
)

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	var responseError *apiError
	var bodyTooLargeError *http.MaxBytesError
	switch {
	case errors.As(err, &responseError):
	case errors.As(err, &bodyTooLargeError):
		responseError = errRequestTooLarge
	case errors.Is(err, auth.ErrTooManyAttempts):
		responseError = errTooManyRequests
	case errors.Is(err, auth.ErrWrongLoginKey):
		responseError = errWrongPassword
	case errors.Is(err, auth.ErrWrongEmailCode):
		responseError = errWrongEmailCode
	case errors.Is(err, auth.ErrNoActiveEmailCode):
		responseError = errExpiredEmailCode
	case errors.Is(err, auth.ErrInvalidSession):
		responseError = errInvalidToken
	default:
		slog.ErrorContext(r.Context(), "request failed", "path", r.URL.Path, "error", err)
		responseError = errInternal
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(responseError.statusCode)
	json.NewEncoder(w).Encode(responseError)
}
