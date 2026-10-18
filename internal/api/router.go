package api

import (
	"net/http"

	"github.com/emberr-app/emberr-server/internal/auth"
	"github.com/emberr-app/emberr-server/internal/store"
)

type Services struct {
	Database   DatabasePinger
	EmailCodes *auth.EmailCodes
	RateLimits store.RateLimitTable
}

func NewRouter(services Services) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handleHealth(services.Database))
	mux.HandleFunc("POST "+emailCodePath, handleEmailCode(services.EmailCodes, services.RateLimits))
	return mux
}
