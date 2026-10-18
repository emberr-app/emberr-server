package api

import (
	"net/http"

	"github.com/emberr-app/emberr-server/internal/auth"
	"github.com/emberr-app/emberr-server/internal/store"
)

type Services struct {
	Database          DatabasePinger
	EmailCodes        *auth.EmailCodes
	Accounts          *auth.Accounts
	RateLimits        store.RateLimitTable
	MinimumAppVersion AppVersion
}

func NewRouter(services Services) http.Handler {
	versionedRoutes := http.NewServeMux()
	versionedRoutes.HandleFunc("POST "+emailCodePath, limitRequestsPerIP(services.RateLimits, emailCodePath, handleEmailCode(services.EmailCodes)))
	versionedRoutes.HandleFunc("GET "+paramsPath, limitRequestsPerIP(services.RateLimits, paramsPath, handleParams(services.Accounts)))
	versionedRoutes.HandleFunc("POST "+signupPath, limitRequestsPerIP(services.RateLimits, signupPath, handleSignup(services.Accounts)))
	versionedRoutes.HandleFunc("POST "+loginPath, limitRequestsPerIP(services.RateLimits, loginPath, handleLogin(services.Accounts)))
	versionedRoutes.HandleFunc("POST "+verifyDevicePath, limitRequestsPerIP(services.RateLimits, verifyDevicePath, handleVerifyDevice(services.Accounts)))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handleHealth(services.Database))
	mux.Handle("/v1/", requireSupportedAppVersion(services.MinimumAppVersion, versionedRoutes))
	return mux
}
