package api

import (
	"context"
	"net/http"

	"github.com/emberr-app/emberr-server/internal/auth"
)

const sessionTokenHeader = "X-Auth-Token"

type contextKey int

const signedInAccountIDKey contextKey = iota

func requireSession(sessions *auth.Sessions, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accountID, err := sessions.AccountForToken(r.Context(), r.Header.Get(sessionTokenHeader))
		if err != nil {
			writeError(w, r, err)
			return
		}
		signedInContext := context.WithValue(r.Context(), signedInAccountIDKey, accountID)
		next.ServeHTTP(w, r.WithContext(signedInContext))
	})
}

func signedInAccountID(ctx context.Context) int64 {
	accountID, _ := ctx.Value(signedInAccountIDKey).(int64)
	return accountID
}

func signedInToken(r *http.Request) string {
	return r.Header.Get(sessionTokenHeader)
}
