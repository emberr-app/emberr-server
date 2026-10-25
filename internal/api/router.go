package api

import (
	"net/http"

	"github.com/emberr-app/emberr-server/internal/auth"
	"github.com/emberr-app/emberr-server/internal/store"
	"github.com/emberr-app/emberr-server/internal/sync"
)

type Services struct {
	Database          DatabasePinger
	EmailCodes        *auth.EmailCodes
	Accounts          *auth.Accounts
	Sessions          *auth.Sessions
	RateLimits        store.RateLimitTable
	Spaces            *sync.Spaces
	Media             *sync.Media
	MinimumAppVersion AppVersion
	MaxItemBytes      int64
}

func NewRouter(services Services) http.Handler {
	versionedRoutes := http.NewServeMux()
	versionedRoutes.HandleFunc("POST "+emailCodePath, limitRequestsPerIP(services.RateLimits, emailCodePath, handleEmailCode(services.EmailCodes)))
	versionedRoutes.HandleFunc("GET "+paramsPath, limitRequestsPerIP(services.RateLimits, paramsPath, handleParams(services.Accounts)))
	versionedRoutes.HandleFunc("POST "+signupPath, limitRequestsPerIP(services.RateLimits, signupPath, handleSignup(services.Accounts)))
	versionedRoutes.HandleFunc("POST "+loginPath, limitRequestsPerIP(services.RateLimits, loginPath, handleLogin(services.Accounts)))
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
	versionedRoutes.Handle("POST "+eraseAccountPath, signedInAndLimited(eraseAccountPath, handleEraseAccountData(services.Accounts)))
	versionedRoutes.Handle("POST "+accountEmailCodePath, signedInAndLimited(accountEmailCodePath, handleAccountEmailCode(services.Accounts)))
	versionedRoutes.Handle("POST "+changeEmailPath, signedInAndLimited(changeEmailPath, handleChangeEmail(services.Accounts)))
	versionedRoutes.Handle("POST "+changePasswordPath, signedInAndLimited(changePasswordPath, handleChangePassword(services.Accounts)))
	versionedRoutes.Handle("POST "+spacesPath, signedIn(handleRegisterSpace(services.Spaces)))
	versionedRoutes.Handle("GET "+syncStatePath, signedIn(handleSyncState(services.Spaces)))
	versionedRoutes.Handle("GET "+spaceChangesPath, signedIn(handleSpaceChanges(services.Spaces)))
	versionedRoutes.Handle("POST "+uploadURLsPath, signedIn(handleCreateUploadURL(services.Spaces, services.MaxItemBytes)))
	versionedRoutes.Handle("PUT "+spaceItemPath, signedIn(handleCommitItem(services.Spaces)))
	versionedRoutes.Handle("DELETE "+spaceItemPath, signedIn(handleDeleteItem(services.Spaces)))
	versionedRoutes.Handle("POST "+mediaUploadURLsPath, signedIn(handleCreateMediaUploadURLs(services.Media)))
	versionedRoutes.Handle("PUT "+mediaPath, signedIn(handleCommitMedia(services.Media)))
	versionedRoutes.Handle("POST "+mediaDownloadURLsPath, signedIn(handleCreateMediaDownloadURLs(services.Media)))
	versionedRoutes.Handle("PUT "+mediaLockPath, signedIn(handleAddMediaLock(services.Spaces)))
	versionedRoutes.Handle("DELETE "+mediaLockPath, signedIn(handleRemoveMediaLock(services.Spaces)))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handleHealth(services.Database))
	mux.Handle("/v1/", requireSupportedAppVersion(services.MinimumAppVersion, versionedRoutes))
	return mux
}
