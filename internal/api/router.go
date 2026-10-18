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
	Sessions          *auth.Sessions
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
	versionedRoutes.HandleFunc("POST "+recoverPath, limitRequestsPerIP(services.RateLimits, recoverPath, handleRecover(services.Accounts)))

	signedIn := func(handler http.HandlerFunc) http.Handler {
		return requireSession(services.Sessions, handler)
	}
	signedInAndLimited := func(path string, handler http.HandlerFunc) http.Handler {
		return requireSession(services.Sessions, limitRequestsPerAccount(services.RateLimits, path, handler))
	}
	versionedRoutes.Handle("GET "+sessionsPath, signedIn(handleListSessions(services.Sessions)))
	versionedRoutes.Handle("DELETE "+sessionsPath+"/{id}", signedIn(handleSignOutSession(services.Sessions)))
	versionedRoutes.Handle("GET "+accountPath, signedIn(handleAccountDetails(services.Accounts)))
	versionedRoutes.Handle("DELETE "+accountPath, signedInAndLimited(accountPath, handleDeleteAccount(services.Accounts)))
	versionedRoutes.Handle("POST "+accountEmailCodePath, signedInAndLimited(accountEmailCodePath, handleAccountEmailCode(services.Accounts)))
	versionedRoutes.Handle("POST "+changeEmailPath, signedInAndLimited(changeEmailPath, handleChangeEmail(services.Accounts)))
	versionedRoutes.Handle("POST "+changePasswordPath, signedInAndLimited(changePasswordPath, handleChangePassword(services.Accounts)))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handleHealth(services.Database))
	mux.Handle("/v1/", requireSupportedAppVersion(services.MinimumAppVersion, versionedRoutes))
	return mux
}
