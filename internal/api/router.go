package api

import "net/http"

func NewRouter(database DatabasePinger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handleHealth(database))
	return mux
}
