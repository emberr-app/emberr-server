package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
)

type DatabasePinger interface {
	Ping(ctx context.Context) error
}

func handleHealth(database DatabasePinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := database.Ping(r.Context()); err != nil {
			slog.ErrorContext(r.Context(), "health check failed", "error", err)
			http.Error(w, "database unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"message": "pong"})
	}
}
