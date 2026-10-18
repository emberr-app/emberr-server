package api

import (
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/emberr-app/emberr-server/internal/store"
)

const (
	requestsPerIP      = 10
	ipLimitWindow      = time.Minute
	requestsPerAccount = 10
	accountLimitWindow = time.Minute
)

func limitRequestsPerIP(rateLimits store.RateLimitTable, path string, next http.HandlerFunc) http.HandlerFunc {
	limitKeyFor := func(r *http.Request) string { return "ip:" + clientIP(r) + ":" + path }
	return limitRequests(rateLimits, limitKeyFor, requestsPerIP, ipLimitWindow, next)
}

func limitRequestsPerAccount(rateLimits store.RateLimitTable, path string, next http.HandlerFunc) http.HandlerFunc {
	limitKeyFor := func(r *http.Request) string {
		return "account:" + strconv.FormatInt(signedInAccountID(r.Context()), 10) + ":" + path
	}
	return limitRequests(rateLimits, limitKeyFor, requestsPerAccount, accountLimitWindow, next)
}

func limitRequests(rateLimits store.RateLimitTable, limitKeyFor func(r *http.Request) string, limit int, window time.Duration, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		allowed, err := rateLimits.CountAttempt(r.Context(), limitKeyFor(r), limit, window, time.Now())
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
