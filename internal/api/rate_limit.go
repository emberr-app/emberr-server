package api

import (
	"net"
	"net/http"
	"time"

	"github.com/emberr-app/emberr-server/internal/store"
)

const (
	requestsPerIP = 10
	ipLimitWindow = time.Minute
)

func limitRequestsPerIP(rateLimits store.RateLimitTable, path string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limitKey := "ip:" + clientIP(r) + ":" + path
		allowed, err := rateLimits.CountAttempt(r.Context(), limitKey, requestsPerIP, ipLimitWindow, time.Now())
		if err != nil {
			writeError(w, r, err)
			return
		}
		if !allowed {
			writeError(w, r, errTooManyRequests)
			return
		}
		next(w, r)
	}
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
