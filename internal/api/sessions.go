package api

import (
	"net/http"

	"github.com/emberr-app/emberr-server/internal/auth"
)

const sessionsPath = "/v1/auth/sessions"

type sessionsResponse struct {
	Sessions []auth.SessionDetails `json:"sessions"`
}

func handleListSessions(sessions *auth.Sessions) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		details, err := sessions.List(r.Context(), signedInAccountID(r.Context()), signedInToken(r))
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, sessionsResponse{Sessions: details})
	}
}

func handleSignOutSession(sessions *auth.Sessions) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := sessions.SignOut(r.Context(), signedInAccountID(r.Context()), r.PathValue("id")); err != nil {
			writeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}
